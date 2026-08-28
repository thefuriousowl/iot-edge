package history

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/utility"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
	"github.com/thefuriousowl/iot-edge/internal/utility/electrical"
	"github.com/thefuriousowl/iot-edge/internal/utility/thermal"
)

type batches struct {
	input  datalogger.RawBatchListInput
	values []datalogger.RawBatch
}

func (reader *batches) ListBatches(_ context.Context, input datalogger.RawBatchListInput) ([]datalogger.RawBatch, error) {
	reader.input = input
	return reader.values, nil
}

type catalog struct{ descriptor utility.SourceDescriptor }

func (value *catalog) Describe(context.Context, uuid.UUID, asset.SourceReference) (utility.SourceDescriptor, error) {
	return value.descriptor, nil
}

func TestDataLoggerReadsPersistedTagHistoryWithNeighbors(t *testing.T) {
	t.Parallel()
	tagID, loggerID := uuid.New(), uuid.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	storage := &batches{values: []datalogger.RawBatch{{LoggerID: loggerID, BatchAt: base, Samples: []datalogger.RawSample{{TagID: tagID, ObservedAt: base, DataType: "float64", Value: float64(42.5), Quality: datalogger.RawQualityGood}}}}}
	mapping := utility.Mapping{Key: "power", Slot: utility.SlotElectricalPower, OwnerAssetID: uuid.New(), Source: asset.TagSource(tagID), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 4}}
	reader, err := NewDataLogger(storage, &catalog{descriptor: utility.SourceDescriptor{Reference: mapping.Source, OwnerAssetID: mapping.OwnerAssetID, DataType: "float64", Unit: asset.UnitWatt}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := reader.Read(context.Background(), loggerID, mapping, analytics.Window{From: base.Add(-time.Hour), To: base.Add(time.Hour)})
	if err != nil || len(values) != 1 || values[0].Value != .0425 || values[0].Quality != analytics.QualityGood || !storage.input.IncludeNeighbors || storage.input.TagIDs[0] != tagID {
		t.Fatalf("values=%#v input=%#v err=%v", values, storage.input, err)
	}
}

func TestDataLoggerRejectsNonPersistedPluginHistory(t *testing.T) {
	t.Parallel()
	mapping := utility.Mapping{Source: asset.PluginOutputSource(uuid.New(), "power")}
	reader, _ := NewDataLogger(&batches{}, &catalog{})
	base := time.Now().UTC()
	_, err := reader.Read(context.Background(), uuid.New(), mapping, analytics.Window{From: base.Add(-time.Hour), To: base})
	if !errors.Is(err, utility.ErrHistoryUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

type descriptorCatalog struct {
	values map[string]utility.SourceDescriptor
}

func (value *descriptorCatalog) Describe(_ context.Context, _ uuid.UUID, reference asset.SourceReference) (utility.SourceDescriptor, error) {
	return value.values[reference.Key()], nil
}

func TestDataLoggerMixedDatasourceCadenceUsesCommittedBatchTimeline(t *testing.T) {
	t.Parallel()
	loggerID, ownerID, thermalTagID, electricalTagID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	storage := &batches{}
	for index := 0; index < 3; index++ {
		batchAt := base.Add(time.Duration(index) * time.Hour)
		storage.values = append(storage.values, datalogger.RawBatch{LoggerID: loggerID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: thermalTagID, ObservedAt: batchAt.Add(-5 * time.Second), DataType: "float64", Value: float64(300), Quality: datalogger.RawQualityGood},
			{TagID: electricalTagID, ObservedAt: batchAt.Add(-time.Duration(10+index*20) * time.Minute), DataType: "float64", Value: float64(100), Quality: datalogger.RawQualityGood},
		}})
	}
	thermalMapping := utility.Mapping{Key: "thermal", Slot: utility.SlotThermalPower, OwnerAssetID: ownerID, Source: asset.TagSource(thermalTagID), Semantic: asset.Semantic{Resource: asset.ResourceThermal, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}}
	electricalMapping := utility.Mapping{Key: "electrical", Slot: utility.SlotElectricalPower, OwnerAssetID: ownerID, Source: asset.TagSource(electricalTagID), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}}
	catalog := &descriptorCatalog{values: map[string]utility.SourceDescriptor{
		thermalMapping.Source.Key():    {Reference: thermalMapping.Source, OwnerAssetID: ownerID, DataType: "float64", Unit: asset.UnitKilowatt},
		electricalMapping.Source.Key(): {Reference: electricalMapping.Source, OwnerAssetID: ownerID, DataType: "float64", Unit: asset.UnitKilowatt},
	}}
	reader, err := NewDataLogger(storage, catalog)
	if err != nil {
		t.Fatal(err)
	}
	window := analytics.Window{From: base, To: base.Add(2 * time.Hour)}
	thermalSamples, err := reader.Read(context.Background(), loggerID, thermalMapping, window)
	if err != nil {
		t.Fatal(err)
	}
	electricalSamples, err := reader.Read(context.Background(), loggerID, electricalMapping, window)
	if err != nil {
		t.Fatal(err)
	}
	for index := range thermalSamples {
		want := base.Add(time.Duration(index) * time.Hour)
		if !thermalSamples[index].At.Equal(want) || !electricalSamples[index].At.Equal(want) {
			t.Fatalf("sample %d timestamps thermal=%s electrical=%s want committed batch=%s", index, thermalSamples[index].At, electricalSamples[index].At, want)
		}
	}
	result, err := thermal.Calculate(thermal.Input{Mode: thermal.ModeDirect, DirectPowerKW: thermalSamples, ElectricalPowerKW: electricalSamples, Window: window, MaxGap: 2 * time.Hour})
	if err != nil || result.ThermalEnergyKWh != 600 || result.ElectricalEnergyKWh != 200 || !result.COPValid || result.COP != 3 || result.CoveragePercent != 100 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestDataLoggerRestartIncompleteCoverageAndChangedMappingRemainExplicit(t *testing.T) {
	t.Parallel()
	loggerID, ownerID, oldTagID, newTagID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	oldMapping := utility.Mapping{Key: "old_power", Slot: utility.SlotElectricalPower, OwnerAssetID: ownerID, Source: asset.TagSource(oldTagID), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}}
	newMapping := utility.Mapping{Key: "new_power", Slot: utility.SlotElectricalPower, OwnerAssetID: ownerID, Source: asset.TagSource(newTagID), Semantic: oldMapping.Semantic}
	catalog := &descriptorCatalog{values: map[string]utility.SourceDescriptor{
		oldMapping.Source.Key(): {Reference: oldMapping.Source, OwnerAssetID: ownerID, DataType: "float64", Unit: asset.UnitKilowatt},
		newMapping.Source.Key(): {Reference: newMapping.Source, OwnerAssetID: ownerID, DataType: "float64", Unit: asset.UnitKilowatt},
	}}
	window := analytics.Window{From: base.Add(30 * time.Minute), To: base.Add(210 * time.Minute)}

	t.Run("restart reads the same persisted samples and changed mapping does not reuse old source", func(t *testing.T) {
		storage := persistedPowerBatches(loggerID, oldTagID, newTagID, base, false)
		first, _ := NewDataLogger(storage, catalog)
		before, err := first.Read(context.Background(), loggerID, newMapping, window)
		if err != nil {
			t.Fatal(err)
		}
		restarted, _ := NewDataLogger(storage, catalog)
		after, err := restarted.Read(context.Background(), loggerID, newMapping, window)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("before=%#v after=%#v err=%v", before, after, err)
		}
		oldValues, err := restarted.Read(context.Background(), loggerID, oldMapping, window)
		if err != nil || len(oldValues) != len(after) || after[0].Value != 200 || oldValues[0].Value != 100 {
			t.Fatalf("old=%#v new=%#v err=%v", oldValues, after, err)
		}
	})

	t.Run("bad persisted sample creates partial coverage with exact clipped boundaries", func(t *testing.T) {
		storage := persistedPowerBatches(loggerID, oldTagID, newTagID, base, true)
		reader, _ := NewDataLogger(storage, catalog)
		values, err := reader.Read(context.Background(), loggerID, newMapping, window)
		if err != nil {
			t.Fatal(err)
		}
		result, err := electrical.Calculate(electrical.Input{PowerKW: values, Window: window, MaxGap: 90 * time.Minute, Tariff: electrical.Tariff{Mode: electrical.TariffFixed, Currency: "THB", FixedRate: 4}})
		if err != nil || result.EnergyKWh != 200 || result.Cost != 800 || result.Covered != time.Hour || result.Skipped != 2*time.Hour || math.Abs(result.CoveragePercent-100.0/3.0) > 1e-12 || result.SkippedSegments != 2 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})

	t.Run("stale persisted gap is skipped instead of backfilled", func(t *testing.T) {
		storage := persistedPowerBatches(loggerID, oldTagID, newTagID, base, false)
		storage.values = append(storage.values[:2], storage.values[4:]...)
		reader, _ := NewDataLogger(storage, catalog)
		values, err := reader.Read(context.Background(), loggerID, newMapping, window)
		if err != nil {
			t.Fatal(err)
		}
		result, err := electrical.Calculate(electrical.Input{PowerKW: values, Window: window, MaxGap: 90 * time.Minute, Tariff: electrical.Tariff{Mode: electrical.TariffFixed, Currency: "THB", FixedRate: 4}})
		if err != nil || result.EnergyKWh != 100 || result.Covered != 30*time.Minute || result.Skipped != 150*time.Minute || math.Abs(result.CoveragePercent-100.0/6.0) > 1e-12 || result.SkippedSegments != 1 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}

func persistedPowerBatches(loggerID, oldTagID, newTagID uuid.UUID, base time.Time, badMiddle bool) *batches {
	storage := &batches{}
	for index := 0; index < 5; index++ {
		batchAt := base.Add(time.Duration(index) * time.Hour)
		newSample := datalogger.RawSample{TagID: newTagID, ObservedAt: batchAt.Add(-time.Minute), DataType: "float64", Value: float64(200), Quality: datalogger.RawQualityGood}
		if badMiddle && index == 2 {
			newSample.Value, newSample.Quality, newSample.Error = nil, datalogger.RawQualityBad, "source timeout"
		}
		storage.values = append(storage.values, datalogger.RawBatch{LoggerID: loggerID, BatchAt: batchAt, Samples: []datalogger.RawSample{{TagID: oldTagID, ObservedAt: batchAt, DataType: "float64", Value: float64(100), Quality: datalogger.RawQualityGood}, newSample}})
	}
	return storage
}
