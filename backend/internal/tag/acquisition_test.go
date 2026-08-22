package tag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestNewAcquisitionRuntimeValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	tests := []struct {
		name       string
		repository Repository
		processor  *Service
		source     DatasourceSubscriber
		values     RuntimeValueStore
		options    []AcquisitionOption
		want       error
	}{
		{name: "repository", processor: processor, source: source, values: values, want: ErrAcquisitionRepositoryRequired},
		{name: "processor", repository: repository, source: source, values: values, want: ErrAcquisitionProcessorRequired},
		{name: "source", repository: repository, processor: processor, values: values, want: ErrAcquisitionSourceRequired},
		{name: "values", repository: repository, processor: processor, source: source, want: ErrAcquisitionValueStoreRequired},
		{name: "interval", repository: repository, processor: processor, source: source, values: values, options: []AcquisitionOption{WithAcquisitionReconcileInterval(-time.Second)}, want: ErrInvalidTagInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewAcquisitionRuntime(test.repository, test.processor, test.source, test.values, test.options...); !errors.Is(err, test.want) {
				t.Fatalf("NewAcquisitionRuntime() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAcquisitionRuntimeFansOneDatasourceSampleOutToAllReadingTags(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	first := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Register one", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)})
	second := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Register two", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":2}}}`)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	observedAt := time.Date(2026, time.August, 22, 9, 0, 0, 0, time.UTC)
	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: observedAt, Quality: "good", Raw: []byte{0, 1, 0, 2}})
	firstValue := awaitTagValue(t, values, first.ID, func(value TagValue) bool { return value.Quality == ValueQualityGood })
	secondValue := awaitTagValue(t, values, second.ID, func(value TagValue) bool { return value.Quality == ValueQualityGood })
	if firstValue.Value != uint16(1) || secondValue.Value != uint16(2) {
		t.Errorf("values = %#v / %#v", firstValue, secondValue)
	}
	if firstValue.ObservedAt != observedAt || secondValue.ObservedAt != observedAt {
		t.Errorf("observed times = %s / %s", firstValue.ObservedAt, secondValue.ObservedAt)
	}
	if source.callCount(datasourceID) != 1 {
		t.Errorf("SubscribeDatasourceForTags() calls = %d, want 1", source.callCount(datasourceID))
	}
}

func TestAcquisitionRuntimeReconcilesTagConfigWithoutDuplicateSubscription(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Selected register", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "good", Raw: []byte{0, 1, 0, 2}})
	first := awaitTagValue(t, values, reading.ID, func(value TagValue) bool { return value.Value == uint16(1) })
	updated := repository.tags[reading.ID]
	updated.Config = json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":2}}}`)
	repository.tags[reading.ID] = updated
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(config) error = %v", err)
	}
	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "good", Raw: []byte{0, 1, 0, 2}})
	second := awaitTagValue(t, values, reading.ID, func(value TagValue) bool { return value.Sequence > first.Sequence && value.Value == uint16(2) })
	if second.Value != uint16(2) || source.callCount(datasourceID) != 1 {
		t.Errorf("updated value/calls = %#v / %d", second, source.callCount(datasourceID))
	}

	updated.Enabled = false
	repository.tags[reading.ID] = updated
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(disabled) error = %v", err)
	}
	if source.unsubscribeCount(datasourceID) != 1 {
		t.Errorf("unsubscribe calls = %d, want 1", source.unsubscribeCount(datasourceID))
	}
}

func TestAcquisitionRuntimeIsolatesTagDecodeFailuresAndRecoversClosedStreams(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	valid := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Valid", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	tooWide := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Too wide", Type: TypeReading, DataType: DataTypeUInt32, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "good", Raw: []byte{0, 7}})
	good := awaitTagValue(t, values, valid.ID, func(value TagValue) bool { return value.Quality == ValueQualityGood })
	bad := awaitTagValue(t, values, tooWide.ID, func(value TagValue) bool { return value.Quality == ValueQualityBad })
	if good.Value != uint16(7) || !strings.Contains(bad.Error, ErrInsufficientBinaryData.Error()) {
		t.Errorf("good/bad = %#v / %#v", good, bad)
	}

	source.closeLatest(datasourceID)
	select {
	case err := <-runtime.Errors():
		if !errors.Is(err, ErrAcquisitionStreamClosed) {
			t.Fatalf("runtime error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed stream was not reported")
	}
	for attempts := 0; attempts < 20 && source.callCount(datasourceID) < 2; attempts++ {
		if err := runtime.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile(closed stream) error = %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if source.callCount(datasourceID) != 2 {
		t.Errorf("subscription calls after recovery = %d, want 2", source.callCount(datasourceID))
	}
}

func TestAcquisitionRuntimePublishesUnavailableValuesWhenSubscriptionFails(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Unavailable", Type: TypeReading, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	source.errors[datasourceID] = errors.New("gateway paused")
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	value := awaitTagValue(t, values, reading.ID, func(value TagValue) bool { return value.Quality == ValueQualityBad })
	if value.Value != nil || !strings.Contains(value.Error, "gateway paused") {
		t.Errorf("unavailable value = %#v", value)
	}
	select {
	case err := <-runtime.Errors():
		if !strings.Contains(err.Error(), "gateway paused") {
			t.Fatalf("runtime error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription failure was not reported")
	}
}

func TestAcquisitionRuntimeHydratesConstantsAndPropagatesCalculatedTags(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	constant := repository.addTag(Tag{Name: "Base", Type: TypeConstant, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"value":2}`)})
	firstExpression := fmt.Sprintf("${%s} + ${%s}", reading.ID, constant.ID)
	first := repository.addTag(Tag{Name: "First", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(firstExpression, reading.ID)})
	second := repository.addTag(Tag{Name: "Second", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} * 2", first.ID), reading.ID)})
	chained := repository.addTag(Tag{Name: "Chained", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} + 1", first.ID), first.ID)})
	sourceReader := newServiceDatasourceReader()
	processor := newTagTestService(t, repository, sourceReader)
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	constantValue := awaitTagValue(t, values, constant.ID, func(value TagValue) bool { return value.Value == uint16(2) })
	if constantValue.Quality != ValueQualityGood || len(values.History(constant.ID, 10)) != 1 {
		t.Errorf("constant value/history = %#v / %#v", constantValue, values.History(constant.ID, 10))
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged) error = %v", err)
	}
	if len(values.History(constant.ID, 10)) != 1 {
		t.Errorf("unchanged constant was republished: %#v", values.History(constant.ID, 10))
	}

	observedAt := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: observedAt, Quality: "good", Raw: []byte{0, 3}})
	firstValue := awaitTagValue(t, values, first.ID, func(value TagValue) bool { return value.Value == uint16(5) })
	secondValue := awaitTagValue(t, values, second.ID, func(value TagValue) bool { return value.Value == uint16(10) })
	chainedValue := awaitTagValue(t, values, chained.ID, func(value TagValue) bool { return value.Value == uint16(6) })
	if firstValue.ObservedAt != observedAt || secondValue.ObservedAt != observedAt || chainedValue.ObservedAt != observedAt {
		t.Errorf("calculated observation times = %s / %s / %s", firstValue.ObservedAt, secondValue.ObservedAt, chainedValue.ObservedAt)
	}
	if len(sourceReader.calls) != 0 {
		t.Errorf("calculated propagation performed datasource reads: %#v", sourceReader.calls)
	}

	updatedConstant := repository.tags[constant.ID]
	updatedConstant.Config = json.RawMessage(`{"value":4}`)
	repository.tags[constant.ID] = updatedConstant
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(updated constant) error = %v", err)
	}
	awaitTagValue(t, values, constant.ID, func(value TagValue) bool { return value.Value == uint16(4) })
	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: observedAt.Add(time.Second), Quality: "good", Raw: []byte{0, 3}})
	awaitTagValue(t, values, second.ID, func(value TagValue) bool { return value.Value == uint16(14) })
}

func TestAcquisitionRuntimePropagatesBadTriggerQualityToCalculatedChains(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	datasourceID := uuid.New()
	reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	calculated := repository.addTag(Tag{Name: "Calculated", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} + 1", reading.ID), reading.ID)})
	chained := repository.addTag(Tag{Name: "Chained", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} + 1", calculated.ID), calculated.ID)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	source := newAcquisitionTestSource()
	values := NewMemoryValueStore()
	runtime := newAcquisitionTestRuntime(t, repository, processor, source, values)
	startAcquisitionTestRuntime(t, runtime)

	source.emit(datasourceID, protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "bad", Error: "illegal data address"})
	firstBad := awaitTagValue(t, values, calculated.ID, func(value TagValue) bool { return value.Quality == ValueQualityBad })
	secondBad := awaitTagValue(t, values, chained.ID, func(value TagValue) bool { return value.Quality == ValueQualityBad })
	if !strings.Contains(firstBad.Error, "trigger tag") || !strings.Contains(secondBad.Error, "trigger tag") {
		t.Errorf("calculated errors = %q / %q", firstBad.Error, secondBad.Error)
	}
}

func TestAcquisitionRuntimeReportsUnchangedInvalidCalculatedTagOnce(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	invalid := repository.addTag(Tag{Name: "Legacy", Type: TypeCalculated, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"expression":"1 + 1"}`)})
	processor := newTagTestService(t, repository, newServiceDatasourceReader())
	runtime := newAcquisitionTestRuntime(t, repository, processor, newAcquisitionTestSource(), NewMemoryValueStore())
	startAcquisitionTestRuntime(t, runtime)

	select {
	case err := <-runtime.Errors():
		if !strings.Contains(err.Error(), invalid.ID.String()) || !strings.Contains(err.Error(), "trigger") {
			t.Fatalf("runtime error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid calculated tag was not reported")
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(unchanged invalid) error = %v", err)
	}
	select {
	case err := <-runtime.Errors():
		t.Fatalf("unchanged invalid calculated tag was reported again: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
}

type acquisitionTestSource struct {
	mu           sync.Mutex
	calls        map[uuid.UUID]int
	unsubscribes map[uuid.UUID]int
	streams      map[uuid.UUID][]chan protocol.DatasourceSample
	errors       map[uuid.UUID]error
}

func newAcquisitionTestSource() *acquisitionTestSource {
	return &acquisitionTestSource{calls: make(map[uuid.UUID]int), unsubscribes: make(map[uuid.UUID]int), streams: make(map[uuid.UUID][]chan protocol.DatasourceSample), errors: make(map[uuid.UUID]error)}
}

func (source *acquisitionTestSource) SubscribeDatasourceForTags(_ context.Context, datasourceID uuid.UUID) (<-chan protocol.DatasourceSample, func(), error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls[datasourceID]++
	if err := source.errors[datasourceID]; err != nil {
		return nil, nil, err
	}
	stream := make(chan protocol.DatasourceSample, 8)
	source.streams[datasourceID] = append(source.streams[datasourceID], stream)
	var once sync.Once
	return stream, func() {
		once.Do(func() {
			source.mu.Lock()
			source.unsubscribes[datasourceID]++
			source.mu.Unlock()
		})
	}, nil
}

func (source *acquisitionTestSource) emit(datasourceID uuid.UUID, sample protocol.DatasourceSample) {
	source.mu.Lock()
	streams := source.streams[datasourceID]
	stream := streams[len(streams)-1]
	source.mu.Unlock()
	stream <- sample
}

func (source *acquisitionTestSource) closeLatest(datasourceID uuid.UUID) {
	source.mu.Lock()
	streams := source.streams[datasourceID]
	close(streams[len(streams)-1])
	source.mu.Unlock()
}

func (source *acquisitionTestSource) callCount(datasourceID uuid.UUID) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls[datasourceID]
}

func (source *acquisitionTestSource) unsubscribeCount(datasourceID uuid.UUID) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.unsubscribes[datasourceID]
}

func newAcquisitionTestRuntime(t *testing.T, repository Repository, processor *Service, source DatasourceSubscriber, values RuntimeValueStore) *AcquisitionRuntime {
	t.Helper()
	runtime, err := NewAcquisitionRuntime(repository, processor, source, values, WithAcquisitionReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewAcquisitionRuntime() error = %v", err)
	}
	return runtime
}

func startAcquisitionTestRuntime(t *testing.T, runtime *AcquisitionRuntime) {
	t.Helper()
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)
}

func awaitTagValue(t *testing.T, store *MemoryValueStore, tagID uuid.UUID, matches func(TagValue) bool) TagValue {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value, exists := store.Latest(tagID); exists && matches(value) {
			return value
		}
		time.Sleep(5 * time.Millisecond)
	}
	value, _ := store.Latest(tagID)
	t.Fatalf("latest Tag value did not match: %#v", value)
	return TagValue{}
}
