package energy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestDefaultRuntimeFactoryRequiresCommittedBatchCapability(t *testing.T) {
	t.Parallel()
	config := testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 60)
	factory := defaultRuntimeFactory{}
	if _, err := factory.NewRuntime(plugin.RuntimeSpec{}, nil, config); !errors.Is(err, plugin.ErrHostRequired) {
		t.Errorf("NewRuntime(nil host) error = %v", err)
	}
	host, _ := plugin.NewCapabilityHost(nil)
	if _, err := factory.NewRuntime(plugin.RuntimeSpec{}, host, config); !errors.Is(err, ErrCommittedBatchFeedUnavailable) {
		t.Errorf("NewRuntime(missing capability) error = %v", err)
	}
	invalidHost, _ := plugin.NewCapabilityHost(map[plugin.Capability]any{plugin.CapabilityLoggerCommittedBatches: struct{}{}})
	if _, err := factory.NewRuntime(plugin.RuntimeSpec{}, invalidHost, config); !errors.Is(err, ErrCommittedBatchFeedUnavailable) {
		t.Errorf("NewRuntime(wrong capability type) error = %v", err)
	}
	feed := newEnergyRuntimeFeed(datalogger.RawBatch{})
	missingHistory, _ := plugin.NewCapabilityHost(map[plugin.Capability]any{plugin.CapabilityLoggerCommittedBatches: feed})
	if _, err := factory.NewRuntime(plugin.RuntimeSpec{}, missingHistory, config); !errors.Is(err, ErrHistoryFeedUnavailable) {
		t.Errorf("NewRuntime(missing history) error = %v", err)
	}
	missingOutput, _ := plugin.NewCapabilityHost(map[plugin.Capability]any{
		plugin.CapabilityLoggerCommittedBatches: feed,
		plugin.CapabilityLoggerHistoryBatches:   feed,
	})
	if _, err := factory.NewRuntime(plugin.RuntimeSpec{}, missingOutput, config); !errors.Is(err, ErrOutputSinkUnavailable) {
		t.Errorf("NewRuntime(missing output) error = %v", err)
	}
}

func TestEnergyRuntimeSubscribesBeforeLatestAndTracksOnlyNewerBatches(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	feed := newEnergyRuntimeFeed(energyRawBatch(loggerID, electricalID, thermalID, start, 2, 6))
	host := newEnergyRuntimeHost(feed, &energyRuntimeOutputSink{})
	built, err := (defaultRuntimeFactory{}).NewRuntime(plugin.RuntimeSpec{InstanceID: uuid.New()}, host, config)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	runtime := built.(*runtime)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitEnergyRuntimeLatest(t, runtime, start)
	feed.publish(energyRawBatch(loggerID, electricalID, thermalID, start, 99, 99))
	feed.publish(energyRawBatch(loggerID, electricalID, thermalID, start.Add(-time.Second), 99, 99))
	newestAt := start.Add(time.Second)
	feed.publish(energyRawBatch(loggerID, electricalID, thermalID, newestAt, 3, 12))
	latest := waitEnergyRuntimeLatest(t, runtime, newestAt)
	if latest.Electrical.Kilowatts != 3 || latest.Thermal.Kilowatts != 12 || latest.COP.Value != 4 {
		t.Errorf("Latest() = %#v", latest)
	}
	if !feed.subscribedBeforeLatest() {
		t.Error("runtime read latest before subscribing")
	}
	latest.Electrical.Errors = append(latest.Electrical.Errors, MetricError{Message: "mutated"})
	stored, _ := runtime.Latest()
	if len(stored.Electrical.Errors) != 0 {
		t.Errorf("Latest() aliases runtime state = %#v", stored)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run(cancel) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
	if !feed.subscription.isClosed() {
		t.Error("runtime did not close subscription")
	}
}

func TestEnergyRuntimePropagatesFeedFailures(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	t.Run("latest read", func(t *testing.T) {
		feedError := errors.New("history unavailable")
		feed := newEnergyRuntimeFeed(datalogger.RawBatch{})
		feed.latest = nil
		feed.latestErr = feedError
		runtime := newTestEnergyRuntime(t, config, feed)
		if err := runtime.Run(context.Background()); !errors.Is(err, feedError) {
			t.Errorf("Run() error = %v", err)
		}
	})
	t.Run("subscriber lag", func(t *testing.T) {
		feed := newEnergyRuntimeFeed(datalogger.RawBatch{})
		feed.latest = nil
		feed.latestErr = datalogger.ErrRawBatchNotFound
		runtime := newTestEnergyRuntime(t, config, feed)
		done := make(chan error, 1)
		go func() { done <- runtime.Run(context.Background()) }()
		waitEnergyRuntimeSubscribed(t, feed)
		feed.subscription.closeWith(datalogger.ErrCommittedBatchSubscriberLag)
		select {
		case err := <-done:
			if !errors.Is(err, datalogger.ErrCommittedBatchSubscriberLag) {
				t.Errorf("Run() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Run() did not report closed subscription")
		}
	})
}

func TestEnergyRuntimeHandlesGenericOutputPersistenceResults(t *testing.T) {
	t.Parallel()

	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	at := time.Date(2026, time.August, 23, 1, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		sinkErr error
		wantErr error
	}{
		{name: "duplicate restart output", sinkErr: plugin.ErrOutputBatchNotNewer},
		{name: "persistence failure", sinkErr: errors.New("output database unavailable"), wantErr: errors.New("output database unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			feed := newEnergyRuntimeFeed(energyRawBatch(loggerID, electricalID, thermalID, at, 2, 6))
			sink := &energyRuntimeOutputSink{err: test.sinkErr}
			host := newEnergyRuntimeHost(feed, sink)
			built, err := (defaultRuntimeFactory{}).NewRuntime(plugin.RuntimeSpec{InstanceID: uuid.New()}, host, config)
			if err != nil {
				t.Fatalf("NewRuntime() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if test.wantErr == nil {
				done := make(chan error, 1)
				go func() { done <- built.Run(ctx) }()
				waitEnergyRuntimeLatest(t, built.(*runtime), at)
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Run() error = %v", err)
				}
				return
			}
			defer cancel()
			if err := built.Run(ctx); err == nil || err.Error() != test.wantErr.Error() {
				t.Errorf("Run() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestEnergyRuntimePublishesCommittedMetricsThroughLiveHub(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID, instanceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	feed := newEnergyRuntimeFeed(energyRawBatch(loggerID, electricalID, thermalID, start, 2, 6))
	host := newEnergyRuntimeHost(feed, &energyRuntimeOutputSink{})
	hub, _ := NewLiveHub()
	built, err := hub.NewRuntime(plugin.RuntimeSpec{InstanceID: instanceID}, host, config)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	subscription, _ := hub.Subscribe(instanceID, "")
	defer subscription.Unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- built.Run(ctx) }()
	select {
	case event := <-subscription.Stream:
		if event.InstanceID != instanceID || event.Metrics.Electrical.Kilowatts != 2 || event.Metrics.COP.Value != 3 {
			t.Errorf("initial live event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not publish persisted latest metrics")
	}
	feed.publish(energyRawBatch(loggerID, electricalID, thermalID, start.Add(time.Second), 3, 12))
	select {
	case event := <-subscription.Stream:
		if event.Sequence != 2 || event.Metrics.COP.Value != 4 {
			t.Errorf("committed live event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not publish committed metrics")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run(cancel) error = %v", err)
	}
}

type energyRuntimeFeed struct {
	mu           sync.Mutex
	latest       *datalogger.RawBatch
	latestErr    error
	subscription *energyRuntimeSubscription
	order        []string
	batches      []datalogger.RawBatch
}

func newEnergyRuntimeFeed(batch datalogger.RawBatch) *energyRuntimeFeed {
	feed := &energyRuntimeFeed{subscription: &energyRuntimeSubscription{events: make(chan datalogger.RawBatch, 8)}}
	if batch.LoggerID != uuid.Nil {
		copy := batch
		feed.latest = &copy
		feed.batches = append(feed.batches, cloneEnergyRawBatch(batch))
	}
	return feed
}

func (feed *energyRuntimeFeed) Latest(_ context.Context, _ uuid.UUID) (*datalogger.RawBatch, error) {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	feed.order = append(feed.order, "latest")
	if feed.latest == nil {
		return nil, feed.latestErr
	}
	copy := *feed.latest
	copy.Samples = append([]datalogger.RawSample(nil), feed.latest.Samples...)
	return &copy, feed.latestErr
}

func (feed *energyRuntimeFeed) Subscribe(uuid.UUID) (datalogger.CommittedBatchSubscription, error) {
	feed.mu.Lock()
	feed.order = append(feed.order, "subscribe")
	feed.mu.Unlock()
	return feed.subscription, nil
}

func (feed *energyRuntimeFeed) LatestBatch(ctx context.Context, loggerID uuid.UUID) (*datalogger.RawBatch, error) {
	return feed.Latest(ctx, loggerID)
}

func (feed *energyRuntimeFeed) ListBatches(_ context.Context, _ datalogger.RawBatchListInput) ([]datalogger.RawBatch, error) {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	batches := make([]datalogger.RawBatch, len(feed.batches))
	for index, batch := range feed.batches {
		batches[index] = cloneEnergyRawBatch(batch)
	}
	return batches, nil
}

func (feed *energyRuntimeFeed) publish(batch datalogger.RawBatch) {
	feed.mu.Lock()
	if len(feed.batches) == 0 || feed.batches[len(feed.batches)-1].BatchAt.Before(batch.BatchAt) {
		feed.batches = append(feed.batches, cloneEnergyRawBatch(batch))
	}
	feed.mu.Unlock()
	feed.subscription.events <- batch
}

func (feed *energyRuntimeFeed) subscribedBeforeLatest() bool {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	return len(feed.order) >= 2 && feed.order[0] == "subscribe" && feed.order[1] == "latest"
}

type energyRuntimeSubscription struct {
	mu     sync.Mutex
	events chan datalogger.RawBatch
	err    error
	closed bool
	once   sync.Once
}

func (subscription *energyRuntimeSubscription) Events() <-chan datalogger.RawBatch {
	return subscription.events
}

func (subscription *energyRuntimeSubscription) Err() error {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.err
}

func (subscription *energyRuntimeSubscription) Close() {
	subscription.mu.Lock()
	subscription.closed = true
	subscription.mu.Unlock()
}

func (subscription *energyRuntimeSubscription) closeWith(err error) {
	subscription.mu.Lock()
	subscription.err = err
	subscription.mu.Unlock()
	subscription.once.Do(func() { close(subscription.events) })
}

func (subscription *energyRuntimeSubscription) isClosed() bool {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.closed
}

func newTestEnergyRuntime(t *testing.T, config Config, feed *energyRuntimeFeed) *runtime {
	t.Helper()
	host := newEnergyRuntimeHost(feed, &energyRuntimeOutputSink{})
	built, err := (defaultRuntimeFactory{}).NewRuntime(plugin.RuntimeSpec{}, host, config)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return built.(*runtime)
}

type energyRuntimeOutputSink struct {
	mu       sync.Mutex
	values   [][]plugin.OutputValue
	err      error
	sequence uint64
}

func (sink *energyRuntimeOutputSink) Publish(_ context.Context, values []plugin.OutputValue) (*plugin.OutputBatch, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.err != nil {
		return nil, sink.err
	}
	sink.sequence++
	cloned := append([]plugin.OutputValue(nil), values...)
	sink.values = append(sink.values, cloned)
	return &plugin.OutputBatch{InstanceID: uuid.New(), Sequence: sink.sequence, PublishedAt: time.Now().UTC(), Values: cloned}, nil
}

func newEnergyRuntimeHost(feed *energyRuntimeFeed, sink plugin.OutputSink) *plugin.CapabilityHost {
	host, _ := plugin.NewCapabilityHost(map[plugin.Capability]any{
		plugin.CapabilityLoggerCommittedBatches: feed,
		plugin.CapabilityLoggerHistoryBatches:   feed,
		plugin.CapabilityPluginOutputsPublish:   sink,
	})
	return host
}

func cloneEnergyRawBatch(batch datalogger.RawBatch) datalogger.RawBatch {
	batch.Samples = append([]datalogger.RawSample(nil), batch.Samples...)
	return batch
}

func waitEnergyRuntimeLatest(t *testing.T, runtime *runtime, at time.Time) BatchMetrics {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		latest, exists := runtime.Latest()
		if exists && latest.BatchAt.Equal(at) {
			return latest
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runtime did not observe batch at %s", at)
	return BatchMetrics{}
}

func waitEnergyRuntimeSubscribed(t *testing.T, feed *energyRuntimeFeed) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		feed.mu.Lock()
		subscribed := len(feed.order) > 0
		feed.mu.Unlock()
		if subscribed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime did not subscribe")
}

func energyRawBatch(loggerID, electricalID, thermalID uuid.UUID, at time.Time, electrical, thermal float64) datalogger.RawBatch {
	return datalogger.RawBatch{LoggerID: loggerID, BatchAt: at, Samples: []datalogger.RawSample{
		goodEnergySample(electricalID, at, "float64", electrical),
		goodEnergySample(thermalID, at, "float64", thermal),
	}}
}

var _ datalogger.CommittedBatchFeed = (*energyRuntimeFeed)(nil)
var _ HistoryReader = (*energyRuntimeFeed)(nil)
var _ datalogger.CommittedBatchSubscription = (*energyRuntimeSubscription)(nil)
var _ plugin.OutputSink = (*energyRuntimeOutputSink)(nil)
