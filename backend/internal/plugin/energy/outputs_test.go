package energy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestEnergyOutputDescriptorsAreStableValidatedAndManifestOwned(t *testing.T) {
	t.Parallel()

	descriptors := outputDescriptors()
	if len(descriptors) != 16 {
		t.Fatalf("output descriptor count = %d, want 16", len(descriptors))
	}
	if err := plugin.ValidateOutputDescriptors(descriptors); err != nil {
		t.Fatalf("ValidateOutputDescriptors() error = %v", err)
	}
	wantKeys := []plugin.OutputKey{
		OutputElectricalDemandKW, OutputThermalOutputKW, OutputInstantaneousCOP, OutputTariffRate,
		OutputTodayElectricalEnergyKWh, OutputTodayThermalEnergyKWh, OutputTodayCOP, OutputTodayEstimatedCost, OutputTodayElectricalCoverage, OutputTodayThermalCoverage,
		OutputMonthElectricalEnergyKWh, OutputMonthThermalEnergyKWh, OutputMonthCOP, OutputMonthEstimatedCost, OutputMonthElectricalCoverage, OutputMonthThermalCoverage,
	}
	for index, want := range wantKeys {
		if descriptors[index].Key != want || descriptors[index].SchemaVersion != 1 || descriptors[index].DataType != plugin.OutputDataTypeFloat64 {
			t.Errorf("descriptor[%d] = %#v, want key %q schema 1 float64", index, descriptors[index], want)
		}
	}
	if !descriptors[3].DynamicUnit || !descriptors[7].DynamicUnit || !descriptors[13].DynamicUnit {
		t.Error("tariff and Cost descriptors must use config-driven units")
	}
	manifest := (&Definition{}).Manifest()
	manifest.Outputs[0].Name = "Changed"
	if (&Definition{}).Manifest().Outputs[0].Name != "Electrical demand" {
		t.Error("Manifest() output descriptors alias caller state")
	}
}

func TestEnergyOutputBuilderPublishesLiveTodayAndMonthWithExactProvenance(t *testing.T) {
	t.Parallel()

	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 40*24*60*60)
	config.Timezone = "Asia/Bangkok"
	location, _ := time.LoadLocation(config.Timezone)
	monthStart := time.Date(2026, time.August, 1, 0, 0, 0, 0, location)
	todayStart := time.Date(2026, time.August, 23, 0, 0, 0, 0, location)
	at := todayStart.Add(12 * time.Hour)
	history := &energyHistoryReader{batches: []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, monthStart.UTC(), 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, todayStart.UTC(), 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, at.UTC(), 10, 30),
	}}
	calculator, _ := NewCalculator(config)
	builder, err := newOutputBuilder(config, calculator, history)
	if err != nil {
		t.Fatalf("newOutputBuilder() error = %v", err)
	}
	latest, _ := calculator.Evaluate(history.batches[len(history.batches)-1])
	values, ready, err := builder.Build(context.Background(), latest)
	if err != nil || !ready {
		t.Fatalf("Build() = ready %t, error %v", ready, err)
	}
	if history.calls != 1 || !history.input.From.Equal(monthStart.UTC()) || !history.input.To.Equal(at.UTC()) || !history.input.IncludeNeighbors {
		t.Fatalf("history input/calls = %#v / %d", history.input, history.calls)
	}
	batch, err := plugin.NormalizeOutputBatch(plugin.OutputBatch{InstanceID: uuid.New(), Sequence: 1, PublishedAt: at.Add(time.Second), Values: values}, outputDescriptors())
	if err != nil {
		t.Fatalf("NormalizeOutputBatch() error = %v", err)
	}
	byKey := outputValuesByKey(batch.Values)
	assertEnergyOutput(t, byKey[OutputElectricalDemandKW], plugin.OutputQualityGood, 10, "kW", at.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputThermalOutputKW], plugin.OutputQualityGood, 30, "kW", at.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputInstantaneousCOP], plugin.OutputQualityGood, 3, "", at.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTariffRate], plugin.OutputQualityGood, 4, "THB/kWh", at.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTodayElectricalEnergyKWh], plugin.OutputQualityGood, 120, "kWh", todayStart.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTodayThermalEnergyKWh], plugin.OutputQualityGood, 360, "kWh", todayStart.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTodayCOP], plugin.OutputQualityGood, 3, "", todayStart.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTodayEstimatedCost], plugin.OutputQualityGood, 480, "THB", todayStart.UTC(), at.UTC())
	assertEnergyOutput(t, byKey[OutputTodayElectricalCoverage], plugin.OutputQualityGood, 100, "%", todayStart.UTC(), at.UTC())
	monthHours := at.Sub(monthStart).Hours()
	assertEnergyOutput(t, byKey[OutputMonthElectricalEnergyKWh], plugin.OutputQualityGood, monthHours*10, "kWh", monthStart.UTC(), at.UTC())
	if byKey[OutputTodayCOP].CoveragePercent == nil || *byKey[OutputTodayCOP].CoveragePercent != 100 || byKey[OutputTodayCOP].Attributes["electrical_covered_seconds"] != "43200" {
		t.Errorf("Today COP provenance = %#v", byKey[OutputTodayCOP])
	}
	if byKey[OutputTodayEstimatedCost].Attributes["currency"] != "THB" || byKey[OutputTodayEstimatedCost].Attributes["timezone"] != "Asia/Bangkok" {
		t.Errorf("Today Cost attributes = %#v", byKey[OutputTodayEstimatedCost].Attributes)
	}

	nextAt := at.Add(time.Minute)
	next, _ := calculator.Evaluate(energyRawBatch(loggerID, electricalID, thermalID, nextAt.UTC(), 20, 60))
	nextValues, ready, err := builder.Build(context.Background(), next)
	if err != nil || !ready {
		t.Fatalf("Build(next) = ready %t, error %v", ready, err)
	}
	if history.calls != 1 {
		t.Errorf("history was re-read within same month: %d calls", history.calls)
	}
	nextByKey := outputValuesByKey(nextValues)
	if !closeEnergy(nextByKey[OutputTodayElectricalEnergyKWh].Value.(float64), 120.25) || !closeEnergy(nextByKey[OutputTodayThermalEnergyKWh].Value.(float64), 360.75) || !closeEnergy(nextByKey[OutputTodayEstimatedCost].Value.(float64), 481) || !closeEnergy(nextByKey[OutputTodayCOP].Value.(float64), 3) {
		t.Errorf("incremental Today outputs = %#v", nextByKey)
	}
}

func TestEnergyOutputBuilderFailsClosedOnCoverageAndBoundsIssues(t *testing.T) {
	t.Parallel()

	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 60)
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	at := from.Add(2 * time.Hour)
	history := &energyHistoryReader{batches: []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, from, 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, at, 10, 30),
	}}
	calculator, _ := NewCalculator(config)
	builder, _ := newOutputBuilder(config, calculator, history)
	latest, _ := calculator.Evaluate(history.batches[1])
	values, ready, err := builder.Build(context.Background(), latest)
	if err != nil || !ready {
		t.Fatalf("Build() = ready %t, error %v", ready, err)
	}
	batch, err := plugin.NormalizeOutputBatch(plugin.OutputBatch{InstanceID: uuid.New(), Sequence: 1, PublishedAt: at.Add(time.Second), Values: values}, outputDescriptors())
	if err != nil {
		t.Fatalf("NormalizeOutputBatch() error = %v", err)
	}
	byKey := outputValuesByKey(batch.Values)
	for _, key := range []plugin.OutputKey{OutputTodayElectricalEnergyKWh, OutputTodayThermalEnergyKWh, OutputTodayCOP, OutputTodayEstimatedCost, OutputMonthElectricalEnergyKWh, OutputMonthThermalEnergyKWh, OutputMonthCOP, OutputMonthEstimatedCost} {
		if byKey[key].Quality != plugin.OutputQualityBad || byKey[key].Value != nil || byKey[key].Error == "" || byKey[key].CoveragePercent == nil || *byKey[key].CoveragePercent != 0 || len(byKey[key].Issues) == 0 {
			t.Errorf("fail-closed output %s = %#v", key, byKey[key])
		}
	}
	for _, key := range []plugin.OutputKey{OutputTodayElectricalCoverage, OutputTodayThermalCoverage, OutputMonthElectricalCoverage, OutputMonthThermalCoverage} {
		if byKey[key].Quality != plugin.OutputQualityPartial || byKey[key].Value != 0.0 || len(byKey[key].Issues) == 0 {
			t.Errorf("coverage output %s = %#v", key, byKey[key])
		}
	}
	issue := byKey[OutputTodayElectricalEnergyKWh].Issues[0]
	if issue.PeriodStart == nil || issue.PeriodEnd == nil || issue.PeriodStart.Before(from) || issue.PeriodEnd.After(at) {
		t.Errorf("issue provenance = %#v", issue)
	}
}

func TestEnergyOutputBuilderPublishesSynchronizedTagTariffAndPeriodCost(t *testing.T) {
	t.Parallel()

	loggerID, electricalID, thermalID, tariffID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 2*60*60)
	config.Tariff = FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID}
	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	at := from.Add(time.Hour)
	first := energyRawBatch(loggerID, electricalID, thermalID, from, 10, 30)
	first.Samples = append(first.Samples, goodEnergySample(tariffID, from, "float64", 2.0))
	last := energyRawBatch(loggerID, electricalID, thermalID, at, 10, 30)
	last.Samples = append(last.Samples, goodEnergySample(tariffID, at, "float64", 5.0))
	history := &energyHistoryReader{batches: []datalogger.RawBatch{first, last}}
	calculator, _ := NewCalculator(config)
	builder, _ := newOutputBuilder(config, calculator, history)
	latest, _ := calculator.Evaluate(last)
	values, ready, err := builder.Build(context.Background(), latest)
	if err != nil || !ready {
		t.Fatalf("Build() = ready %t, error %v", ready, err)
	}
	byKey := outputValuesByKey(values)
	assertEnergyOutput(t, byKey[OutputTariffRate], plugin.OutputQualityGood, 5, "THB/kWh", at, at)
	assertEnergyOutput(t, byKey[OutputTodayEstimatedCost], plugin.OutputQualityGood, 20, "THB", from, at)
	if byKey[OutputTodayEstimatedCost].Attributes["tariff_mode"] != "tag" {
		t.Errorf("Tag tariff Cost attributes = %#v", byKey[OutputTodayEstimatedCost].Attributes)
	}
}

func TestEnergyOutputBuilderSkipsEmptyCalendarWindowAndRollsBoundedAccumulatorsAtMonthChange(t *testing.T) {
	t.Parallel()

	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	config := testEnergyConfig(loggerID, electricalID, thermalID, 40*24*60*60)
	calculator, _ := NewCalculator(config)
	history := &energyHistoryReader{}
	builder, _ := newOutputBuilder(config, calculator, history)
	midnight := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	midnightMetrics, _ := calculator.Evaluate(energyRawBatch(loggerID, electricalID, thermalID, midnight, 10, 30))
	if values, ready, err := builder.Build(context.Background(), midnightMetrics); err != nil || ready || values != nil || history.calls != 0 {
		t.Fatalf("Build(midnight) = %#v, ready %t, error %v, calls %d", values, ready, err, history.calls)
	}

	firstAt := time.Date(2026, time.August, 31, 23, 59, 0, 0, time.UTC)
	history.batches = []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC), 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, firstAt, 10, 30),
	}
	first, _ := calculator.Evaluate(history.batches[1])
	if _, ready, err := builder.Build(context.Background(), first); err != nil || !ready {
		t.Fatalf("Build(August) = ready %t, error %v", ready, err)
	}
	secondAt := midnight.Add(time.Minute)
	history.batches = []datalogger.RawBatch{
		energyRawBatch(loggerID, electricalID, thermalID, midnight, 10, 30),
		energyRawBatch(loggerID, electricalID, thermalID, secondAt, 10, 30),
	}
	second, _ := calculator.Evaluate(history.batches[1])
	values, ready, err := builder.Build(context.Background(), second)
	if err != nil || !ready {
		t.Fatalf("Build(September) = ready %t, error %v", ready, err)
	}
	if history.calls != 1 {
		t.Errorf("history calls after month rollover = %d, want one startup hydration", history.calls)
	}
	monthOutput := outputValuesByKey(values)[OutputMonthElectricalEnergyKWh]
	if !monthOutput.PeriodStart.Equal(midnight) || !monthOutput.PeriodEnd.Equal(secondAt) || !closeEnergy(monthOutput.Value.(float64), 10.0/60) {
		t.Errorf("rolled month output = %#v", monthOutput)
	}
}

func TestEnergyOutputBuilderPropagatesHistoryFailureAndValidatesDependencies(t *testing.T) {
	t.Parallel()

	config := testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 60)
	calculator, _ := NewCalculator(config)
	var nilHistory *energyHistoryReader
	if _, err := newOutputBuilder(config, calculator, nilHistory); !errors.Is(err, ErrHistoryFeedUnavailable) {
		t.Errorf("newOutputBuilder(nil history) error = %v", err)
	}
	historyError := errors.New("history failed")
	history := &energyHistoryReader{err: historyError}
	builder, _ := newOutputBuilder(config, calculator, history)
	at := time.Date(2026, time.August, 23, 1, 0, 0, 0, time.UTC)
	latest, _ := calculator.Evaluate(energyRawBatch(config.LoggerID, config.ElectricalPowerTags[0].TagID, config.ThermalPowerTags[0].TagID, at, 10, 30))
	if _, _, err := builder.Build(context.Background(), latest); !errors.Is(err, historyError) {
		t.Errorf("Build(history failure) error = %v", err)
	}
}

func TestEnergyOutputIssuesAndErrorsStayWithinGenericContractBounds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 23, 1, 0, 0, 0, time.UTC)
	errors := make([]MetricError, 100)
	for index := range errors {
		errors[index] = MetricError{Code: MetricErrorSourceBad, TagID: uuid.New(), At: at, Message: strings.Repeat("source unavailable ", 4)}
	}
	value := demandOutputValue(OutputElectricalDemandKW, DemandMetric{Errors: errors}, at, map[string]string{"timezone": "UTC"})
	if len(value.Issues) != maxEnergyOutputIssues || len(value.Error) > maxEnergyOutputText {
		t.Fatalf("bounded output = error bytes %d, issues %d", len(value.Error), len(value.Issues))
	}
	if _, err := plugin.NormalizeOutputBatch(plugin.OutputBatch{InstanceID: uuid.New(), Sequence: 1, PublishedAt: at.Add(time.Second), Values: []plugin.OutputValue{value}}, []plugin.OutputDescriptor{outputDescriptors()[0]}); err != nil {
		t.Fatalf("NormalizeOutputBatch() error = %v", err)
	}
}

func outputValuesByKey(values []plugin.OutputValue) map[plugin.OutputKey]plugin.OutputValue {
	result := make(map[plugin.OutputKey]plugin.OutputValue, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}

func assertEnergyOutput(t *testing.T, output plugin.OutputValue, quality plugin.OutputQuality, value float64, unit string, from, to time.Time) {
	t.Helper()
	actual, valid := output.Value.(float64)
	if output.Quality != quality || !valid || !closeEnergy(actual, value) || output.Unit != unit || !output.PeriodStart.Equal(from) || !output.PeriodEnd.Equal(to) || !output.ObservedAt.Equal(to) {
		t.Errorf("output %s = %#v; want quality %s value %v %s period %s..%s", output.Key, output, quality, value, unit, from, to)
	}
}
