package utility_test

import (
	"math"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
	"github.com/thefuriousowl/iot-edge/internal/utility/compressedair"
	"github.com/thefuriousowl/iot-edge/internal/utility/electrical"
	"github.com/thefuriousowl/iot-edge/internal/utility/thermal"
)

func TestGoldenUtilityCalculations(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	window := analytics.Window{From: base, To: base.Add(2 * time.Hour)}
	maxGap := 2 * time.Hour

	t.Run("electrical integration demand and cost", func(t *testing.T) {
		result, err := electrical.Calculate(electrical.Input{PowerKW: goldenSamples(base, 100, 150, 100), Window: window, MaxGap: maxGap, Tariff: electrical.Tariff{Mode: electrical.TariffFixed, Currency: "THB", FixedRate: 4}})
		if err != nil || result.EnergyKWh != 250 || result.DemandKW != 150 || result.Cost != 1000 || result.CoveragePercent != 100 {
			t.Fatalf("electrical=%#v err=%v", result, err)
		}
	})

	t.Run("thermal energy and COP", func(t *testing.T) {
		result, err := thermal.Calculate(thermal.Input{Mode: thermal.ModeDirect, DirectPowerKW: goldenSamples(base, 300, 300, 300), ElectricalPowerKW: goldenSamples(base, 100, 100, 100), Window: window, MaxGap: maxGap})
		if err != nil || result.ThermalEnergyKWh != 600 || result.ElectricalEnergyKWh != 200 || !result.COPValid || result.COP != 3 || result.CoveragePercent != 100 {
			t.Fatalf("thermal=%#v err=%v", result, err)
		}
	})

	t.Run("compressed-air SEC pressure and cost", func(t *testing.T) {
		result, err := compressedair.Calculate(compressedair.CalculationInput{PowerKW: goldenSamples(base, 100, 100, 100), Mode: compressedair.ConsumptionFlow, FlowNm3PerHour: goldenSamples(base, 500, 500, 500), PressureBar: goldenSamples(base, 7, 6, 7), Currency: "THB", RatePerKWh: 4, Window: window, MaxGap: maxGap})
		if err != nil || result.EnergyKWh != 200 || result.VolumeNm3 != 1000 || !result.SEC.Valid || result.SEC.Value != .2 || result.Cost != 800 || !result.CostPerNm3.Valid || result.CostPerNm3.Value != .8 || !result.Pressure.Valid || result.Pressure.AverageBar != 6.5 || math.Abs(result.Pressure.StdDevBar-.5) > 1e-12 || result.Pressure.DropBar != 1 {
			t.Fatalf("compressed-air=%#v err=%v", result, err)
		}
	})

	t.Run("explicit baseline estimates without automatic leak diagnosis", func(t *testing.T) {
		result, err := compressedair.EstimateLeakage(compressedair.LeakageInput{Basis: compressedair.BaselineNoProduction, FlowNm3PerHour: goldenSamples(base, 10, 10, 10), PowerKW: goldenSamples(base, 5, 5, 5), Window: window, MaxGap: maxGap, RatePerKWh: 4, Currency: "THB"})
		if err != nil || result.EstimatedFlowNm3PerHour != 10 || result.EstimatedVolumeNm3 != 20 || result.EstimatedEnergyKWh != 10 || result.EstimatedCost != 40 || result.ConfidencePercent != 100 || result.AutomaticLeakClaimSupported {
			t.Fatalf("baseline=%#v err=%v", result, err)
		}
	})
}

func TestGoldenUtilityBoundariesFailClosed(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	window := analytics.Window{From: base, To: base.Add(time.Hour)}
	if _, err := electrical.Calculate(electrical.Input{PowerKW: goldenSamples(base, 10, 10), Window: window, MaxGap: 2 * time.Hour, Tariff: electrical.Tariff{Mode: electrical.TariffFixed, Currency: "THB", FixedRate: -1}}); err == nil {
		t.Fatal("negative tariff must fail closed")
	}
	thermalResult, err := thermal.Calculate(thermal.Input{Mode: thermal.ModeDerived, Flow: goldenSamples(base, 10, 10), SupplyTemperature: goldenSamples(base, 5, 5), ReturnTemperature: goldenSamples(base, 10, 10), MediumFactor: 1.163, Direction: thermal.DirectionSupplyHotter, ElectricalPowerKW: goldenSamples(base, 10, 10), Window: window, MaxGap: 2 * time.Hour})
	if err != nil || thermalResult.CoveragePercent != 0 || thermalResult.COPValid || thermalResult.COPError == "" {
		t.Fatalf("wrong thermal direction must expose zero coverage and invalid COP: result=%#v err=%v", thermalResult, err)
	}
	if _, err := compressedair.EstimateLeakage(compressedair.LeakageInput{FlowNm3PerHour: goldenSamples(base, 10, 10), PowerKW: goldenSamples(base, 5, 5), Window: window, MaxGap: 2 * time.Hour, Currency: "THB"}); err == nil {
		t.Fatal("implicit leakage baseline must fail closed")
	}
}

func goldenSamples(base time.Time, values ...float64) []analytics.Sample {
	result := make([]analytics.Sample, len(values))
	for index, value := range values {
		result[index] = analytics.Sample{At: base.Add(time.Duration(index) * time.Hour), Value: value, Quality: analytics.QualityGood}
	}
	return result
}
