package live

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

type sourceReader struct {
	snapshot     publisher.SourceSnapshot
	subscription *sourceSubscription
	selections   []publisher.SourceSelection
}

func (reader *sourceReader) Snapshot(_ context.Context, selections []publisher.SourceSelection) (publisher.SourceSnapshot, error) {
	reader.selections = append([]publisher.SourceSelection(nil), selections...)
	return reader.snapshot, nil
}
func (reader *sourceReader) Subscribe(_ context.Context, selections []publisher.SourceSelection) (publisher.SourceSubscription, error) {
	reader.selections = append([]publisher.SourceSelection(nil), selections...)
	return reader.subscription, nil
}

type tagHistory struct{ values map[uuid.UUID][]tag.TagValue }

func (history *tagHistory) History(id uuid.UUID, limit int) []tag.TagValue {
	values := history.values[id]
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return append([]tag.TagValue(nil), values...)
}

type sourceSubscription struct {
	events chan publisher.SourceSample
	err    error
	closed bool
}

func (subscription *sourceSubscription) Events() <-chan publisher.SourceSample {
	return subscription.events
}
func (subscription *sourceSubscription) Err() error { return subscription.err }
func (subscription *sourceSubscription) Close()     { subscription.closed = true }

func TestReaderMapsUnifiedSnapshotAndTagHistory(t *testing.T) {
	tagID, pluginID := uuid.New(), uuid.New()
	tagReference, pluginReference := asset.TagSource(tagID), asset.PluginOutputSource(pluginID, "power")
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	sources := &sourceReader{snapshot: publisher.SourceSnapshot{CapturedAt: at, Samples: []publisher.SourceSample{
		{Reference: publisher.TagSource(tagID), Available: true, Sequence: 5, Value: 12.5, Quality: publisher.SourceQualityGood, ObservedAt: &at},
		{Reference: publisher.PluginOutputSource(pluginID, "power"), Available: true, Sequence: 7, Value: 22.0, Quality: publisher.SourceQualityPartial, Error: " partial\ncoverage ", ObservedAt: &at},
	}}}
	history := &tagHistory{values: map[uuid.UUID][]tag.TagValue{tagID: {{TagID: tagID, Sequence: 4, Value: 11.5, Quality: tag.ValueQualityGood, ObservedAt: at, StoredAt: at}}}}
	reader, err := NewReader(sources, history)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.Snapshot(context.Background(), []asset.SourceReference{tagReference, pluginReference})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Samples) != 2 || snapshot.Samples[0].Source != tagReference || snapshot.Samples[1].Source != pluginReference || snapshot.Samples[1].Error != "partial coverage" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(sources.selections) != 2 || sources.selections[0].Reference.TagID != tagID || sources.selections[1].Reference.PluginInstanceID != pluginID {
		t.Fatalf("selections = %#v", sources.selections)
	}
	values := reader.History(tagReference, asset.MeasurementHistoryLimit)
	if len(values) != 1 || values[0].Sequence != 4 || values[0].Source != tagReference {
		t.Fatalf("history = %#v", values)
	}
	if pluginHistory := reader.History(pluginReference, 10); pluginHistory == nil || len(pluginHistory) != 0 {
		t.Fatalf("Plugin history = %#v", pluginHistory)
	}
}

func TestReaderMapsSubscriptionAndClosesUpstream(t *testing.T) {
	tagID := uuid.New()
	upstream := &sourceSubscription{events: make(chan publisher.SourceSample, 1)}
	upstream.events <- publisher.SourceSample{Reference: publisher.TagSource(tagID), Available: true, Sequence: 8, Value: 4.5, Quality: publisher.SourceQualityGood}
	close(upstream.events)
	sources := &sourceReader{subscription: upstream}
	reader, _ := NewReader(sources, &tagHistory{})
	subscription, err := reader.Subscribe(context.Background(), []asset.SourceReference{asset.TagSource(tagID)})
	if err != nil {
		t.Fatal(err)
	}
	value := <-subscription.Events()
	if value.Source != asset.TagSource(tagID) || value.Sequence != 8 {
		t.Fatalf("event = %#v", value)
	}
	for range subscription.Events() {
	}
	if !upstream.closed {
		t.Fatal("upstream was not closed")
	}
}

func TestAssetMonitoringReceivesAcquisitionEventsWithoutCreatingReads(t *testing.T) {
	tagID := uuid.New()
	sources := newCadenceSourceReader()
	reader, err := NewReader(sources, &tagHistory{})
	if err != nil {
		t.Fatal(err)
	}

	// The first value represents the Datasource-owned poll that existed before
	// the Asset view opened. Subscribing must only attach to that shared feed.
	sources.poll(tagID, 1, 41)
	baselineReads := sources.reads.Load()
	subscription, err := reader.Subscribe(t.Context(), []asset.SourceReference{asset.TagSource(tagID)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if got := sources.reads.Load(); got != baselineReads {
		t.Fatalf("Datasource reads after opening Asset monitoring = %d, want unchanged %d", got, baselineReads)
	}

	for poll := uint64(2); poll <= 4; poll++ {
		sources.poll(tagID, poll, int(poll)+40)
		select {
		case event := <-subscription.Events():
			if event.Source != asset.TagSource(tagID) || event.Sequence != poll || event.Value != int(poll)+40 {
				t.Fatalf("Asset event for poll %d = %#v", poll, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for Asset event from poll %d", poll)
		}
	}
	if got := sources.reads.Load(); got != 4 {
		t.Fatalf("Datasource reads = %d, want exactly one per scheduled poll", got)
	}
	if got := sources.subscribeCalls.Load(); got != 1 {
		t.Fatalf("shared-feed subscriptions = %d, want 1", got)
	}

	subscription.Close()
	if got := sources.reads.Load(); got != 4 {
		t.Fatalf("Datasource reads after closing Asset monitoring = %d, want unchanged 4", got)
	}
}

func TestReaderRequiresDependenciesAndRejectsInvalidReference(t *testing.T) {
	sources := &sourceReader{}
	history := &tagHistory{}
	if _, err := NewReader(nil, history); !errors.Is(err, ErrUnifiedSourceReaderRequired) {
		t.Fatalf("nil sources error = %v", err)
	}
	if _, err := NewReader(sources, nil); !errors.Is(err, ErrTagHistoryRequired) {
		t.Fatalf("nil history error = %v", err)
	}
	reader, _ := NewReader(sources, history)
	if _, err := reader.Snapshot(context.Background(), []asset.SourceReference{{}}); !errors.Is(err, asset.ErrInvalidBinding) {
		t.Fatalf("invalid reference error = %v", err)
	}
}

type cadenceSourceReader struct {
	reads          atomic.Int32
	subscribeCalls atomic.Int32
	mu             sync.Mutex
	subscribers    map[uint64]chan publisher.SourceSample
	nextSubscriber uint64
}

func newCadenceSourceReader() *cadenceSourceReader {
	return &cadenceSourceReader{subscribers: make(map[uint64]chan publisher.SourceSample)}
}

func (reader *cadenceSourceReader) Snapshot(context.Context, []publisher.SourceSelection) (publisher.SourceSnapshot, error) {
	return publisher.SourceSnapshot{CapturedAt: time.Now().UTC()}, nil
}

func (reader *cadenceSourceReader) Subscribe(ctx context.Context, _ []publisher.SourceSelection) (publisher.SourceSubscription, error) {
	reader.subscribeCalls.Add(1)
	reader.mu.Lock()
	reader.nextSubscriber++
	id := reader.nextSubscriber
	events := make(chan publisher.SourceSample, 4)
	reader.subscribers[id] = events
	reader.mu.Unlock()
	return &cadenceSubscription{events: events, close: func() {
		reader.mu.Lock()
		if current, exists := reader.subscribers[id]; exists {
			delete(reader.subscribers, id)
			close(current)
		}
		reader.mu.Unlock()
	}}, nil
}

func (reader *cadenceSourceReader) poll(tagID uuid.UUID, sequence uint64, value int) {
	reader.reads.Add(1)
	now := time.Now().UTC()
	event := publisher.SourceSample{Reference: publisher.TagSource(tagID), Available: true, Sequence: sequence, Value: value, Quality: publisher.SourceQualityGood, ObservedAt: &now}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for _, subscriber := range reader.subscribers {
		subscriber <- event
	}
}

type cadenceSubscription struct {
	events chan publisher.SourceSample
	close  func()
	once   sync.Once
}

func (subscription *cadenceSubscription) Events() <-chan publisher.SourceSample {
	return subscription.events
}
func (*cadenceSubscription) Err() error { return nil }
func (subscription *cadenceSubscription) Close() {
	subscription.once.Do(subscription.close)
}
