package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultOutputBuffer = 64

var (
	ErrOutputRepositoryRequired         = errors.New("Plugin output repository is required")
	ErrOutputDescriptorResolverRequired = errors.New("Plugin output descriptor resolver is required")
	ErrOutputBrokerRequired             = errors.New("Plugin output broker is required")
	ErrOutputBatchNotFound              = errors.New("Plugin output batch not found")
	ErrOutputBatchNotNewer              = errors.New("Plugin output batch is not newer than the persisted batch")
	ErrOutputSubscriberLag              = errors.New("Plugin output subscriber lagged")
)

type OutputBatchRepository interface {
	StoreLatest(context.Context, OutputBatch) (*OutputBatch, bool, error)
	Latest(context.Context, uuid.UUID) (*OutputBatch, error)
}

type OutputDescriptorResolver interface {
	OutputDescriptors(context.Context, uuid.UUID) ([]OutputDescriptor, error)
}

type OutputSink interface {
	Publish(context.Context, []OutputValue) (*OutputBatch, error)
}

type OutputBatchPublisher interface {
	PublishOutputBatch(OutputBatch)
}

type OutputSubscription interface {
	Events() <-chan OutputBatch
	Err() error
	Close()
}

type OutputEventBroker interface {
	OutputBatchPublisher
	Subscribe(uuid.UUID) (OutputSubscription, error)
}

type OutputFeed interface {
	Latest(context.Context, uuid.UUID) (*OutputBatch, error)
	Subscribe(uuid.UUID) (OutputSubscription, error)
}

type OutputBrokerOption func(*OutputBroker) error

func WithOutputBuffer(size int) OutputBrokerOption {
	return func(broker *OutputBroker) error {
		if size < 1 {
			return ErrInvalidInput
		}
		broker.bufferSize = size
		return nil
	}
}

type OutputBroker struct {
	mu          sync.Mutex
	bufferSize  int
	subscribers map[*outputSubscription]struct{}
}

type outputSubscription struct {
	broker     *OutputBroker
	instanceID uuid.UUID
	events     chan OutputBatch
	err        error
	closed     bool
}

func NewOutputBroker(options ...OutputBrokerOption) (*OutputBroker, error) {
	broker := &OutputBroker{bufferSize: defaultOutputBuffer, subscribers: make(map[*outputSubscription]struct{})}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(broker); err != nil {
			return nil, err
		}
	}
	return broker, nil
}

func (broker *OutputBroker) Subscribe(instanceID uuid.UUID) (OutputSubscription, error) {
	if broker == nil {
		return nil, ErrOutputBrokerRequired
	}
	if instanceID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	broker.mu.Lock()
	if broker.bufferSize < 1 {
		broker.bufferSize = defaultOutputBuffer
	}
	if broker.subscribers == nil {
		broker.subscribers = make(map[*outputSubscription]struct{})
	}
	subscription := &outputSubscription{broker: broker, instanceID: instanceID, events: make(chan OutputBatch, broker.bufferSize)}
	broker.subscribers[subscription] = struct{}{}
	broker.mu.Unlock()
	return subscription, nil
}

func (broker *OutputBroker) PublishOutputBatch(batch OutputBatch) {
	if broker == nil {
		return
	}
	normalized, valid := normalizePublishedOutputBatch(batch)
	if !valid {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for subscription := range broker.subscribers {
		if subscription.instanceID != normalized.InstanceID {
			continue
		}
		select {
		case subscription.events <- CloneOutputBatch(normalized):
		default:
			subscription.err = ErrOutputSubscriberLag
			broker.closeSubscriptionLocked(subscription)
		}
	}
}

func (subscription *outputSubscription) Events() <-chan OutputBatch {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *outputSubscription) Err() error {
	if subscription == nil || subscription.broker == nil {
		return nil
	}
	subscription.broker.mu.Lock()
	defer subscription.broker.mu.Unlock()
	return subscription.err
}

func (subscription *outputSubscription) Close() {
	if subscription == nil || subscription.broker == nil {
		return
	}
	subscription.broker.mu.Lock()
	subscription.broker.closeSubscriptionLocked(subscription)
	subscription.broker.mu.Unlock()
}

func (broker *OutputBroker) closeSubscriptionLocked(subscription *outputSubscription) {
	if subscription.closed {
		return
	}
	delete(broker.subscribers, subscription)
	subscription.closed = true
	close(subscription.events)
}

type OutputStoreOption func(*OutputStore) error

func WithOutputStoreClock(clock func() time.Time) OutputStoreOption {
	return func(store *OutputStore) error {
		if clock == nil {
			return ErrInvalidInput
		}
		store.now = clock
		return nil
	}
}

type OutputStore struct {
	repository OutputBatchRepository
	resolver   OutputDescriptorResolver
	broker     OutputEventBroker
	now        func() time.Time
}

type scopedOutputSink struct {
	store       *OutputStore
	instanceID  uuid.UUID
	descriptors []OutputDescriptor
}

func NewOutputStore(repository OutputBatchRepository, resolver OutputDescriptorResolver, broker OutputEventBroker, options ...OutputStoreOption) (*OutputStore, error) {
	if isNil(repository) {
		return nil, ErrOutputRepositoryRequired
	}
	if isNil(resolver) {
		return nil, ErrOutputDescriptorResolverRequired
	}
	if isNil(broker) {
		return nil, ErrOutputBrokerRequired
	}
	store := &OutputStore{repository: repository, resolver: resolver, broker: broker, now: time.Now}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(store); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *OutputStore) ScopeCapability(instanceID uuid.UUID, manifest Manifest) (any, error) {
	if store == nil || instanceID == uuid.Nil || len(manifest.Outputs) == 0 {
		return nil, ErrInvalidInput
	}
	if err := ValidateOutputDescriptors(manifest.Outputs); err != nil {
		return nil, err
	}
	return &scopedOutputSink{store: store, instanceID: instanceID, descriptors: append([]OutputDescriptor(nil), manifest.Outputs...)}, nil
}

func (sink *scopedOutputSink) Publish(ctx context.Context, values []OutputValue) (*OutputBatch, error) {
	if sink == nil || sink.store == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	candidate := OutputBatch{
		InstanceID:  sink.instanceID,
		Sequence:    1,
		PublishedAt: sink.store.now().UTC(),
		Values:      values,
	}
	normalized, err := normalizeCompleteOutputBatch(candidate, sink.descriptors)
	if err != nil {
		return nil, err
	}
	stored, accepted, err := sink.store.repository.StoreLatest(ctx, *normalized)
	if err != nil {
		return nil, err
	}
	if !accepted || stored == nil {
		return nil, ErrOutputBatchNotNewer
	}
	stored, err = normalizeCompleteOutputBatch(*stored, sink.descriptors)
	if err != nil {
		return nil, err
	}
	publishOutputBatch(sink.store.broker, *stored)
	cloned := CloneOutputBatch(*stored)
	return &cloned, nil
}

func (store *OutputStore) Latest(ctx context.Context, instanceID uuid.UUID) (*OutputBatch, error) {
	if store == nil || ctx == nil || instanceID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	batch, err := store.repository.Latest(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, ErrOutputBatchNotFound
	}
	descriptors, err := store.resolver.OutputDescriptors(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeCompleteOutputBatch(*batch, descriptors)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func (store *OutputStore) Subscribe(instanceID uuid.UUID) (OutputSubscription, error) {
	if store == nil || isNil(store.broker) {
		return nil, ErrOutputBrokerRequired
	}
	return store.broker.Subscribe(instanceID)
}

func normalizeCompleteOutputBatch(batch OutputBatch, descriptors []OutputDescriptor) (*OutputBatch, error) {
	normalized, err := NormalizeOutputBatch(batch, descriptors)
	if err != nil {
		return nil, err
	}
	if len(descriptors) == 0 || len(normalized.Values) != len(descriptors) {
		return nil, fmt.Errorf("%w: complete output set is required", ErrInvalidOutputBatch)
	}
	observedAt := normalized.Values[0].ObservedAt
	for _, value := range normalized.Values[1:] {
		if !value.ObservedAt.Equal(observedAt) {
			return nil, fmt.Errorf("%w: synchronized output set is required", ErrInvalidOutputBatch)
		}
	}
	return &normalized, nil
}

func normalizePublishedOutputBatch(batch OutputBatch) (OutputBatch, bool) {
	descriptors := make([]OutputDescriptor, len(batch.Values))
	for index, value := range batch.Values {
		periodKind := OutputPeriodWindowed
		if value.PeriodStart.Equal(value.PeriodEnd) && value.PeriodStart.Equal(value.ObservedAt) {
			periodKind = OutputPeriodInstantaneous
		}
		descriptors[index] = OutputDescriptor{
			Key: value.Key, Name: string(value.Key), SchemaVersion: value.SchemaVersion,
			DataType: value.DataType, Unit: value.Unit, PeriodKind: periodKind,
		}
	}
	normalized, err := NormalizeOutputBatch(batch, descriptors)
	return normalized, err == nil
}

func publishOutputBatch(publisher OutputBatchPublisher, batch OutputBatch) {
	defer func() { _ = recover() }()
	publisher.PublishOutputBatch(batch)
}

var (
	_ capabilityScoper     = (*OutputStore)(nil)
	_ OutputFeed           = (*OutputStore)(nil)
	_ OutputSink           = (*scopedOutputSink)(nil)
	_ OutputEventBroker    = (*OutputBroker)(nil)
	_ OutputBatchPublisher = (*OutputBroker)(nil)
)
