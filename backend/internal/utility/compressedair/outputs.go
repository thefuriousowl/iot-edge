package compressedair

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const OutputSchemaVersionV1 uint = 1

var ErrInvalidPluginOutput = errors.New("invalid compressed-air Plugin output")

const (
	OutputLivePower       plugin.OutputKey = "compressed_air.live_power"
	OutputLiveFlow        plugin.OutputKey = "compressed_air.live_flow"
	OutputLivePressure    plugin.OutputKey = "compressed_air.live_pressure"
	OutputEnergy          plugin.OutputKey = "compressed_air.energy"
	OutputVolume          plugin.OutputKey = "compressed_air.volume"
	OutputSEC             plugin.OutputKey = "compressed_air.sec"
	OutputCost            plugin.OutputKey = "compressed_air.cost"
	OutputCostPerVolume   plugin.OutputKey = "compressed_air.cost_per_volume"
	OutputRuntimeRatio    plugin.OutputKey = "compressed_air.runtime_ratio"
	OutputLoadRatio       plugin.OutputKey = "compressed_air.load_ratio"
	OutputPressureAverage plugin.OutputKey = "compressed_air.pressure_average"
	OutputPressureStdDev  plugin.OutputKey = "compressed_air.pressure_stddev"
	OutputPressureDrop    plugin.OutputKey = "compressed_air.pressure_drop"
	OutputLeakFlow        plugin.OutputKey = "compressed_air.estimated_leak_flow"
	OutputLeakEnergy      plugin.OutputKey = "compressed_air.estimated_leak_energy"
	OutputLeakCost        plugin.OutputKey = "compressed_air.estimated_leak_cost"
)

type OutputContext struct {
	InstanceID      uuid.UUID
	Sequence        uint64
	OwnerAssetID    uuid.UUID
	BoundaryAssetID uuid.UUID
	PublishedAt     time.Time
	Sources         map[plugin.OutputKey]string
}

type LiveSnapshot struct {
	ObservedAt  time.Time
	PowerKW     float64
	FlowNm3Hour float64
	PressureBar float64
}

func PluginOutputDescriptors() []plugin.OutputDescriptor {
	return []plugin.OutputDescriptor{
		descriptor(OutputLivePower, "Live compressor power", "kW", plugin.OutputPeriodInstantaneous),
		descriptor(OutputLiveFlow, "Live normalized air flow", "Nm3/h", plugin.OutputPeriodInstantaneous),
		descriptor(OutputLivePressure, "Live air pressure", "bar", plugin.OutputPeriodInstantaneous),
		descriptor(OutputEnergy, "Compressed-air energy", "kWh", plugin.OutputPeriodWindowed),
		descriptor(OutputVolume, "Compressed-air volume", "Nm3", plugin.OutputPeriodWindowed),
		descriptor(OutputSEC, "Specific energy consumption", "kWh/Nm3", plugin.OutputPeriodWindowed),
		dynamicDescriptor(OutputCost, "Compressed-air cost"), dynamicDescriptor(OutputCostPerVolume, "Compressed-air cost per volume"),
		descriptor(OutputRuntimeRatio, "Runtime ratio", "%", plugin.OutputPeriodWindowed), descriptor(OutputLoadRatio, "Load ratio", "%", plugin.OutputPeriodWindowed),
		descriptor(OutputPressureAverage, "Average pressure", "bar", plugin.OutputPeriodWindowed), descriptor(OutputPressureStdDev, "Pressure standard deviation", "bar", plugin.OutputPeriodWindowed), descriptor(OutputPressureDrop, "Pressure drop", "bar", plugin.OutputPeriodWindowed),
		descriptor(OutputLeakFlow, "Estimated baseline flow", "Nm3/h", plugin.OutputPeriodWindowed), descriptor(OutputLeakEnergy, "Estimated baseline energy", "kWh", plugin.OutputPeriodWindowed), dynamicDescriptor(OutputLeakCost, "Estimated baseline cost"),
	}
}

func LivePluginOutputBatch(context OutputContext, snapshot LiveSnapshot) (plugin.OutputBatch, error) {
	if snapshot.ObservedAt.IsZero() || invalidNumber(snapshot.PowerKW) || invalidNumber(snapshot.FlowNm3Hour) || invalidNumber(snapshot.PressureBar) || snapshot.PowerKW < 0 || snapshot.FlowNm3Hour < 0 {
		return plugin.OutputBatch{}, ErrInvalidPluginOutput
	}
	values := []plugin.OutputValue{
		instantValue(OutputLivePower, "kW", snapshot.PowerKW, snapshot.ObservedAt), instantValue(OutputLiveFlow, "Nm3/h", snapshot.FlowNm3Hour, snapshot.ObservedAt), instantValue(OutputLivePressure, "bar", snapshot.PressureBar, snapshot.ObservedAt),
	}
	return normalizeBatch(context, values)
}

func PeriodPluginOutputBatch(context OutputContext, result CalculationResult, leakage *LeakageEstimate) (plugin.OutputBatch, error) {
	if result.From.IsZero() || !result.From.Before(result.To) || context.PublishedAt.Before(result.To) || result.CoveragePercent < 0 || result.CoveragePercent > 100 {
		return plugin.OutputBatch{}, ErrInvalidPluginOutput
	}
	values := []plugin.OutputValue{
		periodValue(OutputEnergy, "kWh", result.EnergyKWh, result.From, result.To, result.CoveragePercent, ""),
		periodValue(OutputVolume, "Nm3", result.VolumeNm3, result.From, result.To, result.CoveragePercent, ""),
		ratioValue(OutputSEC, "kWh/Nm3", result.SEC, result.From, result.To, result.CoveragePercent),
		periodValue(OutputCost, result.Currency, result.Cost, result.From, result.To, result.CoveragePercent, ""),
		ratioValue(OutputCostPerVolume, result.Currency+"/Nm3", result.CostPerNm3, result.From, result.To, result.CoveragePercent),
		ratioValue(OutputRuntimeRatio, "%", percentRatio(result.RuntimeRatio), result.From, result.To, result.CoveragePercent),
		ratioValue(OutputLoadRatio, "%", percentRatio(result.LoadRatio), result.From, result.To, result.CoveragePercent),
		periodValue(OutputPressureAverage, "bar", result.Pressure.AverageBar, result.From, result.To, result.Pressure.CoveragePercent, result.Pressure.Error),
		periodValue(OutputPressureStdDev, "bar", result.Pressure.StdDevBar, result.From, result.To, result.Pressure.CoveragePercent, result.Pressure.Error),
		periodValue(OutputPressureDrop, "bar", result.Pressure.DropBar, result.From, result.To, result.Pressure.CoveragePercent, result.Pressure.Error),
	}
	if leakage != nil {
		if leakage.From.IsZero() || !leakage.From.Before(leakage.To) || context.PublishedAt.Before(leakage.To) {
			return plugin.OutputBatch{}, ErrInvalidPluginOutput
		}
		values = append(values,
			periodValue(OutputLeakFlow, "Nm3/h", leakage.EstimatedFlowNm3PerHour, leakage.From, leakage.To, leakage.CoveragePercent, ""),
			periodValue(OutputLeakEnergy, "kWh", leakage.EstimatedEnergyKWh, leakage.From, leakage.To, leakage.CoveragePercent, ""),
			periodValue(OutputLeakCost, leakage.Currency, leakage.EstimatedCost, leakage.From, leakage.To, leakage.CoveragePercent, ""),
		)
	}
	return normalizeBatch(context, values)
}

func descriptor(key plugin.OutputKey, name, unit string, kind plugin.OutputPeriodKind) plugin.OutputDescriptor {
	return plugin.OutputDescriptor{Key: key, Name: name, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit, PeriodKind: kind}
}
func dynamicDescriptor(key plugin.OutputKey, name string) plugin.OutputDescriptor {
	return plugin.OutputDescriptor{Key: key, Name: name, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodWindowed}
}
func instantValue(key plugin.OutputKey, unit string, value float64, at time.Time) plugin.OutputValue {
	return plugin.OutputValue{Key: key, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit, Value: value, Quality: plugin.OutputQualityGood, ObservedAt: at, PeriodStart: at, PeriodEnd: at}
}
func periodValue(key plugin.OutputKey, unit string, value float64, from, to time.Time, coverage float64, reason string) plugin.OutputValue {
	result := plugin.OutputValue{Key: key, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit, Value: value, Quality: plugin.OutputQualityGood, ObservedAt: to, PeriodStart: from, PeriodEnd: to, CoveragePercent: pointer(coverage)}
	if reason != "" || coverage == 0 {
		result.Value, result.Quality, result.Error = nil, plugin.OutputQualityBad, reason
		if result.Error == "" {
			result.Error = "No usable source coverage"
		}
	} else if coverage < 100 {
		result.Quality = plugin.OutputQualityPartial
		result.Issues = []plugin.OutputIssue{{Code: "partial_coverage", Message: "Source history does not cover the complete period", PeriodStart: &result.PeriodStart, PeriodEnd: &result.PeriodEnd}}
	}
	return result
}
func ratioValue(key plugin.OutputKey, unit string, ratio Ratio, from, to time.Time, coverage float64) plugin.OutputValue {
	if !ratio.Valid && ratio.Error == "" {
		ratio.Error = "Metric is unavailable for this period"
	}
	return periodValue(key, unit, ratio.Value, from, to, coverage, ratio.Error)
}
func percentRatio(value Ratio) Ratio {
	if value.Valid {
		value.Value *= 100
	}
	return value
}
func pointer(value float64) *float64   { return &value }
func invalidNumber(value float64) bool { return math.IsNaN(value) || math.IsInf(value, 0) }

func normalizeBatch(context OutputContext, values []plugin.OutputValue) (plugin.OutputBatch, error) {
	if context.InstanceID == uuid.Nil || context.Sequence == 0 || context.OwnerAssetID == uuid.Nil || context.BoundaryAssetID == uuid.Nil || context.PublishedAt.IsZero() {
		return plugin.OutputBatch{}, ErrInvalidPluginOutput
	}
	for index := range values {
		source := context.Sources[values[index].Key]
		if source == "" {
			return plugin.OutputBatch{}, fmt.Errorf("%w: missing source for %s", ErrInvalidPluginOutput, values[index].Key)
		}
		values[index].ObservedAt = values[index].ObservedAt.UTC()
		values[index].Attributes = map[string]string{"asset_id": context.OwnerAssetID.String(), "boundary_asset_id": context.BoundaryAssetID.String(), "resource": "compressed_air", "source": source}
	}
	batch, err := plugin.NormalizeOutputBatch(plugin.OutputBatch{InstanceID: context.InstanceID, Sequence: context.Sequence, PublishedAt: context.PublishedAt, Values: values}, PluginOutputDescriptors())
	if err != nil {
		return plugin.OutputBatch{}, fmt.Errorf("%w: %v", ErrInvalidPluginOutput, err)
	}
	return batch, nil
}
