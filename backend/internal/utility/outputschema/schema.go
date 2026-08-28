package outputschema

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/utility"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

const VersionV1 uint = 1

var ErrInvalidOutput = errors.New("invalid utility Plugin output")

const (
	AttributeAssetID         = "asset_id"
	AttributeBoundaryAssetID = "boundary_asset_id"
	AttributeResource        = "resource"
	AttributeQuantity        = "quantity"
	AttributeMappingKey      = "mapping_key"
)

func Descriptors(config utility.Config) ([]plugin.OutputDescriptor, error) {
	return CompatibleDescriptors(VersionV1, config)
}

func CompatibleDescriptors(version uint, config utility.Config) ([]plugin.OutputDescriptor, error) {
	if version != VersionV1 {
		return nil, ErrInvalidOutput
	}
	if err := config.Validate(); err != nil {
		return nil, ErrInvalidOutput
	}
	result := make([]plugin.OutputDescriptor, len(config.Mappings))
	for index, mapping := range config.Mappings {
		result[index] = plugin.OutputDescriptor{
			Key:           plugin.OutputKey(mapping.Key),
			Name:          mapping.Key,
			Description:   fmt.Sprintf("Utility metric %s for Asset %s", mapping.Slot, mapping.OwnerAssetID),
			SchemaVersion: VersionV1,
			DataType:      plugin.OutputDataTypeFloat64,
			DynamicUnit:   true,
			PeriodKind:    plugin.OutputPeriodWindowed,
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	if err := plugin.ValidateOutputDescriptors(result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	return result, nil
}

func Value(config utility.Config, mapping utility.Mapping, semantic asset.Semantic, periodValue float64, metric analytics.Metric, observedAt time.Time) (plugin.OutputValue, error) {
	if err := config.Validate(); err != nil || !containsMapping(config, mapping) || semantic.Validate() != nil || semantic.Resource != mapping.Semantic.Resource || metric.From.IsZero() || !metric.From.Before(metric.To) || observedAt.IsZero() || observedAt.Before(metric.To) || math.IsNaN(periodValue) || math.IsInf(periodValue, 0) || math.IsNaN(metric.CoveragePercent) || math.IsInf(metric.CoveragePercent, 0) || metric.CoveragePercent < 0 || metric.CoveragePercent > 100 {
		return plugin.OutputValue{}, ErrInvalidOutput
	}
	coverage := metric.CoveragePercent
	value := plugin.OutputValue{
		Key:             plugin.OutputKey(mapping.Key),
		SchemaVersion:   VersionV1,
		DataType:        plugin.OutputDataTypeFloat64,
		Unit:            string(semantic.Unit),
		ObservedAt:      observedAt.UTC(),
		PeriodStart:     metric.From.UTC(),
		PeriodEnd:       metric.To.UTC(),
		CoveragePercent: &coverage,
		Attributes: map[string]string{
			AttributeAssetID:         mapping.OwnerAssetID.String(),
			AttributeBoundaryAssetID: config.AssetID.String(),
			AttributeResource:        string(semantic.Resource),
			AttributeQuantity:        string(semantic.Quantity),
			AttributeMappingKey:      mapping.Key,
		},
	}
	switch {
	case coverage == 100:
		value.Value = periodValue
		value.Quality = plugin.OutputQualityGood
	case coverage > 0:
		value.Value = periodValue
		value.Quality = plugin.OutputQualityPartial
		value.Issues = []plugin.OutputIssue{coverageIssue("partial_coverage", "Persisted history does not cover the complete period", mapping.Source.Key(), value.PeriodStart, value.PeriodEnd)}
	default:
		value.Value = nil
		value.Quality = plugin.OutputQualityBad
		value.Error = "Persisted history has no usable coverage for this period"
		value.Issues = []plugin.OutputIssue{coverageIssue("no_coverage", value.Error, mapping.Source.Key(), value.PeriodStart, value.PeriodEnd)}
	}
	if err := ValidateValue(config, mapping, semantic, value); err != nil {
		return plugin.OutputValue{}, err
	}
	return value, nil
}

func ValidateValue(config utility.Config, mapping utility.Mapping, semantic asset.Semantic, value plugin.OutputValue) error {
	descriptors, err := Descriptors(config)
	if err != nil || !containsMapping(config, mapping) || semantic.Validate() != nil || semantic.Resource != mapping.Semantic.Resource || value.Key != plugin.OutputKey(mapping.Key) || value.SchemaVersion != VersionV1 || value.DataType != plugin.OutputDataTypeFloat64 || value.Unit != string(semantic.Unit) || value.CoveragePercent == nil || !value.PeriodStart.Before(value.PeriodEnd) {
		return ErrInvalidOutput
	}
	var descriptor plugin.OutputDescriptor
	found := false
	for _, candidate := range descriptors {
		if candidate.Key == value.Key {
			descriptor, found = candidate, true
			break
		}
	}
	if !found {
		return ErrInvalidOutput
	}
	if _, err := plugin.NormalizeOutputBatch(plugin.OutputBatch{InstanceID: uuid.New(), Sequence: 1, PublishedAt: value.ObservedAt, Values: []plugin.OutputValue{value}}, []plugin.OutputDescriptor{descriptor}); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	want := map[string]string{
		AttributeAssetID: mapping.OwnerAssetID.String(), AttributeBoundaryAssetID: config.AssetID.String(),
		AttributeResource: string(semantic.Resource), AttributeQuantity: string(semantic.Quantity), AttributeMappingKey: mapping.Key,
	}
	for key, expected := range want {
		if value.Attributes[key] != expected {
			return ErrInvalidOutput
		}
	}
	if len(value.Attributes) != len(want) {
		return ErrInvalidOutput
	}
	return nil
}

func coverageIssue(code, message, source string, start, end time.Time) plugin.OutputIssue {
	return plugin.OutputIssue{Code: code, Message: message, Source: source, PeriodStart: &start, PeriodEnd: &end}
}

func containsMapping(config utility.Config, mapping utility.Mapping) bool {
	for _, candidate := range config.Mappings {
		if candidate.Key == mapping.Key && candidate.OwnerAssetID == mapping.OwnerAssetID && candidate.Source == mapping.Source && reflect.DeepEqual(candidate.Semantic, mapping.Semantic) && candidate.Slot == mapping.Slot {
			return true
		}
	}
	return false
}
