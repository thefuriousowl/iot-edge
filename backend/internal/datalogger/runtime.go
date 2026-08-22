package datalogger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultRuntimeReconcileInterval = 5 * time.Second

var (
	ErrRuntimeRepositoryRequired = errors.New("Data Logger runtime repository is required")
	ErrRuntimeAlreadyStarted     = errors.New("Data Logger runtime is already started")
	ErrRuntimeNotStarted         = errors.New("Data Logger runtime is not started")
	ErrRuntimeRunnerRequired     = errors.New("Data Logger runtime scheduler is required")
)

type RuntimeRepository interface {
	ListEnabledLoggers(context.Context) ([]Logger, error)
}

type LoggerRunner interface {
	Run(context.Context, Logger) error
}

type RuntimeOption func(*Runtime) error

func WithRuntimeReconcileInterval(interval time.Duration) RuntimeOption {
	return func(runtime *Runtime) error {
		if interval < 0 {
			return fmt.Errorf("%w: reconcile interval must not be negative", ErrInvalidInput)
		}
		runtime.reconcileInterval = interval
		return nil
	}
}

func WithRuntimeRunners(interval, calendar LoggerRunner) RuntimeOption {
	return func(runtime *Runtime) error {
		if interval == nil || calendar == nil {
			return ErrRuntimeRunnerRequired
		}
		runtime.interval = interval
		runtime.calendar = calendar
		return nil
	}
}

type Runtime struct {
	repository        RuntimeRepository
	interval          LoggerRunner
	calendar          LoggerRunner
	reconcileInterval time.Duration
	errors            chan error

	mu          sync.Mutex
	reconcileMu sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	stopped     bool
	jobs        map[uuid.UUID]*runtimeJob
	waitGroup   sync.WaitGroup
}

type runtimeJob struct {
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
}

type runtimeFingerprint struct {
	Mode     Mode                    `json:"mode"`
	Timezone string                  `json:"timezone"`
	StartAt  time.Time               `json:"start_at"`
	EndAt    *time.Time              `json:"end_at"`
	Config   json.RawMessage         `json:"config"`
	Tags     []runtimeFingerprintTag `json:"tags"`
}

type runtimeFingerprintTag struct {
	ID       uuid.UUID `json:"id"`
	DataType string    `json:"data_type"`
}

func NewRuntime(repository RuntimeRepository, snapshots SnapshotReader, history HistoryWriter, options ...RuntimeOption) (*Runtime, error) {
	if repository == nil {
		return nil, ErrRuntimeRepositoryRequired
	}
	if snapshots == nil {
		return nil, ErrSnapshotReaderRequired
	}
	if history == nil {
		return nil, ErrHistoryWriterRequired
	}
	runtime := &Runtime{
		repository:        repository,
		reconcileInterval: defaultRuntimeReconcileInterval,
		errors:            make(chan error, 32),
		jobs:              make(map[uuid.UUID]*runtimeJob),
	}
	interval, err := NewIntervalScheduler(snapshots, history, WithIntervalSchedulerErrorHandler(runtime.reportCaptureError))
	if err != nil {
		return nil, err
	}
	calendar, err := NewCalendarScheduler(snapshots, history, WithCalendarSchedulerErrorHandler(runtime.reportCaptureError))
	if err != nil {
		return nil, err
	}
	runtime.interval = interval
	runtime.calendar = calendar
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(runtime); err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

func (runtime *Runtime) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	runtime.mu.Lock()
	if runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return ErrRuntimeAlreadyStarted
	}
	runtime.ctx, runtime.cancel = context.WithCancel(ctx)
	runtime.started = true
	runtime.mu.Unlock()

	if err := runtime.Reconcile(ctx); err != nil {
		runtime.Stop()
		return err
	}
	runtime.mu.Lock()
	if runtime.reconcileInterval > 0 && !runtime.stopped {
		runtime.waitGroup.Add(1)
		go runtime.runReconciler()
	}
	runtime.mu.Unlock()
	return nil
}

func (runtime *Runtime) Stop() {
	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return
	}
	runtime.stopped = true
	runtime.cancel()
	jobs := make([]*runtimeJob, 0, len(runtime.jobs))
	for _, job := range runtime.jobs {
		jobs = append(jobs, job)
		job.cancel()
	}
	runtime.mu.Unlock()

	for _, job := range jobs {
		<-job.done
	}
	runtime.waitGroup.Wait()
	runtime.mu.Lock()
	runtime.jobs = make(map[uuid.UUID]*runtimeJob)
	runtime.mu.Unlock()
}

func (runtime *Runtime) Errors() <-chan error {
	return runtime.errors
}

func (runtime *Runtime) Reconcile(ctx context.Context) error {
	runtime.reconcileMu.Lock()
	defer runtime.reconcileMu.Unlock()

	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return ErrRuntimeNotStarted
	}
	runtime.mu.Unlock()

	loggers, err := runtime.repository.ListEnabledLoggers(ctx)
	if err != nil {
		return err
	}
	desired := make(map[uuid.UUID]Logger, len(loggers))
	fingerprints := make(map[uuid.UUID]string, len(loggers))
	for _, logger := range loggers {
		if logger.ID == uuid.Nil || !logger.Enabled {
			continue
		}
		desired[logger.ID] = logger
		fingerprints[logger.ID] = loggerRuntimeFingerprint(logger)
	}

	runtime.mu.Lock()
	stoppedJobs := make([]*runtimeJob, 0)
	for loggerID, job := range runtime.jobs {
		_, exists := desired[loggerID]
		if exists && job.fingerprint == fingerprints[loggerID] {
			delete(desired, loggerID)
			continue
		}
		delete(runtime.jobs, loggerID)
		job.cancel()
		stoppedJobs = append(stoppedJobs, job)
	}
	runtime.mu.Unlock()
	for _, job := range stoppedJobs {
		<-job.done
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.stopped {
		return ErrRuntimeNotStarted
	}
	for loggerID, logger := range desired {
		jobContext, cancel := context.WithCancel(runtime.ctx)
		job := &runtimeJob{fingerprint: fingerprints[loggerID], cancel: cancel, done: make(chan struct{})}
		runtime.jobs[loggerID] = job
		runtime.waitGroup.Add(1)
		go runtime.runJob(jobContext, logger, job)
	}
	return nil
}

func (runtime *Runtime) runReconciler() {
	defer runtime.waitGroup.Done()
	ticker := time.NewTicker(runtime.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-ticker.C:
			if err := runtime.Reconcile(runtime.ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrRuntimeNotStarted) {
				runtime.report(fmt.Errorf("reconciling Data Logger runtime: %w", err))
			}
		}
	}
}

func (runtime *Runtime) runJob(ctx context.Context, logger Logger, job *runtimeJob) {
	defer runtime.waitGroup.Done()
	defer close(job.done)
	runner := runtime.interval
	if logger.Mode == ModeSchedule {
		runner = runtime.calendar
	}
	err := runner.Run(ctx, logger)
	if err != nil && ctx.Err() == nil {
		runtime.report(fmt.Errorf("running Data Logger %s: %w", logger.ID, err))
	}
}

func (runtime *Runtime) reportCaptureError(loggerID uuid.UUID, scheduledAt time.Time, err error) {
	runtime.report(fmt.Errorf("capturing Data Logger %s at %s: %w", loggerID, scheduledAt.UTC().Format(time.RFC3339Nano), err))
}

func (runtime *Runtime) report(err error) {
	select {
	case runtime.errors <- err:
	default:
	}
}

func loggerRuntimeFingerprint(logger Logger) string {
	fingerprint := runtimeFingerprint{
		Mode:     logger.Mode,
		Timezone: logger.Timezone,
		StartAt:  logger.StartAt.UTC(),
		EndAt:    logger.EndAt,
		Config:   append(json.RawMessage(nil), logger.Config...),
		Tags:     make([]runtimeFingerprintTag, 0, len(logger.Tags)),
	}
	if fingerprint.EndAt != nil {
		endAt := fingerprint.EndAt.UTC()
		fingerprint.EndAt = &endAt
	}
	for _, reference := range logger.Tags {
		fingerprint.Tags = append(fingerprint.Tags, runtimeFingerprintTag{ID: reference.ID, DataType: reference.DataType})
	}
	payload, _ := json.Marshal(fingerprint)
	return string(payload)
}
