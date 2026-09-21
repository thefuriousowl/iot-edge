package compressedair

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestCompressedAirPluginDescriptorsAndLiveOutputs(t *testing.T) {
	t.Parallel()
	if err := plugin.ValidateOutputDescriptors(PluginOutputDescriptors()); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	context := outputContext(at, OutputLivePower, OutputLiveFlow, OutputLivePressure)
	batch, err := LivePluginOutputBatch(context, LiveSnapshot{ObservedAt: at, PowerKW: 20, FlowNm3Hour: 100, PressureBar: 6.5})
	if err != nil || len(batch.Values) != 3 || batch.Values[0].Attributes["source"] == "" || batch.Values[0].PeriodStart != batch.Values[0].PeriodEnd {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	for _, value := range batch.Values {
		if value.Key == OutputSEC {
			t.Fatal("live snapshot must not publish historical SEC")
		}
	}
}

func TestCompressedAirPeriodOutputsCarryCoverageQualityAndProvenance(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	result := CalculationResult{From: from, To: to, EnergyKWh: 20, VolumeNm3: 100, SEC: Ratio{Value: .2, Valid: true}, Cost: 80, CostPerNm3: Ratio{Value: .8, Valid: true}, RuntimeRatio: Ratio{Value: .5, Valid: true}, LoadRatio: Ratio{Value: .75, Valid: true}, Pressure: PressureMetrics{AverageBar: 6.5, StdDevBar: .1, DropBar: .5, CoveragePercent: 50, Valid: true}, CoveragePercent: 50, Currency: "THB"}
	leakage := &LeakageEstimate{From: from, To: to, EstimatedFlowNm3PerHour: 10, EstimatedEnergyKWh: 2, EstimatedCost: 8, CoveragePercent: 50, Currency: "THB"}
	keys := []plugin.OutputKey{OutputEnergy, OutputVolume, OutputSEC, OutputCost, OutputCostPerVolume, OutputRuntimeRatio, OutputLoadRatio, OutputPressureAverage, OutputPressureStdDev, OutputPressureDrop, OutputLeakFlow, OutputLeakEnergy, OutputLeakCost}
	batch, err := PeriodPluginOutputBatch(outputContext(to, keys...), result, leakage)
	if err != nil || len(batch.Values) != len(keys) {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	for _, value := range batch.Values {
		if value.Quality != plugin.OutputQualityPartial || value.CoveragePercent == nil || *value.CoveragePercent != 50 || value.Attributes["asset_id"] == "" || value.Attributes["boundary_asset_id"] == "" || value.Attributes["source"] == "" {
			t.Fatalf("value=%#v", value)
		}
		if value.Key == OutputSEC {
			sec, ok := value.Value.(float64)
			if value.PeriodStart != from || value.PeriodEnd != to || !ok || sec != .2 {
				t.Fatalf("SEC is not a windowed persisted-history result: %#v", value)
			}
		}
	}
}

func TestCompressedAirOutputsFailClosedWithoutSourceOrInvalidRatio(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	result := CalculationResult{From: from, To: to, SEC: Ratio{Error: "no denominator"}, CostPerNm3: Ratio{Error: "no denominator"}, RuntimeRatio: Ratio{Error: "no state"}, LoadRatio: Ratio{Error: "no state"}, Pressure: PressureMetrics{Error: "no pressure"}, CoveragePercent: 100, Currency: "THB"}
	keys := []plugin.OutputKey{OutputEnergy, OutputVolume, OutputSEC, OutputCost, OutputCostPerVolume, OutputRuntimeRatio, OutputLoadRatio, OutputPressureAverage, OutputPressureStdDev, OutputPressureDrop}
	context := outputContext(to, keys...)
	delete(context.Sources, OutputEnergy)
	if _, err := PeriodPluginOutputBatch(context, result, nil); err == nil {
		t.Fatal("missing source must fail closed")
	}
	context.Sources[OutputEnergy] = "tag:power"
	batch, err := PeriodPluginOutputBatch(context, result, nil)
	if err != nil || batch.Values[2].Quality != plugin.OutputQualityBad || batch.Values[2].Value != nil {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
}

func outputContext(published time.Time, keys ...plugin.OutputKey) OutputContext {
	sources := make(map[plugin.OutputKey]string, len(keys))
	for _, key := range keys {
		sources[key] = "tag:power+tag:flow"
	}
	return OutputContext{InstanceID: uuid.New(), Sequence: 1, OwnerAssetID: uuid.New(), BoundaryAssetID: uuid.New(), PublishedAt: published, Sources: sources}
}
