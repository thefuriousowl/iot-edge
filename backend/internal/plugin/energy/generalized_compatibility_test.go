package energy

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
	"github.com/thefuriousowl/iot-edge/internal/utility/electrical"
	utilitythermal "github.com/thefuriousowl/iot-edge/internal/utility/thermal"
)

func TestGeneralizedElectricalCalculatorPreservesLegacyEnergyAndCost(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	loggerID, powerID, thermalID, tariffID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, test := range []struct {
		name   string
		tariff FlatTariff
	}{
		{name: "fixed", tariff: FlatTariff{Mode: TariffModeFlat, Currency: "THB", RatePerKWh: 4}},
		{name: "Tag", tariff: FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, ThermalPowerTags: []PowerTag{{TagID: thermalID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 7200, Tariff: test.tariff}
			calculator, err := NewCalculator(config)
			if err != nil {
				t.Fatal(err)
			}
			powerValues, tariffValues := []float64{10, 20, 30}, []float64{4, 5, 6}
			metrics := make([]BatchMetrics, len(powerValues))
			powerSamples := make([]analytics.Sample, len(powerValues))
			thermalSamples := make([]analytics.Sample, len(powerValues))
			tariffSamples := make([]analytics.Sample, len(powerValues))
			for index := range powerValues {
				at := base.Add(time.Duration(index) * time.Hour)
				samples := []datalogger.RawSample{goodEnergySample(powerID, at, "float64", powerValues[index]), goodEnergySample(thermalID, at, "float64", powerValues[index]*3)}
				if test.tariff.effectiveMode() == TariffModeTag {
					samples = append(samples, goodEnergySample(tariffID, at, "float64", tariffValues[index]))
				}
				metrics[index], err = calculator.Evaluate(datalogger.RawBatch{LoggerID: loggerID, BatchAt: at, Samples: samples})
				if err != nil {
					t.Fatal(err)
				}
				powerSamples[index] = analytics.Sample{At: at, Value: metrics[index].Electrical.Kilowatts, Quality: analytics.QualityGood}
				thermalSamples[index] = analytics.Sample{At: at, Value: metrics[index].Thermal.Kilowatts, Quality: analytics.QualityGood}
				tariffSamples[index] = analytics.Sample{At: at, Value: metrics[index].Tariff.RatePerKWh, Quality: analytics.QualityGood}
			}
			window := analytics.Window{From: base.Add(30 * time.Minute), To: base.Add(90 * time.Minute)}
			legacy, err := calculator.Integrate(metrics, window.From, window.To)
			if err != nil {
				t.Fatal(err)
			}
			generalizedTariff := electrical.Tariff{Mode: electrical.TariffFixed, Currency: "THB", FixedRate: test.tariff.RatePerKWh}
			if test.tariff.effectiveMode() == TariffModeTag {
				generalizedTariff = electrical.Tariff{Mode: electrical.TariffSource, Currency: "THB", Samples: tariffSamples}
			}
			generalized, err := electrical.Calculate(electrical.Input{PowerKW: powerSamples, Window: window, MaxGap: 2 * time.Hour, Tariff: generalizedTariff})
			if err != nil || generalized.EnergyKWh != legacy.Electrical.KilowattHours || generalized.Cost != legacy.Cost.Value || generalized.Covered != legacy.Electrical.Covered || generalized.Skipped != legacy.Electrical.Skipped {
				t.Fatalf("generalized=%#v legacy=%#v err=%v", generalized, legacy, err)
			}
			generalizedThermal, err := utilitythermal.Calculate(utilitythermal.Input{Mode: utilitythermal.ModeDirect, DirectPowerKW: thermalSamples, ElectricalPowerKW: powerSamples, Window: window, MaxGap: 2 * time.Hour})
			if err != nil || generalizedThermal.ThermalEnergyKWh != legacy.Thermal.KilowattHours || generalizedThermal.ElectricalEnergyKWh != legacy.Electrical.KilowattHours || generalizedThermal.COP != legacy.COP.Value || generalizedThermal.COPValid != legacy.COP.Valid || generalizedThermal.Covered != legacy.Thermal.Covered || generalizedThermal.Skipped != legacy.Thermal.Skipped {
				t.Fatalf("generalized thermal=%#v legacy=%#v err=%v", generalizedThermal, legacy, err)
			}
		})
	}
}
