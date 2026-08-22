package tag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultValuePersistenceRetryInterval = time.Second
	defaultValuePersistenceStopTimeout   = 5 * time.Second
)

var (
	ErrLatestValueRepositoryRequired = errors.New("latest value repository is required")
	ErrPersistentValueStoreStarted   = errors.New("persistent value store is already started")
	ErrPersistentValueStoreNotReady  = errors.New("persistent value store is not ready")
	ErrPersistentValueStoreStopped   = errors.New("persistent value store is stopped")
)

type LatestValueRepository interface {
	ListLatest(context.Context) ([]TagValue, error)
	UpsertLatest(context.Context, TagValue) error
}

type PersistentValueStoreOption func(*PersistentValueStore) error

func WithValuePersistenceRetryInterval(interval time.Duration) PersistentValueStoreOption {
	return func(store *PersistentValueStore) error {
		if interval <= 0 {
			return fmt.Errorf("%w: retry interval must be positive", ErrInvalidTagInput)
		}
		store.retryInterval = interval
		return nil
	}
}

type PersistentValueStore struct {
	memory        *MemoryValueStore
	repository    LatestValueRepository
	retryInterval time.Duration
	stopTimeout   time.Duration
	errors        chan error
	wake          chan struct{}
	stop          chan struct{}
	done          chan struct{}

	startMu sync.Mutex
	mu      sync.Mutex
	started bool
	stopped bool
	pending map[uuid.UUID]TagValue
}

func NewPersistentValueStore(repository LatestValueRepository, options ...PersistentValueStoreOption) (*PersistentValueStore, error) {
	if repository == nil {
		return nil, ErrLatestValueRepositoryRequired
	}
	store := &PersistentValueStore{
		memory:        NewMemoryValueStore(),
		repository:    repository,
		retryInterval: defaultValuePersistenceRetryInterval,
		stopTimeout:   defaultValuePersistenceStopTimeout,
		errors:        make(chan error, 32),
		wake:          make(chan struct{}, 1),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		pending:       make(map[uuid.UUID]TagValue),
	}
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

func (store *PersistentValueStore) Start(ctx context.Context) error {
	store.startMu.Lock()
	defer store.startMu.Unlock()
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidTagInput)
	}

	store.mu.Lock()
	if store.started || store.stopped {
		store.mu.Unlock()
		return ErrPersistentValueStoreStarted
	}
	store.mu.Unlock()

	values, err := store.repository.ListLatest(ctx)
	if err != nil {
		return fmt.Errorf("loading latest Tag values: %w", err)
	}
	if err := store.memory.RestoreLatest(values); err != nil {
		return fmt.Errorf("restoring latest Tag values: %w", err)
	}

	store.mu.Lock()
	store.started = true
	store.mu.Unlock()
	go store.run(ctx)
	return nil
}

func (store *PersistentValueStore) Stop() {
	store.mu.Lock()
	if !store.started {
		store.mu.Unlock()
		return
	}
	if !store.stopped {
		store.stopped = true
		close(store.stop)
	}
	done := store.done
	store.mu.Unlock()
	<-done
}

func (store *PersistentValueStore) Put(value TagValue) (TagValue, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.started {
		return TagValue{}, ErrPersistentValueStoreNotReady
	}
	if store.stopped {
		return TagValue{}, ErrPersistentValueStoreStopped
	}
	stored, err := store.memory.Put(value)
	if err != nil {
		return TagValue{}, err
	}
	store.pending[stored.TagID] = stored
	select {
	case store.wake <- struct{}{}:
	default:
	}
	return stored, nil
}

func (store *PersistentValueStore) Latest(tagID uuid.UUID) (TagValue, bool) {
	return store.memory.Latest(tagID)
}

func (store *PersistentValueStore) Snapshot(tagIDs []uuid.UUID) ValueSnapshot {
	return store.memory.Snapshot(tagIDs)
}

func (store *PersistentValueStore) History(tagID uuid.UUID, limit int) []TagValue {
	return store.memory.History(tagID, limit)
}

func (store *PersistentValueStore) Subscribe(ctx context.Context, tagIDs []uuid.UUID) (<-chan TagValue, func()) {
	return store.memory.Subscribe(ctx, tagIDs)
}

func (store *PersistentValueStore) SubscribeValues(ctx context.Context, tagIDs []uuid.UUID, afterSequence uint64) ValueSubscription {
	return store.memory.SubscribeValues(ctx, tagIDs, afterSequence)
}

func (store *PersistentValueStore) Errors() <-chan error {
	return store.errors
}

func (store *PersistentValueStore) run(ctx context.Context) {
	defer close(store.done)
	ticker := time.NewTicker(store.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-store.wake:
			store.flush(ctx)
		case <-ticker.C:
			store.flush(ctx)
		case <-ctx.Done():
			store.finish()
			return
		case <-store.stop:
			store.finish()
			return
		}
	}
}

func (store *PersistentValueStore) finish() {
	store.mu.Lock()
	store.stopped = true
	store.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), store.stopTimeout)
	defer cancel()
	for store.pendingCount() > 0 && ctx.Err() == nil {
		store.flush(ctx)
		if store.pendingCount() == 0 {
			return
		}
		timer := time.NewTimer(store.retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
	if pending := store.pendingCount(); pending > 0 {
		store.report(fmt.Errorf("stopping latest value persistence with %d pending Tag values", pending))
	}
}

func (store *PersistentValueStore) flush(ctx context.Context) {
	values := store.takePending()
	sort.Slice(values, func(first, second int) bool { return values[first].Sequence < values[second].Sequence })
	for _, value := range values {
		if err := store.repository.UpsertLatest(ctx, value); err != nil {
			store.requeue(value)
			store.report(fmt.Errorf("persisting latest Tag value %s sequence %d: %w", value.TagID, value.Sequence, err))
		}
	}
}

func (store *PersistentValueStore) takePending() []TagValue {
	store.mu.Lock()
	defer store.mu.Unlock()
	values := make([]TagValue, 0, len(store.pending))
	for tagID, value := range store.pending {
		values = append(values, value)
		delete(store.pending, tagID)
	}
	return values
}

func (store *PersistentValueStore) requeue(value TagValue) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if current, exists := store.pending[value.TagID]; !exists || current.Sequence < value.Sequence {
		store.pending[value.TagID] = value
	}
}

func (store *PersistentValueStore) pendingCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.pending)
}

func (store *PersistentValueStore) report(err error) {
	select {
	case store.errors <- err:
	default:
	}
}

var _ RuntimeValueStore = (*PersistentValueStore)(nil)
