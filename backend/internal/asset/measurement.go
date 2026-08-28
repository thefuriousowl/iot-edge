package asset

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
)

const (
	MeasurementHistoryLimit = 10
	MaxMeasurementsPerAsset = 256
)

var (
	ErrLiveSourceReaderRequired = errors.New("Asset live source reader is required")
	ErrMeasurementUnavailable   = errors.New("Asset measurement projection is unavailable")
)

type LiveSample struct {
	Source      SourceReference `json:"source"`
	Available   bool            `json:"available"`
	Sequence    uint64          `json:"sequence,omitempty"`
	Value       any             `json:"value"`
	Quality     string          `json:"quality"`
	Error       string          `json:"error,omitempty"`
	ObservedAt  *time.Time      `json:"observed_at,omitempty"`
	EmittedAt   *time.Time      `json:"emitted_at,omitempty"`
	PeriodStart *time.Time      `json:"period_start,omitempty"`
	PeriodEnd   *time.Time      `json:"period_end,omitempty"`
}

type LiveSnapshot struct {
	CapturedAt time.Time    `json:"captured_at"`
	Samples    []LiveSample `json:"samples"`
}

type LiveSubscription interface {
	Events() <-chan LiveSample
	Err() error
	Close()
}

type LiveSourceReader interface {
	Snapshot(context.Context, []SourceReference) (LiveSnapshot, error)
	History(SourceReference, int) []LiveSample
	Subscribe(context.Context, []SourceReference) (LiveSubscription, error)
}

type MeasurementReading struct {
	BindingID   uuid.UUID       `json:"binding_id"`
	Source      SourceReference `json:"source"`
	Semantic    Semantic        `json:"semantic"`
	Available   bool            `json:"available"`
	Sequence    uint64          `json:"sequence,omitempty"`
	Value       any             `json:"value"`
	Quality     string          `json:"quality"`
	Error       string          `json:"error,omitempty"`
	ObservedAt  *time.Time      `json:"observed_at,omitempty"`
	EmittedAt   *time.Time      `json:"emitted_at,omitempty"`
	PeriodStart *time.Time      `json:"period_start,omitempty"`
	PeriodEnd   *time.Time      `json:"period_end,omitempty"`
}

type MeasurementProjection struct {
	Binding          MeasurementBinding   `json:"binding"`
	Latest           MeasurementReading   `json:"latest"`
	History          []MeasurementReading `json:"history"`
	HistoryRetention string               `json:"history_retention"`
}

type MeasurementSnapshot struct {
	AssetID      uuid.UUID               `json:"asset_id"`
	CapturedAt   time.Time               `json:"captured_at"`
	Measurements []MeasurementProjection `json:"measurements"`
}

type MeasurementSubscription interface {
	Events() <-chan MeasurementReading
	Err() error
	Close()
}

type MeasurementProjector struct {
	repository Repository
	sources    LiveSourceReader
}

func NewMeasurementProjector(repository Repository, sources LiveSourceReader) (*MeasurementProjector, error) {
	if isNilMeasurementDependency(repository) {
		return nil, ErrAssetRepositoryRequired
	}
	if isNilMeasurementDependency(sources) {
		return nil, ErrLiveSourceReaderRequired
	}
	return &MeasurementProjector{repository: repository, sources: sources}, nil
}

func (projector *MeasurementProjector) Snapshot(ctx context.Context, assetID uuid.UUID) (*MeasurementSnapshot, error) {
	bindings, err := projector.bindings(ctx, assetID)
	if err != nil {
		return nil, err
	}
	references := bindingReferences(bindings)
	result := &MeasurementSnapshot{AssetID: assetID, Measurements: make([]MeasurementProjection, len(bindings))}
	if len(references) == 0 {
		result.CapturedAt = time.Now().UTC()
		return result, nil
	}
	snapshot, err := projector.sources.Snapshot(ctx, references)
	if err != nil {
		return nil, err
	}
	result.CapturedAt = snapshot.CapturedAt
	latest := make(map[SourceReference]LiveSample, len(snapshot.Samples))
	for _, sample := range snapshot.Samples {
		latest[sample.Source] = sample
	}
	for index, binding := range bindings {
		sample, exists := latest[binding.Source]
		if !exists {
			sample = unavailableSample(binding.Source)
		}
		history := projector.sources.History(binding.Source, MeasurementHistoryLimit)
		readings := make([]MeasurementReading, len(history))
		for historyIndex, value := range history {
			readings[historyIndex] = projectReading(binding, value)
		}
		retention := "latest_only"
		if binding.Source.Kind == SourceTag {
			retention = "runtime_memory"
		}
		result.Measurements[index] = MeasurementProjection{Binding: cloneBinding(binding), Latest: projectReading(binding, sample), History: readings, HistoryRetention: retention}
	}
	return result, nil
}

func (projector *MeasurementProjector) Subscribe(ctx context.Context, assetID uuid.UUID) (MeasurementSubscription, error) {
	bindings, err := projector.bindings(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, ErrMeasurementUnavailable
	}
	upstream, err := projector.sources.Subscribe(ctx, bindingReferences(bindings))
	if err != nil {
		return nil, err
	}
	bySource := make(map[SourceReference]MeasurementBinding, len(bindings))
	for _, binding := range bindings {
		bySource[binding.Source] = binding
	}
	subscription := &measurementSubscription{upstream: upstream, events: make(chan MeasurementReading, len(bindings)+16), done: make(chan struct{})}
	go subscription.run(bySource)
	return subscription, nil
}

func (projector *MeasurementProjector) bindings(ctx context.Context, assetID uuid.UUID) ([]MeasurementBinding, error) {
	if ctx == nil || assetID == uuid.Nil {
		return nil, ErrInvalidAssetInput
	}
	if _, err := projector.repository.Find(ctx, assetID); err != nil {
		return nil, err
	}
	return projector.repository.ListBindings(ctx, assetID)
}

type measurementSubscription struct {
	upstream LiveSubscription
	events   chan MeasurementReading
	done     chan struct{}
}

func (subscription *measurementSubscription) Events() <-chan MeasurementReading {
	return subscription.events
}
func (subscription *measurementSubscription) Err() error { return subscription.upstream.Err() }
func (subscription *measurementSubscription) Close() {
	select {
	case <-subscription.done:
	default:
		close(subscription.done)
		subscription.upstream.Close()
	}
}
func (subscription *measurementSubscription) run(bindings map[SourceReference]MeasurementBinding) {
	defer close(subscription.events)
	defer subscription.upstream.Close()
	for {
		select {
		case <-subscription.done:
			return
		case sample, open := <-subscription.upstream.Events():
			if !open {
				return
			}
			binding, exists := bindings[sample.Source]
			if !exists {
				continue
			}
			select {
			case subscription.events <- projectReading(binding, sample):
			case <-subscription.done:
				return
			}
		}
	}
}

func bindingReferences(bindings []MeasurementBinding) []SourceReference {
	values := make([]SourceReference, len(bindings))
	for index, binding := range bindings {
		values[index] = binding.Source
	}
	return values
}

func projectReading(binding MeasurementBinding, sample LiveSample) MeasurementReading {
	return MeasurementReading{BindingID: binding.ID, Source: sample.Source, Semantic: cloneSemantic(binding.Semantic), Available: sample.Available, Sequence: sample.Sequence, Value: sample.Value, Quality: sample.Quality, Error: sample.Error, ObservedAt: cloneTimePointer(sample.ObservedAt), EmittedAt: cloneTimePointer(sample.EmittedAt), PeriodStart: cloneTimePointer(sample.PeriodStart), PeriodEnd: cloneTimePointer(sample.PeriodEnd)}
}

func unavailableSample(source SourceReference) LiveSample {
	return LiveSample{Source: source, Available: false, Quality: "unavailable"}
}

func cloneSemantic(value Semantic) Semantic {
	value.Reference.TemperatureKelvin = cloneFloatPointer(value.Reference.TemperatureKelvin)
	value.Reference.PressurePascal = cloneFloatPointer(value.Reference.PressurePascal)
	return value
}
func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func isNilMeasurementDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
