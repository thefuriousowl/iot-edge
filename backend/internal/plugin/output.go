package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxPluginOutputs        = 128
	maxOutputNameLength     = 100
	maxOutputDescription    = 500
	maxOutputUnitLength     = 32
	maxOutputStringLength   = 4096
	maxOutputErrorLength    = 1000
	maxOutputIssues         = 64
	maxOutputIssueSource    = 200
	maxOutputAttributes     = 32
	maxOutputAttributeValue = 500
)

var (
	ErrInvalidOutputDescriptor = errors.New("invalid Plugin output descriptor")
	ErrInvalidOutputBatch      = errors.New("invalid Plugin output batch")

	outputKeyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	outputIssueCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type OutputKey string
type OutputDataType string
type OutputPeriodKind string
type OutputQuality string

const (
	OutputDataTypeBool    OutputDataType = "bool"
	OutputDataTypeInt16   OutputDataType = "int16"
	OutputDataTypeUInt16  OutputDataType = "uint16"
	OutputDataTypeInt32   OutputDataType = "int32"
	OutputDataTypeUInt32  OutputDataType = "uint32"
	OutputDataTypeFloat32 OutputDataType = "float32"
	OutputDataTypeFloat64 OutputDataType = "float64"
	OutputDataTypeString  OutputDataType = "string"
)

const (
	OutputPeriodInstantaneous OutputPeriodKind = "instantaneous"
	OutputPeriodWindowed      OutputPeriodKind = "windowed"
)

const (
	OutputQualityGood    OutputQuality = "good"
	OutputQualityPartial OutputQuality = "partial"
	OutputQualityBad     OutputQuality = "bad"
)

type OutputDescriptor struct {
	Key           OutputKey        `json:"key"`
	Name          string           `json:"name"`
	Description   string           `json:"description,omitempty"`
	SchemaVersion uint             `json:"schema_version"`
	DataType      OutputDataType   `json:"data_type"`
	Unit          string           `json:"unit,omitempty"`
	DynamicUnit   bool             `json:"dynamic_unit,omitempty"`
	PeriodKind    OutputPeriodKind `json:"period_kind"`
}

type OutputIssue struct {
	Code        string     `json:"code"`
	Message     string     `json:"message"`
	Source      string     `json:"source,omitempty"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

type OutputValue struct {
	Key             OutputKey         `json:"key"`
	SchemaVersion   uint              `json:"schema_version"`
	DataType        OutputDataType    `json:"data_type"`
	Unit            string            `json:"unit,omitempty"`
	Value           any               `json:"value"`
	Quality         OutputQuality     `json:"quality"`
	Error           string            `json:"error,omitempty"`
	ObservedAt      time.Time         `json:"observed_at"`
	PeriodStart     time.Time         `json:"period_start"`
	PeriodEnd       time.Time         `json:"period_end"`
	CoveragePercent *float64          `json:"coverage_percent,omitempty"`
	Issues          []OutputIssue     `json:"issues,omitempty"`
	Attributes      map[string]string `json:"attributes,omitempty"`
}

type OutputBatch struct {
	InstanceID  uuid.UUID     `json:"plugin_instance_id"`
	Sequence    uint64        `json:"sequence"`
	PublishedAt time.Time     `json:"published_at"`
	Values      []OutputValue `json:"values"`
}

func (value *OutputValue) UnmarshalJSON(data []byte) error {
	type outputValueAlias OutputValue
	var decoded outputValueAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	typed, err := decodeOutputTypedValue(decoded.DataType, raw.Value)
	if err != nil {
		return err
	}
	decoded.Value = typed
	*value = OutputValue(decoded)
	return nil
}

func ValidateOutputDescriptors(descriptors []OutputDescriptor) error {
	if len(descriptors) > maxPluginOutputs {
		return fmt.Errorf("%w: too many outputs", ErrInvalidOutputDescriptor)
	}
	seen := make(map[OutputKey]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		if !outputKeyPattern.MatchString(string(descriptor.Key)) {
			return fmt.Errorf("%w: key %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if _, duplicate := seen[descriptor.Key]; duplicate {
			return fmt.Errorf("%w: duplicate key %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		seen[descriptor.Key] = struct{}{}
		if descriptor.Name == "" || descriptor.Name != strings.TrimSpace(descriptor.Name) || len(descriptor.Name) > maxOutputNameLength {
			return fmt.Errorf("%w: name for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if descriptor.Description != strings.TrimSpace(descriptor.Description) || len(descriptor.Description) > maxOutputDescription {
			return fmt.Errorf("%w: description for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if descriptor.SchemaVersion == 0 {
			return fmt.Errorf("%w: schema version for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if !validOutputDataType(descriptor.DataType) {
			return fmt.Errorf("%w: data type for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if descriptor.Unit != strings.TrimSpace(descriptor.Unit) || len(descriptor.Unit) > maxOutputUnitLength {
			return fmt.Errorf("%w: unit for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if descriptor.DynamicUnit && descriptor.Unit != "" {
			return fmt.Errorf("%w: dynamic unit for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
		if descriptor.PeriodKind != OutputPeriodInstantaneous && descriptor.PeriodKind != OutputPeriodWindowed {
			return fmt.Errorf("%w: period kind for %q", ErrInvalidOutputDescriptor, descriptor.Key)
		}
	}
	return nil
}

func NormalizeOutputBatch(batch OutputBatch, descriptors []OutputDescriptor) (OutputBatch, error) {
	if err := ValidateOutputDescriptors(descriptors); err != nil {
		return OutputBatch{}, fmt.Errorf("%w: %v", ErrInvalidOutputBatch, err)
	}
	if batch.InstanceID == uuid.Nil || batch.Sequence == 0 || batch.PublishedAt.IsZero() || len(batch.Values) == 0 || len(batch.Values) > maxPluginOutputs {
		return OutputBatch{}, ErrInvalidOutputBatch
	}
	descriptorByKey := make(map[OutputKey]OutputDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		descriptorByKey[descriptor.Key] = descriptor
	}
	normalized := OutputBatch{
		InstanceID:  batch.InstanceID,
		Sequence:    batch.Sequence,
		PublishedAt: batch.PublishedAt.UTC(),
		Values:      make([]OutputValue, 0, len(batch.Values)),
	}
	seen := make(map[OutputKey]struct{}, len(batch.Values))
	for _, value := range batch.Values {
		descriptor, exists := descriptorByKey[value.Key]
		if !exists {
			return OutputBatch{}, fmt.Errorf("%w: output %q is not declared", ErrInvalidOutputBatch, value.Key)
		}
		if _, duplicate := seen[value.Key]; duplicate {
			return OutputBatch{}, fmt.Errorf("%w: duplicate output %q", ErrInvalidOutputBatch, value.Key)
		}
		seen[value.Key] = struct{}{}
		entry, err := normalizeOutputValue(value, descriptor, normalized.PublishedAt)
		if err != nil {
			return OutputBatch{}, err
		}
		normalized.Values = append(normalized.Values, entry)
	}
	return normalized, nil
}

func CloneOutputBatch(batch OutputBatch) OutputBatch {
	cloned := batch
	cloned.Values = make([]OutputValue, len(batch.Values))
	for index, value := range batch.Values {
		cloned.Values[index] = cloneOutputValue(value)
	}
	return cloned
}

func outputBatchSourceAt(batch OutputBatch) time.Time {
	var sourceAt time.Time
	for _, value := range batch.Values {
		if value.ObservedAt.After(sourceAt) {
			sourceAt = value.ObservedAt
		}
	}
	return sourceAt.UTC()
}

func normalizeOutputValue(value OutputValue, descriptor OutputDescriptor, publishedAt time.Time) (OutputValue, error) {
	if value.SchemaVersion != descriptor.SchemaVersion || value.DataType != descriptor.DataType {
		return OutputValue{}, fmt.Errorf("%w: schema for %q", ErrInvalidOutputBatch, value.Key)
	}
	if value.Unit != strings.TrimSpace(value.Unit) || len(value.Unit) > maxOutputUnitLength {
		return OutputValue{}, fmt.Errorf("%w: unit for %q", ErrInvalidOutputBatch, value.Key)
	}
	if descriptor.DynamicUnit {
		if value.Unit == "" {
			return OutputValue{}, fmt.Errorf("%w: dynamic unit for %q", ErrInvalidOutputBatch, value.Key)
		}
	} else if value.Unit != descriptor.Unit {
		return OutputValue{}, fmt.Errorf("%w: fixed unit for %q", ErrInvalidOutputBatch, value.Key)
	}
	if value.ObservedAt.IsZero() || value.PeriodStart.IsZero() || value.PeriodEnd.IsZero() || value.PeriodStart.After(value.PeriodEnd) {
		return OutputValue{}, fmt.Errorf("%w: timestamps for %q", ErrInvalidOutputBatch, value.Key)
	}
	value.ObservedAt = value.ObservedAt.UTC()
	value.PeriodStart = value.PeriodStart.UTC()
	value.PeriodEnd = value.PeriodEnd.UTC()
	if value.ObservedAt.Before(value.PeriodEnd) || publishedAt.Before(value.ObservedAt) {
		return OutputValue{}, fmt.Errorf("%w: timestamp order for %q", ErrInvalidOutputBatch, value.Key)
	}
	if descriptor.PeriodKind == OutputPeriodInstantaneous {
		if !value.PeriodStart.Equal(value.PeriodEnd) || !value.PeriodStart.Equal(value.ObservedAt) {
			return OutputValue{}, fmt.Errorf("%w: instantaneous period for %q", ErrInvalidOutputBatch, value.Key)
		}
	} else if !value.PeriodStart.Before(value.PeriodEnd) {
		return OutputValue{}, fmt.Errorf("%w: windowed period for %q", ErrInvalidOutputBatch, value.Key)
	}

	coverage, err := normalizeCoverage(value.CoveragePercent)
	if err != nil {
		return OutputValue{}, fmt.Errorf("%w: coverage for %q", ErrInvalidOutputBatch, value.Key)
	}
	if descriptor.PeriodKind == OutputPeriodInstantaneous && coverage != nil {
		return OutputValue{}, fmt.Errorf("%w: instantaneous coverage for %q", ErrInvalidOutputBatch, value.Key)
	}
	issues, err := normalizeOutputIssues(value.Issues, value.PeriodStart, value.PeriodEnd)
	if err != nil {
		return OutputValue{}, fmt.Errorf("%w: issues for %q: %v", ErrInvalidOutputBatch, value.Key, err)
	}
	attributes, err := normalizeOutputAttributes(value.Attributes)
	if err != nil {
		return OutputValue{}, fmt.Errorf("%w: attributes for %q: %v", ErrInvalidOutputBatch, value.Key, err)
	}

	value.Error = strings.TrimSpace(value.Error)
	if len(value.Error) > maxOutputErrorLength {
		return OutputValue{}, fmt.Errorf("%w: error for %q", ErrInvalidOutputBatch, value.Key)
	}
	switch value.Quality {
	case OutputQualityGood:
		if value.Error != "" || len(issues) > 0 || coverage != nil && *coverage != 100 {
			return OutputValue{}, fmt.Errorf("%w: good quality for %q", ErrInvalidOutputBatch, value.Key)
		}
	case OutputQualityPartial:
		if value.Error != "" || len(issues) == 0 && (coverage == nil || *coverage >= 100) {
			return OutputValue{}, fmt.Errorf("%w: partial quality for %q", ErrInvalidOutputBatch, value.Key)
		}
	case OutputQualityBad:
		if value.Value != nil || value.Error == "" {
			return OutputValue{}, fmt.Errorf("%w: bad quality for %q", ErrInvalidOutputBatch, value.Key)
		}
	default:
		return OutputValue{}, fmt.Errorf("%w: quality for %q", ErrInvalidOutputBatch, value.Key)
	}
	if value.Quality != OutputQualityBad {
		typed, valid := normalizeOutputTypedValue(value.DataType, value.Value)
		if !valid {
			return OutputValue{}, fmt.Errorf("%w: value for %q", ErrInvalidOutputBatch, value.Key)
		}
		value.Value = typed
	}
	value.CoveragePercent = coverage
	value.Issues = issues
	value.Attributes = attributes
	return value, nil
}

func normalizeCoverage(value *float64) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100 {
		return nil, ErrInvalidOutputBatch
	}
	cloned := *value
	return &cloned, nil
}

func normalizeOutputIssues(values []OutputIssue, periodStart, periodEnd time.Time) ([]OutputIssue, error) {
	if len(values) > maxOutputIssues {
		return nil, ErrInvalidOutputBatch
	}
	issues := make([]OutputIssue, len(values))
	for index, issue := range values {
		issue.Message = strings.TrimSpace(issue.Message)
		issue.Source = strings.TrimSpace(issue.Source)
		if !outputIssueCodePattern.MatchString(issue.Code) || issue.Message == "" || len(issue.Message) > maxOutputErrorLength || len(issue.Source) > maxOutputIssueSource {
			return nil, ErrInvalidOutputBatch
		}
		if (issue.PeriodStart == nil) != (issue.PeriodEnd == nil) {
			return nil, ErrInvalidOutputBatch
		}
		if issue.PeriodStart != nil {
			start, end := issue.PeriodStart.UTC(), issue.PeriodEnd.UTC()
			if start.After(end) || start.Before(periodStart) || end.After(periodEnd) {
				return nil, ErrInvalidOutputBatch
			}
			issue.PeriodStart = &start
			issue.PeriodEnd = &end
		}
		issues[index] = issue
	}
	return issues, nil
}

func normalizeOutputAttributes(values map[string]string) (map[string]string, error) {
	if len(values) > maxOutputAttributes {
		return nil, ErrInvalidOutputBatch
	}
	if values == nil {
		return nil, nil
	}
	attributes := make(map[string]string, len(values))
	for key, value := range values {
		if !outputKeyPattern.MatchString(key) || value != strings.TrimSpace(value) || len(value) > maxOutputAttributeValue {
			return nil, ErrInvalidOutputBatch
		}
		attributes[key] = value
	}
	return attributes, nil
}

func normalizeOutputTypedValue(dataType OutputDataType, value any) (any, bool) {
	switch dataType {
	case OutputDataTypeBool:
		typed, valid := value.(bool)
		return typed, valid
	case OutputDataTypeInt16:
		typed, valid := value.(int16)
		return typed, valid
	case OutputDataTypeUInt16:
		typed, valid := value.(uint16)
		return typed, valid
	case OutputDataTypeInt32:
		typed, valid := value.(int32)
		return typed, valid
	case OutputDataTypeUInt32:
		typed, valid := value.(uint32)
		return typed, valid
	case OutputDataTypeFloat32:
		typed, valid := value.(float32)
		return typed, valid && !float32Invalid(typed)
	case OutputDataTypeFloat64:
		typed, valid := value.(float64)
		return typed, valid && !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case OutputDataTypeString:
		typed, valid := value.(string)
		return typed, valid && len(typed) <= maxOutputStringLength
	default:
		return nil, false
	}
}

func decodeOutputTypedValue(dataType OutputDataType, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: missing value", ErrInvalidOutputBatch)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var target any
	switch dataType {
	case OutputDataTypeBool:
		target = new(bool)
	case OutputDataTypeInt16:
		target = new(int16)
	case OutputDataTypeUInt16:
		target = new(uint16)
	case OutputDataTypeInt32:
		target = new(int32)
	case OutputDataTypeUInt32:
		target = new(uint32)
	case OutputDataTypeFloat32:
		target = new(float32)
	case OutputDataTypeFloat64:
		target = new(float64)
	case OutputDataTypeString:
		target = new(string)
	default:
		return nil, fmt.Errorf("%w: unsupported data type %q", ErrInvalidOutputBatch, dataType)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return nil, fmt.Errorf("%w: decoding %s value: %v", ErrInvalidOutputBatch, dataType, err)
	}
	switch typed := target.(type) {
	case *bool:
		return *typed, nil
	case *int16:
		return *typed, nil
	case *uint16:
		return *typed, nil
	case *int32:
		return *typed, nil
	case *uint32:
		return *typed, nil
	case *float32:
		return *typed, nil
	case *float64:
		return *typed, nil
	case *string:
		return *typed, nil
	default:
		return nil, ErrInvalidOutputBatch
	}
}

func validOutputDataType(dataType OutputDataType) bool {
	switch dataType {
	case OutputDataTypeBool, OutputDataTypeInt16, OutputDataTypeUInt16, OutputDataTypeInt32, OutputDataTypeUInt32, OutputDataTypeFloat32, OutputDataTypeFloat64, OutputDataTypeString:
		return true
	default:
		return false
	}
}

func float32Invalid(value float32) bool {
	converted := float64(value)
	return math.IsNaN(converted) || math.IsInf(converted, 0)
}

func cloneOutputValue(value OutputValue) OutputValue {
	cloned := value
	if value.CoveragePercent != nil {
		coverage := *value.CoveragePercent
		cloned.CoveragePercent = &coverage
	}
	cloned.Issues = make([]OutputIssue, len(value.Issues))
	for index, issue := range value.Issues {
		cloned.Issues[index] = issue
		if issue.PeriodStart != nil {
			start := *issue.PeriodStart
			cloned.Issues[index].PeriodStart = &start
		}
		if issue.PeriodEnd != nil {
			end := *issue.PeriodEnd
			cloned.Issues[index].PeriodEnd = &end
		}
	}
	if value.Attributes != nil {
		cloned.Attributes = make(map[string]string, len(value.Attributes))
		for key, attribute := range value.Attributes {
			cloned.Attributes[key] = attribute
		}
	}
	return cloned
}
