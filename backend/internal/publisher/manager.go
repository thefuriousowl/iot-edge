package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	defaultManagerReconcileInterval = 5 * time.Second
	transportCloseTimeout           = 5 * time.Second
	maxRuntimeErrorLength           = 500
)

var (
	ErrManagerAlreadyStarted     = errors.New("Data Publisher manager is already started")
	ErrManagerNotStarted         = errors.New("Data Publisher manager is not started")
	ErrRuntimeExited             = errors.New("Data Publisher runtime exited unexpectedly")
	ErrRuntimePanicked           = errors.New("Data Publisher runtime panicked")
	ErrTransportRegistryRequired = errors.New("Data Publisher transport registry is required")
)

var (
	runtimeSecretPattern   = regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key)=([^\s&]+)`)
	runtimeUserInfoPattern = regexp.MustCompile(`(://)([^/@\s]+)@`)
)

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

func WithSecretRotationFeed(feed SecretRotationFeed) ManagerOption {
	return func(manager *Manager) error {
		if isNilSourceDependency(feed) {
			return ErrSecretRotationRequired
		}
		manager.secretRotations = feed
		return nil
	}
}

type Manager struct {
	repository        Repository
	sources           UnifiedSourceReader
	definitions       *DefinitionRegistry
	transports        *TransportRegistry
	secretRotations   SecretRotationFeed
	reconcileInterval time.Duration
	now               func() time.Time
	errors            chan error

	mu             sync.Mutex
	reconcileMu    sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	started        bool
	stopped        bool
	jobs           map[uuid.UUID]*publisherJob
	statuses       map[uuid.UUID]RuntimeStatus
	reconcilerDone chan struct{}
	rotationDone   chan struct{}
}

type publisherJob struct {
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
	transport   Transport
}

type desiredPublisher struct {
	entity      Publisher
	resolved    []ResolvedSource
	trigger     TriggerConfig
	factory     TransportFactory
	claims      []ListenerClaim
	fingerprint string
	err         error
}

func NewManager(repository Repository, sources UnifiedSourceReader, definitions *DefinitionRegistry, transports *TransportRegistry, options ...ManagerOption) (*Manager, error) {
	if isNilSourceDependency(repository) {
		return nil, ErrRepositoryRequired
	}
	if isNilSourceDependency(sources) {
		return nil, ErrSourceResolverRequired
	}
	if definitions == nil {
		return nil, ErrDefinitionRegistryRequired
	}
	if transports == nil {
		return nil, ErrTransportRegistryRequired
	}
	manager := &Manager{
		repository: repository, sources: sources, definitions: definitions, transports: transports,
		reconcileInterval: defaultManagerReconcileInterval, now: time.Now,
		errors: make(chan error, 32), jobs: make(map[uuid.UUID]*publisherJob), statuses: make(map[uuid.UUID]RuntimeStatus),
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
	manager.mu.Unlock()
	var rotationSubscription SecretRotationSubscription
	if manager.secretRotations != nil {
		var err error
		rotationSubscription, err = manager.secretRotations.SubscribeSecretRotations()
		if err != nil {
			return err
		}
		if isNilSourceDependency(rotationSubscription) {
			return ErrSecretRotationRequired
		}
	}
	manager.mu.Lock()
	if manager.started || manager.stopped {
		manager.mu.Unlock()
		if rotationSubscription != nil {
			rotationSubscription.Close()
		}
		return ErrManagerAlreadyStarted
	}
	manager.ctx, manager.cancel = context.WithCancel(ctx)
	manager.started = true
	manager.reconcilerDone = make(chan struct{})
	if rotationSubscription != nil {
		manager.rotationDone = make(chan struct{})
	}
	manager.mu.Unlock()
	go manager.runReconciler()
	if rotationSubscription != nil {
		go manager.runSecretRotations(rotationSubscription)
	}
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
	jobs := make(map[uuid.UUID]*publisherJob, len(manager.jobs))
	for publisherID, job := range manager.jobs {
		jobs[publisherID] = job
		manager.setStatusStateLocked(publisherID, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
	}
	reconcilerDone := manager.reconcilerDone
	rotationDone := manager.rotationDone
	manager.mu.Unlock()
	manager.reconcileMu.Unlock()

	for _, job := range jobs {
		if err := waitForPublisherRuntime(ctx, job.done); err != nil {
			return err
		}
	}
	if err := waitForPublisherRuntime(ctx, reconcilerDone); err != nil {
		return err
	}
	if rotationDone != nil {
		if err := waitForPublisherRuntime(ctx, rotationDone); err != nil {
			return err
		}
	}
	manager.mu.Lock()
	for publisherID := range jobs {
		manager.setStatusStateLocked(publisherID, RuntimeStateStopped)
	}
	manager.jobs = make(map[uuid.UUID]*publisherJob)
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

func (manager *Manager) Restart(ctx context.Context, publisherID uuid.UUID) error {
	if ctx == nil || publisherID == uuid.Nil {
		return ErrInvalidInput
	}
	manager.reconcileMu.Lock()
	defer manager.reconcileMu.Unlock()
	manager.mu.Lock()
	if !manager.started || manager.stopped {
		manager.mu.Unlock()
		return ErrManagerNotStarted
	}
	job := manager.jobs[publisherID]
	if job != nil {
		delete(manager.jobs, publisherID)
		manager.setStatusStateLocked(publisherID, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
	}
	manager.mu.Unlock()
	if job != nil {
		if err := waitForPublisherRuntime(ctx, job.done); err != nil {
			return err
		}
		manager.mu.Lock()
		manager.setStatusStateLocked(publisherID, RuntimeStateStopped)
		manager.mu.Unlock()
	}
	return manager.reconcileLocked(ctx)
}

func (manager *Manager) Status(publisherID uuid.UUID) RuntimeStatus {
	manager.mu.Lock()
	status, exists := manager.statuses[publisherID]
	job := manager.jobs[publisherID]
	manager.mu.Unlock()
	if !exists {
		return RuntimeStatus{PublisherID: publisherID, State: RuntimeStateStopped, Sources: []SourceRuntimeStatus{}}
	}
	return applyTransportStatus(clonePublisherRuntimeStatus(status), job)
}

func (manager *Manager) Statuses() []RuntimeStatus {
	manager.mu.Lock()
	statuses := make([]RuntimeStatus, 0, len(manager.statuses))
	jobs := make(map[uuid.UUID]*publisherJob, len(manager.jobs))
	for publisherID, status := range manager.statuses {
		statuses = append(statuses, clonePublisherRuntimeStatus(status))
		jobs[publisherID] = manager.jobs[publisherID]
	}
	manager.mu.Unlock()
	for index := range statuses {
		statuses[index] = applyTransportStatus(statuses[index], jobs[statuses[index].PublisherID])
	}
	sort.Slice(statuses, func(first, second int) bool {
		return statuses[first].PublisherID.String() < statuses[second].PublisherID.String()
	})
	return statuses
}

func (manager *Manager) Diagnostics(publisherID uuid.UUID) []MQTTDiagnosticEvent {
	if manager == nil || publisherID == uuid.Nil {
		return []MQTTDiagnosticEvent{}
	}
	manager.mu.Lock()
	job := manager.jobs[publisherID]
	manager.mu.Unlock()
	if job == nil || job.transport == nil {
		return []MQTTDiagnosticEvent{}
	}
	provider, ok := job.transport.(interface{ Diagnostics() []MQTTDiagnosticEvent })
	if !ok {
		return []MQTTDiagnosticEvent{}
	}
	events := provider.Diagnostics()
	if events == nil {
		return []MQTTDiagnosticEvent{}
	}
	return events
}

func (manager *Manager) Errors() <-chan error { return manager.errors }

func (manager *Manager) reconcileLocked(ctx context.Context) error {
	manager.mu.Lock()
	if !manager.started || manager.stopped {
		manager.mu.Unlock()
		return ErrManagerNotStarted
	}
	manager.mu.Unlock()

	entities, err := manager.repository.ListEnabled(ctx)
	if err != nil {
		return err
	}
	desired := make(map[uuid.UUID]*desiredPublisher, len(entities))
	ids := make([]uuid.UUID, 0, len(entities))
	for _, entity := range entities {
		if entity.ID == uuid.Nil || !entity.Enabled {
			continue
		}
		specification := manager.prepare(ctx, entity)
		desired[entity.ID] = specification
		ids = append(ids, entity.ID)
	}
	sort.Slice(ids, func(first, second int) bool { return ids[first].String() < ids[second].String() })
	manager.assignListenerClaims(ids, desired)

	manager.mu.Lock()
	stoppedJobs := make(map[uuid.UUID]*publisherJob)
	for publisherID, job := range manager.jobs {
		specification, exists := desired[publisherID]
		if exists && job.fingerprint == specification.fingerprint {
			delete(desired, publisherID)
			continue
		}
		delete(manager.jobs, publisherID)
		manager.setStatusStateLocked(publisherID, RuntimeStateStopping)
		if job.cancel != nil {
			job.cancel()
		}
		stoppedJobs[publisherID] = job
	}
	manager.mu.Unlock()
	for publisherID, job := range stoppedJobs {
		if err := waitForPublisherRuntime(ctx, job.done); err != nil {
			return err
		}
		manager.mu.Lock()
		manager.setStatusStateLocked(publisherID, RuntimeStateStopped)
		manager.mu.Unlock()
	}

	for _, publisherID := range ids {
		specification, exists := desired[publisherID]
		if exists {
			manager.startJob(ctx, specification)
		}
	}
	return nil
}

func (manager *Manager) prepare(ctx context.Context, entity Publisher) *desiredPublisher {
	entity = clonePublisher(entity)
	specification := &desiredPublisher{entity: entity, fingerprint: publisherRuntimeFingerprint(entity)}
	definition, err := manager.definitions.Find(entity.Type)
	if err != nil {
		specification.err = err
		return specification
	}
	descriptor := definition.Descriptor()
	if entity.ConfigVersion != descriptor.ConfigVersion {
		specification.err = fmt.Errorf("%w: %s stored %d, implementation %d", ErrConfigVersionMismatch, entity.Type, entity.ConfigVersion, descriptor.ConfigVersion)
		return specification
	}
	entity.Config, err = normalizeDefinitionConfig(ctx, definition, entity.Config)
	if err != nil {
		specification.err = err
		return specification
	}
	entity.Sources, err = NormalizeSourceSelections(entity.Sources)
	if err != nil {
		specification.err = err
		return specification
	}
	resolved, err := manager.sources.Resolve(ctx, entity.Sources)
	if err != nil {
		specification.err = err
		return specification
	}
	if len(resolved) != len(entity.Sources) {
		specification.err = ErrSourceNotFound
		return specification
	}
	for index, source := range resolved {
		selection := entity.Sources[index]
		if source.Alias != selection.Alias || source.Descriptor.Reference != selection.Reference {
			specification.err = ErrSourceNotFound
			return specification
		}
		if !definition.Supports(source.Descriptor) {
			specification.err = fmt.Errorf("%w: %s", ErrIncompatibleSource, selection.Reference)
			return specification
		}
	}
	specification.resolved = cloneResolvedSources(resolved)
	if err := validateDefinitionSourceConfig(ctx, definition, entity.Config, resolved); err != nil {
		specification.err = err
		return specification
	}
	specification.trigger, err = ParseTriggerConfig(entity.Config)
	if err == nil {
		err = validateTriggerSources(entity.Config, entity.Sources)
	}
	if err != nil {
		specification.err = err
		return specification
	}
	specification.factory, err = manager.transports.Find(entity.Type)
	if err != nil {
		specification.err = err
		return specification
	}
	specification.claims, err = publisherListenerClaims(specification.factory, entity)
	if err == nil {
		specification.claims, err = normalizeListenerClaims(entity.Type, specification.claims)
	}
	if err != nil {
		specification.err = err
		return specification
	}
	specification.entity = entity
	specification.fingerprint = publisherRuntimeFingerprint(entity)
	return specification
}

func (manager *Manager) assignListenerClaims(ids []uuid.UUID, desired map[uuid.UUID]*desiredPublisher) {
	type ownership struct {
		publisherID uuid.UUID
		claim       ListenerClaim
	}
	owners := make([]ownership, 0)
	for _, publisherID := range ids {
		specification := desired[publisherID]
		if specification.err != nil {
			continue
		}
		var conflictingOwner uuid.UUID
		for _, claim := range specification.claims {
			for _, owner := range owners {
				if listenerClaimsConflict(owner.claim, claim) {
					conflictingOwner = owner.publisherID
					break
				}
			}
			if conflictingOwner != uuid.Nil {
				break
			}
		}
		if conflictingOwner != uuid.Nil {
			specification.err = fmt.Errorf("%w: owned by %s", ErrListenerConflict, conflictingOwner)
			specification.fingerprint += ":conflict:" + conflictingOwner.String()
			continue
		}
		for _, claim := range specification.claims {
			owners = append(owners, ownership{publisherID: publisherID, claim: claim})
		}
	}
}

func (manager *Manager) startJob(ctx context.Context, specification *desiredPublisher) {
	entity := specification.entity
	manager.mu.Lock()
	manager.statuses[entity.ID] = RuntimeStatus{
		PublisherID: entity.ID, Type: entity.Type, State: RuntimeStateStarting, ConfigVersion: entity.ConfigVersion,
		LastTransitionAt: manager.now().UTC(), Sources: []SourceRuntimeStatus{},
	}
	manager.mu.Unlock()
	if specification.err != nil {
		manager.storeFailedJob(entity, specification.fingerprint, specification.err)
		return
	}
	transport, err := newPublisherTransport(ctx, specification.factory, entity, specification.resolved)
	if err != nil {
		manager.storeFailedJob(entity, specification.fingerprint, err)
		return
	}
	jobContext, cancel := context.WithCancel(manager.ctx)
	job := &publisherJob{fingerprint: specification.fingerprint, cancel: cancel, done: make(chan struct{}), transport: transport}
	now := manager.now().UTC()
	manager.mu.Lock()
	manager.jobs[entity.ID] = job
	manager.statuses[entity.ID] = RuntimeStatus{
		PublisherID: entity.ID, Type: entity.Type, State: RuntimeStateRunning, ConfigVersion: entity.ConfigVersion,
		StartedAt: &now, LastTransitionAt: now, Sources: []SourceRuntimeStatus{},
	}
	manager.mu.Unlock()
	go manager.runJob(jobContext, specification, transport, job)
}

func (manager *Manager) storeFailedJob(entity Publisher, fingerprint string, runtimeError error) {
	done := make(chan struct{})
	close(done)
	now := manager.now().UTC()
	publicError := sanitizeRuntimeError(runtimeError)
	manager.mu.Lock()
	manager.jobs[entity.ID] = &publisherJob{fingerprint: fingerprint, done: done}
	manager.statuses[entity.ID] = RuntimeStatus{
		PublisherID: entity.ID, Type: entity.Type, State: RuntimeStateError, ConfigVersion: entity.ConfigVersion,
		LastTransitionAt: now, Sources: []SourceRuntimeStatus{}, LastError: publicError,
	}
	manager.mu.Unlock()
	manager.report(fmt.Errorf("starting Data Publisher %s (%s): %s", entity.ID, entity.Type, publicError))
}

func (manager *Manager) runJob(ctx context.Context, specification *desiredPublisher, transport Transport, job *publisherJob) {
	defer close(job.done)
	runtimeError := manager.runPublisher(ctx, specification, transport)
	if ctx.Err() != nil {
		manager.mu.Lock()
		manager.setStatusStateLocked(specification.entity.ID, RuntimeStateStopped)
		manager.mu.Unlock()
		return
	}
	if runtimeError == nil {
		runtimeError = ErrRuntimeExited
	}
	publicError := sanitizeRuntimeError(runtimeError)
	now := manager.now().UTC()
	manager.mu.Lock()
	status := manager.statuses[specification.entity.ID]
	status.State = RuntimeStateError
	status.LastTransitionAt = now
	status.LastError = publicError
	manager.statuses[specification.entity.ID] = status
	manager.mu.Unlock()
	manager.report(fmt.Errorf("running Data Publisher %s (%s): %s", specification.entity.ID, specification.entity.Type, publicError))
}

func (manager *Manager) runPublisher(ctx context.Context, specification *desiredPublisher, transport Transport) (runtimeError error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runtimeError = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
		closeContext, cancel := context.WithTimeout(context.Background(), transportCloseTimeout)
		defer cancel()
		if err := closePublisherTransport(closeContext, transport); err != nil && runtimeError == nil {
			runtimeError = err
		}
	}()
	switch specification.trigger.Mode {
	case TriggerModeInterval:
		return manager.runIntervalPublisher(ctx, specification, transport, transportFailureEvents(transport))
	case TriggerModeOnChange:
		return manager.runOnChangePublisher(ctx, specification, transport, transportFailureEvents(transport))
	default:
		return ErrInvalidPublisherConfig
	}
}

func (manager *Manager) runIntervalPublisher(ctx context.Context, specification *desiredPublisher, transport Transport, failures <-chan error) error {
	ticker := time.NewTicker(specification.trigger.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			manager.publishSnapshot(ctx, specification.entity, transport)
		case failure, open := <-failures:
			if !open || failure == nil {
				return ErrRuntimeExited
			}
			return failure
		}
	}
}

func (manager *Manager) runOnChangePublisher(ctx context.Context, specification *desiredPublisher, transport Transport, failures <-chan error) error {
	subscription, err := manager.sources.Subscribe(ctx, specification.entity.Sources)
	if err != nil {
		return err
	}
	defer subscription.Close()
	var timer *time.Timer
	var timerChannel <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		manager.setQueueDepth(specification.entity.ID, 0)
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-timerChannel:
			if !open {
				continue
			}
			timerChannel = nil
			manager.setQueueDepth(specification.entity.ID, 0)
			manager.publishSnapshot(ctx, specification.entity, transport)
		case sample, open := <-subscription.Events():
			if !open {
				if err := subscription.Err(); err != nil {
					return err
				}
				return ErrRuntimeExited
			}
			if sample.Alias != specification.trigger.SourceAlias {
				continue
			}
			if timerChannel != nil {
				manager.recordDrop(specification.entity.ID)
				continue
			}
			timer = time.NewTimer(specification.trigger.Coalesce())
			timerChannel = timer.C
			manager.setQueueDepth(specification.entity.ID, 1)
		case failure, open := <-failures:
			if !open || failure == nil {
				return ErrRuntimeExited
			}
			return failure
		}
	}
}

func (manager *Manager) publishSnapshot(ctx context.Context, entity Publisher, transport Transport) {
	now := manager.now().UTC()
	manager.mu.Lock()
	status := manager.statuses[entity.ID]
	status.RequestCount++
	status.LastRequestAt = &now
	manager.statuses[entity.ID] = status
	manager.mu.Unlock()

	snapshot, err := manager.sources.Snapshot(ctx, entity.Sources)
	if err == nil {
		manager.updateSourceStatus(entity.ID, snapshot)
		err = transport.Publish(ctx, snapshot)
	}
	if metrics, ok := transport.(TransportMetricsProvider); ok {
		manager.storeTransportMetrics(entity.ID, metrics.Metrics())
	}
	if err != nil {
		manager.recordFailure(entity.ID, err)
		return
	}
	now = manager.now().UTC()
	manager.mu.Lock()
	status = manager.statuses[entity.ID]
	status.PublishCount++
	status.LastPublishAt = &now
	status.LastError = ""
	manager.statuses[entity.ID] = status
	manager.mu.Unlock()
}

func (manager *Manager) updateSourceStatus(publisherID uuid.UUID, snapshot SourceSnapshot) {
	sources := make([]SourceRuntimeStatus, len(snapshot.Samples))
	for index, sample := range snapshot.Samples {
		sources[index] = SourceRuntimeStatus{
			Alias: sample.Alias, Reference: sample.Reference, Available: sample.Available, Quality: sample.Quality,
			Sequence: sample.Sequence, ObservedAt: cloneTime(sample.ObservedAt), PeriodStart: cloneTime(sample.PeriodStart), PeriodEnd: cloneTime(sample.PeriodEnd),
		}
	}
	manager.mu.Lock()
	status := manager.statuses[publisherID]
	status.Sources = sources
	manager.statuses[publisherID] = status
	manager.mu.Unlock()
}

func (manager *Manager) recordFailure(publisherID uuid.UUID, runtimeError error) {
	publicError := sanitizeRuntimeError(runtimeError)
	manager.mu.Lock()
	status := manager.statuses[publisherID]
	status.FailureCount++
	status.LastError = publicError
	manager.statuses[publisherID] = status
	manager.mu.Unlock()
	manager.report(fmt.Errorf("publishing Data Publisher %s: %s", publisherID, publicError))
}

func (manager *Manager) recordDrop(publisherID uuid.UUID) {
	manager.mu.Lock()
	status := manager.statuses[publisherID]
	status.DropCount++
	manager.statuses[publisherID] = status
	manager.mu.Unlock()
}

func (manager *Manager) setQueueDepth(publisherID uuid.UUID, depth uint64) {
	manager.mu.Lock()
	status := manager.statuses[publisherID]
	status.QueueDepth = depth
	manager.statuses[publisherID] = status
	manager.mu.Unlock()
}

func (manager *Manager) storeTransportMetrics(publisherID uuid.UUID, metrics TransportMetrics) {
	manager.mu.Lock()
	status := manager.statuses[publisherID]
	status.ReconnectCount = metrics.ReconnectCount
	status.Connected = metrics.Connected
	status.ConnectionCount = metrics.ConnectionCount
	status.DeliveryCount = metrics.DeliveryCount
	status.DeliveryFailureCount = metrics.DeliveryFailureCount
	status.TransportQueueDepth = metrics.TransportQueueDepth
	status.TransportDropCount = metrics.TransportDropCount
	status.DiagnosticCount = metrics.DiagnosticCount
	status.DiagnosticDropCount = metrics.DiagnosticDropCount
	status.ExternalRequestCount = metrics.ExternalRequestCount
	status.RejectedRequestCount = metrics.RejectedRequestCount
	status.ActiveConnections = metrics.ActiveConnections
	status.LastExternalRequestAt = cloneTime(metrics.LastExternalRequestAt)
	status.LastConnectedAt = cloneTime(metrics.LastConnectedAt)
	status.LastDeliveredAt = cloneTime(metrics.LastDeliveredAt)
	status.LastDiagnosticAt = cloneTime(metrics.LastDiagnosticAt)
	status.TransportError = sanitizeRuntimeErrorText(metrics.TransportError)
	manager.statuses[publisherID] = status
	manager.mu.Unlock()
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
				manager.report(fmt.Errorf("reconciling Data Publisher manager: %s", sanitizeRuntimeError(err)))
			}
		}
	}
}

func (manager *Manager) runSecretRotations(subscription SecretRotationSubscription) {
	defer close(manager.rotationDone)
	defer subscription.Close()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case event, open := <-subscription.Events():
			if !open {
				if err := subscription.Err(); err != nil {
					manager.report(fmt.Errorf("watching Data Publisher secret rotations: %s", sanitizeRuntimeError(err)))
				}
				return
			}
			if !validSecretRotationEvent(event) {
				manager.report(fmt.Errorf("watching Data Publisher secret rotations: %s", ErrInvalidInput))
				continue
			}
			if err := manager.Restart(manager.ctx, event.PublisherID); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrManagerNotStarted) {
				manager.report(fmt.Errorf("restarting Data Publisher after secret rotation: %s", sanitizeRuntimeError(err)))
			}
		}
	}
}

func (manager *Manager) setStatusStateLocked(publisherID uuid.UUID, state RuntimeState) {
	status := manager.statuses[publisherID]
	status.PublisherID = publisherID
	status.State = state
	status.LastTransitionAt = manager.now().UTC()
	if state == RuntimeStateStopped {
		status.Connected = false
		status.ActiveConnections = 0
		status.TransportQueueDepth = 0
	}
	if status.Sources == nil {
		status.Sources = []SourceRuntimeStatus{}
	}
	manager.statuses[publisherID] = status
}

func (manager *Manager) report(err error) {
	select {
	case manager.errors <- err:
	default:
	}
}

func newPublisherTransport(ctx context.Context, factory TransportFactory, entity Publisher, resolved []ResolvedSource) (transport Transport, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			transport = nil
			err = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
	}()
	if sourceAware, ok := factory.(ResolvedTransportFactory); ok {
		transport, err = sourceAware.NewResolvedTransport(ctx, clonePublisher(entity), cloneResolvedSources(resolved))
	} else {
		transport, err = factory.NewTransport(ctx, clonePublisher(entity))
	}
	if err == nil && isNilSourceDependency(transport) {
		err = ErrTransportRequired
	}
	return transport, err
}

func publisherListenerClaims(factory TransportFactory, entity Publisher) (claims []ListenerClaim, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			claims = nil
			err = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
	}()
	return factory.ListenerClaims(clonePublisher(entity))
}

func closePublisherTransport(ctx context.Context, transport Transport) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v", ErrRuntimePanicked, recovered)
		}
	}()
	return transport.Close(ctx)
}

func transportFailureEvents(transport Transport) <-chan error {
	source, ok := transport.(TransportFailureSource)
	if !ok {
		return nil
	}
	return source.Failures()
}

func applyTransportStatus(status RuntimeStatus, job *publisherJob) RuntimeStatus {
	if job == nil || isNilSourceDependency(job.transport) {
		return status
	}
	provider, ok := job.transport.(TransportMetricsProvider)
	if !ok {
		return status
	}
	metrics := provider.Metrics()
	status.ReconnectCount = metrics.ReconnectCount
	status.Connected = metrics.Connected
	status.ConnectionCount = metrics.ConnectionCount
	status.DeliveryCount = metrics.DeliveryCount
	status.DeliveryFailureCount = metrics.DeliveryFailureCount
	status.TransportQueueDepth = metrics.TransportQueueDepth
	status.TransportDropCount = metrics.TransportDropCount
	status.DiagnosticCount = metrics.DiagnosticCount
	status.DiagnosticDropCount = metrics.DiagnosticDropCount
	status.ExternalRequestCount = metrics.ExternalRequestCount
	status.RejectedRequestCount = metrics.RejectedRequestCount
	status.ActiveConnections = metrics.ActiveConnections
	status.LastExternalRequestAt = cloneTime(metrics.LastExternalRequestAt)
	status.LastConnectedAt = cloneTime(metrics.LastConnectedAt)
	status.LastDeliveredAt = cloneTime(metrics.LastDeliveredAt)
	status.LastDiagnosticAt = cloneTime(metrics.LastDiagnosticAt)
	status.TransportError = sanitizeRuntimeErrorText(metrics.TransportError)
	return status
}

func sanitizeRuntimeErrorText(message string) string {
	if message == "" {
		return ""
	}
	return sanitizeRuntimeError(errors.New(message))
}

func waitForPublisherRuntime(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func publisherRuntimeFingerprint(entity Publisher) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(entity.Type))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatUint(uint64(entity.ConfigVersion), 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(entity.Config)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatUint(entity.SecretRevision, 10)))
	_, _ = hash.Write([]byte{0})
	sources, _ := json.Marshal(entity.Sources)
	_, _ = hash.Write(sources)
	return hex.EncodeToString(hash.Sum(nil))
}

func sanitizeRuntimeError(runtimeError error) string {
	if runtimeError == nil {
		return ""
	}
	message := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, runtimeError.Error())
	message = runtimeUserInfoPattern.ReplaceAllString(message, `${1}[redacted]@`)
	message = runtimeSecretPattern.ReplaceAllString(message, `${1}=[redacted]`)
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = "Data Publisher runtime error"
	}
	runes := []rune(message)
	if len(runes) > maxRuntimeErrorLength {
		message = string(runes[:maxRuntimeErrorLength])
	}
	return message
}
