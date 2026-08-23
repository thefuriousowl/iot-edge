package energy

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestCalculatorEvaluatesSynchronizedDemandAndInstantaneousCOP(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	electricalW, electricalMW, thermalKW := uuid.New(), uuid.New(), uuid.New()
	calculator := newTestCalculator(t, Config{
		LoggerID: loggerID,
		ElectricalPowerTags: []PowerTag{
			{TagID: electricalW, Unit: PowerUnitW},
			{TagID: electricalMW, Unit: PowerUnitMW},
		},
		ThermalPowerTags: []PowerTag{{TagID: thermalKW, Unit: PowerUnitKW}},
		Timezone:         "UTC", MaxGapSeconds: 3600,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4.5},
	})
	at := time.Date(2026, time.August, 23, 1, 2, 3, 0, time.FixedZone("test", 7*60*60))
	metrics, err := calculator.Evaluate(datalogger.RawBatch{
		LoggerID: loggerID,
		BatchAt:  at,
		Samples: []datalogger.RawSample{
			goodEnergySample(electricalW, at, "uint16", uint16(1000)),
			goodEnergySample(electricalMW, at, "float32", float32(0.002)),
			goodEnergySample(thermalKW, at, "float64", float64(12)),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !metrics.BatchAt.Equal(at.UTC()) || !metrics.Electrical.Valid || !closeEnergy(metrics.Electrical.Kilowatts, 3) {
		t.Errorf("electrical metrics = %#v", metrics)
	}
	if !metrics.Thermal.Valid || metrics.Thermal.Kilowatts != 12 {
		t.Errorf("thermal metrics = %#v", metrics.Thermal)
	}
	if !metrics.COP.Valid || !closeEnergy(metrics.COP.Value, 4) {
		t.Errorf("COP = %#v", metrics.COP)
	}
}

func TestCalculatorEvaluatesAndIntegratesSynchronizedTariffTag(t *testing.T) {
	t.Parallel()
	loggerID, powerID, tariffID := uuid.New(), uuid.New(), uuid.New()
	calculator := newTestCalculator(t, Config{
		LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}},
		Timezone: "UTC", MaxGapSeconds: 3600, Tariff: FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID},
	})
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	rates := []float64{2, 4, 4}
	metrics := make([]BatchMetrics, 0, len(rates))
	for index, rate := range rates {
		at := start.Add(time.Duration(index) * time.Hour)
		value, err := calculator.Evaluate(datalogger.RawBatch{LoggerID: loggerID, BatchAt: at, Samples: []datalogger.RawSample{
			goodEnergySample(powerID, at, "float64", float64(10)), goodEnergySample(tariffID, at, "float64", rate),
		}})
		if err != nil {
			t.Fatalf("Evaluate(%d) error = %v", index, err)
		}
		if !value.Tariff.Valid || value.Tariff.RatePerKWh != rate {
			t.Fatalf("tariff metrics %d = %#v", index, value.Tariff)
		}
		metrics = append(metrics, value)
	}
	period, err := calculator.Integrate(metrics, start, start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if !period.Cost.Valid || period.Cost.Value != 60 {
		t.Errorf("piecewise tariff cost = %#v; want 60", period.Cost)
	}

	bad, err := calculator.Evaluate(datalogger.RawBatch{LoggerID: loggerID, BatchAt: start.Add(3 * time.Hour), Samples: []datalogger.RawSample{
		goodEnergySample(powerID, start, "float64", float64(10)), {TagID: tariffID, ObservedAt: start, DataType: "float64", Quality: datalogger.RawQualityBad, Error: "tariff unavailable"},
	}})
	if err != nil {
		t.Fatalf("Evaluate(bad tariff) error = %v", err)
	}
	if bad.Tariff.Valid || len(bad.Tariff.Errors) != 1 || bad.Tariff.Errors[0].Message != "tariff unavailable" || !bad.Electrical.Valid {
		t.Errorf("bad tariff metrics = %#v, electrical = %#v", bad.Tariff, bad.Electrical)
	}
	metrics[1].Tariff = bad.Tariff
	period, err = calculator.Integrate(metrics, start, start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Integrate(incomplete tariff) error = %v", err)
	}
	if period.Cost.Valid || period.Cost.Error != "tariff Tag coverage is incomplete" {
		t.Errorf("incomplete tariff cost = %#v", period.Cost)
	}
}

func TestCalculatorFailsClosedPerDemandGroupAndPreservesSourceErrors(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	electricalA, electricalB, thermal := uuid.New(), uuid.New(), uuid.New()
	calculator := newTestCalculator(t, Config{
		LoggerID:            loggerID,
		ElectricalPowerTags: []PowerTag{{TagID: electricalA, Unit: PowerUnitKW}, {TagID: electricalB, Unit: PowerUnitKW}},
		ThermalPowerTags:    []PowerTag{{TagID: thermal, Unit: PowerUnitKW}},
		Timezone:            "UTC", MaxGapSeconds: 60,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4},
	})
	at := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	metrics, err := calculator.Evaluate(datalogger.RawBatch{
		LoggerID: loggerID,
		BatchAt:  at,
		Samples: []datalogger.RawSample{
			goodEnergySample(electricalA, at, "float64", float64(5)),
			{TagID: electricalB, ObservedAt: at, DataType: "float64", Quality: datalogger.RawQualityBad, Error: "  Modbus 0x02 Illegal Data Address  "},
			goodEnergySample(thermal, at, "float64", float64(15)),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if metrics.Electrical.Valid || metrics.Electrical.Kilowatts != 0 || len(metrics.Electrical.Errors) != 1 {
		t.Fatalf("electrical metrics = %#v", metrics.Electrical)
	}
	sourceError := metrics.Electrical.Errors[0]
	if sourceError.Code != MetricErrorSourceBad || sourceError.TagID != electricalB || sourceError.Message != "Modbus 0x02 Illegal Data Address" {
		t.Errorf("source error = %#v", sourceError)
	}
	if !metrics.Thermal.Valid || metrics.Thermal.Kilowatts != 15 {
		t.Errorf("thermal metrics = %#v", metrics.Thermal)
	}
	if metrics.COP.Valid || metrics.COP.Error == "" {
		t.Errorf("COP = %#v", metrics.COP)
	}

	missing, err := calculator.Evaluate(datalogger.RawBatch{LoggerID: loggerID, BatchAt: at.Add(time.Minute), Samples: []datalogger.RawSample{
		goodEnergySample(electricalA, at, "float64", float64(5)),
		goodEnergySample(electricalB, at, "float64", math.Inf(1)),
	}})
	if err != nil {
		t.Fatalf("Evaluate(missing) error = %v", err)
	}
	if missing.Electrical.Valid || len(missing.Electrical.Errors) != 1 || missing.Electrical.Errors[0].Code != MetricErrorInvalidValue {
		t.Errorf("invalid electrical = %#v", missing.Electrical)
	}
	if missing.Thermal.Valid || len(missing.Thermal.Errors) != 1 || missing.Thermal.Errors[0].Code != MetricErrorMissingSample {
		t.Errorf("missing thermal = %#v", missing.Thermal)
	}
}

func TestCalculatorIntegratesClippedTrapezoidsAndPeriodMetrics(t *testing.T) {
	t.Parallel()
	calculator := newTestCalculator(t, testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 7200))
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	metrics := []BatchMetrics{
		validBatchMetrics(start, 0, 0),
		validBatchMetrics(start.Add(time.Hour), 10, 30),
		validBatchMetrics(start.Add(2*time.Hour), 20, 60),
	}
	result, err := calculator.Integrate(metrics, start.Add(30*time.Minute), start.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if result.Electrical.KilowattHours != 10 || result.Electrical.Covered != time.Hour || result.Electrical.Skipped != 0 || result.Electrical.Segments != 2 {
		t.Errorf("electrical energy = %#v", result.Electrical)
	}
	if result.Thermal.KilowattHours != 30 || result.Thermal.Covered != time.Hour || result.Thermal.Segments != 2 {
		t.Errorf("thermal energy = %#v", result.Thermal)
	}
	if !result.COP.Valid || result.COP.Value != 3 {
		t.Errorf("period COP = %#v", result.COP)
	}
	if !result.Cost.Valid || result.Cost.Value != 40 {
		t.Errorf("cost = %#v", result.Cost)
	}
	if metrics[0].Electrical.Errors != nil {
		t.Errorf("Integrate() mutated caller metrics = %#v", metrics[0])
	}
}

func TestCalculatorSkipsStaleAndBadSegmentsIndependently(t *testing.T) {
	t.Parallel()
	calculator := newTestCalculator(t, testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 3600))
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	sourceError := MetricError{Code: MetricErrorSourceBad, TagID: uuid.New(), At: start.Add(time.Hour), Message: "source timeout"}
	metrics := []BatchMetrics{
		validBatchMetrics(start, 5, 15),
		{
			BatchAt:    start.Add(time.Hour),
			Electrical: DemandMetric{Kilowatts: 5, Valid: true},
			Thermal:    DemandMetric{Errors: []MetricError{sourceError}},
		},
		validBatchMetrics(start.Add(3*time.Hour), 5, 15),
	}
	result, err := calculator.Integrate(metrics, start, start.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if result.Electrical.KilowattHours != 5 || result.Electrical.Covered != time.Hour || result.Electrical.Skipped != 2*time.Hour || result.Electrical.SkippedSegments != 1 {
		t.Errorf("electrical energy = %#v", result.Electrical)
	}
	if result.Thermal.KilowattHours != 0 || result.Thermal.Covered != 0 || result.Thermal.Skipped != 3*time.Hour || result.Thermal.SkippedSegments != 2 {
		t.Errorf("thermal energy = %#v", result.Thermal)
	}
	if len(result.Electrical.Issues) != 1 || result.Electrical.Issues[0].Code != MetricErrorStaleGap {
		t.Errorf("electrical issues = %#v", result.Electrical.Issues)
	}
	if len(result.Thermal.Issues) != 2 || len(result.Thermal.Issues[0].Errors) != 1 || result.Thermal.Issues[0].Errors[0].Message != "source timeout" || result.Thermal.Issues[1].Code != MetricErrorStaleGap {
		t.Errorf("thermal issues = %#v", result.Thermal.Issues)
	}
	if result.COP.Valid || result.Cost.Value != 20 || !result.Cost.Valid {
		t.Errorf("period metrics = COP %#v, cost %#v", result.COP, result.Cost)
	}
}

func TestCalculatorReportsBoundaryCoverageAndRejectsAmbiguousInputs(t *testing.T) {
	t.Parallel()
	calculator := newTestCalculator(t, testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 7200))
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	metrics := []BatchMetrics{validBatchMetrics(start.Add(time.Hour), 2, 4), validBatchMetrics(start.Add(2*time.Hour), 2, 4)}
	result, err := calculator.Integrate(metrics, start, start.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if result.Electrical.Covered != time.Hour || result.Electrical.Skipped != 2*time.Hour || len(result.Electrical.Issues) != 2 {
		t.Errorf("boundary coverage = %#v", result.Electrical)
	}
	if result.Electrical.Issues[0].Code != MetricErrorNoCoverage || result.Electrical.Issues[1].Code != MetricErrorNoCoverage {
		t.Errorf("boundary issues = %#v", result.Electrical.Issues)
	}
	if _, err := calculator.Integrate(metrics, start, start); !errors.Is(err, ErrInvalidEnergyRange) {
		t.Errorf("Integrate(empty range) error = %v", err)
	}
	if _, err := calculator.Integrate([]BatchMetrics{metrics[1], metrics[0]}, start, start.Add(3*time.Hour)); !errors.Is(err, ErrUnorderedMetrics) {
		t.Errorf("Integrate(unordered) error = %v", err)
	}
	if _, err := calculator.Integrate([]BatchMetrics{metrics[0], metrics[0]}, start, start.Add(3*time.Hour)); !errors.Is(err, ErrUnorderedMetrics) {
		t.Errorf("Integrate(duplicate) error = %v", err)
	}
	if _, err := calculator.Evaluate(datalogger.RawBatch{LoggerID: uuid.New(), BatchAt: start}); !errors.Is(err, ErrInvalidEnergyBatch) {
		t.Errorf("Evaluate(wrong Logger) error = %v", err)
	}
}

func TestCalculatorRejectsNonPositiveElectricalCOPDenominators(t *testing.T) {
	t.Parallel()
	calculator := newTestCalculator(t, testEnergyConfig(uuid.New(), uuid.New(), uuid.New(), 7200))
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	for _, electrical := range []float64{0, -5} {
		metrics := validBatchMetrics(start, electrical, 10)
		metrics.COP = instantaneousCOP(metrics.Electrical, metrics.Thermal)
		if metrics.COP.Valid || metrics.COP.Error == "" {
			t.Errorf("instantaneous COP(%g) = %#v", electrical, metrics.COP)
		}
		period, err := calculator.Integrate([]BatchMetrics{metrics, validBatchMetrics(start.Add(time.Hour), electrical, 10)}, start, start.Add(time.Hour))
		if err != nil {
			t.Fatalf("Integrate(%g) error = %v", electrical, err)
		}
		if period.COP.Valid || period.COP.Error == "" {
			t.Errorf("period COP(%g) = %#v", electrical, period.COP)
		}
	}
}

func newTestCalculator(t *testing.T, config Config) *Calculator {
	t.Helper()
	calculator, err := NewCalculator(config)
	if err != nil {
		t.Fatalf("NewCalculator() error = %v", err)
	}
	return calculator
}

func testEnergyConfig(loggerID, electricalID, thermalID uuid.UUID, maxGapSeconds int64) Config {
	return Config{
		LoggerID:            loggerID,
		ElectricalPowerTags: []PowerTag{{TagID: electricalID, Unit: PowerUnitKW}},
		ThermalPowerTags:    []PowerTag{{TagID: thermalID, Unit: PowerUnitKW}},
		Timezone:            "UTC", MaxGapSeconds: maxGapSeconds,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4},
	}
}

func goodEnergySample(tagID uuid.UUID, at time.Time, dataType string, value any) datalogger.RawSample {
	return datalogger.RawSample{TagID: tagID, ObservedAt: at, DataType: dataType, Value: value, Quality: datalogger.RawQualityGood}
}

func validBatchMetrics(at time.Time, electrical, thermal float64) BatchMetrics {
	return BatchMetrics{
		BatchAt:    at,
		Electrical: DemandMetric{Kilowatts: electrical, Valid: true},
		Thermal:    DemandMetric{Kilowatts: thermal, Valid: true},
		COP:        instantaneousCOP(DemandMetric{Kilowatts: electrical, Valid: true}, DemandMetric{Kilowatts: thermal, Valid: true}),
	}
}

func closeEnergy(left, right float64) bool {
	return math.Abs(left-right) < 1e-6
}
