package history

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/utility"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
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
