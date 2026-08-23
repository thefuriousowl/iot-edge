package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewOutputStoreAndBrokerValidateDependenciesAndInput(t *testing.T) {
	t.Parallel()

	broker, err := NewOutputBroker()
	if err != nil {
		t.Fatalf("NewOutputBroker() error = %v", err)
	}
	repository := newMemoryOutputRepository()
	resolver := &memoryOutputResolver{}
	var nilRepository *memoryOutputRepository
	var nilResolver *memoryOutputResolver
	var nilBroker *OutputBroker
	for _, test := range []struct {
		name       string
		repository OutputBatchRepository
		resolver   OutputDescriptorResolver
		broker     OutputEventBroker
		want       error
	}{
		{name: "nil repository", resolver: resolver, broker: broker, want: ErrOutputRepositoryRequired},
		{name: "typed nil repository", repository: nilRepository, resolver: resolver, broker: broker, want: ErrOutputRepositoryRequired},
		{name: "nil resolver", repository: repository, broker: broker, want: ErrOutputDescriptorResolverRequired},
		{name: "typed nil resolver", repository: repository, resolver: nilResolver, broker: broker, want: ErrOutputDescriptorResolverRequired},
		{name: "nil broker", repository: repository, resolver: resolver, want: ErrOutputBrokerRequired},
		{name: "typed nil broker", repository: repository, resolver: resolver, broker: nilBroker, want: ErrOutputBrokerRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewOutputStore(test.repository, test.resolver, test.broker); !errors.Is(err, test.want) {
				t.Fatalf("NewOutputStore() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := NewOutputStore(repository, resolver, broker, WithOutputStoreClock(nil)); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("NewOutputStore(nil clock) error = %v", err)
	}
	if _, err := NewOutputBroker(WithOutputBuffer(0)); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("NewOutputBroker(invalid buffer) error = %v", err)
	}
	if _, err := broker.Subscribe(uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Subscribe(nil ID) error = %v", err)
	}
	if _, err := (*OutputBroker)(nil).Subscribe(uuid.New()); !errors.Is(err, ErrOutputBrokerRequired) {
		t.Errorf("nil broker Subscribe() error = %v", err)
	}
	zeroBroker := &OutputBroker{}
	subscription, err := zeroBroker.Subscribe(uuid.New())
	if err != nil {
		t.Fatalf("zero broker Subscribe() error = %v", err)
	}
	subscription.Close()
}

func TestOutputStoreScopesPublishToInstancePersistsBeforeEventAndHydratesLatest(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	otherID := uuid.New()
	publishedAt := time.Date(2026, time.August, 23, 13, 0, 1, 0, time.UTC)
	descriptors := []OutputDescriptor{
		validOutputDescriptor("demand_kw"),
		validWindowedDescriptor("energy_kwh"),
	}
	repository := newMemoryOutputRepository()
	resolver := &memoryOutputResolver{descriptors: map[uuid.UUID][]OutputDescriptor{instanceID: descriptors}}
	broker, _ := NewOutputBroker(WithOutputBuffer(4))
	store, err := NewOutputStore(repository, resolver, broker, WithOutputStoreClock(func() time.Time { return publishedAt }))
	if err != nil {
		t.Fatalf("NewOutputStore() error = %v", err)
	}
	host, _ := NewCapabilityHost(map[Capability]any{CapabilityPluginOutputsPublish: store, "core.secret": "secret"})
	manifest := Manifest{Type: "energy_management", Capabilities: []Capability{CapabilityPluginOutputsPublish}, Outputs: append([]OutputDescriptor(nil), descriptors...)}
	scoped, err := newScopedHost(host, instanceID, manifest)
	if err != nil {
		t.Fatalf("newScopedHost() error = %v", err)
	}
	resolved, exists := scoped.ResolveCapability(CapabilityPluginOutputsPublish)
	sink, valid := resolved.(OutputSink)
	if !exists || !valid {
		t.Fatalf("resolved output sink = %T, exists %t", resolved, exists)
	}
	if resolved == store {
		t.Fatal("runtime received global output store instead of scoped sink")
	}
	if value, exists := scoped.ResolveCapability("core.secret"); exists || value != nil {
		t.Fatalf("runtime received undeclared core capability = %#v, %t", value, exists)
	}
	manifest.Outputs[0].DataType = OutputDataTypeString

	subscription, _ := store.Subscribe(instanceID)
	t.Cleanup(subscription.Close)
	otherSubscription, _ := store.Subscribe(otherID)
	t.Cleanup(otherSubscription.Close)
	observedAt := publishedAt.Add(-time.Second)
	coverage := 92.5
	values := []OutputValue{
		{
			Key: "demand_kw", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Unit: "kW", Value: 44.5,
			Quality: OutputQualityGood, ObservedAt: observedAt, PeriodStart: observedAt, PeriodEnd: observedAt,
		},
		{
			Key: "energy_kwh", SchemaVersion: 1, DataType: OutputDataTypeFloat64, Unit: "kW", Value: 12.25,
			Quality: OutputQualityPartial, ObservedAt: observedAt, PeriodStart: observedAt.Add(-time.Hour), PeriodEnd: observedAt,
			CoveragePercent: &coverage, Issues: []OutputIssue{{Code: "gap", Message: "One source interval is missing"}},
			Attributes: map[string]string{"timezone": "Asia/Bangkok"},
		},
	}
	stored, err := sink.Publish(context.Background(), values)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if stored.InstanceID != instanceID || stored.Sequence != 1 || !stored.PublishedAt.Equal(publishedAt) {
		t.Fatalf("stored batch = %#v", stored)
	}
	coverage = 1
	values[1].Attributes["timezone"] = "UTC"
	values[1].Issues[0].Message = "mutated"
	event := awaitOutputBatch(t, subscription.Events())
	if event.InstanceID != instanceID || event.Sequence != 1 || *event.Values[1].CoveragePercent != 92.5 || event.Values[1].Attributes["timezone"] != "Asia/Bangkok" || event.Values[1].Issues[0].Message != "One source interval is missing" {
		t.Fatalf("published event = %#v", event)
	}
	select {
	case unexpected := <-otherSubscription.Events():
		t.Fatalf("other instance received %#v", unexpected)
	default:
	}

	stored.Values[0].Value = 999.0
	latest, err := store.Latest(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.Values[0].Value != 44.5 || latest.Values[1].Attributes["timezone"] != "Asia/Bangkok" {
		t.Fatalf("latest batch aliases caller state: %#v", latest)
	}
	latest.Values[1].Attributes["timezone"] = "changed"
	restarted, err := store.Latest(context.Background(), instanceID)
	if err != nil || restarted.Values[1].Attributes["timezone"] != "Asia/Bangkok" {
		t.Fatalf("restart hydration = %#v, %v", restarted, err)
	}
	if resolver.instanceID != instanceID {
		t.Errorf("descriptor resolver instance = %s, want %s", resolver.instanceID, instanceID)
	}
}

func TestOutputStoreRejectsIncompleteDuplicateRegressingAndInvalidBatches(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	publishedAt := time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
	descriptors := []OutputDescriptor{validOutputDescriptor("a"), validOutputDescriptor("b")}
	repository := newMemoryOutputRepository()
	resolver := &memoryOutputResolver{descriptors: map[uuid.UUID][]OutputDescriptor{instanceID: descriptors}}
	broker, _ := NewOutputBroker(WithOutputBuffer(2))
	store, _ := NewOutputStore(repository, resolver, broker, WithOutputStoreClock(func() time.Time { return publishedAt }))
	resolved, _ := store.ScopeCapability(instanceID, Manifest{Outputs: descriptors})
	sink := resolved.(OutputSink)
	at := publishedAt.Add(-time.Second)
	values := []OutputValue{validOutputValue("a", at, 1), validOutputValue("b", at, 2)}
	if _, err := sink.Publish(context.Background(), values[:1]); !errors.Is(err, ErrInvalidOutputBatch) {
		t.Fatalf("Publish(incomplete) error = %v", err)
	}
	if repository.storeCalls != 0 {
		t.Fatalf("incomplete batch reached repository %d times", repository.storeCalls)
	}
	unsynchronized := CloneOutputBatch(OutputBatch{Values: values}).Values
	unsynchronized[1].ObservedAt = at.Add(time.Millisecond)
	unsynchronized[1].PeriodStart = unsynchronized[1].ObservedAt
	unsynchronized[1].PeriodEnd = unsynchronized[1].ObservedAt
	if _, err := sink.Publish(context.Background(), unsynchronized); !errors.Is(err, ErrInvalidOutputBatch) {
		t.Fatalf("Publish(unsynchronized) error = %v", err)
	}
	first, err := sink.Publish(context.Background(), values)
	if err != nil || first.Sequence != 1 {
		t.Fatalf("Publish(first) = %#v, %v", first, err)
	}
	if _, err := sink.Publish(context.Background(), values); !errors.Is(err, ErrOutputBatchNotNewer) {
		t.Fatalf("Publish(duplicate) error = %v", err)
	}
	regressing := []OutputValue{validOutputValue("a", at.Add(-time.Second), 3), validOutputValue("b", at.Add(-time.Second), 4)}
	if _, err := sink.Publish(context.Background(), regressing); !errors.Is(err, ErrOutputBatchNotNewer) {
		t.Fatalf("Publish(regressing) error = %v", err)
	}
	newerAt := at.Add(time.Millisecond)
	newer := []OutputValue{validOutputValue("a", newerAt, 5), validOutputValue("b", newerAt, 6)}
	second, err := sink.Publish(context.Background(), newer)
	if err != nil || second.Sequence != 2 {
		t.Fatalf("Publish(newer) = %#v, %v", second, err)
	}
	if _, err := store.Latest(nil, instanceID); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Latest(nil context) error = %v", err)
	}
	if _, err := store.Latest(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Latest(nil ID) error = %v", err)
	}
	if _, err := store.Latest(context.Background(), uuid.New()); !errors.Is(err, ErrOutputBatchNotFound) {
		t.Errorf("Latest(missing) error = %v", err)
	}
	if _, err := store.ScopeCapability(uuid.Nil, Manifest{Outputs: descriptors}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ScopeCapability(nil ID) error = %v", err)
	}
	if _, err := store.ScopeCapability(instanceID, Manifest{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ScopeCapability(no descriptors) error = %v", err)
	}
}

func TestOutputStoreDoesNotLosePersistenceWhenBrokerPanics(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	at := time.Date(2026, time.August, 23, 15, 0, 0, 0, time.UTC)
	descriptors := []OutputDescriptor{validOutputDescriptor("metric")}
	repository := newMemoryOutputRepository()
	resolver := &memoryOutputResolver{descriptors: map[uuid.UUID][]OutputDescriptor{instanceID: descriptors}}
	store, err := NewOutputStore(repository, resolver, panickingOutputBroker{}, WithOutputStoreClock(func() time.Time { return at.Add(time.Second) }))
	if err != nil {
		t.Fatalf("NewOutputStore() error = %v", err)
	}
	resolved, _ := store.ScopeCapability(instanceID, Manifest{Outputs: descriptors})
	stored, err := resolved.(OutputSink).Publish(context.Background(), []OutputValue{validOutputValue("metric", at, 1)})
	if err != nil || stored == nil || stored.Sequence != 1 {
		t.Fatalf("Publish() = %#v, %v", stored, err)
	}
	latest, err := repository.Latest(context.Background(), instanceID)
	if err != nil || latest == nil || latest.Sequence != 1 {
		t.Fatalf("persisted latest = %#v, %v", latest, err)
	}
}

func TestOutputBrokerFiltersSnapshotsAndDisconnectsLaggingSubscriber(t *testing.T) {
	t.Parallel()

	broker, _ := NewOutputBroker(WithOutputBuffer(1))
	instanceID := uuid.New()
	otherID := uuid.New()
	subscription, _ := broker.Subscribe(instanceID)
	other, _ := broker.Subscribe(otherID)
	t.Cleanup(other.Close)
	at := time.Date(2026, time.August, 23, 16, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	batch := OutputBatch{InstanceID: instanceID, Sequence: 1, PublishedAt: at.Add(time.Second), Values: []OutputValue{validOutputValue("metric", at, 1)}}
	broker.PublishOutputBatch(batch)
	batch.Values[0].Value = 999.0
	batch.Values[0].Attributes = map[string]string{"mutated": "true"}
	broker.PublishOutputBatch(OutputBatch{InstanceID: instanceID})
	broker.PublishOutputBatch(OutputBatch{InstanceID: otherID, Sequence: 1, PublishedAt: at.Add(time.Second), Values: []OutputValue{validOutputValue("metric", at, 2)}})
	select {
	case unexpected := <-other.Events():
		if unexpected.InstanceID != otherID {
			t.Fatalf("other subscriber received wrong event %#v", unexpected)
		}
	default:
		t.Fatal("other subscriber did not receive its event")
	}

	done := make(chan struct{})
	go func() {
		broker.PublishOutputBatch(OutputBatch{InstanceID: instanceID, Sequence: 2, PublishedAt: at.Add(2 * time.Second), Values: []OutputValue{validOutputValue("metric", at.Add(time.Second), 2)}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on lagging subscriber")
	}
	if !errors.Is(subscription.Err(), ErrOutputSubscriberLag) {
		t.Errorf("subscription error = %v", subscription.Err())
	}
	first, open := <-subscription.Events()
	if !open || first.Values[0].Value != 1.0 || first.PublishedAt.Location() != time.UTC || first.Values[0].Attributes != nil {
		t.Fatalf("buffered event = %#v, open %t", first, open)
	}
	if _, open := <-subscription.Events(); open {
		t.Error("lagging subscription remains open")
	}
	subscription.Close()
}

func TestOutputBrokerSupportsConcurrentPublishAndClose(t *testing.T) {
	broker, _ := NewOutputBroker(WithOutputBuffer(256))
	instanceID := uuid.New()
	const subscriberCount = 16
	subscriptions := make([]OutputSubscription, 0, subscriberCount)
	for range subscriberCount {
		subscription, err := broker.Subscribe(instanceID)
		if err != nil {
			t.Fatalf("Subscribe() error = %v", err)
		}
		subscriptions = append(subscriptions, subscription)
	}
	var waitGroup sync.WaitGroup
	for index, subscription := range subscriptions {
		waitGroup.Add(1)
		go func(index int, subscription OutputSubscription) {
			defer waitGroup.Done()
			if index%2 == 0 {
				subscription.Close()
				return
			}
			for range subscription.Events() {
			}
		}(index, subscription)
	}
	start := time.Now().UTC()
	for index := range 128 {
		at := start.Add(time.Duration(index) * time.Millisecond)
		broker.PublishOutputBatch(OutputBatch{InstanceID: instanceID, Sequence: uint64(index + 1), PublishedAt: at.Add(time.Second), Values: []OutputValue{validOutputValue("metric", at, float64(index))}})
	}
	for _, subscription := range subscriptions {
		subscription.Close()
	}
	waitGroup.Wait()
}

type memoryOutputRepository struct {
	mu         sync.Mutex
	batches    map[uuid.UUID]OutputBatch
	err        error
	storeCalls int
}

func newMemoryOutputRepository() *memoryOutputRepository {
	return &memoryOutputRepository{batches: make(map[uuid.UUID]OutputBatch)}
}

func (repository *memoryOutputRepository) StoreLatest(_ context.Context, batch OutputBatch) (*OutputBatch, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.storeCalls++
	if repository.err != nil {
		return nil, false, repository.err
	}
	current, exists := repository.batches[batch.InstanceID]
	if exists && !outputBatchSourceAt(batch).After(outputBatchSourceAt(current)) {
		return nil, false, nil
	}
	stored := CloneOutputBatch(batch)
	stored.Sequence = 1
	if exists {
		stored.Sequence = current.Sequence + 1
	}
	repository.batches[batch.InstanceID] = stored
	cloned := CloneOutputBatch(stored)
	return &cloned, true, nil
}

func (repository *memoryOutputRepository) Latest(_ context.Context, instanceID uuid.UUID) (*OutputBatch, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.err != nil {
		return nil, repository.err
	}
	batch, exists := repository.batches[instanceID]
	if !exists {
		return nil, ErrOutputBatchNotFound
	}
	cloned := CloneOutputBatch(batch)
	return &cloned, nil
}

type memoryOutputResolver struct {
	descriptors map[uuid.UUID][]OutputDescriptor
	instanceID  uuid.UUID
	err         error
}

func (resolver *memoryOutputResolver) OutputDescriptors(_ context.Context, instanceID uuid.UUID) ([]OutputDescriptor, error) {
	resolver.instanceID = instanceID
	if resolver.err != nil {
		return nil, resolver.err
	}
	return append([]OutputDescriptor(nil), resolver.descriptors[instanceID]...), nil
}

type panickingOutputBroker struct{}

func (panickingOutputBroker) PublishOutputBatch(OutputBatch) { panic("broker panic") }

func (panickingOutputBroker) Subscribe(uuid.UUID) (OutputSubscription, error) {
	return nil, errors.New("not implemented")
}

func validOutputValue(key OutputKey, at time.Time, value float64) OutputValue {
	return OutputValue{
		Key: key, SchemaVersion: 1, DataType: OutputDataTypeFloat64, Unit: "kW", Value: value,
		Quality: OutputQualityGood, ObservedAt: at, PeriodStart: at, PeriodEnd: at,
	}
}

func awaitOutputBatch(t *testing.T, events <-chan OutputBatch) OutputBatch {
	t.Helper()
	select {
	case batch := <-events:
		return batch
	case <-time.After(time.Second):
		t.Fatal("Plugin output batch was not published")
		return OutputBatch{}
	}
}

var (
	_ OutputBatchRepository    = (*memoryOutputRepository)(nil)
	_ OutputDescriptorResolver = (*memoryOutputResolver)(nil)
	_ OutputEventBroker        = panickingOutputBroker{}
)
