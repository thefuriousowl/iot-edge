package datalogger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultRetentionRuntimeInterval = 5 * time.Minute

var (
	ErrRetentionRuntimeRepositoryRequired = errors.New("Data Logger retention runtime repository is required")
	ErrRetentionRuntimeCleanerRequired    = errors.New("Data Logger retention runtime cleaner is required")
	ErrRetentionRuntimeAlreadyStarted     = errors.New("Data Logger retention runtime is already started")
	ErrRetentionRuntimeNotStarted         = errors.New("Data Logger retention runtime is not started")
)

type RetentionCleaner interface {
	CleanupRetention(context.Context, uuid.UUID, RetentionCleanupInput) (*RetentionCleanupResult, error)
}

type RetentionRuntimeOption func(*RetentionRuntime) error

func WithRetentionRuntimeInterval(interval time.Duration) RetentionRuntimeOption {
	return func(runtime *RetentionRuntime) error {
		if interval < 0 {
			return fmt.Errorf("%w: interval must not be negative", ErrInvalidInput)
		}
		runtime.interval = interval
		return nil
	}
}

func WithRetentionRuntimeBatchLimit(batchLimit int) RetentionRuntimeOption {
	return func(runtime *RetentionRuntime) error {
		if batchLimit < 1 || batchLimit > MaxRetentionBatchLimit {
			return fmt.Errorf("%w: batch limit is outside the supported range", ErrInvalidInput)
		}
		runtime.batchLimit = batchLimit
		return nil
	}
}

type RetentionRuntime struct {
	repository RetentionRuntimeRepository
	cleaner    RetentionCleaner
	interval   time.Duration
	batchLimit int
	now        func() time.Time
	errors     chan error

	mu      sync.Mutex
	runMu   sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	stopped bool
}

func NewRetentionRuntime(repository RetentionRuntimeRepository, cleaner RetentionCleaner, options ...RetentionRuntimeOption) (*RetentionRuntime, error) {
	if repository == nil {
		return nil, ErrRetentionRuntimeRepositoryRequired
	}
	if cleaner == nil {
		return nil, ErrRetentionRuntimeCleanerRequired
	}
	runtime := &RetentionRuntime{
		repository: repository,
		cleaner:    cleaner,
		interval:   defaultRetentionRuntimeInterval,
		batchLimit: DefaultRetentionBatchLimit,
		now:        time.Now,
		errors:     make(chan error, 32),
	}
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

func (runtime *RetentionRuntime) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.started || runtime.stopped {
		return ErrRetentionRuntimeAlreadyStarted
	}
	runtime.ctx, runtime.cancel = context.WithCancel(ctx)
	runtime.done = make(chan struct{})
	runtime.started = true
	go runtime.run()
	return nil
}

func (runtime *RetentionRuntime) Stop() {
	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return
	}
	runtime.stopped = true
	runtime.cancel()
	done := runtime.done
	runtime.mu.Unlock()
	<-done
}

func (runtime *RetentionRuntime) Errors() <-chan error {
	return runtime.errors
}

func (runtime *RetentionRuntime) Reconcile(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	runtime.runMu.Lock()
	defer runtime.runMu.Unlock()
	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return ErrRetentionRuntimeNotStarted
	}
	runtimeContext := runtime.ctx
	runtime.mu.Unlock()
	operationContext, cancel := context.WithCancel(ctx)
	stopRuntimeCancellation := context.AfterFunc(runtimeContext, cancel)
	defer stopRuntimeCancellation()
	defer cancel()
	return runtime.reconcile(operationContext)
}

func (runtime *RetentionRuntime) run() {
	defer close(runtime.done)
	runtime.runAndReport()
	if runtime.interval == 0 {
		<-runtime.ctx.Done()
		return
	}
	ticker := time.NewTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-ticker.C:
			runtime.runAndReport()
		}
	}
}

func (runtime *RetentionRuntime) runAndReport() {
	if err := runtime.Reconcile(runtime.ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrRetentionRuntimeNotStarted) {
		runtime.report(fmt.Errorf("reconciling Data Logger retention: %w", err))
	}
}

func (runtime *RetentionRuntime) reconcile(ctx context.Context) error {
	loggerIDs, err := runtime.repository.ListRetentionLoggerIDs(ctx)
	if err != nil {
		return err
	}
	evaluatedAt := runtime.now().UTC()
	for _, loggerID := range loggerIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if loggerID == uuid.Nil {
			runtime.report(fmt.Errorf("cleaning Data Logger retention: %w", ErrInvalidInput))
			continue
		}
		_, err := runtime.cleaner.CleanupRetention(ctx, loggerID, RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: runtime.batchLimit})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			runtime.report(fmt.Errorf("cleaning Data Logger %s retention: %w", loggerID, err))
		}
	}
	return nil
}

func (runtime *RetentionRuntime) report(err error) {
	select {
	case runtime.errors <- err:
	default:
	}
}
