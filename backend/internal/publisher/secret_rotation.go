package publisher

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultSecretRotationBuffer = 64

var ErrSecretRotationSubscriberLag = errors.New("Data Publisher secret rotation subscriber lagged")

type SecretRotationAction string

const (
	SecretRotationUpserted SecretRotationAction = "upserted"
	SecretRotationDeleted  SecretRotationAction = "deleted"
)

type SecretRotationEvent struct {
	PublisherID       uuid.UUID
	Reference         SecretReference
	Kind              SecretKind
	Revision          uint64
	PublisherRevision uint64
	Action            SecretRotationAction
	RotatedAt         time.Time
}

type SecretRotationPublisher interface {
	PublishSecretRotation(SecretRotationEvent)
}

type SecretRotationSubscription interface {
	Events() <-chan SecretRotationEvent
	Err() error
	Close()
}

type SecretRotationFeed interface {
	SubscribeSecretRotations() (SecretRotationSubscription, error)
}

type SecretRotationBroker struct {
	mu          sync.Mutex
	bufferSize  int
	subscribers map[*secretRotationSubscription]struct{}
}

type secretRotationSubscription struct {
	broker *SecretRotationBroker
	events chan SecretRotationEvent
	err    error
	closed bool
}

func NewSecretRotationBroker(bufferSize int) (*SecretRotationBroker, error) {
	if bufferSize < 0 {
		return nil, ErrInvalidInput
	}
	if bufferSize == 0 {
		bufferSize = defaultSecretRotationBuffer
	}
	return &SecretRotationBroker{bufferSize: bufferSize, subscribers: make(map[*secretRotationSubscription]struct{})}, nil
}

func (broker *SecretRotationBroker) SubscribeSecretRotations() (SecretRotationSubscription, error) {
	if broker == nil {
		return nil, ErrSecretRotationRequired
	}
	broker.mu.Lock()
	if broker.bufferSize < 1 {
		broker.bufferSize = defaultSecretRotationBuffer
	}
	if broker.subscribers == nil {
		broker.subscribers = make(map[*secretRotationSubscription]struct{})
	}
	subscription := &secretRotationSubscription{broker: broker, events: make(chan SecretRotationEvent, broker.bufferSize)}
	broker.subscribers[subscription] = struct{}{}
	broker.mu.Unlock()
	return subscription, nil
}

func (broker *SecretRotationBroker) PublishSecretRotation(event SecretRotationEvent) {
	if broker == nil || !validSecretRotationEvent(event) {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for subscription := range broker.subscribers {
		select {
		case subscription.events <- event:
		default:
			subscription.err = ErrSecretRotationSubscriberLag
			broker.closeLocked(subscription)
		}
	}
}

func (subscription *secretRotationSubscription) Events() <-chan SecretRotationEvent {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *secretRotationSubscription) Err() error {
	if subscription == nil || subscription.broker == nil {
		return nil
	}
	subscription.broker.mu.Lock()
	defer subscription.broker.mu.Unlock()
	return subscription.err
}

func (subscription *secretRotationSubscription) Close() {
	if subscription == nil || subscription.broker == nil {
		return
	}
	subscription.broker.mu.Lock()
	subscription.broker.closeLocked(subscription)
	subscription.broker.mu.Unlock()
}

func (broker *SecretRotationBroker) closeLocked(subscription *secretRotationSubscription) {
	if subscription.closed {
		return
	}
	delete(broker.subscribers, subscription)
	subscription.closed = true
	close(subscription.events)
}

func validSecretRotationEvent(event SecretRotationEvent) bool {
	return event.PublisherID != uuid.Nil && event.Reference.Validate() == nil && validSecretKind(event.Kind) && event.Revision > 0 && event.PublisherRevision > 0 &&
		(event.Action == SecretRotationUpserted || event.Action == SecretRotationDeleted) && !event.RotatedAt.IsZero()
}

func publishSecretRotation(publisher SecretRotationPublisher, event SecretRotationEvent) {
	defer func() { _ = recover() }()
	publisher.PublishSecretRotation(event)
}
