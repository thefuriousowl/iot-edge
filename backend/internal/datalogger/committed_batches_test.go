package datalogger

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCommittedBatchFeedValidatesDependenciesAndInput(t *testing.T) {
	t.Parallel()
	broker, err := NewCommittedBatchBroker()
	if err != nil {
		t.Fatalf("NewCommittedBatchBroker() error = %v", err)
	}
	var nilReader *committedBatchReader
	if _, err := NewCommittedBatchFeed(nil, broker); !errors.Is(err, ErrCommittedBatchReaderRequired) {
		t.Errorf("NewCommittedBatchFeed(nil) error = %v", err)
	}
	if _, err := NewCommittedBatchFeed(nilReader, broker); !errors.Is(err, ErrCommittedBatchReaderRequired) {
		t.Errorf("NewCommittedBatchFeed(typed nil) error = %v", err)
	}
	if _, err := NewCommittedBatchFeed(&committedBatchReader{}, nil); !errors.Is(err, ErrCommittedBatchBrokerRequired) {
		t.Errorf("NewCommittedBatchFeed(nil broker) error = %v", err)
	}
	if _, err := NewCommittedBatchBroker(WithCommittedBatchBuffer(0)); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("NewCommittedBatchBroker(invalid buffer) error = %v", err)
	}
	if _, err := broker.Subscribe(uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Subscribe(nil ID) error = %v", err)
	}
	feed, err := NewCommittedBatchFeed(&committedBatchReader{}, broker)
	if err != nil {
		t.Fatalf("NewCommittedBatchFeed() error = %v", err)
	}
	if _, err := feed.Latest(nil, uuid.New()); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Latest(nil context) error = %v", err)
	}
	if _, err := feed.Latest(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Latest(nil ID) error = %v", err)
	}
	var nilFeed *committedBatchFeed
	if _, err := nilFeed.Latest(context.Background(), uuid.New()); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nil feed Latest() error = %v", err)
	}
	if _, err := nilFeed.Subscribe(uuid.New()); !errors.Is(err, ErrCommittedBatchBrokerRequired) {
		t.Errorf("nil feed Subscribe() error = %v", err)
	}
	zeroBroker := &CommittedBatchBroker{}
	zeroSubscription, err := zeroBroker.Subscribe(uuid.New())
	if err != nil {
		t.Fatalf("zero-value broker Subscribe() error = %v", err)
	}
	zeroSubscription.Close()
}

func TestCommittedBatchBrokerFiltersAndSnapshotsImmutableBatches(t *testing.T) {
	t.Parallel()
	broker, _ := NewCommittedBatchBroker(WithCommittedBatchBuffer(2))
	loggerA, loggerB := uuid.New(), uuid.New()
	subscriptionA, _ := broker.Subscribe(loggerA)
	t.Cleanup(subscriptionA.Close)
	subscriptionB, _ := broker.Subscribe(loggerB)
	t.Cleanup(subscriptionB.Close)
	batchAt := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	tagID := uuid.New()
	batch := RawBatch{LoggerID: loggerA, BatchAt: batchAt, Samples: []RawSample{{TagID: tagID, ObservedAt: batchAt.Add(-time.Second), DataType: "float64", Value: float64(42.5), Quality: RawQualityGood}}}

	broker.PublishCommittedBatch(batch)
	batch.Samples[0].Value = float64(999)
	batch.Samples[0].Error = "mutated"
	received := awaitCommittedBatch(t, subscriptionA.Events())
	if received.LoggerID != loggerA || !received.BatchAt.Equal(batchAt.UTC()) || len(received.Samples) != 1 || received.Samples[0].Value != float64(42.5) || received.Samples[0].Error != "" || !received.Samples[0].ObservedAt.Equal(batchAt.Add(-time.Second).UTC()) {
		t.Errorf("received batch = %#v", received)
	}
	select {
	case unexpected := <-subscriptionB.Events():
		t.Fatalf("other Logger subscription received %#v", unexpected)
	default:
	}

	invalid := received
	invalid.Samples[0].Value = "not a float"
	broker.PublishCommittedBatch(invalid)
	select {
	case unexpected := <-subscriptionA.Events():
		t.Fatalf("invalid batch was published: %#v", unexpected)
	default:
	}
}

func TestCommittedBatchFeedReadsLatestWithoutAliasingReaderState(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	batchAt := time.Date(2026, time.August, 22, 5, 0, 0, 0, time.UTC)
	reader := &committedBatchReader{batch: &RawBatch{LoggerID: loggerID, BatchAt: batchAt, Samples: []RawSample{{TagID: uuid.New(), ObservedAt: batchAt, DataType: "uint16", Value: uint16(7), Quality: RawQualityGood}}}}
	broker, _ := NewCommittedBatchBroker()
	feed, _ := NewCommittedBatchFeed(reader, broker)

	first, err := feed.Latest(context.Background(), loggerID)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	first.Samples[0].Value = uint16(99)
	first.Samples = append(first.Samples, RawSample{})
	second, err := feed.Latest(context.Background(), loggerID)
	if err != nil {
		t.Fatalf("Latest(second) error = %v", err)
	}
	if reader.loggerID != loggerID || len(second.Samples) != 1 || second.Samples[0].Value != uint16(7) {
		t.Errorf("latest batch = %#v", second)
	}

	reader.batch = nil
	if _, err := feed.Latest(context.Background(), loggerID); !errors.Is(err, ErrRawBatchNotFound) {
		t.Errorf("Latest(nil batch) error = %v", err)
	}
	reader.err = context.Canceled
	if _, err := feed.Latest(context.Background(), loggerID); !errors.Is(err, context.Canceled) {
		t.Errorf("Latest(reader error) error = %v", err)
	}
}

func TestCommittedBatchBrokerDisconnectsLaggingSubscriberWithoutBlocking(t *testing.T) {
	t.Parallel()
	broker, _ := NewCommittedBatchBroker(WithCommittedBatchBuffer(1))
	loggerID := uuid.New()
	subscription, _ := broker.Subscribe(loggerID)
	batch := validTestRawBatch(loggerID, time.Now().UTC())
	broker.PublishCommittedBatch(batch)

	completed := make(chan struct{})
	go func() {
		broker.PublishCommittedBatch(validTestRawBatch(loggerID, batch.BatchAt.Add(time.Second)))
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("publishing blocked on lagging subscriber")
	}
	if !errors.Is(subscription.Err(), ErrCommittedBatchSubscriberLag) {
		t.Errorf("subscription error = %v", subscription.Err())
	}
	if received, open := <-subscription.Events(); !open || !received.BatchAt.Equal(batch.BatchAt) {
		t.Errorf("buffered event = %#v, open %t", received, open)
	}
	if _, open := <-subscription.Events(); open {
		t.Error("lagging subscription event channel remains open")
	}
	subscription.Close()
}

func TestCommittedBatchBrokerSupportsConcurrentPublishAndClose(t *testing.T) {
	broker, _ := NewCommittedBatchBroker(WithCommittedBatchBuffer(256))
	loggerID := uuid.New()
	const subscriberCount = 16
	subscriptions := make([]CommittedBatchSubscription, 0, subscriberCount)
	for range subscriberCount {
		subscription, err := broker.Subscribe(loggerID)
		if err != nil {
			t.Fatalf("Subscribe() error = %v", err)
		}
		subscriptions = append(subscriptions, subscription)
	}
	var waitGroup sync.WaitGroup
	for index, subscription := range subscriptions {
		waitGroup.Add(1)
		go func(index int, subscription CommittedBatchSubscription) {
			defer waitGroup.Done()
			if index%2 == 0 {
				subscription.Close()
				return
			}
			for range subscription.Events() {
			}
		}(index, subscription)
	}
	for index := range 128 {
		broker.PublishCommittedBatch(validTestRawBatch(loggerID, time.Now().UTC().Add(time.Duration(index)*time.Millisecond)))
	}
	for _, subscription := range subscriptions {
		subscription.Close()
	}
	waitGroup.Wait()
}

type committedBatchReader struct {
	loggerID uuid.UUID
	batch    *RawBatch
	err      error
}

func (reader *committedBatchReader) LatestBatch(_ context.Context, loggerID uuid.UUID) (*RawBatch, error) {
	reader.loggerID = loggerID
	return reader.batch, reader.err
}

func validTestRawBatch(loggerID uuid.UUID, batchAt time.Time) RawBatch {
	return RawBatch{LoggerID: loggerID, BatchAt: batchAt, Samples: []RawSample{{TagID: uuid.New(), ObservedAt: batchAt, DataType: "float64", Value: float64(1), Quality: RawQualityGood}}}
}

func awaitCommittedBatch(t *testing.T, events <-chan RawBatch) RawBatch {
	t.Helper()
	select {
	case batch := <-events:
		return batch
	case <-time.After(time.Second):
		t.Fatal("committed batch was not published")
		return RawBatch{}
	}
}

var _ LatestBatchReader = (*committedBatchReader)(nil)
