package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewPublisherManagerValidatesDependenciesAndLifecycle(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	definitions, _ := NewDefaultDefinitionRegistry()
	transports := NewTransportRegistry()
	var nilRepository *sourceTestPublisherRepository
	var nilSources *managerSourceFeed
	for _, test := range []struct {
		name        string
		repository  Repository
		sources     UnifiedSourceReader
		definitions *DefinitionRegistry
		transports  *TransportRegistry
		want        error
	}{
		{name: "repository", sources: sources, definitions: definitions, transports: transports, want: ErrRepositoryRequired},
		{name: "typed nil repository", repository: nilRepository, sources: sources, definitions: definitions, transports: transports, want: ErrRepositoryRequired},
		{name: "sources", repository: repository, definitions: definitions, transports: transports, want: ErrSourceResolverRequired},
		{name: "typed nil sources", repository: repository, sources: nilSources, definitions: definitions, transports: transports, want: ErrSourceResolverRequired},
		{name: "definitions", repository: repository, sources: sources, transports: transports, want: ErrDefinitionRegistryRequired},
		{name: "transports", repository: repository, sources: sources, definitions: definitions, want: ErrTransportRegistryRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewManager(test.repository, test.sources, test.definitions, test.transports); !errors.Is(err, test.want) {
				t.Fatalf("NewManager() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := NewManager(repository, sources, definitions, transports, WithManagerReconcileInterval(-time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative reconcile interval error = %v", err)
	}
	if _, err := NewManager(repository, sources, definitions, transports, WithManagerClock(nil)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil clock error = %v", err)
	}
	var nilRotations *SecretRotationBroker
	if _, err := NewManager(repository, sources, definitions, transports, WithSecretRotationFeed(nilRotations)); !errors.Is(err, ErrSecretRotationRequired) {
		t.Fatalf("nil secret rotation feed error = %v", err)
	}
	manager, err := NewManager(repository, sources, definitions, transports, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Start(nil) error = %v", err)
	}
	if err := manager.Reconcile(context.Background()); !errors.Is(err, ErrManagerNotStarted) {
		t.Errorf("Reconcile(before start) error = %v", err)
	}
	if err := manager.Restart(context.Background(), uuid.New()); !errors.Is(err, ErrManagerNotStarted) {
		t.Errorf("Restart(before start) error = %v", err)
	}
	if err := manager.Restart(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Restart(nil ID) error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Errorf("Stop(before start) error = %v", err)
	}
}

func TestPublisherManagerRunsIntervalLifecycleAndReconcilesChanges(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Power", SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	sources.setSnapshot(SourceSnapshot{Samples: []SourceSample{{Alias: "power", Reference: reference, Available: true, Sequence: 7, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", Value: 42.5, Quality: SourceQualityGood}}})
	entity := managerPublisher(uuid.New(), TypeMQTT, Config(`{"trigger":{"mode":"interval","interval_ms":100}}`), []SourceSelection{{Alias: "power", Reference: reference}})
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	factory := newManagerTransportFactory()
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	first := receiveManagerTransport(t, factory.created)
	receiveSnapshot(t, first.published)
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateRunning && status.RequestCount >= 1 && status.PublishCount >= 1 && len(status.Sources) == 1
	})
	status := manager.Status(entity.ID)
	status.Sources[0].Alias = "mutated"
	if manager.Status(entity.ID).Sources[0].Alias != "power" {
		t.Fatal("Status() returned aliased source state")
	}

	mutateManagerPublisher(repository, entity.ID, func(stored *Publisher) { stored.Name = "Renamed" })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(name) error = %v", err)
	}
	assertNoManagerTransport(t, factory.created)

	mutateManagerPublisher(repository, entity.ID, func(stored *Publisher) {
		stored.Config = mqttTestConfigWithTrigger(Config(`{"trigger":{"mode":"interval","interval_ms":150}}`))
	})
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(config) error = %v", err)
	}
	waitClosed(t, first.closed, "first transport close")
	second := receiveManagerTransport(t, factory.created)
	manager.storeTransportMetrics(entity.ID, TransportMetrics{Connected: true, ActiveConnections: 2, TransportQueueDepth: 3})

	mutateManagerPublisher(repository, entity.ID, func(stored *Publisher) { stored.Enabled = false })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(disabled) error = %v", err)
	}
	waitClosed(t, second.closed, "second transport close")
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateStopped && !status.Connected && status.ActiveConnections == 0 && status.TransportQueueDepth == 0
	})
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrManagerAlreadyStarted) {
		t.Errorf("Start(after stop) error = %v", err)
	}
}

func TestPublisherManagerCoalescesTriggerSourceAndReportsSanitizedFailures(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	powerReference := TagSource(uuid.New())
	temperatureReference := TagSource(uuid.New())
	for reference, name := range map[SourceReference]string{powerReference: "Power", temperatureReference: "Temperature"} {
		sources.add(reference, SourceDescriptor{Reference: reference, Name: name, SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	}
	sources.setSnapshot(SourceSnapshot{Samples: []SourceSample{
		{Alias: "power", Reference: powerReference, Available: true, Sequence: 4, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Value: 10.0, Quality: SourceQualityGood},
		{Alias: "temperature", Reference: temperatureReference, Available: true, Sequence: 9, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Value: 24.0, Quality: SourceQualityGood},
	}})
	entity := managerPublisher(uuid.New(), TypeMQTT, Config(`{"trigger":{"mode":"on_change","source_alias":"power","coalesce_ms":100}}`), []SourceSelection{
		{Alias: "power", Reference: powerReference}, {Alias: "temperature", Reference: temperatureReference},
	})
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	factory := newManagerTransportFactory()
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	transport := receiveManagerTransport(t, factory.created)
	subscription := receiveManagerSubscription(t, sources.subscribed)
	subscription.events <- SourceSample{Alias: "temperature", Reference: temperatureReference}
	assertNoSnapshot(t, transport.published)

	transport.setPublishError(errors.New("mqtt://alice:plain-text@broker password=hunter2\nconnection failed"))
	subscription.events <- SourceSample{Alias: "power", Reference: powerReference, Sequence: 1}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.QueueDepth == 1 })
	subscription.events <- SourceSample{Alias: "power", Reference: powerReference, Sequence: 2}
	subscription.events <- SourceSample{Alias: "power", Reference: powerReference, Sequence: 3}
	snapshot := receiveSnapshot(t, transport.published)
	if len(snapshot.Samples) != 2 {
		t.Fatalf("published snapshot = %#v", snapshot)
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateRunning && status.FailureCount == 1 && status.DropCount == 2 && status.QueueDepth == 0 && status.ReconnectCount == 3
	})
	status := manager.Status(entity.ID)
	if strings.Contains(status.LastError, "plain-text") || strings.Contains(status.LastError, "hunter2") || !strings.Contains(status.LastError, "[redacted]") {
		t.Fatalf("LastError was not sanitized: %q", status.LastError)
	}

	transport.setPublishError(nil)
	subscription.events <- SourceSample{Alias: "power", Reference: powerReference, Sequence: 4}
	receiveSnapshot(t, transport.published)
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.PublishCount == 1 && status.LastError == "" })
	if sources.snapshotCalls() != 2 {
		t.Fatalf("Snapshot() calls = %d, want 2", sources.snapshotCalls())
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestPublisherManagerConflictsHTTPAndModbusListenerOwnership(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeUInt16, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	selection := []SourceSelection{{Alias: "value", Reference: reference}}
	httpPublisher := managerPublisher(uuid.MustParse("00000000-0000-0000-0000-000000000001"), TypeHTTPServer, nil, selection)
	modbusPublisher := managerPublisher(uuid.MustParse("00000000-0000-0000-0000-000000000002"), TypeModbusTCPServer, nil, selection)
	if err := repository.Create(context.Background(), &httpPublisher); err != nil {
		t.Fatalf("Create(HTTP) error = %v", err)
	}
	if err := repository.Create(context.Background(), &modbusPublisher); err != nil {
		t.Fatalf("Create(Modbus) error = %v", err)
	}
	httpFactory := newManagerTransportFactory()
	httpFactory.claims = []ListenerClaim{{Network: "tcp", Address: ":8080"}}
	modbusFactory := newManagerTransportFactory()
	modbusFactory.claims = []ListenerClaim{{Network: "tcp4", Address: "127.0.0.1:8080"}}
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeHTTPServer: httpFactory, TypeModbusTCPServer: modbusFactory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	httpTransport := receiveManagerTransport(t, httpFactory.created)
	waitPublisherStatus(t, manager, modbusPublisher.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateError && strings.Contains(status.LastError, ErrListenerConflict.Error())
	})
	assertNoManagerTransport(t, modbusFactory.created)

	mutateManagerPublisher(repository, httpPublisher.ID, func(stored *Publisher) { stored.Enabled = false })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(release listener) error = %v", err)
	}
	waitClosed(t, httpTransport.closed, "HTTP transport close")
	modbusTransport := receiveManagerTransport(t, modbusFactory.created)
	waitPublisherStatus(t, manager, modbusPublisher.ID, func(status RuntimeStatus) bool { return status.State == RuntimeStateRunning })
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitClosed(t, modbusTransport.closed, "Modbus transport close")
}

func TestPublisherManagerReportsFactoryFailureAndSupportsExplicitRestart(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	entity := managerPublisher(uuid.New(), TypeMQTT, nil, []SourceSelection{{Alias: "value", Reference: reference}})
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	factory := newManagerTransportFactory()
	factory.newError = errors.New("broker token=private-token unavailable")
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.State == RuntimeStateError })
	if status := manager.Status(entity.ID); strings.Contains(status.LastError, "private-token") {
		t.Fatalf("factory error leaked secret: %q", status.LastError)
	}
	select {
	case reported := <-manager.Errors():
		if strings.Contains(reported.Error(), "private-token") {
			t.Fatalf("Errors() leaked secret: %v", reported)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for factory error")
	}
	if factory.attemptCount() != 1 {
		t.Fatalf("factory attempts = %d", factory.attemptCount())
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged failure) error = %v", err)
	}
	if factory.attemptCount() != 1 {
		t.Fatalf("unchanged failure retried %d times", factory.attemptCount())
	}
	factory.setNewError(nil)
	if err := manager.Restart(context.Background(), entity.ID); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	transport := receiveManagerTransport(t, factory.created)
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.State == RuntimeStateRunning })
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitClosed(t, transport.closed, "restarted transport close")
}

func TestPublisherManagerContainsTransportPanic(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	sources.setSnapshot(SourceSnapshot{Samples: []SourceSample{{Alias: "value", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Value: 1.0, Quality: SourceQualityGood}}})
	entity := managerPublisher(uuid.New(), TypeMQTT, Config(`{"trigger":{"mode":"interval","interval_ms":100}}`), []SourceSelection{{Alias: "value", Reference: reference}})
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	factory := newManagerTransportFactory()
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	transport := receiveManagerTransport(t, factory.created)
	transport.setPublishPanic(true)
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateError && strings.Contains(status.LastError, ErrRuntimePanicked.Error())
	})
	waitClosed(t, transport.closed, "panicked transport close")
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestPublisherManagerRestartsOnlySecretRotationOwnerAndReconcilesMissedEvents(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	selection := []SourceSelection{{Alias: "value", Reference: reference}}
	firstEntity := managerPublisher(uuid.MustParse("00000000-0000-0000-0000-000000000011"), TypeMQTT, nil, selection)
	secondEntity := managerPublisher(uuid.MustParse("00000000-0000-0000-0000-000000000012"), TypeMQTT, nil, selection)
	for _, entity := range []*Publisher{&firstEntity, &secondEntity} {
		if err := repository.Create(context.Background(), entity); err != nil {
			t.Fatalf("Create(%s) error = %v", entity.ID, err)
		}
	}
	factory := newManagerTransportFactory()
	rotations, _ := NewSecretRotationBroker(8)
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory}, WithSecretRotationFeed(rotations))
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	active := map[uuid.UUID]*managerTransport{}
	for range 2 {
		transport := receiveManagerTransport(t, factory.created)
		active[transport.publisherID] = transport
	}

	mutateManagerPublisher(repository, firstEntity.ID, func(stored *Publisher) { stored.SecretRevision++ })
	rotations.PublishSecretRotation(SecretRotationEvent{
		PublisherID: firstEntity.ID, Reference: SecretReference{Name: "mqtt.password"}, Kind: SecretKindOpaque,
		Revision: 1, PublisherRevision: 1, Action: SecretRotationUpserted, RotatedAt: time.Now().UTC(),
	})
	restartedFirst := receiveManagerTransport(t, factory.created)
	if restartedFirst.publisherID != firstEntity.ID {
		t.Fatalf("rotation restarted %s, want %s", restartedFirst.publisherID, firstEntity.ID)
	}
	waitClosed(t, active[firstEntity.ID].closed, "first secret owner transport close")
	select {
	case <-active[secondEntity.ID].closed:
		t.Fatal("unaffected Publisher transport was restarted")
	case <-time.After(50 * time.Millisecond):
	}

	mutateManagerPublisher(repository, secondEntity.ID, func(stored *Publisher) { stored.SecretRevision++ })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(missed rotation) error = %v", err)
	}
	restartedSecond := receiveManagerTransport(t, factory.created)
	if restartedSecond.publisherID != secondEntity.ID {
		t.Fatalf("revision reconcile restarted %s, want %s", restartedSecond.publisherID, secondEntity.ID)
	}
	waitClosed(t, active[secondEntity.ID].closed, "second revision owner transport close")
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitClosed(t, restartedFirst.closed, "restarted first transport close")
	waitClosed(t, restartedSecond.closed, "restarted second transport close")
}

func TestPublisherManagerSurfacesTransportFailureAndLiveMetrics(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Value", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	entity := managerPublisher(uuid.New(), TypeMQTT, nil, []SourceSelection{{Alias: "value", Reference: reference}})
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	factory := newManagerTransportFactory()
	manager := newTestPublisherManager(t, repository, sources, map[Type]*managerTransportFactory{TypeMQTT: factory})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	transport := receiveManagerTransport(t, factory.created)
	requestedAt := time.Date(2026, time.August, 23, 16, 0, 0, 0, time.UTC)
	transport.setMetrics(TransportMetrics{
		ReconnectCount: 2, ExternalRequestCount: 9, RejectedRequestCount: 3, ActiveConnections: 1, LastExternalRequestAt: &requestedAt,
	})
	status := manager.Status(entity.ID)
	if status.ReconnectCount != 2 || status.ExternalRequestCount != 9 || status.RejectedRequestCount != 3 || status.ActiveConnections != 1 || status.LastExternalRequestAt == nil || !status.LastExternalRequestAt.Equal(requestedAt) {
		t.Fatalf("live transport status = %#v", status)
	}
	transport.failures <- errors.New("listener stopped")
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool {
		return status.State == RuntimeStateError && strings.Contains(status.LastError, "listener stopped")
	})
	waitClosed(t, transport.closed, "failed transport close")
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

type managerSourceFeed struct {
	mu          sync.Mutex
	descriptors map[SourceReference]SourceDescriptor
	snapshot    SourceSnapshot
	snapshots   int
	subscribed  chan *managerSubscription
}

func newManagerSourceFeed() *managerSourceFeed {
	return &managerSourceFeed{descriptors: make(map[SourceReference]SourceDescriptor), subscribed: make(chan *managerSubscription, 8)}
}

func (feed *managerSourceFeed) add(reference SourceReference, descriptor SourceDescriptor) {
	feed.mu.Lock()
	feed.descriptors[reference] = descriptor
	feed.mu.Unlock()
}

func (feed *managerSourceFeed) setSnapshot(snapshot SourceSnapshot) {
	feed.mu.Lock()
	feed.snapshot = snapshot
	feed.mu.Unlock()
}

func (feed *managerSourceFeed) snapshotCalls() int {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	return feed.snapshots
}

func (feed *managerSourceFeed) Catalog(context.Context, SourceCatalogInput) ([]SourceCatalogEntry, error) {
	return []SourceCatalogEntry{}, nil
}

func (feed *managerSourceFeed) Resolve(ctx context.Context, selections []SourceSelection) ([]ResolvedSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	resolved := make([]ResolvedSource, 0, len(selections))
	for _, selection := range selections {
		descriptor, exists := feed.descriptors[selection.Reference]
		if !exists {
			return nil, ErrSourceNotFound
		}
		resolved = append(resolved, ResolvedSource{Alias: selection.Alias, Descriptor: descriptor})
	}
	return resolved, nil
}

func (feed *managerSourceFeed) Latest(ctx context.Context, selection SourceSelection) (SourceSample, error) {
	snapshot, err := feed.Snapshot(ctx, []SourceSelection{selection})
	if err != nil || len(snapshot.Samples) == 0 {
		return SourceSample{}, err
	}
	return snapshot.Samples[0], nil
}

func (feed *managerSourceFeed) Snapshot(ctx context.Context, _ []SourceSelection) (SourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SourceSnapshot{}, err
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	feed.snapshots++
	snapshot := SourceSnapshot{CapturedAt: feed.snapshot.CapturedAt, Samples: make([]SourceSample, len(feed.snapshot.Samples))}
	for index, sample := range feed.snapshot.Samples {
		snapshot.Samples[index] = cloneSourceSample(sample)
	}
	return snapshot, nil
}

func (feed *managerSourceFeed) Subscribe(ctx context.Context, _ []SourceSelection) (SourceSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	subscription := &managerSubscription{events: make(chan SourceSample, 16)}
	feed.subscribed <- subscription
	return subscription, nil
}

type managerSubscription struct {
	events chan SourceSample
	mu     sync.Mutex
	err    error
	once   sync.Once
}

func (subscription *managerSubscription) Events() <-chan SourceSample { return subscription.events }
func (subscription *managerSubscription) Err() error {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.err
}
func (subscription *managerSubscription) Close() {}

type managerTransport struct {
	publisherID  uuid.UUID
	mu           sync.Mutex
	publishError error
	panicPublish bool
	published    chan SourceSnapshot
	closed       chan struct{}
	failures     chan error
	closeOnce    sync.Once
	metrics      TransportMetrics
}

func newManagerTransport(publisherID uuid.UUID) *managerTransport {
	return &managerTransport{
		publisherID: publisherID, published: make(chan SourceSnapshot, 16), closed: make(chan struct{}), failures: make(chan error, 1),
		metrics: TransportMetrics{ReconnectCount: 3},
	}
}

func (transport *managerTransport) Publish(_ context.Context, snapshot SourceSnapshot) error {
	transport.mu.Lock()
	panicPublish := transport.panicPublish
	transport.mu.Unlock()
	if panicPublish {
		panic("transport publish panic")
	}
	transport.published <- snapshot
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.publishError
}

func (transport *managerTransport) Close(context.Context) error {
	transport.closeOnce.Do(func() { close(transport.closed) })
	return nil
}

func (transport *managerTransport) Metrics() TransportMetrics {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	metrics := transport.metrics
	metrics.LastExternalRequestAt = cloneTime(metrics.LastExternalRequestAt)
	return metrics
}

func (transport *managerTransport) Failures() <-chan error { return transport.failures }

func (transport *managerTransport) setPublishError(err error) {
	transport.mu.Lock()
	transport.publishError = err
	transport.mu.Unlock()
}

func (transport *managerTransport) setPublishPanic(enabled bool) {
	transport.mu.Lock()
	transport.panicPublish = enabled
	transport.mu.Unlock()
}

func (transport *managerTransport) setMetrics(metrics TransportMetrics) {
	transport.mu.Lock()
	metrics.LastExternalRequestAt = cloneTime(metrics.LastExternalRequestAt)
	transport.metrics = metrics
	transport.mu.Unlock()
}

type managerTransportFactory struct {
	mu       sync.Mutex
	claims   []ListenerClaim
	newError error
	attempts int
	created  chan *managerTransport
}

func newManagerTransportFactory() *managerTransportFactory {
	return &managerTransportFactory{created: make(chan *managerTransport, 8)}
}

func (factory *managerTransportFactory) NewTransport(_ context.Context, entity Publisher) (Transport, error) {
	factory.mu.Lock()
	factory.attempts++
	err := factory.newError
	factory.mu.Unlock()
	if err != nil {
		return nil, err
	}
	transport := newManagerTransport(entity.ID)
	factory.created <- transport
	return transport, nil
}

func (factory *managerTransportFactory) ListenerClaims(Publisher) ([]ListenerClaim, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return append([]ListenerClaim(nil), factory.claims...), nil
}

func (factory *managerTransportFactory) setNewError(err error) {
	factory.mu.Lock()
	factory.newError = err
	factory.mu.Unlock()
}

func (factory *managerTransportFactory) attemptCount() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.attempts
}

func newTestPublisherManager(t *testing.T, repository Repository, sources UnifiedSourceReader, factories map[Type]*managerTransportFactory, options ...ManagerOption) *Manager {
	t.Helper()
	definitions, err := NewDefaultDefinitionRegistry()
	if err != nil {
		t.Fatalf("NewDefaultDefinitionRegistry() error = %v", err)
	}
	transports := NewTransportRegistry()
	for publisherType, factory := range factories {
		if err := transports.Register(publisherType, factory); err != nil {
			t.Fatalf("Register(%s) error = %v", publisherType, err)
		}
	}
	options = append([]ManagerOption{WithManagerReconcileInterval(0)}, options...)
	manager, err := NewManager(repository, sources, definitions, transports, options...)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func managerPublisher(id uuid.UUID, publisherType Type, config Config, sources []SourceSelection) Publisher {
	if publisherType == TypeMQTT {
		config = mqttTestConfigWithTrigger(config)
	} else if len(config) == 0 {
		if publisherType == TypeHTTPServer {
			config = Config(defaultHTTPConfigJSON)
		} else {
			config = Config(`{"trigger":{"mode":"interval","interval_ms":60000}}`)
		}
	}
	configVersion := uint(2)
	if publisherType == TypeHTTPServer || publisherType == TypeMQTT {
		configVersion = 3
	}
	return Publisher{ID: id, Type: publisherType, Name: publisherType.String() + " " + id.String(), Enabled: true, Config: config, ConfigVersion: configVersion, Sources: sources, SourceCount: len(sources)}
}

func mqttTestConfigWithTrigger(raw Config) Config {
	config := defaultMQTTPublisherConfig()
	if len(raw) != 0 {
		trigger, err := ParseTriggerConfig(raw)
		if err != nil {
			panic(err)
		}
		config.Trigger = trigger
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	normalized, err := normalizeMQTTPublisherConfig(encoded)
	if err != nil {
		panic(err)
	}
	return normalized
}

func (publisherType Type) String() string { return string(publisherType) }

func mutateManagerPublisher(repository *sourceTestPublisherRepository, id uuid.UUID, mutate func(*Publisher)) {
	repository.mu.Lock()
	entity := repository.entities[id]
	mutate(&entity)
	repository.entities[id] = clonePublisher(entity)
	repository.mu.Unlock()
}

func receiveManagerTransport(t *testing.T, transports <-chan *managerTransport) *managerTransport {
	t.Helper()
	select {
	case transport := <-transports:
		return transport
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport")
		return nil
	}
}

func assertNoManagerTransport(t *testing.T, transports <-chan *managerTransport) {
	t.Helper()
	select {
	case <-transports:
		t.Fatal("unexpected transport was created")
	case <-time.After(50 * time.Millisecond):
	}
}

func receiveManagerSubscription(t *testing.T, subscriptions <-chan *managerSubscription) *managerSubscription {
	t.Helper()
	select {
	case subscription := <-subscriptions:
		return subscription
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for source subscription")
		return nil
	}
}

func receiveSnapshot(t *testing.T, snapshots <-chan SourceSnapshot) SourceSnapshot {
	t.Helper()
	select {
	case snapshot := <-snapshots:
		return snapshot
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published snapshot")
		return SourceSnapshot{}
	}
}

func assertNoSnapshot(t *testing.T, snapshots <-chan SourceSnapshot) {
	t.Helper()
	select {
	case <-snapshots:
		t.Fatal("unexpected snapshot was published")
	case <-time.After(50 * time.Millisecond):
	}
}

func waitPublisherStatus(t *testing.T, manager *Manager, publisherID uuid.UUID, condition func(RuntimeStatus) bool) RuntimeStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status(publisherID)
		if condition(status) {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	status := manager.Status(publisherID)
	t.Fatalf("timed out waiting for Publisher status: %#v", status)
	return RuntimeStatus{}
}

func waitClosed(t *testing.T, closed <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
