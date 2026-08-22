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
	ValueQualityGood            = "good"
	ValueQualityBad             = "bad"
	DefaultTagValueHistoryLimit = 10
	DefaultTagEventBuffer       = 64
)

var (
	ErrInvalidTagValue     = errors.New("invalid tag value")
	ErrTagValueNotFound    = errors.New("tag value not found")
	ErrTagValueUnavailable = errors.New("tag value is unavailable")
	ErrTagValueStoreInUse  = errors.New("tag value store is already in use")
)

type TagValue struct {
	TagID      uuid.UUID `json:"tag_id"`
	Sequence   uint64    `json:"sequence"`
	ObservedAt time.Time `json:"observed_at"`
	StoredAt   time.Time `json:"stored_at"`
	Quality    string    `json:"quality"`
	DataType   DataType  `json:"data_type"`
	Value      any       `json:"value"`
	Error      string    `json:"error,omitempty"`
}

type ValueSnapshot map[uuid.UUID]TagValue

type ValueSubscription struct {
	Replay      []TagValue
	Stream      <-chan TagValue
	Unsubscribe func()
}

func (snapshot ValueSnapshot) Resolve(ctx context.Context, tagID uuid.UUID) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, exists := snapshot[tagID]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrTagValueNotFound, tagID)
	}
	if value.Quality != ValueQualityGood {
		return nil, fmt.Errorf("%w: %s: %s", ErrTagValueUnavailable, tagID, value.Error)
	}
	return value.Value, nil
}

type MemoryValueStore struct {
	mu             sync.RWMutex
	sequence       uint64
	latest         map[uuid.UUID]TagValue
	history        map[uuid.UUID][]TagValue
	subscribers    map[uint64]*valueSubscriber
	nextSubscriber uint64
}

func NewMemoryValueStore() *MemoryValueStore {
	return &MemoryValueStore{latest: make(map[uuid.UUID]TagValue), history: make(map[uuid.UUID][]TagValue), subscribers: make(map[uint64]*valueSubscriber)}
}

type valueSubscriber struct {
	tagIDs          map[uuid.UUID]struct{}
	stream          chan TagValue
	done            chan struct{}
	disconnectOnLag bool
}

func (store *MemoryValueStore) Put(value TagValue) (TagValue, error) {
	value, err := normalizeTagValue(value)
	if err != nil {
		return TagValue{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.latest == nil {
		store.latest = make(map[uuid.UUID]TagValue)
	}
	if store.history == nil {
		store.history = make(map[uuid.UUID][]TagValue)
	}
	if store.subscribers == nil {
		store.subscribers = make(map[uint64]*valueSubscriber)
	}
	store.sequence++
	value.Sequence = store.sequence
	value.StoredAt = time.Now().UTC()
	store.latest[value.TagID] = value
	history := append(store.history[value.TagID], value)
	if len(history) > DefaultTagValueHistoryLimit {
		history = append([]TagValue(nil), history[len(history)-DefaultTagValueHistoryLimit:]...)
	}
	store.history[value.TagID] = history
	for subscriberID, subscriber := range store.subscribers {
		if len(subscriber.tagIDs) > 0 {
			if _, subscribed := subscriber.tagIDs[value.TagID]; !subscribed {
				continue
			}
		}
		if offerTagValue(subscriber.stream, value) {
			continue
		}
		if subscriber.disconnectOnLag {
			store.closeSubscriberLocked(subscriberID, subscriber)
			continue
		}
		offerLatestTagValue(subscriber.stream, value)
	}
	return value, nil
}

func (store *MemoryValueStore) RestoreLatest(values []TagValue) error {
	normalized := make([]TagValue, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		entry, err := normalizeTagValue(value)
		if err != nil {
			return err
		}
		if entry.Sequence == 0 || entry.StoredAt.IsZero() {
			return fmt.Errorf("%w: restored sequence and stored_at are required", ErrInvalidTagValue)
		}
		if _, exists := seen[entry.TagID]; exists {
			return fmt.Errorf("%w: duplicate restored tag %s", ErrInvalidTagValue, entry.TagID)
		}
		seen[entry.TagID] = struct{}{}
		normalized = append(normalized, entry)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.sequence != 0 || len(store.latest) != 0 || len(store.history) != 0 {
		return ErrTagValueStoreInUse
	}
	if store.latest == nil {
		store.latest = make(map[uuid.UUID]TagValue)
	}
	if store.history == nil {
		store.history = make(map[uuid.UUID][]TagValue)
	}
	for _, value := range normalized {
		store.latest[value.TagID] = value
		store.history[value.TagID] = []TagValue{value}
		if value.Sequence > store.sequence {
			store.sequence = value.Sequence
		}
	}
	return nil
}

func normalizeTagValue(value TagValue) (TagValue, error) {
	if value.TagID == uuid.Nil || !validTagDataType(value.DataType) {
		return TagValue{}, ErrInvalidTagValue
	}
	switch value.Quality {
	case ValueQualityGood:
		coerced, err := coerceTagValue(value.DataType, value.Value)
		if err != nil {
			return TagValue{}, fmt.Errorf("%w: %v", ErrInvalidTagValue, err)
		}
		value.Value = coerced
		value.Error = ""
	case ValueQualityBad:
		value.Value = nil
		if value.Error == "" {
			return TagValue{}, fmt.Errorf("%w: bad quality requires an error", ErrInvalidTagValue)
		}
	default:
		return TagValue{}, fmt.Errorf("%w: unsupported quality %q", ErrInvalidTagValue, value.Quality)
	}
	if value.ObservedAt.IsZero() {
		return TagValue{}, fmt.Errorf("%w: observed_at is required", ErrInvalidTagValue)
	}
	return value, nil
}

func (store *MemoryValueStore) Latest(tagID uuid.UUID) (TagValue, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	value, exists := store.latest[tagID]
	return value, exists
}

func (store *MemoryValueStore) Snapshot(tagIDs []uuid.UUID) ValueSnapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	snapshot := make(ValueSnapshot, len(tagIDs))
	for _, tagID := range tagIDs {
		if value, exists := store.latest[tagID]; exists {
			snapshot[tagID] = value
		}
	}
	return snapshot
}

func (store *MemoryValueStore) History(tagID uuid.UUID, limit int) []TagValue {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if limit <= 0 || limit > DefaultTagValueHistoryLimit {
		limit = DefaultTagValueHistoryLimit
	}
	history := store.history[tagID]
	if len(history) > limit {
		history = history[len(history)-limit:]
	}
	return append([]TagValue{}, history...)
}

func (store *MemoryValueStore) Subscribe(ctx context.Context, tagIDs []uuid.UUID) (<-chan TagValue, func()) {
	store.mu.Lock()
	subscriberID, subscriber := store.addSubscriberLocked(tagIDs, DefaultTagValueHistoryLimit+1, false)
	if len(subscriber.tagIDs) > 0 {
		for tagID := range subscriber.tagIDs {
			if value, exists := store.latest[tagID]; exists {
				offerLatestTagValue(subscriber.stream, value)
			}
		}
	}
	store.mu.Unlock()
	unsubscribe := store.watchSubscriber(ctx, subscriberID, subscriber)
	return subscriber.stream, unsubscribe
}

func (store *MemoryValueStore) SubscribeValues(ctx context.Context, tagIDs []uuid.UUID, afterSequence uint64) ValueSubscription {
	store.mu.Lock()
	subscriberID, subscriber := store.addSubscriberLocked(tagIDs, DefaultTagEventBuffer, true)
	replay := make([]TagValue, 0, len(store.latest))
	for tagID, value := range store.latest {
		if value.Sequence <= afterSequence || !subscriber.matches(tagID) {
			continue
		}
		replay = append(replay, value)
	}
	sort.Slice(replay, func(first, second int) bool { return replay[first].Sequence < replay[second].Sequence })
	store.mu.Unlock()

	return ValueSubscription{
		Replay:      replay,
		Stream:      subscriber.stream,
		Unsubscribe: store.watchSubscriber(ctx, subscriberID, subscriber),
	}
}

func (store *MemoryValueStore) addSubscriberLocked(tagIDs []uuid.UUID, buffer int, disconnectOnLag bool) (uint64, *valueSubscriber) {
	if store.subscribers == nil {
		store.subscribers = make(map[uint64]*valueSubscriber)
	}
	store.nextSubscriber++
	filter := make(map[uuid.UUID]struct{}, len(tagIDs))
	for _, tagID := range tagIDs {
		if tagID != uuid.Nil {
			filter[tagID] = struct{}{}
		}
	}
	subscriber := &valueSubscriber{
		tagIDs:          filter,
		stream:          make(chan TagValue, buffer),
		done:            make(chan struct{}),
		disconnectOnLag: disconnectOnLag,
	}
	store.subscribers[store.nextSubscriber] = subscriber
	return store.nextSubscriber, subscriber
}

func (store *MemoryValueStore) watchSubscriber(ctx context.Context, subscriberID uint64, subscriber *valueSubscriber) func() {
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			store.mu.Lock()
			if current := store.subscribers[subscriberID]; current == subscriber {
				store.closeSubscriberLocked(subscriberID, subscriber)
			}
			store.mu.Unlock()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			unsubscribe()
		case <-subscriber.done:
		}
	}()
	return unsubscribe
}

func (store *MemoryValueStore) closeSubscriberLocked(subscriberID uint64, subscriber *valueSubscriber) {
	delete(store.subscribers, subscriberID)
	close(subscriber.stream)
	close(subscriber.done)
}

func (subscriber *valueSubscriber) matches(tagID uuid.UUID) bool {
	if len(subscriber.tagIDs) == 0 {
		return true
	}
	_, matches := subscriber.tagIDs[tagID]
	return matches
}

func offerTagValue(stream chan TagValue, value TagValue) bool {
	select {
	case stream <- value:
		return true
	default:
		return false
	}
}

func offerLatestTagValue(stream chan TagValue, value TagValue) {
	select {
	case stream <- value:
	default:
		select {
		case <-stream:
		default:
		}
		select {
		case stream <- value:
		default:
		}
	}
}
