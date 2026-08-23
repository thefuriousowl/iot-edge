package datalogger

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const defaultCommittedBatchBuffer = 64

var (
	ErrCommittedBatchReaderRequired = errors.New("committed Data Logger batch reader is required")
	ErrCommittedBatchBrokerRequired = errors.New("committed Data Logger batch broker is required")
	ErrCommittedBatchSubscriberLag  = errors.New("committed Data Logger batch subscriber lagged")
)

type LatestBatchReader interface {
	LatestBatch(context.Context, uuid.UUID) (*RawBatch, error)
}

type CommittedBatchPublisher interface {
	PublishCommittedBatch(RawBatch)
}

type CommittedBatchSubscription interface {
	Events() <-chan RawBatch
	Err() error
	Close()
}

type CommittedBatchFeed interface {
	Latest(context.Context, uuid.UUID) (*RawBatch, error)
	Subscribe(uuid.UUID) (CommittedBatchSubscription, error)
}

type CommittedBatchBrokerOption func(*CommittedBatchBroker) error

func WithCommittedBatchBuffer(size int) CommittedBatchBrokerOption {
	return func(broker *CommittedBatchBroker) error {
		if size < 1 {
			return ErrInvalidInput
		}
		broker.bufferSize = size
		return nil
	}
}

type CommittedBatchBroker struct {
	mu          sync.Mutex
	bufferSize  int
	subscribers map[*committedBatchSubscription]struct{}
}

type committedBatchSubscription struct {
	broker   *CommittedBatchBroker
	loggerID uuid.UUID
	events   chan RawBatch
	err      error
	closed   bool
}

type committedBatchFeed struct {
	reader LatestBatchReader
	broker *CommittedBatchBroker
}

func NewCommittedBatchBroker(options ...CommittedBatchBrokerOption) (*CommittedBatchBroker, error) {
	broker := &CommittedBatchBroker{bufferSize: defaultCommittedBatchBuffer, subscribers: make(map[*committedBatchSubscription]struct{})}
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

func NewCommittedBatchFeed(reader LatestBatchReader, broker *CommittedBatchBroker) (CommittedBatchFeed, error) {
	if isNilCommittedBatchDependency(reader) {
		return nil, ErrCommittedBatchReaderRequired
	}
	if broker == nil {
		return nil, ErrCommittedBatchBrokerRequired
	}
	return &committedBatchFeed{reader: reader, broker: broker}, nil
}

func (broker *CommittedBatchBroker) Subscribe(loggerID uuid.UUID) (CommittedBatchSubscription, error) {
	if broker == nil {
		return nil, ErrCommittedBatchBrokerRequired
	}
	if loggerID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	broker.mu.Lock()
	if broker.bufferSize < 1 {
		broker.bufferSize = defaultCommittedBatchBuffer
	}
	if broker.subscribers == nil {
		broker.subscribers = make(map[*committedBatchSubscription]struct{})
	}
	subscription := &committedBatchSubscription{broker: broker, loggerID: loggerID, events: make(chan RawBatch, broker.bufferSize)}
	broker.subscribers[subscription] = struct{}{}
	broker.mu.Unlock()
	return subscription, nil
}

func (broker *CommittedBatchBroker) PublishCommittedBatch(batch RawBatch) {
	if broker == nil || !validCommittedBatch(batch) {
		return
	}
	batch = cloneRawBatch(batch)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for subscription := range broker.subscribers {
		if subscription.loggerID != batch.LoggerID {
			continue
		}
		select {
		case subscription.events <- cloneRawBatch(batch):
		default:
			subscription.err = ErrCommittedBatchSubscriberLag
			broker.closeSubscriptionLocked(subscription)
		}
	}
}

func (feed *committedBatchFeed) Latest(ctx context.Context, loggerID uuid.UUID) (*RawBatch, error) {
	if feed == nil || ctx == nil || loggerID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	batch, err := feed.reader.LatestBatch(ctx, loggerID)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, ErrRawBatchNotFound
	}
	cloned := cloneRawBatch(*batch)
	return &cloned, nil
}

func (feed *committedBatchFeed) Subscribe(loggerID uuid.UUID) (CommittedBatchSubscription, error) {
	if feed == nil || feed.broker == nil {
		return nil, ErrCommittedBatchBrokerRequired
	}
	return feed.broker.Subscribe(loggerID)
}

func (subscription *committedBatchSubscription) Events() <-chan RawBatch {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *committedBatchSubscription) Err() error {
	if subscription == nil || subscription.broker == nil {
		return nil
	}
	subscription.broker.mu.Lock()
	defer subscription.broker.mu.Unlock()
	return subscription.err
}

func (subscription *committedBatchSubscription) Close() {
	if subscription == nil || subscription.broker == nil {
		return
	}
	subscription.broker.mu.Lock()
	subscription.broker.closeSubscriptionLocked(subscription)
	subscription.broker.mu.Unlock()
}

func (broker *CommittedBatchBroker) closeSubscriptionLocked(subscription *committedBatchSubscription) {
	if subscription.closed {
		return
	}
	delete(broker.subscribers, subscription)
	subscription.closed = true
	close(subscription.events)
}

func validCommittedBatch(batch RawBatch) bool {
	if batch.LoggerID == uuid.Nil || batch.BatchAt.IsZero() || len(batch.Samples) == 0 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(batch.Samples))
	for _, sample := range batch.Samples {
		if sample.TagID == uuid.Nil || sample.ObservedAt.IsZero() || !validHistoryDataType(sample.DataType) {
			return false
		}
		if _, duplicate := seen[sample.TagID]; duplicate {
			return false
		}
		seen[sample.TagID] = struct{}{}
		switch sample.Quality {
		case RawQualityGood:
			if sample.Error != "" || !validCommittedValue(sample.DataType, sample.Value) {
				return false
			}
		case RawQualityBad:
			if sample.Value != nil || strings.TrimSpace(sample.Error) == "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validCommittedValue(dataType string, value any) bool {
	switch dataType {
	case "bool":
		_, valid := value.(bool)
		return valid
	case "int16":
		_, valid := value.(int16)
		return valid
	case "uint16":
		_, valid := value.(uint16)
		return valid
	case "int32":
		_, valid := value.(int32)
		return valid
	case "uint32":
		_, valid := value.(uint32)
		return valid
	case "float32":
		_, valid := value.(float32)
		return valid
	case "float64":
		_, valid := value.(float64)
		return valid
	default:
		return false
	}
}

func cloneRawBatch(batch RawBatch) RawBatch {
	batch.BatchAt = batch.BatchAt.UTC()
	batch.Samples = append([]RawSample(nil), batch.Samples...)
	for index := range batch.Samples {
		batch.Samples[index].ObservedAt = batch.Samples[index].ObservedAt.UTC()
		if batch.Samples[index].Quality == RawQualityBad {
			batch.Samples[index].Error = strings.TrimSpace(batch.Samples[index].Error)
		}
	}
	return batch
}

func isNilCommittedBatchDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ CommittedBatchPublisher = (*CommittedBatchBroker)(nil)
var _ CommittedBatchFeed = (*committedBatchFeed)(nil)
