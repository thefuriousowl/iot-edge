package compressedair

import (
	"math"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

func TestCalculateFlowConsumptionEfficiencyAndPressure(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := CalculationInput{PowerKW: values(base, 100, 100, 100), Mode: ConsumptionFlow, FlowNm3PerHour: values(base, 500, 500, 500), PressureBar: values(base, 7, 6, 7), OperatingState: values(base, 1, 1, 1), ProductionState: values(base, 0, 1, 1), Currency: "THB", RatePerKWh: 4, Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour}
	result, err := Calculate(input)
	if err != nil || result.EnergyKWh != 200 || result.VolumeNm3 != 1000 || !result.SEC.Valid || result.SEC.Value != .2 || result.Cost != 800 || !result.CostPerNm3.Valid || result.CostPerNm3.Value != .8 || !result.RuntimeRatio.Valid || result.RuntimeRatio.Value != 1 || !result.LoadRatio.Valid || result.LoadRatio.Value != .75 || !result.Pressure.Valid || result.Pressure.AverageBar != 6.5 || result.Pressure.DropBar != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCalculateCounterDeltaAndResetCoverage(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := CalculationInput{PowerKW: values(base, 50, 50, 50), Mode: ConsumptionCounter, CounterNm3: values(base, 1000, 1100, 1250), Currency: "THB", RatePerKWh: 4, Window: analytics.Window{From: base.Add(30 * time.Minute), To: base.Add(90 * time.Minute)}, MaxGap: 2 * time.Hour}
	result, err := Calculate(input)
	if err != nil || result.EnergyKWh != 50 || result.VolumeNm3 != 125 || !result.SEC.Valid || result.SEC.Value != .4 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	input.CounterNm3[2].Value = 10
	reset, err := Calculate(input)
	if err != nil || reset.CoveragePercent != 50 || reset.SEC.Valid || reset.VolumeNm3 != 50 {
		t.Fatalf("reset=%#v err=%v", reset, err)
	}
}

func TestCompressedAirPreviousPeriodComparison(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := CalculationInput{PowerKW: values(base, 50, 50, 100), Mode: ConsumptionFlow, FlowNm3PerHour: values(base, 500, 500, 500), Currency: "THB", RatePerKWh: 4, Window: analytics.Window{From: base.Add(time.Hour), To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour}
	result, err := CompareCalculation(input)
	if err != nil || result.EnergyDeltaKWh != 25 || result.VolumeDeltaNm3 != 0 || math.Abs(result.SECDelta-.05) > 1e-12 || result.CostDelta != 100 {
		t.Fatalf("comparison=%#v err=%v", result, err)
	}
}

func TestCompressedAirPhysicalAndStateBoundariesFailClosed(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := CalculationInput{PowerKW: values(base, 10, 10), Mode: ConsumptionFlow, FlowNm3PerHour: values(base, 100, -1), Currency: "THB", Window: analytics.Window{From: base, To: base.Add(time.Hour)}, MaxGap: 2 * time.Hour}
	if _, err := Calculate(input); err == nil {
		t.Fatal("negative flow must fail closed")
	}
	input.FlowNm3PerHour = values(base, 100, 100)
	input.OperatingState = values(base, 2, 0)
	result, err := Calculate(input)
	if err != nil || result.RuntimeRatio.Valid || result.RuntimeRatio.Error == "" {
		t.Fatalf("state result=%#v err=%v", result, err)
	}
	input.OperatingState = values(base, 0, 1)
	input.ProductionState = values(base, 1, 1)
	result, err = Calculate(input)
	if err != nil || result.LoadRatio.Valid || result.LoadRatio.Error == "" {
		t.Fatalf("load result=%#v err=%v", result, err)
	}
}

func values(base time.Time, data ...float64) []analytics.Sample {
	result := make([]analytics.Sample, len(data))
	for i, value := range data {
		result[i] = analytics.Sample{At: base.Add(time.Duration(i) * time.Hour), Value: value, Quality: analytics.QualityGood}
	}
	return result
}
