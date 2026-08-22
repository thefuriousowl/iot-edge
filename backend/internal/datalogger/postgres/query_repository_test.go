package dataloggerpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestHistoryRepositoryQueriesWideRawAndAggregateRows_Integration(t *testing.T) {
	db, _ := newRepositoryDatabase(t)
	tagIDs := insertHistoryTags(t, db)
	boolTagID, numericTagID := tagIDs[0], tagIDs[6]
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Query Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: from, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, []uuid.UUID{boolTagID, numericTagID}); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	batches := []struct {
		minute       int
		boolean      bool
		numeric      float64
		numericError string
	}{
		{minute: 0, boolean: true, numeric: 10},
		{minute: 1, boolean: false, numeric: 20},
		{minute: 2, boolean: false, numericError: "illegal data address"},
		{minute: 6, boolean: true, numeric: 30},
	}
	for _, batch := range batches {
		batchAt := from.Add(time.Duration(batch.minute) * time.Minute)
		numeric := datalogger.RawSample{TagID: numericTagID, ObservedAt: batchAt.Add(-time.Second), DataType: "float64", Value: batch.numeric, Quality: datalogger.RawQualityGood}
		if batch.numericError != "" {
			numeric.Value = nil
			numeric.Quality = datalogger.RawQualityBad
			numeric.Error = batch.numericError
		}
		if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: boolTagID, ObservedAt: batchAt.Add(-time.Second), DataType: "bool", Value: batch.boolean, Quality: datalogger.RawQualityGood},
			numeric,
		}}); err != nil {
			t.Fatalf("WriteBatch(%s) error = %v", batchAt, err)
		}
	}

	raw, err := history.Query(ctx, datalogger.QueryInput{LoggerID: logger.ID, TagIDs: []uuid.UUID{boolTagID, numericTagID}, From: from, To: from.Add(10 * time.Minute), Mode: datalogger.QueryModeRaw, Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("Query(raw) error = %v", err)
	}
	if raw.Total != 4 || raw.TotalPages != 2 || len(raw.Data) != 2 || !raw.Data[0].At.Equal(from.Add(6*time.Minute)) || !raw.Data[1].At.Equal(from.Add(2*time.Minute)) {
		t.Fatalf("Query(raw) = %#v", raw)
	}
	if value := raw.Data[0].Values[boolTagID.String()]; value.Value != true || value.Quality != datalogger.RawQualityGood || value.ObservedAt == nil || !value.ObservedAt.Equal(from.Add(6*time.Minute-time.Second)) {
		t.Errorf("raw bool value = %#v", value)
	}
	if value := raw.Data[1].Values[numericTagID.String()]; value.Value != nil || value.Quality != datalogger.RawQualityBad || value.Error != "illegal data address" {
		t.Errorf("raw bad value = %#v", value)
	}

	aggregated, err := history.Query(ctx, datalogger.QueryInput{LoggerID: logger.ID, TagIDs: []uuid.UUID{boolTagID, numericTagID}, From: from, To: from.Add(10 * time.Minute), Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket5Minutes, Aggregate: datalogger.AggregateAvg, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("Query(avg) error = %v", err)
	}
	if aggregated.Total != 2 || aggregated.TotalPages != 1 || len(aggregated.Data) != 2 || !aggregated.Data[0].At.Equal(from.Add(5*time.Minute)) || !aggregated.Data[1].At.Equal(from) {
		t.Fatalf("Query(avg) = %#v", aggregated)
	}
	firstBucket := aggregated.Data[1]
	if value := firstBucket.Values[numericTagID.String()]; value.Value != float64(15) || value.GoodCount != 2 || value.BadCount != 1 || value.TotalCount != 3 || value.Error != "illegal data address" || value.Supported == nil || !*value.Supported {
		t.Errorf("aggregate numeric value = %#v", value)
	}
	if value := firstBucket.Values[boolTagID.String()]; value.Value != nil || value.GoodCount != 3 || value.BadCount != 0 || value.TotalCount != 3 || value.Supported == nil || *value.Supported {
		t.Errorf("unsupported bool average = %#v", value)
	}

	functions := []struct {
		function datalogger.AggregateFunction
		want     float64
	}{
		{function: datalogger.AggregateMin, want: 10},
		{function: datalogger.AggregateMax, want: 20},
		{function: datalogger.AggregateSum, want: 30},
		{function: datalogger.AggregateCount, want: 2},
	}
	for _, test := range functions {
		t.Run(string(test.function), func(t *testing.T) {
			result, queryErr := history.Query(ctx, datalogger.QueryInput{LoggerID: logger.ID, TagIDs: []uuid.UUID{boolTagID, numericTagID}, From: from, To: from.Add(5 * time.Minute), Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket5Minutes, Aggregate: test.function, Page: 1, PerPage: 10})
			if queryErr != nil {
				t.Fatalf("Query(%s) error = %v", test.function, queryErr)
			}
			if value := result.Data[0].Values[numericTagID.String()]; value.Value != test.want {
				t.Errorf("Query(%s) numeric value = %#v, want %v", test.function, value, test.want)
			}
			if test.function == datalogger.AggregateCount {
				if value := result.Data[0].Values[boolTagID.String()]; value.Value != float64(3) || value.Supported == nil || !*value.Supported {
					t.Errorf("Query(count) bool value = %#v", value)
				}
			}
		})
	}

	for _, test := range []struct {
		function datalogger.AggregateFunction
		want     bool
	}{{function: datalogger.AggregateFirst, want: true}, {function: datalogger.AggregateLast, want: false}} {
		t.Run(string(test.function), func(t *testing.T) {
			result, queryErr := history.Query(ctx, datalogger.QueryInput{LoggerID: logger.ID, TagIDs: []uuid.UUID{boolTagID}, From: from, To: from.Add(5 * time.Minute), Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket5Minutes, Aggregate: test.function, Page: 1, PerPage: 10})
			if queryErr != nil {
				t.Fatalf("Query(%s) error = %v", test.function, queryErr)
			}
			if value := result.Data[0].Values[boolTagID.String()]; value.Value != test.want || value.Supported == nil || !*value.Supported {
				t.Errorf("Query(%s) bool value = %#v, want %t", test.function, value, test.want)
			}
		})
	}
}

func TestHistoryRepositoryRejectsInvalidQueries_Integration(t *testing.T) {
	db, _ := newRepositoryDatabase(t)
	history := NewHistoryRepository(db)
	from := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	tests := []datalogger.QueryInput{
		{},
		{LoggerID: uuid.New(), TagIDs: []uuid.UUID{uuid.New()}, From: from, To: from, Mode: datalogger.QueryModeRaw, Page: 1, PerPage: 10},
		{LoggerID: uuid.New(), TagIDs: []uuid.UUID{uuid.New()}, From: from, To: from.Add(time.Hour), Mode: datalogger.QueryModeAggregate, Bucket: "month", Aggregate: datalogger.AggregateAvg, Page: 1, PerPage: 10},
		{LoggerID: uuid.New(), TagIDs: []uuid.UUID{uuid.New()}, From: from, To: from.Add(time.Hour), Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Hour, Aggregate: "median", Page: 1, PerPage: 10},
	}
	for _, input := range tests {
		if _, err := history.Query(context.Background(), input); !errors.Is(err, datalogger.ErrInvalidQuery) {
			t.Errorf("Query(%#v) error = %v", input, err)
		}
	}
}
