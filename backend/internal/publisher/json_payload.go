package publisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/google/uuid"
)

const (
	MaxJSONPayloadTemplateBytes = 16 * 1024
	MaxJSONPayloadBytes         = 64 * 1024
	MaxJSONPayloadActions       = 256
	MaxJSONPayloadDepth         = 32
	MaxJSONPayloadNodes         = 4096
	maxJSONPayloadArgumentBytes = 256
	maxJSONPayloadDecimals      = 9
)

var (
	ErrInvalidJSONPayloadTemplate = errors.New("invalid JSON payload template")
	ErrJSONPayloadTooComplex      = errors.New("JSON payload template is too complex")
	ErrJSONPayloadUnknownAlias    = errors.New("JSON payload template references an unknown source alias")
	ErrJSONPayloadRender          = errors.New("JSON payload rendering failed")
	ErrJSONPayloadInvalid         = errors.New("rendered payload is invalid JSON")
	ErrJSONPayloadTooLarge        = errors.New("rendered JSON payload is too large")
)

type JSONPayloadEngine struct{}

type JSONPayloadValidation struct {
	ReferencedAliases []string        `json:"referenced_aliases"`
	HelperCalls       int             `json:"helper_calls"`
	Good              json.RawMessage `json:"good"`
	Unavailable       json.RawMessage `json:"unavailable"`
	Windowed          json.RawMessage `json:"windowed"`
}

type JSONPayloadRenderContext struct {
	PublisherID uuid.UUID
	PublishedAt time.Time
	Snapshot    SourceSnapshot
}

type CompiledJSONPayload struct {
	operations []jsonPayloadOperation
	sources    map[string]SourceDescriptor
	aliases    []string
}

type jsonPayloadOperation struct {
	literal  []byte
	helper   string
	args     []any
	location *time.Location
}

type jsonPayloadHelperSpec struct {
	minArgs int
	maxArgs int
	source  bool
}

var jsonPayloadHelperSpecs = map[string]jsonPayloadHelperSpec{
	"value":             {minArgs: 1, maxArgs: 1, source: true},
	"available":         {minArgs: 1, maxArgs: 1, source: true},
	"quality":           {minArgs: 1, maxArgs: 1, source: true},
	"error":             {minArgs: 1, maxArgs: 1, source: true},
	"unit":              {minArgs: 1, maxArgs: 1, source: true},
	"sequence":          {minArgs: 1, maxArgs: 1, source: true},
	"schema_version":    {minArgs: 1, maxArgs: 1, source: true},
	"data_type":         {minArgs: 1, maxArgs: 1, source: true},
	"observed_at":       {minArgs: 1, maxArgs: 1, source: true},
	"emitted_at":        {minArgs: 1, maxArgs: 1, source: true},
	"period_start":      {minArgs: 1, maxArgs: 1, source: true},
	"period_end":        {minArgs: 1, maxArgs: 1, source: true},
	"coverage":          {minArgs: 1, maxArgs: 1, source: true},
	"round":             {minArgs: 2, maxArgs: 2, source: true},
	"scale":             {minArgs: 3, maxArgs: 3, source: true},
	"default":           {minArgs: 2, maxArgs: 2, source: true},
	"format_time":       {minArgs: 4, maxArgs: 4, source: true},
	"published_at":      {minArgs: 0, maxArgs: 0},
	"published_unix_ms": {minArgs: 0, maxArgs: 0},
	"publisher_id":      {minArgs: 0, maxArgs: 0},
}

func NewJSONPayloadEngine() *JSONPayloadEngine {
	return &JSONPayloadEngine{}
}

func (engine *JSONPayloadEngine) Compile(raw string, sources []ResolvedSource) (*CompiledJSONPayload, error) {
	if engine == nil {
		return nil, ErrInvalidJSONPayloadTemplate
	}
	if len(raw) > MaxJSONPayloadTemplateBytes {
		return nil, ErrJSONPayloadTooComplex
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrInvalidJSONPayloadTemplate
	}
	normalizedSources, err := normalizeJSONPayloadSources(sources)
	if err != nil {
		return nil, err
	}
	parsed, err := template.New("payload").Funcs(jsonPayloadParserFunctions()).Parse(raw)
	if err != nil || parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Templates()) != 1 {
		return nil, ErrInvalidJSONPayloadTemplate
	}
	compiled := &CompiledJSONPayload{sources: normalizedSources}
	referenced := make(map[string]struct{})
	if err := compileJSONPayloadList(parsed.Tree.Root, compiled, referenced); err != nil {
		return nil, err
	}
	if len(compiled.operations) == 0 {
		return nil, ErrInvalidJSONPayloadTemplate
	}
	compiled.aliases = make([]string, 0, len(referenced))
	for alias := range referenced {
		compiled.aliases = append(compiled.aliases, alias)
	}
	sort.Strings(compiled.aliases)
	if _, err := compiled.Render(jsonPayloadFixture(normalizedSources, jsonPayloadFixtureGood)); err != nil {
		return nil, fmt.Errorf("%w: good fixture", err)
	}
	if _, err := compiled.Render(jsonPayloadFixture(normalizedSources, jsonPayloadFixtureUnavailable)); err != nil {
		return nil, fmt.Errorf("%w: unavailable fixture", err)
	}
	if _, err := compiled.Render(jsonPayloadFixture(normalizedSources, jsonPayloadFixtureWindowed)); err != nil {
		return nil, fmt.Errorf("%w: windowed fixture", err)
	}
	return compiled, nil
}

func (engine *JSONPayloadEngine) Validate(raw string, sources []ResolvedSource) (JSONPayloadValidation, error) {
	compiled, err := engine.Compile(raw, sources)
	if err != nil {
		return JSONPayloadValidation{}, err
	}
	good, _ := compiled.Render(jsonPayloadFixture(compiled.sources, jsonPayloadFixtureGood))
	unavailable, _ := compiled.Render(jsonPayloadFixture(compiled.sources, jsonPayloadFixtureUnavailable))
	windowed, _ := compiled.Render(jsonPayloadFixture(compiled.sources, jsonPayloadFixtureWindowed))
	return JSONPayloadValidation{
		ReferencedAliases: append([]string(nil), compiled.aliases...),
		HelperCalls:       compiled.helperCallCount(),
		Good:              append(json.RawMessage(nil), good...),
		Unavailable:       append(json.RawMessage(nil), unavailable...),
		Windowed:          append(json.RawMessage(nil), windowed...),
	}, nil
}

func (engine *JSONPayloadEngine) Preview(raw string, sources []ResolvedSource, snapshot SourceSnapshot) (json.RawMessage, error) {
	compiled, err := engine.Compile(raw, sources)
	if err != nil {
		return nil, err
	}
	return compiled.Render(snapshot)
}

func (compiled *CompiledJSONPayload) Render(snapshot SourceSnapshot) (json.RawMessage, error) {
	return compiled.RenderWithContext(JSONPayloadRenderContext{PublishedAt: snapshot.CapturedAt, Snapshot: snapshot})
}

func (compiled *CompiledJSONPayload) RenderWithContext(renderContext JSONPayloadRenderContext) (json.RawMessage, error) {
	if compiled == nil {
		return nil, ErrJSONPayloadRender
	}
	if renderContext.PublishedAt.IsZero() {
		renderContext.PublishedAt = renderContext.Snapshot.CapturedAt
	}
	renderContext.PublishedAt = renderContext.PublishedAt.UTC()
	samples, err := compiled.samplesByAlias(renderContext.Snapshot)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, 0, min(MaxJSONPayloadBytes, MaxJSONPayloadTemplateBytes))
	for _, operation := range compiled.operations {
		fragment := operation.literal
		if operation.helper != "" {
			fragment, err = renderJSONPayloadHelper(operation, samples, renderContext)
			if err != nil {
				return nil, err
			}
		}
		if len(buffer)+len(fragment) > MaxJSONPayloadBytes {
			return nil, ErrJSONPayloadTooLarge
		}
		buffer = append(buffer, fragment...)
	}
	return normalizeRenderedJSONPayload(buffer)
}

func (compiled *CompiledJSONPayload) helperCallCount() int {
	count := 0
	for _, operation := range compiled.operations {
		if operation.helper != "" {
			count++
		}
	}
	return count
}

func normalizeJSONPayloadSources(sources []ResolvedSource) (map[string]SourceDescriptor, error) {
	if len(sources) == 0 || len(sources) > MaxSourceSelections {
		return nil, ErrInvalidSourceSelection
	}
	normalized := make(map[string]SourceDescriptor, len(sources))
	for _, source := range sources {
		if !sourceAliasPattern.MatchString(source.Alias) || source.Descriptor.Reference.Validate() != nil || !validSourceDataType(source.Descriptor.DataType) || source.Descriptor.SchemaVersion == 0 {
			return nil, ErrInvalidSourceSelection
		}
		if _, exists := normalized[source.Alias]; exists {
			return nil, ErrDuplicateSourceAlias
		}
		normalized[source.Alias] = cloneSourceDescriptor(source.Descriptor)
	}
	return normalized, nil
}

func jsonPayloadParserFunctions() template.FuncMap {
	functions := make(template.FuncMap, len(jsonPayloadHelperSpecs))
	for name := range jsonPayloadHelperSpecs {
		functions[name] = func(...any) string { return "null" }
	}
	return functions
}

func compileJSONPayloadList(list *parse.ListNode, compiled *CompiledJSONPayload, referenced map[string]struct{}) error {
	if list == nil {
		return ErrInvalidJSONPayloadTemplate
	}
	for _, node := range list.Nodes {
		switch typed := node.(type) {
		case *parse.TextNode:
			if len(typed.Text) != 0 {
				compiled.operations = append(compiled.operations, jsonPayloadOperation{literal: append([]byte(nil), typed.Text...)})
			}
		case *parse.ActionNode:
			operation, alias, err := compileJSONPayloadAction(typed, compiled.sources)
			if err != nil {
				return err
			}
			compiled.operations = append(compiled.operations, operation)
			if alias != "" {
				referenced[alias] = struct{}{}
			}
		default:
			return ErrInvalidJSONPayloadTemplate
		}
		if len(compiled.operations) > MaxJSONPayloadNodes || compiled.helperCallCount() > MaxJSONPayloadActions {
			return ErrJSONPayloadTooComplex
		}
	}
	return nil
}

func compileJSONPayloadAction(action *parse.ActionNode, sources map[string]SourceDescriptor) (jsonPayloadOperation, string, error) {
	if action == nil || action.Pipe == nil || len(action.Pipe.Decl) != 0 || action.Pipe.IsAssign || len(action.Pipe.Cmds) != 1 {
		return jsonPayloadOperation{}, "", ErrInvalidJSONPayloadTemplate
	}
	command := action.Pipe.Cmds[0]
	if len(command.Args) == 0 {
		return jsonPayloadOperation{}, "", ErrInvalidJSONPayloadTemplate
	}
	identifier, ok := command.Args[0].(*parse.IdentifierNode)
	if !ok {
		return jsonPayloadOperation{}, "", ErrInvalidJSONPayloadTemplate
	}
	spec, ok := jsonPayloadHelperSpecs[identifier.Ident]
	if !ok || len(command.Args)-1 < spec.minArgs || len(command.Args)-1 > spec.maxArgs {
		return jsonPayloadOperation{}, "", ErrInvalidJSONPayloadTemplate
	}
	args := make([]any, 0, len(command.Args)-1)
	for _, argument := range command.Args[1:] {
		value, err := jsonPayloadLiteral(argument)
		if err != nil {
			return jsonPayloadOperation{}, "", err
		}
		args = append(args, value)
	}
	alias := ""
	var descriptor SourceDescriptor
	if spec.source {
		var ok bool
		alias, ok = args[0].(string)
		if !ok || len(alias) > maxJSONPayloadArgumentBytes {
			return jsonPayloadOperation{}, "", ErrInvalidJSONPayloadTemplate
		}
		var exists bool
		descriptor, exists = sources[alias]
		if !exists {
			return jsonPayloadOperation{}, "", fmt.Errorf("%w: %s", ErrJSONPayloadUnknownAlias, alias)
		}
		if err := validateJSONPayloadHelper(identifier.Ident, args, descriptor); err != nil {
			return jsonPayloadOperation{}, "", err
		}
	}
	operation := jsonPayloadOperation{helper: identifier.Ident, args: args}
	if identifier.Ident == "format_time" {
		operation.location, _ = time.LoadLocation(args[2].(string))
	}
	return operation, alias, nil
}

func jsonPayloadLiteral(node parse.Node) (any, error) {
	switch typed := node.(type) {
	case *parse.StringNode:
		if len(typed.Text) > maxJSONPayloadArgumentBytes {
			return nil, ErrInvalidJSONPayloadTemplate
		}
		return typed.Text, nil
	case *parse.BoolNode:
		return typed.True, nil
	case *parse.NilNode:
		return nil, nil
	case *parse.NumberNode:
		if typed.IsInt {
			return typed.Int64, nil
		}
		if typed.IsUint {
			return typed.Uint64, nil
		}
		if typed.IsFloat && !math.IsNaN(typed.Float64) && !math.IsInf(typed.Float64, 0) {
			return typed.Float64, nil
		}
	}
	return nil, ErrInvalidJSONPayloadTemplate
}

func validateJSONPayloadHelper(name string, args []any, descriptor SourceDescriptor) error {
	switch name {
	case "round":
		if !isNumericSourceDataType(descriptor.DataType) {
			return ErrInvalidJSONPayloadTemplate
		}
		decimals, ok := integerArgument(args[1])
		if !ok || decimals < 0 || decimals > maxJSONPayloadDecimals {
			return ErrInvalidJSONPayloadTemplate
		}
	case "scale":
		if !isNumericSourceDataType(descriptor.DataType) {
			return ErrInvalidJSONPayloadTemplate
		}
		for _, argument := range args[1:] {
			if _, ok := finiteFloatArgument(argument); !ok {
				return ErrInvalidJSONPayloadTemplate
			}
		}
	case "default":
		if _, err := json.Marshal(args[1]); err != nil {
			return ErrInvalidJSONPayloadTemplate
		}
	case "format_time":
		field, fieldOK := args[1].(string)
		zone, zoneOK := args[2].(string)
		layout, layoutOK := args[3].(string)
		if !fieldOK || !validJSONPayloadTimeField(field) || !zoneOK || zone == "" || !layoutOK || layout == "" {
			return ErrInvalidJSONPayloadTemplate
		}
		if _, err := time.LoadLocation(zone); err != nil {
			return ErrInvalidJSONPayloadTemplate
		}
	}
	return nil
}

func (compiled *CompiledJSONPayload) samplesByAlias(snapshot SourceSnapshot) (map[string]SourceSample, error) {
	samples := make(map[string]SourceSample, len(compiled.sources))
	for _, sample := range snapshot.Samples {
		descriptor, selected := compiled.sources[sample.Alias]
		if !selected {
			continue
		}
		if _, duplicate := samples[sample.Alias]; duplicate {
			return nil, ErrJSONPayloadRender
		}
		if sample.Reference != descriptor.Reference || sample.SchemaVersion != descriptor.SchemaVersion || sample.DataType != descriptor.DataType {
			return nil, ErrJSONPayloadRender
		}
		if sample.Value != nil && !validJSONPayloadSampleValue(sample.DataType, sample.Value) {
			return nil, ErrJSONPayloadRender
		}
		samples[sample.Alias] = cloneSourceSample(sample)
	}
	for alias, descriptor := range compiled.sources {
		if _, exists := samples[alias]; !exists {
			samples[alias] = SourceSample{
				Alias: alias, Reference: descriptor.Reference, SchemaVersion: descriptor.SchemaVersion,
				DataType: descriptor.DataType, Unit: descriptor.Unit, Quality: SourceQualityUnavailable,
			}
		}
	}
	return samples, nil
}

func renderJSONPayloadHelper(operation jsonPayloadOperation, samples map[string]SourceSample, renderContext JSONPayloadRenderContext) ([]byte, error) {
	var sample SourceSample
	if len(operation.args) > 0 {
		if alias, ok := operation.args[0].(string); ok {
			sample = samples[alias]
		}
	}
	var value any
	switch operation.helper {
	case "value":
		value = availableJSONPayloadValue(sample, nil)
	case "available":
		value = sample.Available
	case "quality":
		value = sample.Quality
	case "error":
		value = sample.Error
	case "unit":
		value = sample.Unit
	case "sequence":
		value = sample.Sequence
	case "schema_version":
		value = sample.SchemaVersion
	case "data_type":
		value = sample.DataType
	case "observed_at":
		value = jsonPayloadTime(sample.ObservedAt)
	case "emitted_at":
		value = jsonPayloadTime(sample.EmittedAt)
	case "period_start":
		value = jsonPayloadTime(sample.PeriodStart)
	case "period_end":
		value = jsonPayloadTime(sample.PeriodEnd)
	case "coverage":
		if sample.CoveragePercent != nil {
			value = *sample.CoveragePercent
		}
	case "round":
		value = roundedJSONPayloadValue(sample, operation.args[1])
	case "scale":
		value = scaledJSONPayloadValue(sample, operation.args[1], operation.args[2])
	case "default":
		value = availableJSONPayloadValue(sample, operation.args[1])
	case "format_time":
		value = formattedJSONPayloadTime(sample, operation.args[1].(string), operation.location, operation.args[3].(string))
	case "published_at":
		if renderContext.PublishedAt.IsZero() {
			return nil, ErrJSONPayloadRender
		}
		value = renderContext.PublishedAt.Format(time.RFC3339Nano)
	case "published_unix_ms":
		if renderContext.PublishedAt.IsZero() {
			return nil, ErrJSONPayloadRender
		}
		value = renderContext.PublishedAt.UnixMilli()
	case "publisher_id":
		if renderContext.PublisherID != uuid.Nil {
			value = renderContext.PublisherID.String()
		}
	default:
		return nil, ErrJSONPayloadRender
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrJSONPayloadRender
	}
	return encoded, nil
}

func availableJSONPayloadValue(sample SourceSample, fallback any) any {
	if !sample.Available || sample.Value == nil {
		return fallback
	}
	return sample.Value
}

func roundedJSONPayloadValue(sample SourceSample, decimalsArgument any) any {
	if !sample.Available || sample.Value == nil {
		return nil
	}
	value, ok := sourceNumericValue(sample.Value)
	decimals, decimalsOK := integerArgument(decimalsArgument)
	if !ok || !decimalsOK {
		return nil
	}
	factor := math.Pow10(decimals)
	return math.Round(value*factor) / factor
}

func scaledJSONPayloadValue(sample SourceSample, gainArgument, offsetArgument any) any {
	if !sample.Available || sample.Value == nil {
		return nil
	}
	value, valueOK := sourceNumericValue(sample.Value)
	gain, gainOK := finiteFloatArgument(gainArgument)
	offset, offsetOK := finiteFloatArgument(offsetArgument)
	if !valueOK || !gainOK || !offsetOK {
		return nil
	}
	result := value*gain + offset
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil
	}
	return result
}

func formattedJSONPayloadTime(sample SourceSample, field string, location *time.Location, layout string) any {
	value := sourceSampleTime(sample, field)
	if value == nil || location == nil {
		return nil
	}
	return value.In(location).Format(layout)
}

func sourceSampleTime(sample SourceSample, field string) *time.Time {
	switch field {
	case "observed_at":
		return sample.ObservedAt
	case "emitted_at":
		return sample.EmittedAt
	case "period_start":
		return sample.PeriodStart
	case "period_end":
		return sample.PeriodEnd
	default:
		return nil
	}
}

func jsonPayloadTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func normalizeRenderedJSONPayload(rendered []byte) (json.RawMessage, error) {
	if len(rendered) > MaxJSONPayloadBytes {
		return nil, ErrJSONPayloadTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(rendered))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrJSONPayloadInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrJSONPayloadInvalid
	}
	if _, err := inspectJSONPayloadValue(value, 1); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, rendered); err != nil {
		return nil, ErrJSONPayloadInvalid
	}
	return append(json.RawMessage(nil), compact.Bytes()...), nil
}

func inspectJSONPayloadValue(value any, depth int) (int, error) {
	if depth > MaxJSONPayloadDepth {
		return 0, ErrJSONPayloadTooComplex
	}
	nodes := 1
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			count, err := inspectJSONPayloadValue(item, depth+1)
			if err != nil {
				return 0, err
			}
			nodes += count
			if nodes > MaxJSONPayloadNodes {
				return 0, ErrJSONPayloadTooComplex
			}
		}
	case map[string]any:
		for _, item := range typed {
			count, err := inspectJSONPayloadValue(item, depth+1)
			if err != nil {
				return 0, err
			}
			nodes += count
			if nodes > MaxJSONPayloadNodes {
				return 0, ErrJSONPayloadTooComplex
			}
		}
	}
	return nodes, nil
}

func integerArgument(value any) (int, bool) {
	switch typed := value.(type) {
	case int64:
		return int(typed), int64(int(typed)) == typed
	case uint64:
		return int(typed), uint64(int(typed)) == typed
	default:
		return 0, false
	}
}

func finiteFloatArgument(value any) (float64, bool) {
	var converted float64
	switch typed := value.(type) {
	case int64:
		converted = float64(typed)
	case uint64:
		converted = float64(typed)
	case float64:
		converted = typed
	default:
		return 0, false
	}
	return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
}

func sourceNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int16:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case float32:
		converted := float64(typed)
		return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		converted, err := strconv.ParseFloat(string(typed), 64)
		return converted, err == nil && !math.IsNaN(converted) && !math.IsInf(converted, 0)
	default:
		return 0, false
	}
}

func isNumericSourceDataType(dataType SourceDataType) bool {
	switch dataType {
	case SourceDataTypeInt16, SourceDataTypeUInt16, SourceDataTypeInt32, SourceDataTypeUInt32, SourceDataTypeFloat32, SourceDataTypeFloat64:
		return true
	default:
		return false
	}
}

func validJSONPayloadSampleValue(dataType SourceDataType, value any) bool {
	if value == nil {
		return false
	}
	switch dataType {
	case SourceDataTypeBool:
		_, valid := value.(bool)
		return valid
	case SourceDataTypeInt16:
		_, valid := value.(int16)
		return valid
	case SourceDataTypeUInt16:
		_, valid := value.(uint16)
		return valid
	case SourceDataTypeInt32:
		_, valid := value.(int32)
		return valid
	case SourceDataTypeUInt32:
		_, valid := value.(uint32)
		return valid
	case SourceDataTypeFloat32:
		converted, valid := value.(float32)
		return valid && !math.IsNaN(float64(converted)) && !math.IsInf(float64(converted), 0)
	case SourceDataTypeFloat64:
		converted, valid := value.(float64)
		return valid && !math.IsNaN(converted) && !math.IsInf(converted, 0)
	case SourceDataTypeString:
		_, valid := value.(string)
		return valid
	default:
		return false
	}
}

func validJSONPayloadTimeField(field string) bool {
	switch field {
	case "observed_at", "emitted_at", "period_start", "period_end":
		return true
	default:
		return false
	}
}

type jsonPayloadFixtureKind uint8

const (
	jsonPayloadFixtureGood jsonPayloadFixtureKind = iota
	jsonPayloadFixtureUnavailable
	jsonPayloadFixtureWindowed
)

func jsonPayloadFixture(sources map[string]SourceDescriptor, kind jsonPayloadFixtureKind) SourceSnapshot {
	capturedAt := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	aliases := make([]string, 0, len(sources))
	for alias := range sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	samples := make([]SourceSample, 0, len(aliases))
	for index, alias := range aliases {
		descriptor := sources[alias]
		observedAt := capturedAt.Add(-time.Duration(index) * time.Second)
		sample := SourceSample{
			Alias: alias, Reference: descriptor.Reference, Available: kind != jsonPayloadFixtureUnavailable,
			Sequence: uint64(index + 1), SchemaVersion: descriptor.SchemaVersion, DataType: descriptor.DataType,
			Unit: descriptor.Unit, Value: jsonPayloadFixtureValue(descriptor.DataType), Quality: SourceQualityGood,
			ObservedAt: &observedAt, EmittedAt: &capturedAt,
		}
		if kind == jsonPayloadFixtureUnavailable {
			sample.Value = nil
			sample.Quality = SourceQualityUnavailable
			sample.Error = "representative source unavailable"
		}
		if descriptor.PeriodKind == SourcePeriodWindowed {
			periodStart := capturedAt.Add(-time.Hour)
			periodEnd := capturedAt
			sample.PeriodStart = &periodStart
			sample.PeriodEnd = &periodEnd
			if kind != jsonPayloadFixtureUnavailable {
				coverage := 100.0
				if kind == jsonPayloadFixtureWindowed {
					coverage = 75
					sample.Quality = SourceQualityPartial
				}
				sample.CoveragePercent = &coverage
			}
		}
		samples = append(samples, sample)
	}
	return SourceSnapshot{CapturedAt: capturedAt, Samples: samples}
}

func jsonPayloadFixtureValue(dataType SourceDataType) any {
	switch dataType {
	case SourceDataTypeBool:
		return true
	case SourceDataTypeInt16:
		return int16(-12)
	case SourceDataTypeUInt16:
		return uint16(12)
	case SourceDataTypeInt32:
		return int32(-1200)
	case SourceDataTypeUInt32:
		return uint32(1200)
	case SourceDataTypeFloat32:
		return float32(12.25)
	case SourceDataTypeFloat64:
		return 12.25
	case SourceDataTypeString:
		return "running"
	default:
		return nil
	}
}
