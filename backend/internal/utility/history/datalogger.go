package history

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/utility"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

type BatchReader interface {
	ListBatches(context.Context, datalogger.RawBatchListInput) ([]datalogger.RawBatch, error)
}

type DataLogger struct {
	batches BatchReader
	catalog utility.SourceCatalog
}

func NewDataLogger(batches BatchReader, catalog utility.SourceCatalog) (*DataLogger, error) {
	if batches == nil || catalog == nil {
		return nil, utility.ErrHistoryRequired
	}
	return &DataLogger{batches: batches, catalog: catalog}, nil
}

func (reader *DataLogger) Read(ctx context.Context, loggerID uuid.UUID, mapping utility.Mapping, window analytics.Window) ([]analytics.Sample, error) {
	if reader == nil || reader.batches == nil || reader.catalog == nil || ctx == nil || loggerID == uuid.Nil || mapping.Source.Validate() != nil || !window.From.Before(window.To) {
		return nil, utility.ErrInvalidQuery
	}
	if mapping.Source.Kind != asset.SourceTag {
		return nil, utility.ErrHistoryUnavailable
	}
	if mapping.Semantic.Validate() != nil {
		return nil, utility.ErrInvalidQuery
	}
	descriptor, err := reader.catalog.Describe(ctx, mapping.OwnerAssetID, mapping.Source)
	if err != nil || descriptor.Reference != mapping.Source || descriptor.OwnerAssetID != mapping.OwnerAssetID || descriptor.DynamicUnit {
		return nil, utility.ErrHistoryUnavailable
	}
	batches, err := reader.batches.ListBatches(ctx, datalogger.RawBatchListInput{LoggerID: loggerID, TagIDs: []uuid.UUID{mapping.Source.TagID}, From: window.From.UTC(), To: window.To.UTC(), IncludeNeighbors: true})
	if err != nil {
		return nil, err
	}
	result := make([]analytics.Sample, 0, len(batches))
	for _, batch := range batches {
		for _, sample := range batch.Samples {
			if sample.TagID != mapping.Source.TagID {
				continue
			}
			value, ok := number(sample.Value)
			if !ok {
				if sample.Quality != datalogger.RawQualityBad {
					return nil, fmt.Errorf("%w: nonnumeric Tag sample", utility.ErrHistoryUnavailable)
				}
				value = 0
			}
			quality := analytics.QualityBad
			if sample.Quality == datalogger.RawQualityGood && sample.Error == "" {
				quality = analytics.QualityGood
			}
			if mapping.Semantic.Quantity != asset.QuantityState {
				sourceSemantic := mapping.Semantic
				sourceSemantic.Unit = descriptor.Unit
				value, err = asset.Convert(value, sourceSemantic, mapping.Semantic)
				if err != nil {
					return nil, utility.ErrHistoryUnavailable
				}
			}
			// BatchAt is the persisted synchronization boundary. ObservedAt remains
			// acquisition provenance and may differ across Datasource cadences.
			result = append(result, analytics.Sample{At: batch.BatchAt.UTC(), Value: value, Quality: quality})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result, nil
}

func number(value any) (float64, bool) {
	var converted float64
	switch typed := value.(type) {
	case bool:
		if typed {
			converted = 1
		}
	case int16:
		converted = float64(typed)
	case uint16:
		converted = float64(typed)
	case int32:
		converted = float64(typed)
	case uint32:
		converted = float64(typed)
	case float32:
		converted = float64(typed)
	case float64:
		converted = typed
	default:
		return 0, false
	}
	return converted, !math.IsNaN(converted) && !math.IsInf(converted, 0)
}

var _ utility.PersistedHistory = (*DataLogger)(nil)
