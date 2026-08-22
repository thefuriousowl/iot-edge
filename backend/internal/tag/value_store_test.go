package tag

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryValueStoreStoresLatestTypedValues(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	tagID := uuid.New()
	firstObservedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	first, err := store.Put(TagValue{TagID: tagID, ObservedAt: firstObservedAt, Quality: ValueQualityGood, DataType: DataTypeFloat32, Value: float64(12.5), Error: "stale"})
	if err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	if first.Sequence != 1 || first.StoredAt.IsZero() || first.Value != float32(12.5) || first.Error != "" {
		t.Errorf("first = %#v", first)
	}
	secondObservedAt := firstObservedAt.Add(time.Second)
	second, err := store.Put(TagValue{TagID: tagID, ObservedAt: secondObservedAt, Quality: ValueQualityGood, DataType: DataTypeFloat32, Value: float32(13.5)})
	if err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}
	latest, exists := store.Latest(tagID)
	if !exists || latest != second || latest.Sequence != 2 {
		t.Errorf("Latest() = %#v, %v", latest, exists)
	}
}

func TestMemoryValueStoreSupportsZeroValue(t *testing.T) {
	t.Parallel()
	var store MemoryValueStore
	tagID := uuid.New()
	stored, err := store.Put(TagValue{TagID: tagID, ObservedAt: time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC), Quality: ValueQualityGood, DataType: DataTypeBool, Value: true})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	latest, exists := store.Latest(tagID)
	if !exists || latest != stored {
		t.Errorf("Latest() = %#v, %v", latest, exists)
	}
}

func TestMemoryValueStoreRestoresLatestValuesAndContinuesSequence(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	firstID := uuid.New()
	secondID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	storedAt := observedAt.Add(time.Second)
	values := []TagValue{
		{TagID: firstID, Sequence: 4, ObservedAt: observedAt, StoredAt: storedAt, Quality: ValueQualityGood, DataType: DataTypeBool, Value: true},
		{TagID: secondID, Sequence: 9, ObservedAt: observedAt, StoredAt: storedAt, Quality: ValueQualityBad, DataType: DataTypeFloat64, Error: "connection lost"},
	}
	if err := store.RestoreLatest(values); err != nil {
		t.Fatalf("RestoreLatest() error = %v", err)
	}
	first, exists := store.Latest(firstID)
	if !exists || first.Sequence != 4 || first.StoredAt != storedAt || first.Value != true {
		t.Errorf("restored first = %#v, exists = %t", first, exists)
	}
	if history := store.History(secondID, 10); len(history) != 1 || history[0].Error != "connection lost" {
		t.Errorf("restored history = %#v", history)
	}
	next, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt.Add(2 * time.Second), Quality: ValueQualityGood, DataType: DataTypeBool, Value: false})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if next.Sequence != 10 {
		t.Errorf("next sequence = %d, want 10", next.Sequence)
	}
	if err := store.RestoreLatest(nil); !errors.Is(err, ErrTagValueStoreInUse) {
		t.Errorf("RestoreLatest(in-use) error = %v, want %v", err, ErrTagValueStoreInUse)
	}
}

func TestMemoryValueStoreRejectsInvalidRestoreBatchAtomically(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	tagID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	err := store.RestoreLatest([]TagValue{
		{TagID: tagID, Sequence: 1, ObservedAt: observedAt, StoredAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 1},
		{TagID: tagID, Sequence: 2, ObservedAt: observedAt, StoredAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 2},
	})
	if !errors.Is(err, ErrInvalidTagValue) {
		t.Fatalf("RestoreLatest(duplicate) error = %v", err)
	}
	if _, exists := store.Latest(tagID); exists {
		t.Error("invalid restore batch mutated latest values")
	}
}

func TestMemoryValueStoreSnapshotsAreStableAndResolvable(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	firstID := uuid.New()
	secondID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	if _, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 2}); err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	if _, err := store.Put(TagValue{TagID: secondID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 3}); err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}
	snapshot := store.Snapshot([]uuid.UUID{firstID, secondID})
	if _, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt.Add(time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 20}); err != nil {
		t.Fatalf("Put(update) error = %v", err)
	}
	first, err := snapshot.Resolve(context.Background(), firstID)
	if err != nil || first != uint16(2) {
		t.Errorf("Resolve(first) = %#v, %v", first, err)
	}
	second, err := snapshot.Resolve(context.Background(), secondID)
	if err != nil || second != uint16(3) {
		t.Errorf("Resolve(second) = %#v, %v", second, err)
	}
}

func TestMemoryValueStoreRetainsTenValuesAndPublishesFilteredUpdates(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	firstID := uuid.New()
	secondID := uuid.New()
	if empty := store.History(firstID, DefaultTagValueHistoryLimit); empty == nil || len(empty) != 0 {
		t.Fatalf("History(empty) = %#v, want non-nil empty slice", empty)
	}
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	for value := 1; value <= 12; value++ {
		if _, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt.Add(time.Duration(value) * time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: value}); err != nil {
			t.Fatalf("Put(%d) error = %v", value, err)
		}
	}
	history := store.History(firstID, DefaultTagValueHistoryLimit)
	if len(history) != 10 || history[0].Value != uint16(3) || history[9].Value != uint16(12) {
		t.Errorf("History() = %#v", history)
	}
	if limited := store.History(firstID, 3); len(limited) != 3 || limited[0].Value != uint16(10) {
		t.Errorf("History(limit 3) = %#v", limited)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, unsubscribe := store.Subscribe(ctx, []uuid.UUID{firstID})
	defer unsubscribe()
	select {
	case initial := <-stream:
		if initial.Value != uint16(12) {
			t.Errorf("initial subscription value = %#v", initial)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not receive current value")
	}
	if _, err := store.Put(TagValue{TagID: secondID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 99}); err != nil {
		t.Fatalf("Put(other) error = %v", err)
	}
	select {
	case unexpected := <-stream:
		t.Fatalf("filtered subscription received %#v", unexpected)
	case <-time.After(20 * time.Millisecond):
	}
	updated, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt.Add(13 * time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 13})
	if err != nil {
		t.Fatalf("Put(updated) error = %v", err)
	}
	select {
	case received := <-stream:
		if received != updated {
			t.Errorf("subscription value = %#v, want %#v", received, updated)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not receive update")
	}
	cancel()
	select {
	case _, open := <-stream:
		if open {
			t.Fatal("subscription remained open after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after cancellation")
	}
}

func TestMemoryValueStorePreservesBadQualityAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	badID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	bad, err := store.Put(TagValue{TagID: badID, ObservedAt: observedAt, Quality: ValueQualityBad, DataType: DataTypeFloat64, Value: 42, Error: "connection lost"})
	if err != nil {
		t.Fatalf("Put(bad) error = %v", err)
	}
	if bad.Value != nil || bad.Error != "connection lost" {
		t.Errorf("bad = %#v", bad)
	}
	if _, err := store.Snapshot([]uuid.UUID{badID}).Resolve(context.Background(), badID); !errors.Is(err, ErrTagValueUnavailable) {
		t.Fatalf("Resolve(bad) error = %v", err)
	}
	if _, err := store.Snapshot(nil).Resolve(context.Background(), uuid.New()); !errors.Is(err, ErrTagValueNotFound) {
		t.Fatalf("Resolve(missing) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Snapshot([]uuid.UUID{badID}).Resolve(cancelled, badID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve(cancelled) error = %v", err)
	}

	tests := []TagValue{
		{ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 1},
		{TagID: uuid.New(), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 1},
		{TagID: uuid.New(), ObservedAt: observedAt, Quality: "uncertain", DataType: DataTypeUInt16, Value: 1},
		{TagID: uuid.New(), ObservedAt: observedAt, Quality: ValueQualityBad, DataType: DataTypeUInt16},
		{TagID: uuid.New(), ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: -1},
	}
	for _, value := range tests {
		if _, err := store.Put(value); !errors.Is(err, ErrInvalidTagValue) {
			t.Errorf("Put(%#v) error = %v", value, err)
		}
	}
}

func TestMemoryValueStoreResumesFromLatestValuesAfterSequence(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	firstID := uuid.New()
	secondID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	first, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 1})
	if err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	second, err := store.Put(TagValue{TagID: secondID, ObservedAt: observedAt.Add(time.Second), Quality: ValueQualityBad, DataType: DataTypeFloat64, Error: "connection lost"})
	if err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}
	latestFirst, err := store.Put(TagValue{TagID: firstID, ObservedAt: observedAt.Add(2 * time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 3})
	if err != nil {
		t.Fatalf("Put(latest first) error = %v", err)
	}

	subscription := store.SubscribeValues(context.Background(), nil, first.Sequence)
	defer subscription.Unsubscribe()
	if len(subscription.Replay) != 2 || subscription.Replay[0] != second || subscription.Replay[1] != latestFirst {
		t.Fatalf("Replay = %#v, want sequences [%d %d]", subscription.Replay, second.Sequence, latestFirst.Sequence)
	}
	live, err := store.Put(TagValue{TagID: secondID, ObservedAt: observedAt.Add(3 * time.Second), Quality: ValueQualityGood, DataType: DataTypeFloat64, Value: 4.5})
	if err != nil {
		t.Fatalf("Put(live) error = %v", err)
	}
	select {
	case received := <-subscription.Stream:
		if received != live {
			t.Errorf("live value = %#v, want %#v", received, live)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not receive live value")
	}
}

func TestMemoryValueStoreDisconnectsLaggingResumableSubscriber(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	tagID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	subscription := store.SubscribeValues(context.Background(), nil, 0)
	for index := 0; index <= DefaultTagEventBuffer; index++ {
		if _, err := store.Put(TagValue{TagID: tagID, ObservedAt: observedAt.Add(time.Duration(index) * time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: index}); err != nil {
			t.Fatalf("Put(%d) error = %v", index, err)
		}
	}

	var lastReceived TagValue
	receivedCount := 0
	for value := range subscription.Stream {
		lastReceived = value
		receivedCount++
	}
	if receivedCount != DefaultTagEventBuffer || lastReceived.Sequence != DefaultTagEventBuffer {
		t.Fatalf("lagging stream received %d values through sequence %d", receivedCount, lastReceived.Sequence)
	}
	latest, exists := store.Latest(tagID)
	if !exists || latest.Sequence != DefaultTagEventBuffer+1 {
		t.Fatalf("latest = %#v, exists = %v", latest, exists)
	}

	resumed := store.SubscribeValues(context.Background(), nil, lastReceived.Sequence)
	defer resumed.Unsubscribe()
	if len(resumed.Replay) != 1 || resumed.Replay[0] != latest {
		t.Errorf("resumed Replay = %#v, want latest %#v", resumed.Replay, latest)
	}
}

func TestMemoryValueStoreSerializesConcurrentWrites(t *testing.T) {
	t.Parallel()
	store := NewMemoryValueStore()
	observedAt := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	const writeCount = 64
	sequences := make(chan uint64, writeCount)
	errorsChannel := make(chan error, writeCount)
	var writers sync.WaitGroup
	for index := 0; index < writeCount; index++ {
		writers.Add(1)
		go func(value int) {
			defer writers.Done()
			stored, err := store.Put(TagValue{TagID: uuid.New(), ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: value})
			if err != nil {
				errorsChannel <- err
				return
			}
			sequences <- stored.Sequence
		}(index)
	}
	writers.Wait()
	close(sequences)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("Put() error = %v", err)
	}
	seen := make(map[uint64]bool, writeCount)
	for sequence := range sequences {
		seen[sequence] = true
	}
	if len(seen) != writeCount || !seen[1] || !seen[writeCount] {
		t.Errorf("sequences = %v", seen)
	}
}
