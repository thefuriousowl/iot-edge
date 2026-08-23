package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultManagerReconcileInterval = 5 * time.Second

var (
	ErrManagerAlreadyStarted = errors.New("plugin manager is already started")
	ErrManagerNotStarted     = errors.New("plugin manager is not started")
	ErrRuntimeExited         = errors.New("plugin runtime exited unexpectedly")
	ErrRuntimePanicked       = errors.New("plugin runtime panicked")
)

type RuntimeState string

const (
	RuntimeStateStopped  RuntimeState = "stopped"
	RuntimeStateStarting RuntimeState = "starting"
	RuntimeStateRunning  RuntimeState = "running"
	RuntimeStateStopping RuntimeState = "stopping"
	RuntimeStateError    RuntimeState = "error"
)

type RuntimeStatus struct {
	InstanceID       uuid.UUID
	Type             Type
	State            RuntimeState
	ConfigVersion    uint
	StartedAt        *time.Time
	LastTransitionAt time.Time
	LastError        error
}

type ManagerOption func(*Manager) error

func WithManagerReconcileInterval(interval time.Duration) ManagerOption {
	return func(manager *Manager) error {
		if interval < 0 {
			return ErrInvalidInput
		}
		manager.reconcileInterval = interval
		return nil
	}
}

func WithManagerClock(clock func() time.Time) ManagerOption {
	return func(manager *Manager) error {
		if clock == nil {
			return ErrInvalidInput
		}
		manager.now = clock
		return nil
	}
}

type Manager struct {
	repository        Repository
	registry          *Registry
	host              Host
	reconcileInterval time.Duration
	now               func() time.Time
	errors            chan error

	mu             sync.Mutex
	reconcileMu    sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	started        bool
	stopped        bool
	jobs           map[uuid.UUID]*managerJob
	statuses       map[uuid.UUID]RuntimeStatus
	reconcilerDone chan struct{}
}

type managerJob struct {
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewManager(repository Repository, registry *Registry, host Host, options ...ManagerOption) (*Manager, error) {
	if isNil(repository) {
		return nil, ErrRepositoryRequired
	}
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	if isNil(host) {
		emptyHost, err := NewCapabilityHost(nil)
		if err != nil {
			return nil, err
		}
		host = emptyHost
	}
	manager := &Manager{
		repository:        repository,
		registry:          registry,
		host:              host,
		reconcileInterval: defaultManagerReconcileInterval,
		now:               time.Now,
		errors:            make(chan error, 32),
		jobs:              make(map[uuid.UUID]*managerJob),
		statuses:          make(map[uuid.UUID]RuntimeStatus),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(manager); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (manager *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	manager.mu.Lock()
	if manager.started || manager.stopped {
		manager.mu.Unlock()
		return ErrManagerAlreadyStarted
	}
	manager.ctx, manager.cancel = context.WithCancel(ctx)
	manager.started = true
	manager.reconcilerDone = make(chan struct{})
	manager.mu.Unlock()
	go manager.runReconciler()
	if err := manager.Reconcile(ctx); err != nil {
		_ = manager.Stop(context.Background())
		return err
	}
	return nil
}

func (manager *Manager) Stop(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	manager.reconcileMu.Lock()
	manager.mu.Lock()
	if !manager.started {
		manager.mu.Unlock()
		manager.reconcileMu.Unlock()
		return nil
	}
	if !manager.stopped {
		manager.stopped = true
		manager.cancel()
	}
	jobs := make(map[uuid.UUID]*managerJob, len(manager.jobs))
	for instanceID, job := range manager.jobs {
		jobs[instanceID] = job
		manager.setStatusStateLocked(instanceID, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
	}
	reconcilerDone := manager.reconcilerDone
	manager.mu.Unlock()
	manager.reconcileMu.Unlock()

	for _, job := range jobs {
		if err := waitForRuntime(ctx, job.done); err != nil {
			return err
		}
	}
	if err := waitForRuntime(ctx, reconcilerDone); err != nil {
		return err
	}
	manager.mu.Lock()
	for instanceID := range jobs {
		manager.setStatusStateLocked(instanceID, RuntimeStateStopped)
	}
	manager.jobs = make(map[uuid.UUID]*managerJob)
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) Reconcile(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	manager.reconcileMu.Lock()
	defer manager.reconcileMu.Unlock()
	return manager.reconcileLocked(ctx)
}

func (manager *Manager) Restart(ctx context.Context, id uuid.UUID) error {
	if ctx == nil || id == uuid.Nil {
		return ErrInvalidInput
	}
	manager.reconcileMu.Lock()
	defer manager.reconcileMu.Unlock()
	manager.mu.Lock()
	if !manager.started || manager.stopped {
		manager.mu.Unlock()
		return ErrManagerNotStarted
	}
	job := manager.jobs[id]
	if job != nil {
		delete(manager.jobs, id)
		manager.setStatusStateLocked(id, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
	}
	manager.mu.Unlock()
	if job != nil {
		if err := waitForRuntime(ctx, job.done); err != nil {
			return err
		}
		manager.mu.Lock()
		manager.setStatusStateLocked(id, RuntimeStateStopped)
		manager.mu.Unlock()
	}
	return manager.reconcileLocked(ctx)
}

func (manager *Manager) Status(id uuid.UUID) RuntimeStatus {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	status, exists := manager.statuses[id]
	if !exists {
		return RuntimeStatus{InstanceID: id, State: RuntimeStateStopped}
	}
	return cloneRuntimeStatus(status)
}

func (manager *Manager) Errors() <-chan error { return manager.errors }

func (manager *Manager) reconcileLocked(ctx context.Context) error {
	manager.mu.Lock()
	if !manager.started || manager.stopped {
		manager.mu.Unlock()
		return ErrManagerNotStarted
	}
	manager.mu.Unlock()

	instances, err := manager.repository.ListEnabled(ctx)
	if err != nil {
		return err
	}
	desired := make(map[uuid.UUID]Instance, len(instances))
	fingerprints := make(map[uuid.UUID]string, len(instances))
	for _, instance := range instances {
		if instance.ID == uuid.Nil || !instance.Enabled {
			continue
		}
		instance.Config = cloneConfig(instance.Config)
		desired[instance.ID] = instance
		fingerprints[instance.ID] = instanceRuntimeFingerprint(instance)
	}

	manager.mu.Lock()
	stoppedJobs := make(map[uuid.UUID]*managerJob)
	for instanceID, job := range manager.jobs {
		_, exists := desired[instanceID]
		if exists && job.fingerprint == fingerprints[instanceID] {
			delete(desired, instanceID)
			continue
		}
		delete(manager.jobs, instanceID)
		manager.setStatusStateLocked(instanceID, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
		stoppedJobs[instanceID] = job
	}
	manager.mu.Unlock()
	for instanceID, job := range stoppedJobs {
		if err := waitForRuntime(ctx, job.done); err != nil {
			return err
		}
		manager.mu.Lock()
		manager.setStatusStateLocked(instanceID, RuntimeStateStopped)
		manager.mu.Unlock()
	}

	instanceIDs := make([]uuid.UUID, 0, len(desired))
	for instanceID := range desired {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Slice(instanceIDs, func(left, right int) bool { return instanceIDs[left].String() < instanceIDs[right].String() })
	for _, instanceID := range instanceIDs {
		manager.startJob(ctx, desired[instanceID], fingerprints[instanceID])
	}
	return nil
}

func (manager *Manager) startJob(ctx context.Context, instance Instance, fingerprint string) {
	manager.mu.Lock()
	manager.statuses[instance.ID] = RuntimeStatus{
		InstanceID:       instance.ID,
		Type:             instance.Type,
		State:            RuntimeStateStarting,
		ConfigVersion:    instance.ConfigVersion,
		LastTransitionAt: manager.now().UTC(),
	}
	manager.mu.Unlock()

	runtime, err := manager.buildRuntime(ctx, instance)
	if err != nil {
		manager.storeFailedJob(instance, fingerprint, err)
		return
	}
	jobContext, cancel := context.WithCancel(manager.ctx)
	job := &managerJob{fingerprint: fingerprint, cancel: cancel, done: make(chan struct{})}
	now := manager.now().UTC()
	manager.mu.Lock()
	manager.jobs[instance.ID] = job
	manager.statuses[instance.ID] = RuntimeStatus{
		InstanceID:       instance.ID,
		Type:             instance.Type,
		State:            RuntimeStateRunning,
		ConfigVersion:    instance.ConfigVersion,
		StartedAt:        &now,
		LastTransitionAt: now,
	}
	manager.mu.Unlock()
	go manager.runJob(jobContext, instance, runtime, job)
}

func (manager *Manager) buildRuntime(ctx context.Context, instance Instance) (runtime Runtime, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runtime = nil
			err = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
	}()
	manifest, err := manager.registry.Manifest(instance.Type)
	if err != nil {
		return nil, err
	}
	if instance.ConfigVersion != manifest.ConfigVersion {
		return nil, fmt.Errorf("%w: %s stored %d, implementation %d", ErrConfigVersionMismatch, instance.Type, instance.ConfigVersion, manifest.ConfigVersion)
	}
	host, err := newScopedHost(manager.host, manifest)
	if err != nil {
		return nil, err
	}
	return manager.registry.NewRuntime(ctx, instance.Type, RuntimeSpec{InstanceID: instance.ID, Config: instance.Config}, host)
}

func (manager *Manager) storeFailedJob(instance Instance, fingerprint string, runtimeError error) {
	done := make(chan struct{})
	close(done)
	now := manager.now().UTC()
	manager.mu.Lock()
	manager.jobs[instance.ID] = &managerJob{fingerprint: fingerprint, done: done}
	manager.statuses[instance.ID] = RuntimeStatus{
		InstanceID:       instance.ID,
		Type:             instance.Type,
		State:            RuntimeStateError,
		ConfigVersion:    instance.ConfigVersion,
		LastTransitionAt: now,
		LastError:        runtimeError,
	}
	manager.mu.Unlock()
	manager.report(fmt.Errorf("starting plugin instance %s (%s): %w", instance.ID, instance.Type, runtimeError))
}

func (manager *Manager) runJob(ctx context.Context, instance Instance, runtime Runtime, job *managerJob) {
	defer close(job.done)
	runtimeError := runPluginRuntime(ctx, runtime)
	if ctx.Err() != nil {
		manager.mu.Lock()
		manager.setStatusStateLocked(instance.ID, RuntimeStateStopped)
		manager.mu.Unlock()
		return
	}
	if runtimeError == nil {
		runtimeError = ErrRuntimeExited
	}
	now := manager.now().UTC()
	manager.mu.Lock()
	status := manager.statuses[instance.ID]
	status.State = RuntimeStateError
	status.LastTransitionAt = now
	status.LastError = runtimeError
	manager.statuses[instance.ID] = status
	manager.mu.Unlock()
	manager.report(fmt.Errorf("running plugin instance %s (%s): %w", instance.ID, instance.Type, runtimeError))
}

func (manager *Manager) runReconciler() {
	defer close(manager.reconcilerDone)
	if manager.reconcileInterval == 0 {
		<-manager.ctx.Done()
		return
	}
	ticker := time.NewTicker(manager.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-ticker.C:
			if err := manager.Reconcile(manager.ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrManagerNotStarted) {
				manager.report(fmt.Errorf("reconciling plugin manager: %w", err))
			}
		}
	}
}

func (manager *Manager) setStatusStateLocked(instanceID uuid.UUID, state RuntimeState) {
	status := manager.statuses[instanceID]
	status.InstanceID = instanceID
	status.State = state
	status.LastTransitionAt = manager.now().UTC()
	manager.statuses[instanceID] = status
}

func (manager *Manager) report(err error) {
	select {
	case manager.errors <- err:
	default:
	}
}

func runPluginRuntime(ctx context.Context, runtime Runtime) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
	}()
	return runtime.Run(ctx)
}

func waitForRuntime(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func instanceRuntimeFingerprint(instance Instance) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(instance.Type))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatUint(uint64(instance.ConfigVersion), 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(instance.Config)
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneRuntimeStatus(status RuntimeStatus) RuntimeStatus {
	if status.StartedAt != nil {
		startedAt := *status.StartedAt
		status.StartedAt = &startedAt
	}
	return status
}
