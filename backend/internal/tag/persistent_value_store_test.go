package tag

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewPersistentValueStoreValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()
	if _, err := NewPersistentValueStore(nil); !errors.Is(err, ErrLatestValueRepositoryRequired) {
		t.Errorf("NewPersistentValueStore(nil) error = %v", err)
	}
	if _, err := NewPersistentValueStore(&persistentValueTestRepository{}, WithValuePersistenceRetryInterval(0)); !errors.Is(err, ErrInvalidTagInput) {
		t.Errorf("NewPersistentValueStore(interval) error = %v", err)
	}
}

func TestPersistentValueStoreHydratesAndPersistsWithContinuousSequence(t *testing.T) {
	t.Parallel()
	tagID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 9, 0, 0, 0, time.UTC)
	repository := &persistentValueTestRepository{latest: []TagValue{{TagID: tagID, Sequence: 40, ObservedAt: observedAt, StoredAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: uint16(12)}}}
	store := newPersistentValueTestStore(t, repository)
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)

	hydrated, exists := store.Latest(tagID)
	if !exists || hydrated.Sequence != 40 || hydrated.Value != uint16(12) || len(store.History(tagID, 10)) != 1 {
		t.Errorf("hydrated value = %#v, exists = %t", hydrated, exists)
	}
	stored, err := store.Put(TagValue{TagID: tagID, ObservedAt: observedAt.Add(time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 13})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if stored.Sequence != 41 {
		t.Errorf("stored sequence = %d, want 41", stored.Sequence)
	}
	awaitPersistentValue(t, repository, tagID, 41)
}

func TestPersistentValueStoreCoalescesPendingWritesPerTag(t *testing.T) {
	t.Parallel()
	repository := &persistentValueTestRepository{block: make(chan struct{}), upsertStarted: make(chan struct{}, 1)}
	store := newPersistentValueTestStore(t, repository)
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)
	tagID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 9, 0, 0, 0, time.UTC)
	if _, err := store.Put(TagValue{TagID: tagID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: 1}); err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	select {
	case <-repository.upsertStarted:
	case <-time.After(time.Second):
		t.Fatal("first persistence write did not start")
	}
	for value := 2; value <= 3; value++ {
		if _, err := store.Put(TagValue{TagID: tagID, ObservedAt: observedAt.Add(time.Duration(value) * time.Second), Quality: ValueQualityGood, DataType: DataTypeUInt16, Value: value}); err != nil {
			t.Fatalf("Put(%d) error = %v", value, err)
		}
	}
	close(repository.block)
	awaitPersistentValue(t, repository, tagID, 3)
	sequences := repository.sequences(tagID)
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 3 {
		t.Errorf("persisted sequences = %v, want [1 3]", sequences)
	}
}

func TestPersistentValueStoreRetriesFailures(t *testing.T) {
	t.Parallel()
	repository := &persistentValueTestRepository{failures: 1}
	store := newPersistentValueTestStore(t, repository)
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(store.Stop)
	tagID := uuid.New()
	if _, err := store.Put(TagValue{TagID: tagID, ObservedAt: time.Now().UTC(), Quality: ValueQualityBad, DataType: DataTypeFloat64, Error: "illegal data address"}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	select {
	case err := <-store.Errors():
		if !stringsContainAll(err.Error(), tagID.String(), "temporary persistence failure") {
			t.Fatalf("persistence error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("persistence failure was not reported")
	}
	awaitPersistentValue(t, repository, tagID, 1)
}

func TestPersistentValueStoreLifecycleAndLoadFailures(t *testing.T) {
	t.Parallel()
	repository := &persistentValueTestRepository{listErr: errors.New("database unavailable")}
	store := newPersistentValueTestStore(t, repository)
	if _, err := store.Put(TagValue{}); !errors.Is(err, ErrPersistentValueStoreNotReady) {
		t.Errorf("Put(before Start) error = %v", err)
	}
	if err := store.Start(nil); !errors.Is(err, ErrInvalidTagInput) {
		t.Errorf("Start(nil) error = %v", err)
	}
	if err := store.Start(context.Background()); !stringsContainAll(errorString(err), "loading latest Tag values", "database unavailable") {
		t.Errorf("Start(load failure) error = %v", err)
	}
	repository.mu.Lock()
	repository.listErr = nil
	repository.mu.Unlock()
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start(retry) error = %v", err)
	}
	if err := store.Start(context.Background()); !errors.Is(err, ErrPersistentValueStoreStarted) {
		t.Errorf("Start(second) error = %v", err)
	}
	store.Stop()
	if _, err := store.Put(TagValue{}); !errors.Is(err, ErrPersistentValueStoreStopped) {
		t.Errorf("Put(after Stop) error = %v", err)
	}
}

func TestPersistentValueStoreFlushesPendingValuesOnStop(t *testing.T) {
	t.Parallel()
	repository := &persistentValueTestRepository{}
	store := newPersistentValueTestStore(t, repository)
	if err := store.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	tagID := uuid.New()
	if _, err := store.Put(TagValue{TagID: tagID, ObservedAt: time.Now().UTC(), Quality: ValueQualityGood, DataType: DataTypeBool, Value: true}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	store.Stop()
	if sequences := repository.sequences(tagID); len(sequences) != 1 || sequences[0] != 1 {
		t.Errorf("persisted sequences after Stop = %v, want [1]", sequences)
	}
}

type persistentValueTestRepository struct {
	mu            sync.Mutex
	latest        []TagValue
	upserts       []TagValue
	listErr       error
	failures      int
	block         chan struct{}
	upsertStarted chan struct{}
}

func (repository *persistentValueTestRepository) ListLatest(context.Context) ([]TagValue, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return append([]TagValue(nil), repository.latest...), nil
}

func (repository *persistentValueTestRepository) UpsertLatest(ctx context.Context, value TagValue) error {
	if repository.upsertStarted != nil {
		select {
		case repository.upsertStarted <- struct{}{}:
		default:
		}
	}
	if repository.block != nil {
		select {
		case <-repository.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.failures > 0 {
		repository.failures--
		return errors.New("temporary persistence failure")
	}
	repository.upserts = append(repository.upserts, value)
	return nil
}

func (repository *persistentValueTestRepository) sequences(tagID uuid.UUID) []uint64 {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	sequences := make([]uint64, 0)
	for _, value := range repository.upserts {
		if value.TagID == tagID {
			sequences = append(sequences, value.Sequence)
		}
	}
	return sequences
}

func newPersistentValueTestStore(t *testing.T, repository LatestValueRepository) *PersistentValueStore {
	t.Helper()
	store, err := NewPersistentValueStore(repository, WithValuePersistenceRetryInterval(5*time.Millisecond))
	if err != nil {
		t.Fatalf("NewPersistentValueStore() error = %v", err)
	}
	store.stopTimeout = 100 * time.Millisecond
	return store
}

func awaitPersistentValue(t *testing.T, repository *persistentValueTestRepository, tagID uuid.UUID, sequence uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sequences := repository.sequences(tagID)
		if len(sequences) > 0 && sequences[len(sequences)-1] == sequence {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("persisted sequences = %v, want latest %d", repository.sequences(tagID), sequence)
}

func stringsContainAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
