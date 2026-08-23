package energy

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLiveHubValidatesOptionsAndCursors(t *testing.T) {
	t.Parallel()
	if _, err := NewLiveHub(WithLiveHistoryLimit(0)); !errors.Is(err, ErrInvalidLiveCursor) {
		t.Errorf("NewLiveHub(history 0) error = %v", err)
	}
	if _, err := NewLiveHub(WithLiveSubscriberBuffer(0)); !errors.Is(err, ErrInvalidLiveCursor) {
		t.Errorf("NewLiveHub(buffer 0) error = %v", err)
	}
	hub, _ := NewLiveHub()
	if _, err := hub.Subscribe(uuid.Nil, ""); !errors.Is(err, ErrInvalidLiveCursor) {
		t.Errorf("Subscribe(nil ID) error = %v", err)
	}
	for _, cursor := range []string{"invalid", uuid.NewString(), uuid.NewString() + ":x", " " + uuid.NewString() + ":1", ":1"} {
		if _, err := hub.Subscribe(uuid.New(), cursor); !errors.Is(err, ErrInvalidLiveCursor) {
			t.Errorf("Subscribe(%q) error = %v", cursor, err)
		}
	}
	var nilHub *LiveHub
	if _, err := nilHub.Subscribe(uuid.New(), ""); !errors.Is(err, ErrLiveHubRequired) {
		t.Errorf("nil Subscribe() error = %v", err)
	}
}

func TestLiveHubReplaysResumesAndSnapshotsImmutableEvents(t *testing.T) {
	t.Parallel()
	hub, _ := NewLiveHub(WithLiveHistoryLimit(3), WithLiveSubscriberBuffer(2))
	instanceID, otherID := uuid.New(), uuid.New()
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	first := validBatchMetrics(start, 2, 6)
	first.Electrical.Errors = []MetricError{{Code: MetricErrorSourceBad, Message: "original"}}
	hub.Publish(instanceID, first)
	first.Electrical.Errors[0].Message = "mutated caller"
	hub.Publish(instanceID, validBatchMetrics(start.Add(time.Second), 3, 9))
	hub.Publish(instanceID, validBatchMetrics(start.Add(time.Second), 99, 99))
	hub.Publish(otherID, validBatchMetrics(start.Add(2*time.Second), 100, 100))

	latest, err := hub.Subscribe(instanceID, "")
	if err != nil {
		t.Fatalf("Subscribe(latest) error = %v", err)
	}
	defer latest.Unsubscribe()
	if latest.Reset || len(latest.Replay) != 1 || latest.Replay[0].Sequence != 2 || latest.Replay[0].Metrics.Electrical.Kilowatts != 3 {
		t.Fatalf("latest replay = %#v", latest)
	}
	secondID := latest.Replay[0].ID
	session := secondID[:len(secondID)-2]
	resumed, err := hub.Subscribe(instanceID, session+":1")
	if err != nil {
		t.Fatalf("Subscribe(resume) error = %v", err)
	}
	defer resumed.Unsubscribe()
	if resumed.Reset || len(resumed.Replay) != 1 || resumed.Replay[0].ID != secondID {
		t.Errorf("resumed replay = %#v", resumed)
	}

	next := validBatchMetrics(start.Add(3*time.Second), 4, 12)
	hub.Publish(instanceID, next)
	select {
	case event := <-resumed.Stream:
		if event.Sequence != 3 || event.InstanceID != instanceID || event.Metrics.COP.Value != 3 {
			t.Errorf("live event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive live event")
	}
	resumed.Replay[0].Metrics.Electrical.Errors = append(resumed.Replay[0].Metrics.Electrical.Errors, MetricError{Message: "mutation"})
	check, _ := hub.Subscribe(instanceID, "")
	defer check.Unsubscribe()
	if len(check.Replay) != 1 || len(check.Replay[0].Metrics.Electrical.Errors) != 0 {
		t.Errorf("hub state aliases subscriber replay = %#v", check.Replay)
	}
}

func TestLiveHubResetsExpiredOrForeignCursorsToLatest(t *testing.T) {
	t.Parallel()
	hub, _ := NewLiveHub(WithLiveHistoryLimit(2))
	instanceID := uuid.New()
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		hub.Publish(instanceID, validBatchMetrics(start.Add(time.Duration(index)*time.Second), float64(index+1), float64(index+3)))
	}
	latest, _ := hub.Subscribe(instanceID, "")
	session := latest.Replay[0].ID[:len(latest.Replay[0].ID)-2]
	latest.Unsubscribe()
	for _, cursor := range []string{session + ":0", uuid.NewString() + ":2", session + ":99"} {
		subscription, err := hub.Subscribe(instanceID, cursor)
		if err != nil {
			t.Fatalf("Subscribe(%q) error = %v", cursor, err)
		}
		if !subscription.Reset || len(subscription.Replay) != 1 || subscription.Replay[0].Sequence != 3 {
			t.Errorf("reset subscription = %#v", subscription)
		}
		subscription.Unsubscribe()
	}
}

func TestLiveHubDisconnectsLaggingSubscriberWithoutBlocking(t *testing.T) {
	t.Parallel()
	hub, _ := NewLiveHub(WithLiveSubscriberBuffer(1))
	instanceID := uuid.New()
	subscription, _ := hub.Subscribe(instanceID, "")
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	hub.Publish(instanceID, validBatchMetrics(start, 1, 3))
	done := make(chan struct{})
	go func() {
		hub.Publish(instanceID, validBatchMetrics(start.Add(time.Second), 2, 6))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish() blocked on lagging subscriber")
	}
	if !errors.Is(subscription.Err(), ErrLiveSubscriberLag) {
		t.Errorf("subscription error = %v", subscription.Err())
	}
	if _, open := <-subscription.Stream; !open {
		t.Fatal("buffered event was not retained before close")
	}
	if _, open := <-subscription.Stream; open {
		t.Fatal("lagging subscription stream remains open")
	}
}

func TestLiveHubResetClosesOldGenerationAndAcceptsSameTimestamp(t *testing.T) {
	t.Parallel()
	hub, _ := NewLiveHub()
	instanceID := uuid.New()
	at := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	hub.Publish(instanceID, validBatchMetrics(at, 2, 6))
	subscription, _ := hub.Subscribe(instanceID, "")
	oldCursor := subscription.Replay[0].ID
	hub.Reset(instanceID)
	if !errors.Is(subscription.Err(), ErrLiveStreamReset) {
		t.Errorf("reset subscription error = %v", subscription.Err())
	}
	if _, open := <-subscription.Stream; open {
		t.Fatal("reset subscription remains open")
	}
	hub.Publish(instanceID, validBatchMetrics(at, 4, 12))
	rebuilt, _ := hub.Subscribe(instanceID, oldCursor)
	defer rebuilt.Unsubscribe()
	if !rebuilt.Reset || len(rebuilt.Replay) != 1 || rebuilt.Replay[0].Sequence != 1 || rebuilt.Replay[0].Metrics.Electrical.Kilowatts != 4 {
		t.Errorf("rebuilt generation = %#v", rebuilt.Replay)
	}
}

func TestLiveHubSupportsConcurrentPublishSubscribeAndClose(t *testing.T) {
	hub, _ := NewLiveHub(WithLiveHistoryLimit(32), WithLiveSubscriberBuffer(64))
	instanceID := uuid.New()
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			subscription, err := hub.Subscribe(instanceID, "")
			if err != nil {
				t.Errorf("Subscribe() error = %v", err)
				return
			}
			subscription.Unsubscribe()
			_ = subscription.Err()
		}()
	}
	for index := 0; index < 50; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			hub.Publish(instanceID, validBatchMetrics(start.Add(time.Duration(index)*time.Millisecond), float64(index), float64(index*3)))
		}(index)
	}
	wait.Wait()
	subscription, err := hub.Subscribe(instanceID, "")
	if err != nil || len(subscription.Replay) != 1 || subscription.Replay[0].Metrics.BatchAt.Before(start) {
		t.Errorf("final subscription = %#v, %v", subscription, err)
	}
	subscription.Unsubscribe()
}
