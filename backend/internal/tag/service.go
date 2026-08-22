package tag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

const (
	defaultTagsPerPage    = 20
	maxTagsPerPage        = 100
	maxTagDependencyDepth = 128
)

var (
	ErrTagRepositoryRequired       = errors.New("tag repository is required")
	ErrDatasourceReaderRequired    = errors.New("datasource reader is required")
	ErrReadingDecodersRequired     = errors.New("reading decoders are required")
	ErrDuplicateReadingDecoder     = errors.New("duplicate reading decoder")
	ErrInvalidTagInput             = errors.New("invalid tag")
	ErrUnsupportedTagType          = errors.New("unsupported tag type")
	ErrUnsupportedTagDataType      = errors.New("unsupported tag data type")
	ErrTagDisabled                 = errors.New("tag is disabled")
	ErrTagSourceReadFailed         = errors.New("tag datasource read failed")
	ErrCalculatedSnapshotRequired  = errors.New("calculated tag requires a value snapshot")
	ErrCalculatedDependencyMissing = errors.New("calculated tag dependency not found")
	ErrCalculatedTriggerInvalid    = errors.New("calculated tag trigger is invalid")
	ErrCircularTagDependency       = errors.New("circular tag dependency")
	ErrTagDependencyTooDeep        = errors.New("tag dependency graph is too deep")
)

type OptionalDescription struct {
	Set   bool
	Value *string
}

type OptionalDatasourceID struct {
	Set   bool
	Value *uuid.UUID
}

type CreateInput struct {
	DatasourceID *uuid.UUID
	Name         string
	Type         Type
	DataType     DataType
	Description  *string
	Enabled      *bool
	Config       Config
}

type UpdateInput struct {
	DatasourceID OptionalDatasourceID
	Name         *string
	DataType     *DataType
	Description  OptionalDescription
	Enabled      *bool
	Config       *Config
}

type PreviewInput struct {
	DatasourceID *uuid.UUID
	Type         Type
	DataType     DataType
	Config       Config
}

type PreviewResult struct {
	TagID      uuid.UUID `json:"tag_id"`
	ObservedAt time.Time `json:"observed_at"`
	Quality    string    `json:"quality"`
	DataType   DataType  `json:"data_type"`
	Value      any       `json:"value"`
}

type ValueResolver func(context.Context, uuid.UUID) (any, error)

type Service struct {
	repository Repository
	source     DatasourceReader
	decoders   map[string]ReadingDecoder
}

func NewService(repository Repository, source DatasourceReader, decoders ...ReadingDecoder) (*Service, error) {
	if repository == nil {
		return nil, ErrTagRepositoryRequired
	}
	if source == nil {
		return nil, ErrDatasourceReaderRequired
	}
	if len(decoders) == 0 {
		return nil, ErrReadingDecodersRequired
	}
	service := &Service{repository: repository, source: source, decoders: map[string]ReadingDecoder{}}
	for _, decoder := range decoders {
		if decoder == nil {
			return nil, ErrReadingDecodersRequired
		}
		decoderType := strings.TrimSpace(decoder.Type())
		if decoderType == "" {
			return nil, ErrReadingDecodersRequired
		}
		if _, exists := service.decoders[decoderType]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateReadingDecoder, decoderType)
		}
		service.decoders[decoderType] = decoder
	}
	return service, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*Tag, error) {
	name, err := validateTagName(input.Name)
	if err != nil {
		return nil, err
	}
	if err := validateTagShape(input.Type, input.DataType, input.DatasourceID); err != nil {
		return nil, err
	}
	config, dependencies, err := s.normalizeConfig(input.Type, input.DataType, input.Config)
	if err != nil {
		return nil, err
	}
	entity := &Tag{ID: uuid.New(), DatasourceID: cloneUUIDPointer(input.DatasourceID), Name: name, Type: input.Type, DataType: input.DataType, Description: normalizeTagDescription(input.Description), Enabled: defaultTagEnabled(input.Enabled), Config: config}
	if err := s.validateCalculatedTrigger(ctx, entity); err != nil {
		return nil, err
	}
	if err := s.validateDependencyGraph(ctx, entity.ID, dependencies); err != nil {
		return nil, err
	}
	if err := s.repository.Create(ctx, entity, dependencies); err != nil {
		return nil, err
	}
	return entity, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Tag, error) {
	return s.repository.Find(ctx, id)
}

func (s *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if input.Type != nil && !validTagType(*input.Type) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTagType, *input.Type)
	}
	if input.DataType != nil && !validTagDataType(*input.DataType) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTagDataType, *input.DataType)
	}
	if input.Page < 0 || input.PerPage < 0 || input.PerPage > maxTagsPerPage {
		return nil, fmt.Errorf("%w: invalid pagination", ErrInvalidTagInput)
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultTagsPerPage
	}
	return s.repository.List(ctx, input)
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Tag, error) {
	entity, err := s.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		entity.Name, err = validateTagName(*input.Name)
		if err != nil {
			return nil, err
		}
	}
	if input.DataType != nil {
		entity.DataType = *input.DataType
	}
	if input.DatasourceID.Set {
		entity.DatasourceID = cloneUUIDPointer(input.DatasourceID.Value)
	}
	if input.Description.Set {
		entity.Description = normalizeTagDescription(input.Description.Value)
	}
	if input.Enabled != nil {
		entity.Enabled = *input.Enabled
	}
	if input.Config != nil {
		entity.Config = *input.Config
	}
	if err := validateTagShape(entity.Type, entity.DataType, entity.DatasourceID); err != nil {
		return nil, err
	}
	normalizedConfig, dependencies, err := s.normalizeConfig(entity.Type, entity.DataType, entity.Config)
	if err != nil {
		return nil, err
	}
	entity.Config = normalizedConfig
	if err := s.validateCalculatedTrigger(ctx, entity); err != nil {
		return nil, err
	}
	if err := s.validateDependencyGraph(ctx, entity.ID, dependencies); err != nil {
		return nil, err
	}
	if err := s.repository.Update(ctx, entity, dependencies); err != nil {
		return nil, err
	}
	return entity, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repository.Delete(ctx, id)
}

func (s *Service) Preview(ctx context.Context, input PreviewInput) (*PreviewResult, error) {
	if err := validateTagShape(input.Type, input.DataType, input.DatasourceID); err != nil {
		return nil, err
	}
	config, _, err := s.normalizeConfig(input.Type, input.DataType, input.Config)
	if err != nil {
		return nil, err
	}
	entity := &Tag{DatasourceID: input.DatasourceID, Type: input.Type, DataType: input.DataType, Enabled: true, Config: config}
	state := newPreviewState(s, ctx)
	return state.evaluateEntity(entity)
}

func (s *Service) PreviewSaved(ctx context.Context, id uuid.UUID) (*PreviewResult, error) {
	return newPreviewState(s, ctx).evaluateSaved(id, false)
}

func (s *Service) ValidateCalculatedExpression(ctx context.Context, tagID uuid.UUID, raw string) ([]uuid.UUID, error) {
	expression, err := ParseExpression(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	dependencies := expression.Dependencies()
	if err := s.validateDependencyGraph(ctx, tagID, dependencies); err != nil {
		return nil, err
	}
	return dependencies, nil
}

func (s *Service) EvaluateCalculated(ctx context.Context, dataType DataType, raw Config, resolve ValueResolver) (any, error) {
	if resolve == nil {
		return nil, ErrCalculatedSnapshotRequired
	}
	config, _, err := s.normalizeConfig(TypeCalculated, dataType, raw)
	if err != nil {
		return nil, err
	}
	var calculated calculatedConfigInput
	if err := json.Unmarshal(config, &calculated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	expression, err := ParseExpression(calculated.Expression)
	if err != nil {
		return nil, err
	}
	value, err := expression.Evaluate(ctx, ReferenceResolver(resolve))
	if err != nil {
		return nil, err
	}
	return coerceTagValue(dataType, value)
}

func (s *Service) ProcessReadingSample(dataType DataType, raw Config, sample protocol.DatasourceSample) (any, error) {
	config, _, err := s.normalizeConfig(TypeReading, dataType, raw)
	if err != nil {
		return nil, err
	}
	return s.processNormalizedReadingSample(dataType, config, sample)
}

func (s *Service) processNormalizedReadingSample(dataType DataType, config Config, sample protocol.DatasourceSample) (any, error) {
	var reading readingConfig
	if err := json.Unmarshal(config, &reading); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	if sample.Quality != "" && sample.Quality != "good" {
		message := strings.TrimSpace(sample.Error)
		if message == "" {
			message = "datasource sample quality is " + sample.Quality
		}
		return nil, fmt.Errorf("%w: %s", ErrTagSourceReadFailed, message)
	}
	sourceDataType := dataType
	if reading.Decoder.DataType != nil {
		sourceDataType = *reading.Decoder.DataType
	}
	value, err := s.decoders[reading.Decoder.Type].Decode(sample, sourceDataType, reading.Decoder.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTagSourceReadFailed, err)
	}
	if reading.Transform != nil {
		switch reading.Transform.Type {
		case LinearTransformType:
			value, err = applyLinearTransform(value, reading.Transform.Config)
		default:
			err = fmt.Errorf("%w: %q", ErrUnsupportedTransform, reading.Transform.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrTagSourceReadFailed, err)
		}
	}
	value, err = coerceTagValue(dataType, value)
	if err != nil {
		return nil, err
	}
	return value, nil
}

type decoderEnvelopeInput struct {
	Type     string    `json:"type"`
	DataType *DataType `json:"data_type"`
	Config   Config    `json:"config"`
}

type readingConfigInput struct {
	Decoder   *decoderEnvelopeInput   `json:"decoder"`
	Transform *transformEnvelopeInput `json:"transform"`
}

type decoderEnvelope struct {
	Type     string    `json:"type"`
	DataType *DataType `json:"data_type,omitempty"`
	Config   Config    `json:"config"`
}

type readingConfig struct {
	Decoder   decoderEnvelope    `json:"decoder"`
	Transform *transformEnvelope `json:"transform,omitempty"`
}

type transformEnvelopeInput struct {
	Type   string `json:"type"`
	Config Config `json:"config"`
}

type transformEnvelope struct {
	Type   string `json:"type"`
	Config Config `json:"config"`
}

type constantConfigInput struct {
	Value json.RawMessage `json:"value"`
}

type calculatedConfigInput struct {
	Expression string                  `json:"expression"`
	Trigger    *calculatedTriggerInput `json:"trigger"`
}

type calculatedTriggerInput struct {
	TagID uuid.UUID `json:"tag_id"`
	Mode  string    `json:"mode"`
}

func (s *Service) normalizeConfig(tagType Type, dataType DataType, raw Config) (Config, []uuid.UUID, error) {
	switch tagType {
	case TypeReading:
		var input readingConfigInput
		if err := decodeStrictJSON(raw, &input); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
		}
		if input.Decoder == nil || strings.TrimSpace(input.Decoder.Type) == "" {
			return nil, nil, fmt.Errorf("%w: decoder is required", ErrInvalidTagInput)
		}
		decoderType := strings.TrimSpace(input.Decoder.Type)
		decoder, ok := s.decoders[decoderType]
		if !ok {
			return nil, nil, fmt.Errorf("%w: %q", ErrUnsupportedDecoder, decoderType)
		}
		sourceDataType := dataType
		if input.Decoder.DataType != nil {
			sourceDataType = *input.Decoder.DataType
		}
		if !validTagDataType(sourceDataType) {
			return nil, nil, fmt.Errorf("%w: decoder data type %q", ErrUnsupportedTagDataType, sourceDataType)
		}
		decoderConfig, err := decoder.NormalizeConfig(sourceDataType, input.Decoder.Config)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalidTagInput, err)
		}
		var normalizedTransform *transformEnvelope
		if input.Transform != nil {
			if sourceDataType == DataTypeBool || dataType == DataTypeBool {
				return nil, nil, fmt.Errorf("%w: linear transform requires numeric source and output types", ErrInvalidTransformConfig)
			}
			transformType := strings.TrimSpace(input.Transform.Type)
			if transformType != LinearTransformType {
				return nil, nil, fmt.Errorf("%w: %q", ErrUnsupportedTransform, transformType)
			}
			transformConfig, transformErr := normalizeLinearTransform(input.Transform.Config)
			if transformErr != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrInvalidTagInput, transformErr)
			}
			normalizedTransform = &transformEnvelope{Type: transformType, Config: transformConfig}
		}
		var canonicalSourceType *DataType
		if sourceDataType != dataType {
			canonicalSourceType = &sourceDataType
		}
		canonical, err := json.Marshal(readingConfig{Decoder: decoderEnvelope{Type: decoderType, DataType: canonicalSourceType, Config: decoderConfig}, Transform: normalizedTransform})
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
		}
		return canonical, nil, nil
	case TypeConstant:
		canonical, _, err := normalizeConstantConfig(dataType, raw)
		return canonical, nil, err
	case TypeCalculated:
		var input calculatedConfigInput
		if err := decodeStrictJSON(raw, &input); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
		}
		input.Expression = strings.TrimSpace(input.Expression)
		if input.Trigger == nil || input.Trigger.TagID == uuid.Nil {
			return nil, nil, fmt.Errorf("%w: trigger.tag_id is required", ErrCalculatedTriggerInvalid)
		}
		input.Trigger.Mode = strings.TrimSpace(input.Trigger.Mode)
		if input.Trigger.Mode == "" {
			input.Trigger.Mode = "on_sample"
		}
		if input.Trigger.Mode != "on_sample" {
			return nil, nil, fmt.Errorf("%w: unsupported trigger mode %q", ErrCalculatedTriggerInvalid, input.Trigger.Mode)
		}
		expression, err := ParseExpression(input.Expression)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalidTagInput, err)
		}
		canonical, err := json.Marshal(input)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
		}
		dependencies := expression.Dependencies()
		triggerFound := false
		for _, dependencyID := range dependencies {
			if dependencyID == input.Trigger.TagID {
				triggerFound = true
				break
			}
		}
		if !triggerFound {
			dependencies = append(dependencies, input.Trigger.TagID)
		}
		return canonical, dependencies, nil
	default:
		return nil, nil, fmt.Errorf("%w: %q", ErrUnsupportedTagType, tagType)
	}
}

func normalizeConstantConfig(dataType DataType, raw Config) (Config, any, error) {
	var input constantConfigInput
	if err := decodeStrictJSON(raw, &input); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	if len(input.Value) == 0 {
		return nil, nil, fmt.Errorf("%w: value is required", ErrInvalidTagInput)
	}
	value, err := typedJSONValue(dataType, input.Value)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(struct {
		Value any `json:"value"`
	}{Value: value})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	return canonical, value, nil
}

func typedJSONValue(dataType DataType, raw json.RawMessage) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%w: value must not be null", ErrInvalidTagInput)
	}
	if dataType == DataTypeBool {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("%w: bool value is required", ErrInvalidTagInput)
		}
		return value, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return nil, fmt.Errorf("%w: numeric value is required", ErrInvalidTagInput)
	}
	switch dataType {
	case DataTypeInt16:
		value, err := strconv.ParseInt(number.String(), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("%w: value must be int16", ErrInvalidTagInput)
		}
		return int16(value), nil
	case DataTypeUInt16:
		value, err := strconv.ParseUint(number.String(), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("%w: value must be uint16", ErrInvalidTagInput)
		}
		return uint16(value), nil
	case DataTypeInt32:
		value, err := strconv.ParseInt(number.String(), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: value must be int32", ErrInvalidTagInput)
		}
		return int32(value), nil
	case DataTypeUInt32:
		value, err := strconv.ParseUint(number.String(), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: value must be uint32", ErrInvalidTagInput)
		}
		return uint32(value), nil
	case DataTypeFloat32:
		value, err := strconv.ParseFloat(number.String(), 32)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: value must be finite float32", ErrInvalidTagInput)
		}
		return float32(value), nil
	case DataTypeFloat64:
		value, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: value must be finite float64", ErrInvalidTagInput)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTagDataType, dataType)
	}
}

func validateTagShape(tagType Type, dataType DataType, datasourceID *uuid.UUID) error {
	if !validTagType(tagType) {
		return fmt.Errorf("%w: %q", ErrUnsupportedTagType, tagType)
	}
	if !validTagDataType(dataType) {
		return fmt.Errorf("%w: %q", ErrUnsupportedTagDataType, dataType)
	}
	if tagType == TypeReading {
		if datasourceID == nil || *datasourceID == uuid.Nil {
			return fmt.Errorf("%w: reading tag requires datasource_id", ErrInvalidTagInput)
		}
	} else if datasourceID != nil {
		return fmt.Errorf("%w: %s tag must not have datasource_id", ErrInvalidTagInput, tagType)
	}
	return nil
}

func validTagType(tagType Type) bool {
	return tagType == TypeReading || tagType == TypeConstant || tagType == TypeCalculated
}

func validTagDataType(dataType DataType) bool {
	switch dataType {
	case DataTypeBool, DataTypeInt16, DataTypeUInt16, DataTypeInt32, DataTypeUInt32, DataTypeFloat32, DataTypeFloat64:
		return true
	default:
		return false
	}
}

func validateTagName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalidTagInput)
	}
	if utf8.RuneCountInString(name) > 100 {
		return "", fmt.Errorf("%w: name must not exceed 100 characters", ErrInvalidTagInput)
	}
	return name, nil
}

func normalizeTagDescription(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func defaultTagEnabled(value *bool) bool { return value == nil || *value }

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Service) validateDependencyGraph(ctx context.Context, tagID uuid.UUID, dependencyIDs []uuid.UUID) error {
	if len(dependencyIDs) == 0 {
		return nil
	}
	for _, dependencyID := range dependencyIDs {
		if dependencyID == tagID {
			return ErrCircularTagDependency
		}
		if _, err := s.repository.Find(ctx, dependencyID); err != nil {
			if errors.Is(err, ErrTagNotFound) {
				return fmt.Errorf("%w: %s", ErrCalculatedDependencyMissing, dependencyID)
			}
			return err
		}
	}
	edges, err := s.repository.ListDependencies(ctx)
	if err != nil {
		return err
	}
	graph := map[uuid.UUID][]uuid.UUID{}
	for _, edge := range edges {
		if edge.TagID != tagID {
			graph[edge.TagID] = append(graph[edge.TagID], edge.DependsOnTagID)
		}
	}
	graph[tagID] = append([]uuid.UUID(nil), dependencyIDs...)
	circular, tooDeep := graphCycleState(tagID, graph, map[uuid.UUID]uint8{}, 0)
	if tooDeep {
		return ErrTagDependencyTooDeep
	}
	if circular {
		return ErrCircularTagDependency
	}
	return nil
}

func (s *Service) validateCalculatedTrigger(ctx context.Context, entity *Tag) error {
	if entity.Type != TypeCalculated {
		return nil
	}
	var config calculatedConfigInput
	if err := json.Unmarshal(entity.Config, &config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	if config.Trigger == nil || config.Trigger.TagID == uuid.Nil || config.Trigger.TagID == entity.ID {
		return ErrCalculatedTriggerInvalid
	}
	trigger, err := s.repository.Find(ctx, config.Trigger.TagID)
	if err != nil {
		if errors.Is(err, ErrTagNotFound) {
			return fmt.Errorf("%w: %s", ErrCalculatedDependencyMissing, config.Trigger.TagID)
		}
		return err
	}
	if trigger.Type == TypeConstant {
		return fmt.Errorf("%w: constant tags cannot trigger calculations", ErrCalculatedTriggerInvalid)
	}
	return nil
}

func graphCycleState(id uuid.UUID, graph map[uuid.UUID][]uuid.UUID, state map[uuid.UUID]uint8, depth int) (bool, bool) {
	if state[id] == 1 {
		return true, false
	}
	if state[id] == 2 {
		return false, false
	}
	if depth > maxTagDependencyDepth {
		return false, true
	}
	state[id] = 1
	for _, dependencyID := range graph[id] {
		circular, tooDeep := graphCycleState(dependencyID, graph, state, depth+1)
		if circular || tooDeep {
			return circular, tooDeep
		}
	}
	state[id] = 2
	return false, false
}

type previewState struct {
	service  *Service
	ctx      context.Context
	cache    map[uuid.UUID]*PreviewResult
	visiting map[uuid.UUID]bool
}

func newPreviewState(service *Service, ctx context.Context) *previewState {
	return &previewState{service: service, ctx: ctx, cache: map[uuid.UUID]*PreviewResult{}, visiting: map[uuid.UUID]bool{}}
}

func (state *previewState) evaluateSaved(id uuid.UUID, dependency bool) (*PreviewResult, error) {
	if cached := state.cache[id]; cached != nil {
		copy := *cached
		return &copy, nil
	}
	if state.visiting[id] {
		return nil, ErrCircularTagDependency
	}
	if len(state.visiting) >= maxTagDependencyDepth {
		return nil, ErrTagDependencyTooDeep
	}
	entity, err := state.service.repository.Find(state.ctx, id)
	if err != nil {
		if dependency && errors.Is(err, ErrTagNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrCalculatedDependencyMissing, id)
		}
		return nil, err
	}
	if !entity.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrTagDisabled, id)
	}
	state.visiting[id] = true
	result, err := state.evaluateEntity(entity)
	delete(state.visiting, id)
	if err != nil {
		return nil, err
	}
	state.cache[id] = result
	copy := *result
	return &copy, nil
}

func (state *previewState) evaluateEntity(entity *Tag) (*PreviewResult, error) {
	config, _, err := state.service.normalizeConfig(entity.Type, entity.DataType, entity.Config)
	if err != nil {
		return nil, err
	}
	observedAt := time.Now().UTC()
	var value any
	switch entity.Type {
	case TypeReading:
		sample, err := state.service.source.ReadDatasourceForTag(state.ctx, *entity.DatasourceID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrTagSourceReadFailed, err)
		}
		value, err = state.service.processNormalizedReadingSample(entity.DataType, config, sample)
		if err != nil {
			return nil, err
		}
		if !sample.ObservedAt.IsZero() {
			observedAt = sample.ObservedAt
		}
	case TypeConstant:
		_, value, err = normalizeConstantConfig(entity.DataType, config)
		if err != nil {
			return nil, err
		}
	case TypeCalculated:
		return nil, ErrCalculatedSnapshotRequired
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTagType, entity.Type)
	}
	return &PreviewResult{TagID: entity.ID, ObservedAt: observedAt, Quality: "good", DataType: entity.DataType, Value: value}, nil
}

func coerceTagValue(dataType DataType, value any) (any, error) {
	if dataType == DataTypeBool {
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: tag value must be bool, got %T", ErrInvalidTagInput, value)
		}
		return boolean, nil
	}
	number, ok := expressionNumber(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("%w: tag value must be numeric, got %T", ErrInvalidTagInput, value)
	}
	switch dataType {
	case DataTypeInt16:
		if number != math.Trunc(number) || number < math.MinInt16 || number > math.MaxInt16 {
			return nil, fmt.Errorf("%w: tag value is outside int16", ErrInvalidTagInput)
		}
		return int16(number), nil
	case DataTypeUInt16:
		if number != math.Trunc(number) || number < 0 || number > math.MaxUint16 {
			return nil, fmt.Errorf("%w: tag value is outside uint16", ErrInvalidTagInput)
		}
		return uint16(number), nil
	case DataTypeInt32:
		if number != math.Trunc(number) || number < math.MinInt32 || number > math.MaxInt32 {
			return nil, fmt.Errorf("%w: tag value is outside int32", ErrInvalidTagInput)
		}
		return int32(number), nil
	case DataTypeUInt32:
		if number != math.Trunc(number) || number < 0 || number > math.MaxUint32 {
			return nil, fmt.Errorf("%w: tag value is outside uint32", ErrInvalidTagInput)
		}
		return uint32(number), nil
	case DataTypeFloat32:
		converted := float32(number)
		if math.IsInf(float64(converted), 0) {
			return nil, fmt.Errorf("%w: tag value is outside float32", ErrInvalidTagInput)
		}
		return converted, nil
	case DataTypeFloat64:
		return number, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTagDataType, dataType)
	}
}
