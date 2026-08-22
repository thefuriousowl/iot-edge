package tagsnapshot

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

func TestReaderAdaptsOnlyRequestedLatestValues(t *testing.T) {
	t.Parallel()
	store := tag.NewMemoryValueStore()
	firstID, secondID := uuid.New(), uuid.New()
	observedAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	if _, err := store.Put(tag.TagValue{TagID: firstID, ObservedAt: observedAt, DataType: tag.DataTypeFloat64, Value: 12.5, Quality: tag.ValueQualityGood}); err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	if _, err := store.Put(tag.TagValue{TagID: secondID, ObservedAt: observedAt.Add(time.Second), DataType: tag.DataTypeUInt16, Quality: tag.ValueQualityBad, Error: "connection lost"}); err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}
	reader, err := NewReader(store)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	values := reader.Snapshot([]uuid.UUID{secondID, uuid.New()})
	if len(values) != 1 {
		t.Fatalf("Snapshot() count = %d, want 1", len(values))
	}
	value := values[secondID]
	if value.DataType != "uint16" || value.Quality != tag.ValueQualityBad || value.Error != "connection lost" || !value.ObservedAt.Equal(observedAt.Add(time.Second)) || value.Value != nil {
		t.Errorf("Snapshot() value = %#v", value)
	}
}

func TestNewReaderRequiresStore(t *testing.T) {
	t.Parallel()
	if _, err := NewReader(nil); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("NewReader() error = %v, want %v", err, ErrStoreRequired)
	}
}
