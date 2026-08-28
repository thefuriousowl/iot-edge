package thermal

import (
	"errors"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

func TestDirectThermalEnergyAndCOP(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	thermal := samples(base, 30, 30, 30)
	electrical := samples(base, 10, 10, 10)
	result, err := Calculate(Input{Mode: ModeDirect, DirectPowerKW: thermal, ElectricalPowerKW: electrical, Window: analytics.Window{From: base.Add(30 * time.Minute), To: base.Add(90 * time.Minute)}, MaxGap: 2 * time.Hour})
	if err != nil || result.ThermalEnergyKWh != 30 || result.ElectricalEnergyKWh != 10 || !result.COPValid || result.COP != 3 || result.CoveragePercent != 100 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestDerivedThermalPowerRequiresDirectionAndSharedQuality(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow := samples(base, 2, 2, 2)
	supply := samples(base, 80, 80, 80)
	ret := samples(base, 70, 70, 70)
	electrical := samples(base, 10, 10, 10)
	input := Input{Mode: ModeDerived, Flow: flow, SupplyTemperature: supply, ReturnTemperature: ret, MediumFactor: .5, Direction: DirectionSupplyHotter, ElectricalPowerKW: electrical, Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour}
	result, err := Calculate(input)
	if err != nil || result.ThermalEnergyKWh != 20 || !result.COPValid || result.COP != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	input.Direction = DirectionReturnHotter
	wrong, err := Calculate(input)
	if err != nil || wrong.Covered != 0 || wrong.COPValid || wrong.COPError == "" {
		t.Fatalf("wrong direction=%#v err=%v", wrong, err)
	}
	input.Direction = ""
	if _, err := Calculate(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing direction error=%v", err)
	}
}

func TestThermalComparison(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	thermal := samples(base, 10, 10, 20)
	electrical := samples(base, 10, 10, 10)
	result, err := Compare(Input{Mode: ModeDirect, DirectPowerKW: thermal, ElectricalPowerKW: electrical, Window: analytics.Window{From: base.Add(time.Hour), To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour})
	if err != nil || result.ThermalEnergyDeltaKWh != 5 || result.ThermalEnergyPercentChange == nil || *result.ThermalEnergyPercentChange != 50 || result.COPDelta != .5 {
		t.Fatalf("comparison=%#v err=%v", result, err)
	}
}

func samples(base time.Time, values ...float64) []analytics.Sample {
	result := make([]analytics.Sample, len(values))
	for i, value := range values {
		result[i] = analytics.Sample{At: base.Add(time.Duration(i) * time.Hour), Value: value, Quality: analytics.QualityGood}
	}
	return result
}
