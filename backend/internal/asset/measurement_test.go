package asset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type measurementReader struct {
	snapshot     LiveSnapshot
	history      map[SourceReference][]LiveSample
	subscription *measurementLiveSubscription
	err          error
	references   []SourceReference
	historyLimit int
}

func (reader *measurementReader) Snapshot(_ context.Context, references []SourceReference) (LiveSnapshot, error) {
	reader.references = append([]SourceReference(nil), references...)
	return reader.snapshot, reader.err
}
func (reader *measurementReader) History(reference SourceReference, limit int) []LiveSample {
	reader.historyLimit = limit
	return append([]LiveSample(nil), reader.history[reference]...)
}
func (reader *measurementReader) Subscribe(_ context.Context, references []SourceReference) (LiveSubscription, error) {
	reader.references = append([]SourceReference(nil), references...)
	return reader.subscription, reader.err
}

type measurementLiveSubscription struct {
	events chan LiveSample
	err    error
	closed bool
}

func (subscription *measurementLiveSubscription) Events() <-chan LiveSample {
	return subscription.events
}
func (subscription *measurementLiveSubscription) Err() error { return subscription.err }
func (subscription *measurementLiveSubscription) Close()     { subscription.closed = true }

func TestMeasurementProjectorSnapshotsLatestAndBoundedHistory(t *testing.T) {
	ctx := context.Background()
	assetID, tagID, pluginID := uuid.New(), uuid.New(), uuid.New()
	repository := newMemoryAssetRepository(Asset{ID: assetID, Name: "Meter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)})
	tagSource, pluginSource := TagSource(tagID), PluginOutputSource(pluginID, "power")
	tagBinding := measurementBindingForTest(assetID, tagSource)
	pluginBinding := measurementBindingForTest(assetID, pluginSource)
	repository.bindings[assetID] = []MeasurementBinding{tagBinding, pluginBinding}
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	reader := &measurementReader{
		snapshot: LiveSnapshot{CapturedAt: at, Samples: []LiveSample{{Source: tagSource, Available: true, Sequence: 8, Value: 12.5, Quality: "good", ObservedAt: &at}}},
		history:  map[SourceReference][]LiveSample{tagSource: {{Source: tagSource, Available: true, Sequence: 7, Value: 11.5, Quality: "good", ObservedAt: &at}}},
	}
	projector, err := NewMeasurementProjector(repository, reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := projector.Snapshot(ctx, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AssetID != assetID || !snapshot.CapturedAt.Equal(at) || len(snapshot.Measurements) != 2 || len(reader.references) != 2 || reader.historyLimit != MeasurementHistoryLimit {
		t.Fatalf("snapshot=%#v references=%#v limit=%d", snapshot, reader.references, reader.historyLimit)
	}
	if snapshot.Measurements[0].Latest.BindingID != tagBinding.ID || snapshot.Measurements[0].Latest.Sequence != 8 || len(snapshot.Measurements[0].History) != 1 || snapshot.Measurements[0].HistoryRetention != "runtime_memory" {
		t.Fatalf("Tag projection = %#v", snapshot.Measurements[0])
	}
	if snapshot.Measurements[1].Latest.Available || snapshot.Measurements[1].Latest.Quality != "unavailable" || snapshot.Measurements[1].HistoryRetention != "latest_only" {
		t.Fatalf("Plugin projection = %#v", snapshot.Measurements[1])
	}
	snapshot.Measurements[0].Latest.Semantic.Reference.TemperatureKelvin = floatPointer(1)
	if repository.bindings[assetID][0].Semantic.Reference.TemperatureKelvin != nil {
		t.Fatal("projection aliases binding semantic")
	}
}

func TestMeasurementProjectorStreamsBindingContext(t *testing.T) {
	assetID, tagID := uuid.New(), uuid.New()
	repository := newMemoryAssetRepository(Asset{ID: assetID, Name: "Meter", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)})
	source := TagSource(tagID)
	binding := measurementBindingForTest(assetID, source)
	repository.bindings[assetID] = []MeasurementBinding{binding}
	upstream := &measurementLiveSubscription{events: make(chan LiveSample, 1)}
	upstream.events <- LiveSample{Source: source, Available: true, Sequence: 9, Value: 14.0, Quality: "good"}
	close(upstream.events)
	projector, _ := NewMeasurementProjector(repository, &measurementReader{subscription: upstream})
	subscription, err := projector.Subscribe(context.Background(), assetID)
	if err != nil {
		t.Fatal(err)
	}
	reading := <-subscription.Events()
	if reading.BindingID != binding.ID || reading.Source != source || reading.Sequence != 9 || reading.Value != 14.0 {
		t.Fatalf("reading = %#v", reading)
	}
	for range subscription.Events() {
	}
	if !upstream.closed {
		t.Fatal("upstream was not closed")
	}
}

func TestMeasurementProjectorRejectsDependenciesAndEmptyStream(t *testing.T) {
	reader := &measurementReader{}
	if _, err := NewMeasurementProjector(nil, reader); !errors.Is(err, ErrAssetRepositoryRequired) {
		t.Fatalf("nil repository error = %v", err)
	}
	repository := newMemoryAssetRepository()
	if _, err := NewMeasurementProjector(repository, nil); !errors.Is(err, ErrLiveSourceReaderRequired) {
		t.Fatalf("nil source reader error = %v", err)
	}
	assetID := uuid.New()
	repository.assets[assetID] = Asset{ID: assetID, Name: "Empty", Kind: KindMeter, Enabled: true, Metadata: Metadata(`{}`)}
	projector, _ := NewMeasurementProjector(repository, reader)
	if _, err := projector.Subscribe(context.Background(), assetID); !errors.Is(err, ErrMeasurementUnavailable) {
		t.Fatalf("empty stream error = %v", err)
	}
}

func measurementBindingForTest(owner uuid.UUID, source SourceReference) MeasurementBinding {
	return MeasurementBinding{ID: uuid.New(), OwnerAssetID: owner, BoundaryAssetID: owner, SourceKey: source.Key(), Source: source, Semantic: Semantic{Resource: ResourceElectricity, Quantity: QuantityPower, Unit: UnitKilowatt, Precision: 3}, MeterRole: MeterRoleDirect, RollupPolicy: RollupInclude}
}

func floatPointer(value float64) *float64 { return &value }
