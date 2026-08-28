package energy

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const (
	maxEnergyOutputIssues = 64
	maxEnergyOutputText   = 1000
)

const (
	OutputElectricalDemandKW       plugin.OutputKey = "electrical_demand_kw"
	OutputThermalOutputKW          plugin.OutputKey = "thermal_output_kw"
	OutputInstantaneousCOP         plugin.OutputKey = "instantaneous_cop"
	OutputTariffRate               plugin.OutputKey = "tariff_rate"
	OutputTodayElectricalEnergyKWh plugin.OutputKey = "today.electrical_energy_kwh"
	OutputTodayThermalEnergyKWh    plugin.OutputKey = "today.thermal_energy_kwh"
	OutputTodayCOP                 plugin.OutputKey = "today.cop"
	OutputTodayEstimatedCost       plugin.OutputKey = "today.estimated_cost"
	OutputTodayCoveredCost         plugin.OutputKey = "today.covered_estimated_cost"
	OutputTodayElectricalCoverage  plugin.OutputKey = "today.electrical_coverage_percent"
	OutputTodayThermalCoverage     plugin.OutputKey = "today.thermal_coverage_percent"
	OutputMonthElectricalEnergyKWh plugin.OutputKey = "month.electrical_energy_kwh"
	OutputMonthThermalEnergyKWh    plugin.OutputKey = "month.thermal_energy_kwh"
	OutputMonthCOP                 plugin.OutputKey = "month.cop"
	OutputMonthEstimatedCost       plugin.OutputKey = "month.estimated_cost"
	OutputMonthCoveredCost         plugin.OutputKey = "month.covered_estimated_cost"
	OutputMonthElectricalCoverage  plugin.OutputKey = "month.electrical_coverage_percent"
	OutputMonthThermalCoverage     plugin.OutputKey = "month.thermal_coverage_percent"
)

func outputDescriptors() []plugin.OutputDescriptor {
	return []plugin.OutputDescriptor{
		instantaneousOutput(OutputElectricalDemandKW, "Electrical demand", "Total synchronized electrical input demand", "kW"),
		instantaneousOutput(OutputThermalOutputKW, "Thermal output", "Total synchronized thermal output", "kW"),
		instantaneousOutput(OutputInstantaneousCOP, "Instantaneous COP", "Thermal output divided by electrical input", ""),
		{Key: OutputTariffRate, Name: "Tariff rate", Description: "Synchronized flat or Tag tariff rate", SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodInstantaneous},
		windowedOutput(OutputTodayElectricalEnergyKWh, "Today electrical energy", "Integrated electrical input since local day start", "kWh"),
		windowedOutput(OutputTodayThermalEnergyKWh, "Today thermal energy", "Integrated thermal output since local day start", "kWh"),
		windowedOutput(OutputTodayCOP, "Today COP", "Covered thermal energy divided by covered electrical energy since local day start", ""),
		{Key: OutputTodayEstimatedCost, Name: "Today estimated cost", Description: "Fail-closed estimated electrical cost since local day start", SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodWindowed},
		{Key: OutputTodayCoveredCost, Name: "Today covered estimated cost", Description: "Estimated electrical cost for covered local-day duration with explicit partial quality", SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodWindowed},
		windowedOutput(OutputTodayElectricalCoverage, "Today electrical coverage", "Covered electrical duration as a percentage of the local-day period", "%"),
		windowedOutput(OutputTodayThermalCoverage, "Today thermal coverage", "Covered thermal duration as a percentage of the local-day period", "%"),
		windowedOutput(OutputMonthElectricalEnergyKWh, "Month electrical energy", "Integrated electrical input since local month start", "kWh"),
		windowedOutput(OutputMonthThermalEnergyKWh, "Month thermal energy", "Integrated thermal output since local month start", "kWh"),
		windowedOutput(OutputMonthCOP, "Month COP", "Covered thermal energy divided by covered electrical energy since local month start", ""),
		{Key: OutputMonthEstimatedCost, Name: "Month estimated cost", Description: "Fail-closed estimated electrical cost since local month start", SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodWindowed},
		{Key: OutputMonthCoveredCost, Name: "Month covered estimated cost", Description: "Estimated electrical cost for covered local-month duration with explicit partial quality", SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, DynamicUnit: true, PeriodKind: plugin.OutputPeriodWindowed},
		windowedOutput(OutputMonthElectricalCoverage, "Month electrical coverage", "Covered electrical duration as a percentage of the local-month period", "%"),
		windowedOutput(OutputMonthThermalCoverage, "Month thermal coverage", "Covered thermal duration as a percentage of the local-month period", "%"),
	}
}

func instantaneousOutput(key plugin.OutputKey, name, description, unit string) plugin.OutputDescriptor {
	return plugin.OutputDescriptor{Key: key, Name: name, Description: description, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit, PeriodKind: plugin.OutputPeriodInstantaneous}
}

func windowedOutput(key plugin.OutputKey, name, description, unit string) plugin.OutputDescriptor {
	return plugin.OutputDescriptor{Key: key, Name: name, Description: description, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit, PeriodKind: plugin.OutputPeriodWindowed}
}

type outputBuilder struct {
	config     Config
	calculator *Calculator
	history    HistoryReader
	last       BatchMetrics
	today      outputPeriodAccumulator
	month      outputPeriodAccumulator
	hydrated   bool
}

type outputPeriodAccumulator struct {
	metrics     PeriodMetrics
	tagCost     float64
	costInvalid bool
	costError   string
}

func newOutputBuilder(config Config, calculator *Calculator, history HistoryReader) (*outputBuilder, error) {
	if calculator == nil || isNil(history) {
		return nil, ErrHistoryFeedUnavailable
	}
	return &outputBuilder{config: config, calculator: calculator, history: history}, nil
}

func (builder *outputBuilder) Build(ctx context.Context, latest BatchMetrics) ([]plugin.OutputValue, bool, error) {
	if builder == nil || ctx == nil || latest.BatchAt.IsZero() {
		return nil, false, plugin.ErrRuntimeUnavailable
	}
	location, err := time.LoadLocation(builder.config.Timezone)
	if err != nil {
		return nil, false, ErrInvalidConfiguration
	}
	localAt := latest.BatchAt.In(location)
	todayStart := time.Date(localAt.Year(), localAt.Month(), localAt.Day(), 0, 0, 0, 0, location).UTC()
	monthStart := time.Date(localAt.Year(), localAt.Month(), 1, 0, 0, 0, 0, location).UTC()
	batchAt := latest.BatchAt.UTC()
	if !todayStart.Before(batchAt) || !monthStart.Before(batchAt) {
		return nil, false, nil
	}
	if err := builder.advance(ctx, latest, todayStart, monthStart); err != nil {
		return nil, false, err
	}
	values := make([]plugin.OutputValue, 0, len(outputDescriptors()))
	values = append(values, liveOutputValues(builder.config, latest)...)
	values = append(values, periodOutputValues("today", builder.config, latest.BatchAt, builder.today.metrics)...)
	values = append(values, periodOutputValues("month", builder.config, latest.BatchAt, builder.month.metrics)...)
	return values, true, nil
}

func (builder *outputBuilder) advance(ctx context.Context, latest BatchMetrics, todayStart, monthStart time.Time) error {
	if !builder.hydrated {
		batches, err := builder.history.ListBatches(ctx, datalogger.RawBatchListInput{
			LoggerID: builder.config.LoggerID, TagIDs: powerTagIDs(builder.config),
			From: monthStart, To: latest.BatchAt.UTC(), IncludeNeighbors: true,
		})
		if err != nil {
			return err
		}
		metrics, err := evaluateBatches(builder.calculator, batches)
		if err != nil {
			return err
		}
		today, err := calculatePeriod(builder.calculator, metrics, todayStart, latest.BatchAt.UTC())
		if err != nil {
			return err
		}
		month, err := calculatePeriod(builder.calculator, metrics, monthStart, latest.BatchAt.UTC())
		if err != nil {
			return err
		}
		builder.today = newOutputPeriodAccumulator(builder.config, today)
		builder.month = newOutputPeriodAccumulator(builder.config, month)
		builder.last = cloneBatchMetrics(latest)
		builder.hydrated = true
		return nil
	}
	if latest.BatchAt.Before(builder.last.BatchAt) {
		return ErrUnorderedMetrics
	}
	if latest.BatchAt.Equal(builder.last.BatchAt) {
		builder.last = cloneBatchMetrics(latest)
		return nil
	}
	if !builder.today.metrics.From.Equal(todayStart) {
		builder.today = outputPeriodAccumulator{metrics: PeriodMetrics{From: todayStart, To: todayStart}}
	}
	if !builder.month.metrics.From.Equal(monthStart) {
		builder.month = outputPeriodAccumulator{metrics: PeriodMetrics{From: monthStart, To: monthStart}}
	}
	if err := builder.extendPeriod(&builder.today, latest); err != nil {
		return err
	}
	if err := builder.extendPeriod(&builder.month, latest); err != nil {
		return err
	}
	builder.last = cloneBatchMetrics(latest)
	return nil
}

func newOutputPeriodAccumulator(config Config, metrics PeriodMetrics) outputPeriodAccumulator {
	metrics.Electrical.Issues = boundedSegmentIssues(metrics.Electrical.Issues)
	metrics.Thermal.Issues = boundedSegmentIssues(metrics.Thermal.Issues)
	accumulator := outputPeriodAccumulator{metrics: metrics}
	if config.Tariff.effectiveMode() == TariffModeTag {
		if metrics.Cost.Valid {
			accumulator.tagCost = metrics.Cost.Value
		} else if metrics.Electrical.Covered > 0 {
			accumulator.costInvalid = true
			accumulator.costError = metrics.Cost.Error
		}
	}
	return accumulator
}

func (builder *outputBuilder) extendPeriod(accumulator *outputPeriodAccumulator, latest BatchMetrics) error {
	segmentFrom := accumulator.metrics.To
	if !segmentFrom.Before(latest.BatchAt) {
		return nil
	}
	segment, err := builder.calculator.Integrate([]BatchMetrics{builder.last, latest}, segmentFrom, latest.BatchAt)
	if err != nil {
		return err
	}
	accumulator.metrics.Electrical = mergeEnergyMetrics(accumulator.metrics.Electrical, segment.Electrical)
	accumulator.metrics.Thermal = mergeEnergyMetrics(accumulator.metrics.Thermal, segment.Thermal)
	accumulator.metrics.To = latest.BatchAt.UTC()
	if builder.config.Tariff.effectiveMode() == TariffModeTag && segment.Electrical.Covered > 0 {
		if segment.Cost.Valid && !accumulator.costInvalid {
			accumulator.tagCost += segment.Cost.Value
		} else {
			accumulator.costInvalid = true
			accumulator.costError = fallbackError(segment.Cost.Error, "tariff Tag coverage is incomplete")
		}
	}
	accumulator.recalculate(builder.config)
	return nil
}

func (accumulator *outputPeriodAccumulator) recalculate(config Config) {
	accumulator.metrics.COP = periodCOP(accumulator.metrics.Electrical, accumulator.metrics.Thermal)
	if config.Tariff.effectiveMode() == TariffModeFlat {
		accumulator.metrics.Cost = periodCost(accumulator.metrics.Electrical, config.Tariff.RatePerKWh)
		return
	}
	if accumulator.metrics.Electrical.Covered == 0 {
		accumulator.metrics.Cost = RatioMetric{Error: "electrical energy has no covered duration"}
	} else if accumulator.costInvalid {
		accumulator.metrics.Cost = RatioMetric{Error: fallbackError(accumulator.costError, "tariff Tag coverage is incomplete")}
	} else {
		accumulator.metrics.Cost = RatioMetric{Value: accumulator.tagCost, Valid: true}
	}
}

func mergeEnergyMetrics(current, segment EnergyMetric) EnergyMetric {
	current.KilowattHours += segment.KilowattHours
	current.Covered += segment.Covered
	current.Skipped += segment.Skipped
	current.Segments += segment.Segments
	current.SkippedSegments += segment.SkippedSegments
	current.Issues = boundedSegmentIssues(append(current.Issues, segment.Issues...))
	return current
}

func boundedSegmentIssues(issues []SegmentIssue) []SegmentIssue {
	if len(issues) <= maxEnergyOutputIssues {
		return append([]SegmentIssue(nil), issues...)
	}
	return append([]SegmentIssue(nil), issues[:maxEnergyOutputIssues]...)
}

func liveOutputValues(config Config, metrics BatchMetrics) []plugin.OutputValue {
	at := metrics.BatchAt.UTC()
	attributes := map[string]string{"timezone": config.Timezone}
	values := []plugin.OutputValue{
		demandOutputValue(OutputElectricalDemandKW, metrics.Electrical, at, attributes),
		demandOutputValue(OutputThermalOutputKW, metrics.Thermal, at, attributes),
		ratioOutputValue(OutputInstantaneousCOP, metrics.COP, "", at, at, at, attributes, nil),
		tariffOutputValue(config, metrics.Tariff, at),
	}
	return values
}

func periodOutputValues(period string, config Config, observedAt time.Time, metrics PeriodMetrics) []plugin.OutputValue {
	electricalCoverage := coveragePercent(metrics.Electrical, metrics.To.Sub(metrics.From))
	thermalCoverage := coveragePercent(metrics.Thermal, metrics.To.Sub(metrics.From))
	electricalIssues := segmentOutputIssues(metrics.Electrical.Issues)
	thermalIssues := segmentOutputIssues(metrics.Thermal.Issues)
	electricalAttributes := periodAttributes(config, metrics.Electrical)
	thermalAttributes := periodAttributes(config, metrics.Thermal)
	copAttributes := map[string]string{
		"electrical_covered_seconds": strconv.FormatFloat(metrics.Electrical.Covered.Seconds(), 'f', -1, 64),
		"electrical_skipped_seconds": strconv.FormatFloat(metrics.Electrical.Skipped.Seconds(), 'f', -1, 64),
		"thermal_covered_seconds":    strconv.FormatFloat(metrics.Thermal.Covered.Seconds(), 'f', -1, 64),
		"thermal_skipped_seconds":    strconv.FormatFloat(metrics.Thermal.Skipped.Seconds(), 'f', -1, 64),
		"timezone":                   config.Timezone,
	}
	costAttributes := map[string]string{
		"covered_seconds": strconv.FormatFloat(metrics.Electrical.Covered.Seconds(), 'f', -1, 64),
		"currency":        config.Tariff.Currency,
		"skipped_seconds": strconv.FormatFloat(metrics.Electrical.Skipped.Seconds(), 'f', -1, 64),
		"tariff_mode":     string(config.Tariff.effectiveMode()),
		"timezone":        config.Timezone,
	}
	keys := periodOutputKeys(period)
	return []plugin.OutputValue{
		energyOutputValue(keys.electricalEnergy, metrics.Electrical, electricalCoverage, observedAt, metrics.From, metrics.To, electricalAttributes, electricalIssues),
		energyOutputValue(keys.thermalEnergy, metrics.Thermal, thermalCoverage, observedAt, metrics.From, metrics.To, thermalAttributes, thermalIssues),
		failClosedRatioOutputValue(keys.cop, metrics.COP, "", observedAt, metrics.From, metrics.To, copAttributes, electricalCoverage, thermalCoverage, boundedOutputIssues(append(append([]plugin.OutputIssue(nil), electricalIssues...), thermalIssues...))),
		failClosedRatioOutputValue(keys.cost, metrics.Cost, config.Tariff.Currency, observedAt, metrics.From, metrics.To, costAttributes, electricalCoverage, electricalCoverage, electricalIssues),
		coveredRatioOutputValue(keys.coveredCost, metrics.Cost, config.Tariff.Currency, observedAt, metrics.From, metrics.To, costAttributes, electricalCoverage, electricalIssues),
		coverageOutputValue(keys.electricalCoverage, electricalCoverage, observedAt, metrics.From, metrics.To, electricalAttributes, electricalIssues),
		coverageOutputValue(keys.thermalCoverage, thermalCoverage, observedAt, metrics.From, metrics.To, thermalAttributes, thermalIssues),
	}
}

type outputKeys struct {
	electricalEnergy   plugin.OutputKey
	thermalEnergy      plugin.OutputKey
	cop                plugin.OutputKey
	cost               plugin.OutputKey
	coveredCost        plugin.OutputKey
	electricalCoverage plugin.OutputKey
	thermalCoverage    plugin.OutputKey
}

func periodOutputKeys(period string) outputKeys {
	if period == "today" {
		return outputKeys{OutputTodayElectricalEnergyKWh, OutputTodayThermalEnergyKWh, OutputTodayCOP, OutputTodayEstimatedCost, OutputTodayCoveredCost, OutputTodayElectricalCoverage, OutputTodayThermalCoverage}
	}
	return outputKeys{OutputMonthElectricalEnergyKWh, OutputMonthThermalEnergyKWh, OutputMonthCOP, OutputMonthEstimatedCost, OutputMonthCoveredCost, OutputMonthElectricalCoverage, OutputMonthThermalCoverage}
}

func demandOutputValue(key plugin.OutputKey, metric DemandMetric, at time.Time, attributes map[string]string) plugin.OutputValue {
	if metric.Valid {
		return goodOutputValue(key, "kW", metric.Kilowatts, at, at, at, attributes)
	}
	issues := metricOutputIssues(metric.Errors)
	return badOutputValue(key, "kW", metricErrorMessage(metric.Errors, "demand is unavailable"), at, at, at, attributes, nil, issues)
}

func tariffOutputValue(config Config, metric TariffMetric, at time.Time) plugin.OutputValue {
	unit := config.Tariff.Currency + "/kWh"
	attributes := map[string]string{"currency": config.Tariff.Currency, "tariff_mode": string(config.Tariff.effectiveMode()), "timezone": config.Timezone}
	if metric.Valid {
		return goodOutputValue(OutputTariffRate, unit, metric.RatePerKWh, at, at, at, attributes)
	}
	issues := metricOutputIssues(metric.Errors)
	return badOutputValue(OutputTariffRate, unit, metricErrorMessage(metric.Errors, "tariff is unavailable"), at, at, at, attributes, nil, issues)
}

func ratioOutputValue(key plugin.OutputKey, metric RatioMetric, unit string, observedAt, from, to time.Time, attributes map[string]string, issues []plugin.OutputIssue) plugin.OutputValue {
	if metric.Valid {
		return goodOutputValue(key, unit, metric.Value, observedAt, from, to, attributes)
	}
	message := fallbackError(metric.Error, "ratio is unavailable")
	issues = append(append([]plugin.OutputIssue(nil), issues...), plugin.OutputIssue{Code: "calculation_unavailable", Message: message, Source: "energy"})
	return badOutputValue(key, unit, message, observedAt, from, to, attributes, nil, boundedOutputIssues(issues))
}

func energyOutputValue(key plugin.OutputKey, metric EnergyMetric, coverage float64, observedAt, from, to time.Time, attributes map[string]string, issues []plugin.OutputIssue) plugin.OutputValue {
	if metric.Covered <= 0 {
		return badOutputValue(key, "kWh", "energy has no covered duration", observedAt, from, to, attributes, &coverage, issues)
	}
	value := baseOutputValue(key, "kWh", observedAt, from, to, attributes)
	value.Value = metric.KilowattHours
	value.CoveragePercent = floatPointer(coverage)
	value.Issues = issues
	if coverage == 100 && len(issues) == 0 {
		value.Quality = plugin.OutputQualityGood
	} else {
		value.Quality = plugin.OutputQualityPartial
	}
	return value
}

func failClosedRatioOutputValue(key plugin.OutputKey, metric RatioMetric, unit string, observedAt, from, to time.Time, attributes map[string]string, firstCoverage, secondCoverage float64, issues []plugin.OutputIssue) plugin.OutputValue {
	coverage := firstCoverage
	if secondCoverage < coverage {
		coverage = secondCoverage
	}
	if coverage < 100 || len(issues) > 0 {
		return badOutputValue(key, unit, "source coverage is incomplete", observedAt, from, to, attributes, &coverage, issues)
	}
	value := ratioOutputValue(key, metric, unit, observedAt, from, to, attributes, nil)
	value.CoveragePercent = floatPointer(coverage)
	return value
}

func coveredRatioOutputValue(key plugin.OutputKey, metric RatioMetric, unit string, observedAt, from, to time.Time, attributes map[string]string, coverage float64, issues []plugin.OutputIssue) plugin.OutputValue {
	if !metric.Valid || coverage <= 0 {
		message := fallbackError(metric.Error, "covered cost is unavailable")
		issues = append(append([]plugin.OutputIssue(nil), issues...), plugin.OutputIssue{Code: "calculation_unavailable", Message: message, Source: "energy"})
		return badOutputValue(key, unit, message, observedAt, from, to, attributes, &coverage, boundedOutputIssues(issues))
	}
	value := baseOutputValue(key, unit, observedAt, from, to, attributes)
	value.Value = metric.Value
	value.CoveragePercent = floatPointer(coverage)
	value.Issues = issues
	if coverage == 100 && len(issues) == 0 {
		value.Quality = plugin.OutputQualityGood
	} else {
		value.Quality = plugin.OutputQualityPartial
	}
	return value
}

func coverageOutputValue(key plugin.OutputKey, coverage float64, observedAt, from, to time.Time, attributes map[string]string, issues []plugin.OutputIssue) plugin.OutputValue {
	value := baseOutputValue(key, "%", observedAt, from, to, attributes)
	value.Value = coverage
	value.CoveragePercent = floatPointer(coverage)
	value.Issues = issues
	if coverage == 100 && len(issues) == 0 {
		value.Quality = plugin.OutputQualityGood
	} else {
		value.Quality = plugin.OutputQualityPartial
	}
	return value
}

func goodOutputValue(key plugin.OutputKey, unit string, value float64, observedAt, from, to time.Time, attributes map[string]string) plugin.OutputValue {
	output := baseOutputValue(key, unit, observedAt, from, to, attributes)
	output.Value = value
	output.Quality = plugin.OutputQualityGood
	return output
}

func badOutputValue(key plugin.OutputKey, unit, message string, observedAt, from, to time.Time, attributes map[string]string, coverage *float64, issues []plugin.OutputIssue) plugin.OutputValue {
	output := baseOutputValue(key, unit, observedAt, from, to, attributes)
	output.Quality = plugin.OutputQualityBad
	output.Error = truncateOutputText(message)
	output.CoveragePercent = floatPointerValue(coverage)
	output.Issues = issues
	return output
}

func baseOutputValue(key plugin.OutputKey, unit string, observedAt, from, to time.Time, attributes map[string]string) plugin.OutputValue {
	return plugin.OutputValue{
		Key: key, SchemaVersion: OutputSchemaVersionV1, DataType: plugin.OutputDataTypeFloat64, Unit: unit,
		ObservedAt: observedAt.UTC(), PeriodStart: from.UTC(), PeriodEnd: to.UTC(),
		Attributes: cloneStringMap(attributes),
	}
}

func metricOutputIssues(errors []MetricError) []plugin.OutputIssue {
	issues := make([]plugin.OutputIssue, 0, len(errors))
	for _, metricError := range errors {
		source := ""
		if metricError.TagID != uuid.Nil {
			source = "tag:" + metricError.TagID.String()
		}
		issues = append(issues, plugin.OutputIssue{Code: string(metricError.Code), Message: truncateOutputText(metricError.Message), Source: source})
	}
	return boundedOutputIssues(issues)
}

func segmentOutputIssues(values []SegmentIssue) []plugin.OutputIssue {
	issues := make([]plugin.OutputIssue, 0, len(values))
	for _, value := range values {
		from, to := value.From.UTC(), value.To.UTC()
		message := strings.ReplaceAll(string(value.Code), "_", " ")
		if len(value.Errors) > 0 {
			message = metricErrorMessage(value.Errors, message)
		}
		issues = append(issues, plugin.OutputIssue{Code: string(value.Code), Message: truncateOutputText(message), Source: "data_logger", PeriodStart: &from, PeriodEnd: &to})
	}
	return boundedOutputIssues(issues)
}

func boundedOutputIssues(issues []plugin.OutputIssue) []plugin.OutputIssue {
	if len(issues) <= maxEnergyOutputIssues {
		return issues
	}
	bounded := append([]plugin.OutputIssue(nil), issues[:maxEnergyOutputIssues-1]...)
	bounded = append(bounded, plugin.OutputIssue{Code: "issues_truncated", Message: strconv.Itoa(len(issues)-len(bounded)) + " additional issues were omitted", Source: "energy"})
	return bounded
}

func metricErrorMessage(values []MetricError, fallback string) string {
	messages := make([]string, 0, len(values))
	for _, value := range values {
		if message := strings.TrimSpace(value.Message); message != "" {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return fallback
	}
	return strings.Join(messages, "; ")
}

func periodAttributes(config Config, metric EnergyMetric) map[string]string {
	return map[string]string{
		"covered_seconds":  strconv.FormatFloat(metric.Covered.Seconds(), 'f', -1, 64),
		"issue_count":      strconv.Itoa(len(metric.Issues)),
		"segments":         strconv.Itoa(metric.Segments),
		"skipped_seconds":  strconv.FormatFloat(metric.Skipped.Seconds(), 'f', -1, 64),
		"skipped_segments": strconv.Itoa(metric.SkippedSegments),
		"timezone":         config.Timezone,
	}
}

func coveragePercent(metric EnergyMetric, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	coverage := float64(metric.Covered) / float64(duration) * 100
	if coverage < 0 {
		return 0
	}
	if coverage > 100 {
		return 100
	}
	return coverage
}

func fallbackError(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return truncateOutputText(value)
	}
	return truncateOutputText(fallback)
}

func truncateOutputText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxEnergyOutputText {
		return value
	}
	limit := maxEnergyOutputText - 3
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit] + "..."
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func floatPointer(value float64) *float64 { return &value }

func floatPointerValue(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return floatPointer(*value)
}
