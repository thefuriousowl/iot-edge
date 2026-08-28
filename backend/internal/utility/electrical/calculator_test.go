package electrical

import (
	"errors"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

func TestCalculateElectricalFixedAndSourceTariffs(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	power := []analytics.Sample{{At: base, Value: 10, Quality: analytics.QualityGood}, {At: base.Add(time.Hour), Value: 20, Quality: analytics.QualityGood}, {At: base.Add(2 * time.Hour), Value: 30, Quality: analytics.QualityGood}}
	window := analytics.Window{From: base.Add(30 * time.Minute), To: base.Add(90 * time.Minute)}
	fixed, err := Calculate(Input{PowerKW: power, Window: window, MaxGap: 2 * time.Hour, Tariff: Tariff{Mode: TariffFixed, Currency: "THB", FixedRate: 4}})
	if err != nil || fixed.EnergyKWh != 20 || fixed.DemandKW != 25 || fixed.Cost != 80 || fixed.CoveragePercent != 100 {
		t.Fatalf("fixed=%#v err=%v", fixed, err)
	}
	tariff := []analytics.Sample{{At: base, Value: 4, Quality: analytics.QualityGood}, {At: base.Add(time.Hour), Value: 5, Quality: analytics.QualityGood}, {At: base.Add(2 * time.Hour), Value: 6, Quality: analytics.QualityGood}}
	dynamic, err := Calculate(Input{PowerKW: power, Window: window, MaxGap: 2 * time.Hour, Tariff: Tariff{Mode: TariffSource, Currency: "THB", Samples: tariff}})
	if err != nil || dynamic.Cost != 91.25 {
		t.Fatalf("dynamic=%#v err=%v", dynamic, err)
	}
}

func TestCompareAndCoverageFailClosed(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	power := []analytics.Sample{{At: base, Value: 10, Quality: analytics.QualityGood}, {At: base.Add(time.Hour), Value: 10, Quality: analytics.QualityGood}, {At: base.Add(2 * time.Hour), Value: 20, Quality: analytics.QualityGood}}
	comparison, err := Compare(Input{PowerKW: power, Window: analytics.Window{From: base.Add(time.Hour), To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour, Tariff: Tariff{Mode: TariffFixed, Currency: "THB", FixedRate: 4}})
	if err != nil || comparison.EnergyDeltaKWh != 5 || comparison.EnergyPercentChange == nil || *comparison.EnergyPercentChange != 50 {
		t.Fatalf("comparison=%#v err=%v", comparison, err)
	}
	_, err = Calculate(Input{PowerKW: power, Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: time.Minute, Tariff: Tariff{Mode: TariffSource, Currency: "THB", Samples: nil}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("incomplete tariff error=%v", err)
	}
}
