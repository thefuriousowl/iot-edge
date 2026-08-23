package energy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLiveHistoryLimit     = 256
	defaultLiveSubscriberBuffer = 64
)

var (
	ErrLiveHubRequired   = errors.New("Energy live hub is required")
	ErrInvalidLiveCursor = errors.New("invalid Energy live event cursor")
	ErrLiveSubscriberLag = errors.New("Energy live subscriber lagged")
	ErrLiveStreamReset   = errors.New("Energy live stream reset")
)

type LiveEvent struct {
	ID         string       `json:"id"`
	Sequence   uint64       `json:"sequence"`
	InstanceID uuid.UUID    `json:"instance_id"`
	Metrics    BatchMetrics `json:"metrics"`
}

type LiveSubscription struct {
	Replay []LiveEvent
	Stream <-chan LiveEvent
	Reset  bool

	hub        *LiveHub
	subscriber *liveSubscriber
}

type LiveHubOption func(*LiveHub) error

func WithLiveHistoryLimit(limit int) LiveHubOption {
	return func(hub *LiveHub) error {
		if limit < 1 {
			return ErrInvalidLiveCursor
		}
		hub.historyLimit = limit
		return nil
	}
}

func WithLiveSubscriberBuffer(size int) LiveHubOption {
	return func(hub *LiveHub) error {
		if size < 1 {
			return ErrInvalidLiveCursor
		}
		hub.subscriberBuffer = size
		return nil
	}
}

type LiveHub struct {
	mu               sync.Mutex
	historyLimit     int
	subscriberBuffer int
	streams          map[uuid.UUID]*liveInstanceStream
}

type liveInstanceStream struct {
	generation  string
	next        uint64
	latestAt    time.Time
	events      []LiveEvent
	subscribers map[*liveSubscriber]struct{}
}

type liveSubscriber struct {
	events chan LiveEvent
	err    error
	closed bool
}

func NewLiveHub(options ...LiveHubOption) (*LiveHub, error) {
	hub := &LiveHub{
		historyLimit: defaultLiveHistoryLimit, subscriberBuffer: defaultLiveSubscriberBuffer,
		streams: make(map[uuid.UUID]*liveInstanceStream),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(hub); err != nil {
			return nil, err
		}
	}
	return hub, nil
}

func (hub *LiveHub) Publish(instanceID uuid.UUID, metrics BatchMetrics) {
	if hub == nil || instanceID == uuid.Nil || metrics.BatchAt.IsZero() {
		return
	}
	metrics = cloneBatchMetrics(metrics)
	metrics.BatchAt = metrics.BatchAt.UTC()
	hub.mu.Lock()
	stream := hub.instanceStreamLocked(instanceID)
	if !stream.latestAt.IsZero() && !stream.latestAt.Before(metrics.BatchAt) {
		hub.mu.Unlock()
		return
	}
	stream.next++
	event := LiveEvent{
		ID: fmt.Sprintf("%s:%d", stream.generation, stream.next), Sequence: stream.next,
		InstanceID: instanceID, Metrics: metrics,
	}
	stream.latestAt = metrics.BatchAt
	stream.events = append(stream.events, cloneLiveEvent(event))
	if len(stream.events) > hub.historyLimit {
		stream.events = append([]LiveEvent(nil), stream.events[len(stream.events)-hub.historyLimit:]...)
	}
	for subscriber := range stream.subscribers {
		select {
		case subscriber.events <- cloneLiveEvent(event):
		default:
			subscriber.err = ErrLiveSubscriberLag
			hub.closeSubscriberLocked(stream, subscriber)
		}
	}
	hub.mu.Unlock()
}

func (hub *LiveHub) Reset(instanceID uuid.UUID) {
	if hub == nil || instanceID == uuid.Nil {
		return
	}
	hub.mu.Lock()
	stream := hub.streams[instanceID]
	if stream != nil {
		for subscriber := range stream.subscribers {
			subscriber.err = ErrLiveStreamReset
			hub.closeSubscriberLocked(stream, subscriber)
		}
		delete(hub.streams, instanceID)
	}
	hub.mu.Unlock()
}

func (hub *LiveHub) Subscribe(instanceID uuid.UUID, cursor string) (*LiveSubscription, error) {
	if hub == nil {
		return nil, ErrLiveHubRequired
	}
	if instanceID == uuid.Nil {
		return nil, ErrInvalidLiveCursor
	}
	cursorSession, cursorSequence, hasCursor, err := parseLiveCursor(cursor)
	if err != nil {
		return nil, err
	}
	hub.mu.Lock()
	stream := hub.instanceStreamLocked(instanceID)
	replay := make([]LiveEvent, 0)
	reset := false
	if !hasCursor {
		if len(stream.events) > 0 {
			replay = append(replay, cloneLiveEvent(stream.events[len(stream.events)-1]))
		}
	} else if cursorSession != stream.generation || cursorSequence > stream.next {
		reset = true
		if len(stream.events) > 0 {
			replay = append(replay, cloneLiveEvent(stream.events[len(stream.events)-1]))
		}
	} else if len(stream.events) > 0 {
		oldest := stream.events[0].Sequence
		if cursorSequence < oldest && oldest-cursorSequence > 1 {
			reset = true
			replay = append(replay, cloneLiveEvent(stream.events[len(stream.events)-1]))
		} else {
			for _, event := range stream.events {
				if event.Sequence > cursorSequence {
					replay = append(replay, cloneLiveEvent(event))
				}
			}
		}
	}
	subscriber := &liveSubscriber{events: make(chan LiveEvent, hub.subscriberBuffer)}
	stream.subscribers[subscriber] = struct{}{}
	hub.mu.Unlock()
	return &LiveSubscription{Replay: replay, Stream: subscriber.events, Reset: reset, hub: hub, subscriber: subscriber}, nil
}

func (subscription *LiveSubscription) Unsubscribe() {
	if subscription == nil || subscription.hub == nil || subscription.subscriber == nil {
		return
	}
	subscription.hub.mu.Lock()
	for _, stream := range subscription.hub.streams {
		if _, exists := stream.subscribers[subscription.subscriber]; exists {
			subscription.hub.closeSubscriberLocked(stream, subscription.subscriber)
			break
		}
	}
	subscription.hub.mu.Unlock()
}

func (subscription *LiveSubscription) Err() error {
	if subscription == nil || subscription.hub == nil || subscription.subscriber == nil {
		return nil
	}
	subscription.hub.mu.Lock()
	defer subscription.hub.mu.Unlock()
	return subscription.subscriber.err
}

func (hub *LiveHub) instanceStreamLocked(instanceID uuid.UUID) *liveInstanceStream {
	stream := hub.streams[instanceID]
	if stream == nil {
		stream = &liveInstanceStream{generation: uuid.NewString(), subscribers: make(map[*liveSubscriber]struct{})}
		hub.streams[instanceID] = stream
	}
	return stream
}

func (hub *LiveHub) closeSubscriberLocked(stream *liveInstanceStream, subscriber *liveSubscriber) {
	if subscriber.closed {
		return
	}
	delete(stream.subscribers, subscriber)
	subscriber.closed = true
	close(subscriber.events)
}

func parseLiveCursor(value string) (string, uint64, bool, error) {
	if value == "" {
		return "", 0, false, nil
	}
	if value != strings.TrimSpace(value) {
		return "", 0, false, ErrInvalidLiveCursor
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", 0, false, ErrInvalidLiveCursor
	}
	sessionID, err := uuid.Parse(parts[0])
	if err != nil || sessionID == uuid.Nil {
		return "", 0, false, ErrInvalidLiveCursor
	}
	sequence, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return "", 0, false, ErrInvalidLiveCursor
	}
	return sessionID.String(), sequence, true, nil
}

func cloneLiveEvent(event LiveEvent) LiveEvent {
	event.Metrics = cloneBatchMetrics(event.Metrics)
	return event
}
