package tag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestNewTagServiceValidatesDependencies(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	source := newServiceDatasourceReader()
	decoder := NewBinaryNumericDecoder()
	tests := []struct {
		name     string
		repo     Repository
		source   DatasourceReader
		decoders []ReadingDecoder
		want     error
	}{
		{name: "repository", source: source, decoders: []ReadingDecoder{decoder}, want: ErrTagRepositoryRequired},
		{name: "source", repo: repository, decoders: []ReadingDecoder{decoder}, want: ErrDatasourceReaderRequired},
		{name: "decoders", repo: repository, source: source, want: ErrReadingDecodersRequired},
		{name: "nil decoder", repo: repository, source: source, decoders: []ReadingDecoder{nil}, want: ErrReadingDecodersRequired},
		{name: "duplicate decoder", repo: repository, source: source, decoders: []ReadingDecoder{decoder, decoder}, want: ErrDuplicateReadingDecoder},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.repo, test.source, test.decoders...); !errors.Is(err, test.want) {
				t.Fatalf("NewService() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTagServiceCreatesAndNormalizesAllTagTypes(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	service := newTagTestService(t, repository, newServiceDatasourceReader())
	datasourceID := uuid.New()
	description := "  Main line  "

	reading, err := service.Create(context.Background(), CreateInput{DatasourceID: &datasourceID, Name: "  Line voltage  ", Type: TypeReading, DataType: DataTypeUInt16, Description: &description, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":2}}}`)})
	if err != nil {
		t.Fatalf("Create(reading) error = %v", err)
	}
	if reading.ID == uuid.Nil || reading.Name != "Line voltage" || reading.Description == nil || *reading.Description != "Main line" || !reading.Enabled {
		t.Errorf("reading tag = %#v", reading)
	}
	if string(reading.Config) != `{"decoder":{"type":"binary_numeric","config":{"byte_offset":2,"byte_order":"big_endian","bit_offset":0}}}` {
		t.Errorf("reading config = %s", reading.Config)
	}

	disabled := false
	constant, err := service.Create(context.Background(), CreateInput{Name: "Nominal", Type: TypeConstant, DataType: DataTypeFloat64, Enabled: &disabled, Config: json.RawMessage(`{"value":230.5}`)})
	if err != nil {
		t.Fatalf("Create(constant) error = %v", err)
	}
	if constant.Enabled || string(constant.Config) != `{"value":230.5}` {
		t.Errorf("constant tag = %#v", constant)
	}

	expression := fmt.Sprintf("${%s} - ${%s}", reading.ID, constant.ID)
	calculated, err := service.Create(context.Background(), CreateInput{Name: "Delta", Type: TypeCalculated, DataType: DataTypeFloat64, Config: expressionConfig(expression, reading.ID)})
	if err != nil {
		t.Fatalf("Create(calculated) error = %v", err)
	}
	dependencies := repository.dependencies[calculated.ID]
	if len(dependencies) != 2 || dependencies[0] != reading.ID || dependencies[1] != constant.ID {
		t.Errorf("calculated dependencies = %v", dependencies)
	}
}

func TestTagServiceRejectsInvalidTagContracts(t *testing.T) {
	t.Parallel()
	datasourceID := uuid.New()
	longName := strings.Repeat("ก", 101)
	tests := []struct {
		name  string
		input CreateInput
		want  error
	}{
		{name: "empty name", input: CreateInput{Name: " ", Type: TypeConstant, DataType: DataTypeFloat64, Config: json.RawMessage(`{"value":1}`)}, want: ErrInvalidTagInput},
		{name: "long name", input: CreateInput{Name: longName, Type: TypeConstant, DataType: DataTypeFloat64, Config: json.RawMessage(`{"value":1}`)}, want: ErrInvalidTagInput},
		{name: "unsupported type", input: CreateInput{Name: "Tag", Type: "future", DataType: DataTypeFloat64, Config: json.RawMessage(`{}`)}, want: ErrUnsupportedTagType},
		{name: "unsupported data type", input: CreateInput{Name: "Tag", Type: TypeConstant, DataType: "string", Config: json.RawMessage(`{"value":1}`)}, want: ErrUnsupportedTagDataType},
		{name: "reading without datasource", input: CreateInput{Name: "Tag", Type: TypeReading, DataType: DataTypeUInt16, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)}, want: ErrInvalidTagInput},
		{name: "constant with datasource", input: CreateInput{DatasourceID: &datasourceID, Name: "Tag", Type: TypeConstant, DataType: DataTypeFloat64, Config: json.RawMessage(`{"value":1}`)}, want: ErrInvalidTagInput},
		{name: "unknown decoder", input: CreateInput{DatasourceID: &datasourceID, Name: "Tag", Type: TypeReading, DataType: DataTypeUInt16, Config: json.RawMessage(`{"decoder":{"type":"json_path"}}`)}, want: ErrUnsupportedDecoder},
		{name: "decoder unknown field", input: CreateInput{DatasourceID: &datasourceID, Name: "Tag", Type: TypeReading, DataType: DataTypeUInt16, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"address":1}}}`)}, want: ErrInvalidTagInput},
		{name: "constant out of range", input: CreateInput{Name: "Tag", Type: TypeConstant, DataType: DataTypeInt16, Config: json.RawMessage(`{"value":32768}`)}, want: ErrInvalidTagInput},
		{name: "constant wrong type", input: CreateInput{Name: "Tag", Type: TypeConstant, DataType: DataTypeBool, Config: json.RawMessage(`{"value":1}`)}, want: ErrInvalidTagInput},
		{name: "constant null", input: CreateInput{Name: "Tag", Type: TypeConstant, DataType: DataTypeBool, Config: json.RawMessage(`{"value":null}`)}, want: ErrInvalidTagInput},
		{name: "invalid expression", input: CreateInput{Name: "Tag", Type: TypeCalculated, DataType: DataTypeFloat64, Config: expressionConfig("1 +", uuid.New())}, want: ErrInvalidTagInput},
		{name: "missing trigger", input: CreateInput{Name: "Tag", Type: TypeCalculated, DataType: DataTypeFloat64, Config: json.RawMessage(`{"expression":"1"}`)}, want: ErrCalculatedTriggerInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTagTestService(t, newServiceMemoryRepository(), newServiceDatasourceReader())
			if _, err := service.Create(context.Background(), test.input); !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTagServiceValidatesMissingAndCircularDependencies(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	service := newTagTestService(t, repository, newServiceDatasourceReader())
	missingID := uuid.New()
	if _, err := service.Create(context.Background(), CreateInput{Name: "Missing", Type: TypeCalculated, DataType: DataTypeFloat64, Config: calculatedConfig(missingID)}); !errors.Is(err, ErrCalculatedDependencyMissing) {
		t.Fatalf("Create(missing dependency) error = %v", err)
	}

	trigger := repository.addTag(Tag{Name: "Trigger", Type: TypeReading, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	first := repository.addTag(Tag{Name: "First", Type: TypeCalculated, DataType: DataTypeFloat64, Enabled: true, Config: expressionConfig("1", trigger.ID)})
	second := repository.addTag(Tag{Name: "Second", Type: TypeCalculated, DataType: DataTypeFloat64, Enabled: true, Config: calculatedConfig(first.ID)})
	repository.dependencies[second.ID] = []uuid.UUID{first.ID}
	if _, err := service.Update(context.Background(), first.ID, UpdateInput{Config: configPointer(calculatedConfig(second.ID))}); !errors.Is(err, ErrCircularTagDependency) {
		t.Fatalf("Update(circular) error = %v", err)
	}
	if _, err := service.ValidateCalculatedExpression(context.Background(), first.ID, fmt.Sprintf("${%s}", first.ID)); !errors.Is(err, ErrCircularTagDependency) {
		t.Fatalf("ValidateCalculatedExpression(self) error = %v", err)
	}

	chain := make([]uuid.UUID, maxTagDependencyDepth+1)
	for index := range chain {
		entity := repository.addTag(Tag{Name: fmt.Sprintf("Chain %d", index), Type: TypeCalculated, DataType: DataTypeFloat64, Enabled: true, Config: expressionConfig("1", trigger.ID)})
		chain[index] = entity.ID
		if index > 0 {
			repository.dependencies[chain[index-1]] = []uuid.UUID{chain[index]}
		}
	}
	if _, err := service.ValidateCalculatedExpression(context.Background(), uuid.New(), fmt.Sprintf("${%s}", chain[0])); !errors.Is(err, ErrTagDependencyTooDeep) {
		t.Fatalf("ValidateCalculatedExpression(deep graph) error = %v", err)
	}
}

func TestTagServiceValidatesCalculatedTriggerContract(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	service := newTagTestService(t, repository, newServiceDatasourceReader())
	reading := repository.addTag(Tag{Name: "Reading trigger", Type: TypeReading, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
	constant := repository.addTag(Tag{Name: "Constant trigger", Type: TypeConstant, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"value":1}`)})

	calculated, err := service.Create(context.Background(), CreateInput{Name: "Triggered independently", Type: TypeCalculated, DataType: DataTypeFloat64, Config: expressionConfig("1 + 2", reading.ID)})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	dependencies := repository.dependencies[calculated.ID]
	if len(dependencies) != 1 || dependencies[0] != reading.ID {
		t.Errorf("dependencies = %v, want trigger %s", dependencies, reading.ID)
	}

	if _, err := service.Create(context.Background(), CreateInput{Name: "Constant triggered", Type: TypeCalculated, DataType: DataTypeFloat64, Config: expressionConfig("1", constant.ID)}); !errors.Is(err, ErrCalculatedTriggerInvalid) {
		t.Fatalf("Create(constant trigger) error = %v", err)
	}
	unsupportedMode := json.RawMessage(fmt.Sprintf(`{"expression":"1","trigger":{"tag_id":"%s","mode":"interval"}}`, reading.ID))
	if _, err := service.Create(context.Background(), CreateInput{Name: "Unsupported trigger", Type: TypeCalculated, DataType: DataTypeFloat64, Config: unsupportedMode}); !errors.Is(err, ErrCalculatedTriggerInvalid) {
		t.Fatalf("Create(unsupported mode) error = %v", err)
	}
}

func TestTagServiceUpdatesListsAndDeletes(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	service := newTagTestService(t, repository, newServiceDatasourceReader())
	entity := repository.addTag(Tag{Name: "Original", Type: TypeConstant, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"value":1}`)})
	name := " Updated "
	description := " Description "
	dataType := DataTypeUInt32
	enabled := false
	config := Config(`{"value":42}`)
	updated, err := service.Update(context.Background(), entity.ID, UpdateInput{Name: &name, DataType: &dataType, Description: OptionalDescription{Set: true, Value: &description}, Enabled: &enabled, Config: &config})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Updated" || updated.Type != TypeConstant || updated.DataType != DataTypeUInt32 || updated.Description == nil || *updated.Description != "Description" || updated.Enabled || string(updated.Config) != `{"value":42}` {
		t.Errorf("updated tag = %#v", updated)
	}

	result, err := service.List(context.Background(), ListInput{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.lastList.Page != 1 || repository.lastList.PerPage != 20 || len(result.Data) != 1 {
		t.Errorf("list input/result = %#v / %#v", repository.lastList, result)
	}
	if _, err := service.List(context.Background(), ListInput{PerPage: 101}); !errors.Is(err, ErrInvalidTagInput) {
		t.Fatalf("List(invalid pagination) error = %v", err)
	}
	if err := service.Delete(context.Background(), entity.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(context.Background(), entity.ID); !errors.Is(err, ErrTagNotFound) {
		t.Fatalf("Get(deleted) error = %v", err)
	}
}

func TestTagServicePreviewsSourcesAndEvaluatesCalculatedTagsFromSnapshots(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	source := newServiceDatasourceReader()
	service := newTagTestService(t, repository, source)
	datasourceID := uuid.New()
	observedAt := time.Date(2026, time.August, 21, 16, 30, 0, 0, time.UTC)
	source.samples[datasourceID] = protocol.DatasourceSample{ObservedAt: observedAt, Quality: "good", Raw: []byte{0x34, 0x12}}

	reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_order":"little_endian"}}}`)})
	constant := repository.addTag(Tag{Name: "Constant", Type: TypeConstant, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"value":2}`)})
	firstCalculated := repository.addTag(Tag{Name: "First calculated", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} + ${%s}", reading.ID, constant.ID), reading.ID)})
	secondCalculated := repository.addTag(Tag{Name: "Second calculated", Type: TypeCalculated, DataType: DataTypeUInt16, Enabled: true, Config: expressionConfig(fmt.Sprintf("${%s} + ${%s}", firstCalculated.ID, reading.ID), reading.ID)})

	readingResult, err := service.PreviewSaved(context.Background(), reading.ID)
	if err != nil {
		t.Fatalf("PreviewSaved(reading) error = %v", err)
	}
	if readingResult.Value != uint16(0x1234) || readingResult.ObservedAt != observedAt || readingResult.Quality != "good" {
		t.Errorf("reading preview = %#v", readingResult)
	}
	source.calls[datasourceID] = 0
	if _, err := service.PreviewSaved(context.Background(), secondCalculated.ID); !errors.Is(err, ErrCalculatedSnapshotRequired) {
		t.Fatalf("PreviewSaved(calculated) error = %v", err)
	}
	values := map[uuid.UUID]any{reading.ID: uint16(0x1234), constant.ID: uint16(2)}
	firstValue, err := service.EvaluateCalculated(context.Background(), firstCalculated.DataType, firstCalculated.Config, func(_ context.Context, id uuid.UUID) (any, error) { return values[id], nil })
	if err != nil {
		t.Fatalf("EvaluateCalculated(first) error = %v", err)
	}
	values[firstCalculated.ID] = firstValue
	secondValue, err := service.EvaluateCalculated(context.Background(), secondCalculated.DataType, secondCalculated.Config, func(_ context.Context, id uuid.UUID) (any, error) { return values[id], nil })
	if err != nil || secondValue != uint16(0x246A) {
		t.Fatalf("EvaluateCalculated(second) = %#v, %v", secondValue, err)
	}
	if source.calls[datasourceID] != 0 {
		t.Errorf("datasource reads = %d, want 0", source.calls[datasourceID])
	}

	unsaved, err := service.Preview(context.Background(), PreviewInput{Type: TypeConstant, DataType: DataTypeBool, Config: json.RawMessage(`{"value":true}`)})
	if err != nil {
		t.Fatalf("Preview(constant) error = %v", err)
	}
	if unsaved.TagID != uuid.Nil || unsaved.Value != true || unsaved.DataType != DataTypeBool {
		t.Errorf("unsaved preview = %#v", unsaved)
	}
}

func TestTagServiceUsesRegisteredProtocolNeutralDecoder(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	source := newServiceDatasourceReader()
	decoder := &serviceJSONDecoder{}
	service, err := NewService(repository, source, NewBinaryNumericDecoder(), decoder)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	datasourceID := uuid.New()
	source.samples[datasourceID] = protocol.DatasourceSample{Quality: "good", Data: json.RawMessage(`{"temperature":25.5}`)}
	reading, err := service.Create(context.Background(), CreateInput{DatasourceID: &datasourceID, Name: "JSON temperature", Type: TypeReading, DataType: DataTypeFloat64, Config: json.RawMessage(`{"decoder":{"type":"json_value","config":{"field":"temperature"}}}`)})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := service.PreviewSaved(context.Background(), reading.ID)
	if err != nil {
		t.Fatalf("PreviewSaved() error = %v", err)
	}
	if result.Value != float64(25.5) || decoder.normalizeCalls != 2 || decoder.decodeCalls != 1 {
		t.Errorf("result/calls = %#v / (%d,%d)", result, decoder.normalizeCalls, decoder.decodeCalls)
	}
}

func TestTagServiceDecodesRawTypeBeforeApplyingEngineeringScaling(t *testing.T) {
	t.Parallel()
	repository := newServiceMemoryRepository()
	source := newServiceDatasourceReader()
	service := newTagTestService(t, repository, source)
	datasourceID := uuid.New()
	source.samples[datasourceID] = protocol.DatasourceSample{Quality: "good", Raw: []byte{0x80, 0x00}}
	config := json.RawMessage(`{"decoder":{"type":"binary_numeric","data_type":"uint16","config":{"byte_order":"big_endian"}},"transform":{"type":"linear","config":{"gain":0.0015259021896696422,"offset":0}}}`)
	reading, err := service.Create(context.Background(), CreateInput{DatasourceID: &datasourceID, Name: "Pressure", Type: TypeReading, DataType: DataTypeFloat64, Config: config})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err := service.PreviewSaved(context.Background(), reading.ID)
	if err != nil {
		t.Fatalf("PreviewSaved() error = %v", err)
	}
	if difference := math.Abs(result.Value.(float64) - 50.000762951094835); difference > 1e-12 {
		t.Errorf("preview value = %.15f", result.Value)
	}
	processed, err := service.ProcessReadingSample(DataTypeFloat64, reading.Config, source.samples[datasourceID])
	if err != nil {
		t.Fatalf("ProcessReadingSample() error = %v", err)
	}
	if difference := math.Abs(processed.(float64) - result.Value.(float64)); difference > 1e-12 {
		t.Errorf("processed value = %.15f, preview = %.15f", processed, result.Value)
	}
}

func TestTagServicePreviewMapsDisabledMissingSourceAndRuntimeCycleErrors(t *testing.T) {
	t.Parallel()
	t.Run("missing root", func(t *testing.T) {
		service := newTagTestService(t, newServiceMemoryRepository(), newServiceDatasourceReader())
		if _, err := service.PreviewSaved(context.Background(), uuid.New()); !errors.Is(err, ErrTagNotFound) {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
	t.Run("disabled dependency", func(t *testing.T) {
		repository := newServiceMemoryRepository()
		service := newTagTestService(t, repository, newServiceDatasourceReader())
		dependency := repository.addTag(Tag{Name: "Disabled", Type: TypeConstant, DataType: DataTypeFloat64, Enabled: false, Config: json.RawMessage(`{"value":1}`)})
		calculated := repository.addTag(Tag{Name: "Calculated", Type: TypeCalculated, DataType: DataTypeFloat64, Enabled: true, Config: calculatedConfig(dependency.ID)})
		if _, err := service.PreviewSaved(context.Background(), calculated.ID); !errors.Is(err, ErrCalculatedSnapshotRequired) {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
	t.Run("source failure", func(t *testing.T) {
		repository := newServiceMemoryRepository()
		source := newServiceDatasourceReader()
		service := newTagTestService(t, repository, source)
		datasourceID := uuid.New()
		offline := errors.New("offline")
		source.err = offline
		reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
		if _, err := service.PreviewSaved(context.Background(), reading.ID); !errors.Is(err, ErrTagSourceReadFailed) || !errors.Is(err, offline) {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
	t.Run("bad source quality", func(t *testing.T) {
		repository := newServiceMemoryRepository()
		source := newServiceDatasourceReader()
		service := newTagTestService(t, repository, source)
		datasourceID := uuid.New()
		source.samples[datasourceID] = protocol.DatasourceSample{Quality: "bad", Error: "connection lost"}
		reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
		if _, err := service.PreviewSaved(context.Background(), reading.ID); !errors.Is(err, ErrTagSourceReadFailed) || !strings.Contains(err.Error(), "connection lost") {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
	t.Run("short binary payload", func(t *testing.T) {
		repository := newServiceMemoryRepository()
		source := newServiceDatasourceReader()
		service := newTagTestService(t, repository, source)
		datasourceID := uuid.New()
		source.samples[datasourceID] = protocol.DatasourceSample{Quality: "good", Raw: []byte{0x01}}
		reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeUInt32, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)})
		if _, err := service.PreviewSaved(context.Background(), reading.ID); !errors.Is(err, ErrTagSourceReadFailed) || !errors.Is(err, ErrInsufficientBinaryData) {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
	t.Run("decoder output type", func(t *testing.T) {
		repository := newServiceMemoryRepository()
		source := newServiceDatasourceReader()
		decoder := &serviceJSONDecoder{output: "not-a-number"}
		service, err := NewService(repository, source, NewBinaryNumericDecoder(), decoder)
		if err != nil {
			t.Fatalf("NewService() error = %v", err)
		}
		datasourceID := uuid.New()
		source.samples[datasourceID] = protocol.DatasourceSample{Quality: "good"}
		reading := repository.addTag(Tag{DatasourceID: &datasourceID, Name: "Reading", Type: TypeReading, DataType: DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"json_value","config":{"field":"temperature"}}}`)})
		if _, err := service.PreviewSaved(context.Background(), reading.ID); !errors.Is(err, ErrInvalidTagInput) {
			t.Fatalf("PreviewSaved() error = %v", err)
		}
	})
}

func TestBinaryNumericDecoderNormalizesAndDecodesStrictConfig(t *testing.T) {
	t.Parallel()
	decoder := NewBinaryNumericDecoder()
	canonical, err := decoder.NormalizeConfig(DataTypeBool, json.RawMessage(`{"byte_offset":1,"bit_offset":3}`))
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if string(canonical) != `{"byte_offset":1,"byte_order":"big_endian","bit_offset":3}` {
		t.Errorf("canonical config = %s", canonical)
	}
	value, err := decoder.Decode(protocol.DatasourceSample{Raw: []byte{0, 8}}, DataTypeBool, canonical)
	if err != nil || value != true {
		t.Errorf("Decode() = %#v, %v", value, err)
	}
	for _, raw := range []string{`{"byte_offset":-1}`, `{"byte_order":"middle"}`, `{"bit_offset":8}`, `{"unknown":1}`, `{} {}`} {
		if _, err := decoder.NormalizeConfig(DataTypeBool, json.RawMessage(raw)); !errors.Is(err, ErrInvalidDecoderConfig) {
			t.Errorf("NormalizeConfig(%s) error = %v", raw, err)
		}
	}
}

type serviceMemoryRepository struct {
	tags         map[uuid.UUID]Tag
	dependencies map[uuid.UUID][]uuid.UUID
	lastList     ListInput
}

func newServiceMemoryRepository() *serviceMemoryRepository {
	return &serviceMemoryRepository{tags: map[uuid.UUID]Tag{}, dependencies: map[uuid.UUID][]uuid.UUID{}}
}

func (repository *serviceMemoryRepository) addTag(entity Tag) Tag {
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	repository.tags[entity.ID] = entity
	return entity
}

func (repository *serviceMemoryRepository) Create(_ context.Context, entity *Tag, dependencies []uuid.UUID) error {
	repository.tags[entity.ID] = *entity
	repository.dependencies[entity.ID] = append([]uuid.UUID(nil), dependencies...)
	return nil
}

func (repository *serviceMemoryRepository) Find(_ context.Context, id uuid.UUID) (*Tag, error) {
	entity, exists := repository.tags[id]
	if !exists {
		return nil, ErrTagNotFound
	}
	return &entity, nil
}

func (repository *serviceMemoryRepository) List(_ context.Context, input ListInput) (*ListResult, error) {
	repository.lastList = input
	entities := make([]Tag, 0, len(repository.tags))
	for _, entity := range repository.tags {
		entities = append(entities, entity)
	}
	return &ListResult{Data: entities, Page: input.Page, PerPage: input.PerPage, Total: int64(len(entities)), TotalPages: 1}, nil
}

func (repository *serviceMemoryRepository) Update(_ context.Context, entity *Tag, dependencies []uuid.UUID) error {
	if _, exists := repository.tags[entity.ID]; !exists {
		return ErrTagNotFound
	}
	repository.tags[entity.ID] = *entity
	repository.dependencies[entity.ID] = append([]uuid.UUID(nil), dependencies...)
	return nil
}

func (repository *serviceMemoryRepository) Delete(_ context.Context, id uuid.UUID) error {
	if _, exists := repository.tags[id]; !exists {
		return ErrTagNotFound
	}
	delete(repository.tags, id)
	delete(repository.dependencies, id)
	return nil
}

func (repository *serviceMemoryRepository) ListDependencies(context.Context) ([]Dependency, error) {
	result := make([]Dependency, 0)
	for tagID, dependencyIDs := range repository.dependencies {
		for _, dependencyID := range dependencyIDs {
			result = append(result, Dependency{TagID: tagID, DependsOnTagID: dependencyID})
		}
	}
	return result, nil
}

func (repository *serviceMemoryRepository) ListEnabledReadingTags(context.Context) ([]Tag, error) {
	entities := make([]Tag, 0)
	for _, entity := range repository.tags {
		if entity.Type == TypeReading && entity.Enabled {
			entities = append(entities, entity)
		}
	}
	return entities, nil
}

func (repository *serviceMemoryRepository) ListEnabledTags(context.Context) ([]Tag, error) {
	entities := make([]Tag, 0)
	for _, entity := range repository.tags {
		if entity.Enabled {
			entities = append(entities, entity)
		}
	}
	return entities, nil
}

type serviceDatasourceReader struct {
	samples map[uuid.UUID]protocol.DatasourceSample
	calls   map[uuid.UUID]int
	err     error
}

type serviceJSONDecoder struct {
	normalizeCalls int
	decodeCalls    int
	output         any
}

func (*serviceJSONDecoder) Type() string { return "json_value" }

func (decoder *serviceJSONDecoder) NormalizeConfig(_ DataType, raw Config) (Config, error) {
	decoder.normalizeCalls++
	var config struct {
		Field string `json:"field"`
	}
	if err := decodeStrictJSON(raw, &config); err != nil || config.Field == "" {
		return nil, ErrInvalidDecoderConfig
	}
	return json.Marshal(config)
}

func (decoder *serviceJSONDecoder) Decode(sample protocol.DatasourceSample, _ DataType, raw Config) (any, error) {
	decoder.decodeCalls++
	if decoder.output != nil {
		return decoder.output, nil
	}
	var config struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var data map[string]float64
	if err := json.Unmarshal(sample.Data, &data); err != nil {
		return nil, err
	}
	return data[config.Field], nil
}

func newServiceDatasourceReader() *serviceDatasourceReader {
	return &serviceDatasourceReader{samples: map[uuid.UUID]protocol.DatasourceSample{}, calls: map[uuid.UUID]int{}}
}

func (reader *serviceDatasourceReader) ReadDatasourceForTag(_ context.Context, id uuid.UUID) (protocol.DatasourceSample, error) {
	reader.calls[id]++
	if reader.err != nil {
		return protocol.DatasourceSample{}, reader.err
	}
	return reader.samples[id], nil
}

func newTagTestService(t *testing.T, repository Repository, source DatasourceReader) *Service {
	t.Helper()
	service, err := NewService(repository, source, NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func expressionConfig(expression string, triggerID uuid.UUID) Config {
	encoded, _ := json.Marshal(calculatedConfigInput{Expression: expression, Trigger: &calculatedTriggerInput{TagID: triggerID, Mode: "on_sample"}})
	return encoded
}

func calculatedConfig(id uuid.UUID) Config { return expressionConfig(fmt.Sprintf("${%s}", id), id) }
func configPointer(value Config) *Config   { return &value }
