package dataloggerpostgres

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/datalogger/tagsnapshot"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"gorm.io/gorm"
)

func TestIntervalSchedulerCapturesTagMemorySnapshotToRawHistory_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	batchAt := time.Date(2026, time.August, 23, 8, 30, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Memory Snapshot Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: batchAt.Add(-time.Hour), Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	storedLogger, err := definitionRepository.Find(ctx, logger.ID)
	if err != nil {
		t.Fatalf("Find(logger) error = %v", err)
	}
	values := tag.NewMemoryValueStore()
	if _, err := values.Put(tag.TagValue{TagID: tagIDs[0], ObservedAt: batchAt.Add(-2 * time.Second), DataType: tag.DataTypeFloat64, Value: 42.5, Quality: tag.ValueQualityGood}); err != nil {
		t.Fatalf("Put(good) error = %v", err)
	}
	if _, err := values.Put(tag.TagValue{TagID: tagIDs[1], ObservedAt: batchAt.Add(-time.Second), DataType: tag.DataTypeFloat64, Quality: tag.ValueQualityBad, Error: "illegal data address"}); err != nil {
		t.Fatalf("Put(bad) error = %v", err)
	}
	reader, err := tagsnapshot.NewReader(values)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	scheduler, err := datalogger.NewIntervalScheduler(reader, history)
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}
	if err := scheduler.Capture(ctx, *storedLogger, batchAt); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	result, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID})
	if err != nil {
		t.Fatalf("ListValues() error = %v", err)
	}
	if result.Total != 3 || len(result.Data) != 3 {
		t.Fatalf("history = %#v", result)
	}
	if result.LastBatchAt == nil || !result.LastBatchAt.Equal(batchAt) {
		t.Errorf("last batch at = %v, want %v", result.LastBatchAt, batchAt)
	}
	byTag := make(map[uuid.UUID]datalogger.RawValue, len(result.Data))
	for _, value := range result.Data {
		byTag[value.TagID] = value
	}
	if byTag[tagIDs[0]].Quality != datalogger.RawQualityGood || byTag[tagIDs[0]].Value != float64(42.5) {
		t.Errorf("good history = %#v", byTag[tagIDs[0]])
	}
	if byTag[tagIDs[1]].Quality != datalogger.RawQualityBad || byTag[tagIDs[1]].Error != "illegal data address" {
		t.Errorf("bad history = %#v", byTag[tagIDs[1]])
	}
	if byTag[tagIDs[2]].Quality != datalogger.RawQualityBad || byTag[tagIDs[2]].Error != "latest Tag value is unavailable" || !byTag[tagIDs[2]].ObservedAt.Equal(batchAt) {
		t.Errorf("missing history = %#v", byTag[tagIDs[2]])
	}
}

func TestCalendarSchedulerUsesPersistedDefinitionAndWritesRawHistory_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	service, err := datalogger.NewService(definitionRepository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	location, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	startAt := time.Date(2026, time.August, 24, 7, 0, 0, 0, location)
	logger, err := service.Create(context.Background(), datalogger.CreateInput{
		Name: "Calendar History Logger", Timezone: location.String(), Mode: datalogger.ModeSchedule, StartAt: startAt,
		Config: []byte(`{"unit":"day","times":["08:00"]}`), TagIDs: []uuid.UUID{tagIDs[0]},
	})
	if err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	next, exists, err := datalogger.NextCalendarRun(*logger, startAt)
	wantNext := time.Date(2026, time.August, 24, 8, 0, 0, 0, location)
	if err != nil || !exists || !next.Equal(wantNext) {
		t.Fatalf("NextCalendarRun() = %s, %t, %v; want %s", next, exists, err, wantNext)
	}
	values := tag.NewMemoryValueStore()
	if _, err := values.Put(tag.TagValue{TagID: tagIDs[0], ObservedAt: next.Add(-time.Second), DataType: tag.DataTypeFloat64, Value: 88.25, Quality: tag.ValueQualityGood}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	reader, err := tagsnapshot.NewReader(values)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	history := NewHistoryRepository(db)
	scheduler, err := datalogger.NewCalendarScheduler(reader, history)
	if err != nil {
		t.Fatalf("NewCalendarScheduler() error = %v", err)
	}
	if err := scheduler.Capture(context.Background(), *logger, next); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	result, err := history.ListValues(context.Background(), datalogger.RawValueListInput{LoggerID: logger.ID})
	if err != nil || result.Total != 1 || !result.Data[0].BatchAt.Equal(wantNext.UTC()) || result.Data[0].Value != float64(88.25) {
		t.Errorf("calendar history = %#v, error = %v", result, err)
	}
}

func TestRuntimeAutoStartsPersistedLoggerAndWritesRawHistory_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	startAt := time.Now().UTC().Add(150 * time.Millisecond)
	endAt := startAt.Add(100 * time.Millisecond)
	logger := datalogger.Logger{Name: "Auto-start Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, EndAt: &endAt, Config: []byte(`{"interval_seconds":1}`)}
	if err := definitionRepository.Create(context.Background(), &logger, tagIDs); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	values := tag.NewMemoryValueStore()
	for index, tagID := range tagIDs {
		if _, err := values.Put(tag.TagValue{TagID: tagID, ObservedAt: startAt.Add(-time.Second), DataType: tag.DataTypeFloat64, Value: float64(index) + 10.5, Quality: tag.ValueQualityGood}); err != nil {
			t.Fatalf("Put(%d) error = %v", index, err)
		}
	}
	reader, err := tagsnapshot.NewReader(values)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	runtime, err := datalogger.NewRuntime(definitionRepository, reader, history, datalogger.WithRuntimeReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(runtime.Stop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, listErr := history.ListValues(context.Background(), datalogger.RawValueListInput{LoggerID: logger.ID})
		if listErr != nil {
			t.Fatalf("ListValues() error = %v", listErr)
		}
		if result.Total == int64(len(tagIDs)) {
			for _, value := range result.Data {
				if value.Quality != datalogger.RawQualityGood || !value.BatchAt.Equal(result.Data[0].BatchAt) {
					t.Errorf("runtime history value = %#v", value)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("runtime did not persist the scheduled batch")
}

func TestHistoryRepositoryWritesPartitionsAndQueriesTypedBatches_Integration(t *testing.T) {
	db, _ := newRepositoryDatabase(t)
	tagIDs := insertHistoryTags(t, db)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	startAt := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "History Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	batchAt := time.Date(2026, time.August, 23, 8, 30, 0, 123000000, time.FixedZone("ICT", 7*60*60))
	observedAt := batchAt.Add(-time.Second)
	values := []any{true, int16(-12), uint16(65535), int32(-200000), uint32(4000000000), float32(12.25), float64(-999.5)}
	dataTypes := []string{"bool", "int16", "uint16", "int32", "uint32", "float32", "float64"}
	samples := make([]datalogger.RawSample, 0, len(tagIDs))
	for index, tagID := range tagIDs {
		samples = append(samples, datalogger.RawSample{TagID: tagID, ObservedAt: observedAt.Add(time.Duration(index) * time.Millisecond), DataType: dataTypes[index], Value: values[index], Quality: datalogger.RawQualityGood})
	}
	batch := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: samples}
	if err := history.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if err := history.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("WriteBatch(idempotent retry) error = %v", err)
	}
	result, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID})
	if err != nil {
		t.Fatalf("ListValues() error = %v", err)
	}
	if result.Total != int64(len(samples)) || len(result.Data) != len(samples) || result.Page != 1 || result.PerPage != 100 || result.TotalPages != 1 {
		t.Fatalf("ListValues() = %#v", result)
	}
	wantByTag := make(map[uuid.UUID]any, len(tagIDs))
	for index, tagID := range tagIDs {
		wantByTag[tagID] = values[index]
	}
	for _, value := range result.Data {
		if value.LoggerID != logger.ID || !value.BatchAt.Equal(batchAt.UTC()) || value.Quality != datalogger.RawQualityGood || value.PersistedAt.IsZero() || !reflect.DeepEqual(value.Value, wantByTag[value.TagID]) {
			t.Errorf("persisted value = %#v, want %#v", value, wantByTag[value.TagID])
		}
	}
	assertPartitionRows(t, db, "tag_values_raw_2026_08", len(samples))
	assertPartitionRows(t, db, "tag_values_raw_default", 0)

	badAt := batchAt.Add(time.Minute)
	badBatch := datalogger.RawBatch{LoggerID: logger.ID, BatchAt: badAt, Samples: []datalogger.RawSample{{TagID: tagIDs[6], ObservedAt: badAt.Add(-time.Second), DataType: "float64", Quality: datalogger.RawQualityBad, Error: "  connection lost  "}}}
	if err := history.WriteBatch(ctx, badBatch); err != nil {
		t.Fatalf("WriteBatch(bad quality) error = %v", err)
	}
	tagID := tagIDs[6]
	filtered, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID, TagID: &tagID, From: timePointer(batchAt.UTC()), To: timePointer(badAt.Add(time.Second).UTC()), Page: 1, PerPage: 1})
	if err != nil {
		t.Fatalf("ListValues(filtered) error = %v", err)
	}
	if filtered.Total != 2 || filtered.TotalPages != 2 || len(filtered.Data) != 1 || !filtered.Data[0].BatchAt.Equal(badAt.UTC()) || filtered.Data[0].Quality != datalogger.RawQualityBad || filtered.Data[0].Value != nil || filtered.Data[0].Error != "connection lost" {
		t.Errorf("filtered history = %#v", filtered)
	}

	septemberAt := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: septemberAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: septemberAt, DataType: "bool", Value: false, Quality: datalogger.RawQualityGood}}}); err != nil {
		t.Fatalf("WriteBatch(next month) error = %v", err)
	}
	assertPartitionRows(t, db, "tag_values_raw_2026_09", 1)

	if err := db.Exec(`UPDATE tags SET data_type='float32' WHERE id=?`, tagIDs[6]).Error; err != nil {
		t.Fatalf("changing Tag data type: %v", err)
	}
	preserved, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID, TagID: &tagID})
	if err != nil || preserved.Total != 2 || preserved.Data[1].DataType != "float64" || preserved.Data[1].Value != float64(-999.5) {
		t.Errorf("history after Tag type change = %#v, error = %v", preserved, err)
	}
}

func TestHistoryRepositoryListsSelectedBatchWindowWithNeighbors_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitions := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Energy Window Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: start, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitions.Create(ctx, &logger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	for index := 0; index < 4; index++ {
		batchAt := start.Add(time.Duration(index) * time.Minute)
		samples := []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: float64(index + 1), Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "float64", Value: float64((index + 1) * 10), Quality: datalogger.RawQualityGood},
		}
		if index == 2 {
			samples[0].Value = nil
			samples[0].Quality = datalogger.RawQualityBad
			samples[0].Error = "Modbus 0x02 Illegal Data Address"
		}
		if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: samples}); err != nil {
			t.Fatalf("WriteBatch(%d) error = %v", index, err)
		}
	}
	batches, err := history.ListBatches(ctx, datalogger.RawBatchListInput{
		LoggerID: logger.ID, TagIDs: []uuid.UUID{tagIDs[0]}, From: start.Add(time.Minute), To: start.Add(2 * time.Minute), IncludeNeighbors: true,
	})
	if err != nil {
		t.Fatalf("ListBatches() error = %v", err)
	}
	if len(batches) != 4 {
		t.Fatalf("ListBatches() = %#v", batches)
	}
	for index, batch := range batches {
		if !batch.BatchAt.Equal(start.Add(time.Duration(index)*time.Minute)) || batch.LoggerID != logger.ID || len(batch.Samples) != 1 || batch.Samples[0].TagID != tagIDs[0] {
			t.Errorf("batch %d = %#v", index, batch)
		}
	}
	if batches[0].Samples[0].Value != float64(1) || batches[2].Samples[0].Quality != datalogger.RawQualityBad || batches[2].Samples[0].Error != "Modbus 0x02 Illegal Data Address" {
		t.Errorf("typed batches = %#v", batches)
	}
	withoutNeighbors, err := history.ListBatches(ctx, datalogger.RawBatchListInput{
		LoggerID: logger.ID, TagIDs: []uuid.UUID{tagIDs[1]}, From: start.Add(time.Minute), To: start.Add(2 * time.Minute),
	})
	if err != nil || len(withoutNeighbors) != 2 || withoutNeighbors[0].Samples[0].Value != float64(20) || withoutNeighbors[1].Samples[0].Value != float64(30) {
		t.Errorf("ListBatches(no neighbors) = %#v, %v", withoutNeighbors, err)
	}
	for _, input := range []datalogger.RawBatchListInput{
		{},
		{LoggerID: logger.ID, From: start, To: start.Add(time.Minute)},
		{LoggerID: logger.ID, TagIDs: []uuid.UUID{uuid.Nil}, From: start, To: start.Add(time.Minute)},
		{LoggerID: logger.ID, TagIDs: []uuid.UUID{tagIDs[0], tagIDs[0]}, From: start, To: start.Add(time.Minute)},
		{LoggerID: logger.ID, TagIDs: []uuid.UUID{tagIDs[0]}, From: start.Add(time.Minute), To: start},
	} {
		if _, err := history.ListBatches(ctx, input); !errors.Is(err, datalogger.ErrInvalidInput) {
			t.Errorf("ListBatches(%#v) error = %v", input, err)
		}
	}

	partialAt := start.Add(4 * time.Minute)
	if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: partialAt, Samples: []datalogger.RawSample{
		{TagID: tagIDs[1], ObservedAt: partialAt, DataType: "float64", Value: float64(50), Quality: datalogger.RawQualityGood},
	}}); err != nil {
		t.Fatalf("WriteBatch(partial) error = %v", err)
	}
	partial, err := history.ListBatches(ctx, datalogger.RawBatchListInput{
		LoggerID: logger.ID, TagIDs: []uuid.UUID{tagIDs[0]}, From: partialAt, To: partialAt,
	})
	if err != nil || len(partial) != 1 || len(partial[0].Samples) != 0 || !partial[0].BatchAt.Equal(partialAt) {
		t.Errorf("ListBatches(partial) = %#v, %v", partial, err)
	}
}

func TestHistoryRepositoryEnforcesRollingLimitByCompleteBatch_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	startAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Rolling Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	write := func(batchAt time.Time, value float64) {
		t.Helper()
		samples := []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: value, Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "float64", Value: value + 1, Quality: datalogger.RawQualityGood},
		}
		if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: samples}); err != nil {
			t.Fatalf("WriteBatch(%s) error = %v", batchAt, err)
		}
	}

	write(startAt, 10)
	first, err := history.Storage(ctx, logger.ID, 2, nil)
	if err != nil || first.RowCount != 2 || first.BatchCount != 1 || first.EstimatedSizeBytes <= 0 || first.AverageRowBytes <= 0 {
		t.Fatalf("Storage(first) = %#v, %v", first, err)
	}
	if err := db.Exec(`ALTER TABLE data_loggers DROP CONSTRAINT data_loggers_max_size_check`).Error; err != nil {
		t.Fatalf("dropping limit constraint for compact retention fixture: %v", err)
	}
	limit := first.EstimatedSizeBytes * 2
	if err := db.Exec(`UPDATE data_loggers SET max_size_bytes=? WHERE id=?`, limit, logger.ID).Error; err != nil {
		t.Fatalf("setting compact storage limit: %v", err)
	}
	write(startAt.Add(time.Minute), 20)
	write(startAt.Add(2*time.Minute), 30)

	values, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID})
	if err != nil || values.Total != 4 || len(values.Data) != 4 {
		t.Fatalf("retained values = %#v, %v", values, err)
	}
	for _, value := range values.Data {
		if value.BatchAt.Equal(startAt) {
			t.Errorf("oldest batch was retained: %#v", value)
		}
	}
	stats, err := history.Storage(ctx, logger.ID, 2, &limit)
	if err != nil || stats.RowCount != 4 || stats.BatchCount != 2 || stats.EstimatedCapacityRows == nil || *stats.EstimatedCapacityRows != 4 || stats.OldestBatchAt == nil || !stats.OldestBatchAt.Equal(startAt.Add(time.Minute)) {
		t.Errorf("Storage(retained) = %#v, %v", stats, err)
	}

	tooSmall := first.EstimatedSizeBytes - 1
	if err := db.Exec(`UPDATE data_loggers SET max_size_bytes=? WHERE id=?`, tooSmall, logger.ID).Error; err != nil {
		t.Fatalf("setting too-small limit: %v", err)
	}
	if err := history.EnforceRetention(ctx, logger.ID, datalogger.RetentionPolicy{MaxSizeBytes: &tooSmall}); !errors.Is(err, datalogger.ErrStorageLimitTooSmall) {
		t.Errorf("EnforceRetention() error = %v, want %v", err, datalogger.ErrStorageLimitTooSmall)
	}
}

func TestHistoryRepositoryReportsLogicalAndPhysicalManagementOverview_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitions := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	maxAgeSeconds := int64(24 * 60 * 60)
	policyLogger := datalogger.Logger{Name: "Managed Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: evaluatedAt.Add(-time.Hour), MaxAgeSeconds: &maxAgeSeconds, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitions.Create(ctx, &policyLogger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(policy Logger) error = %v", err)
	}
	unlimitedLogger := datalogger.Logger{Name: "Disabled Unlimited Logger", Enabled: false, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: evaluatedAt.Add(-time.Hour), Config: []byte(`{"interval_seconds":60}`)}
	if err := definitions.Create(ctx, &unlimitedLogger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(unlimited Logger) error = %v", err)
	}
	batchAt := evaluatedAt.UTC().Add(-time.Minute)
	if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: policyLogger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
		{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: 10.5, Quality: datalogger.RawQualityGood},
		{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "float64", Value: 11.5, Quality: datalogger.RawQualityGood},
	}}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	overview, err := history.ManagementOverview(ctx, evaluatedAt)
	if err != nil {
		t.Fatalf("ManagementOverview() error = %v", err)
	}
	if !overview.EvaluatedAt.Equal(evaluatedAt.UTC()) || overview.LoggerCount != 2 || overview.EnabledLoggerCount != 1 || overview.PolicyLoggerCount != 1 {
		t.Errorf("management counts = %#v", overview)
	}
	if overview.LogicalHistory.RowCount != 2 || overview.LogicalHistory.BatchCount != 1 || overview.LogicalHistory.EstimatedSizeBytes <= 0 || overview.LogicalHistory.OldestBatchAt == nil || !overview.LogicalHistory.OldestBatchAt.Equal(batchAt) || overview.LogicalHistory.NewestBatchAt == nil || !overview.LogicalHistory.NewestBatchAt.Equal(batchAt) {
		t.Errorf("logical history = %#v", overview.LogicalHistory)
	}
	physical := overview.PostgreSQLPhysicalAllocation
	if physical.RawHistoryBytes <= 0 || physical.BatchAccountingBytes <= 0 || physical.TotalBytes != physical.RawHistoryBytes+physical.BatchAccountingBytes {
		t.Errorf("physical allocation = %#v", physical)
	}
	if _, err := history.ManagementOverview(nil, evaluatedAt); !errors.Is(err, datalogger.ErrInvalidInput) {
		t.Errorf("ManagementOverview(nil context) error = %v", err)
	}
	if _, err := history.ManagementOverview(ctx, time.Time{}); !errors.Is(err, datalogger.ErrInvalidInput) {
		t.Errorf("ManagementOverview(zero time) error = %v", err)
	}
}

func TestHistoryRepositoryPreviewsAndCleansCombinedRetentionByBoundedWholeBatches_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Combined Retention Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: evaluatedAt.Add(-24 * time.Hour), Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	for hoursAgo := 5; hoursAgo >= 1; hoursAgo-- {
		batchAt := evaluatedAt.Add(-time.Duration(hoursAgo) * time.Hour)
		if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: float64(hoursAgo), Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "float64", Value: float64(hoursAgo * 10), Quality: datalogger.RawQualityGood},
		}}); err != nil {
			t.Fatalf("WriteBatch(%s) error = %v", batchAt, err)
		}
	}
	if err := db.Exec(`
		INSERT INTO tag_values_latest (tag_id, sequence, data_type, value, quality, observed_at, stored_at)
		VALUES (?, 1, 'float64', '42.5'::jsonb, 'good', ?, ?)
	`, tagIDs[0], evaluatedAt, evaluatedAt).Error; err != nil {
		t.Fatalf("inserting latest Tag value: %v", err)
	}
	var batchSize int64
	if err := db.Table("data_logger_batches").Select("estimated_size_bytes").Where("logger_id = ?", logger.ID).Limit(1).Scan(&batchSize).Error; err != nil || batchSize <= 0 {
		t.Fatalf("batch size = %d, %v", batchSize, err)
	}
	maxAgeSeconds := int64((3 * time.Hour) / time.Second)
	maxSizeBytes := batchSize * 2
	if err := db.Exec(`ALTER TABLE data_loggers DROP CONSTRAINT data_loggers_max_size_check`).Error; err != nil {
		t.Fatalf("dropping limit constraint for compact retention fixture: %v", err)
	}
	if err := db.Exec("UPDATE data_loggers SET max_age_seconds = ?, max_size_bytes = ? WHERE id = ?", maxAgeSeconds, maxSizeBytes, logger.ID).Error; err != nil {
		t.Fatalf("setting retention policy: %v", err)
	}

	preview, err := history.Retention(ctx, logger.ID, evaluatedAt)
	if err != nil {
		t.Fatalf("Retention() error = %v", err)
	}
	if preview.LastRun != nil || preview.Plan.Current.BatchCount != 5 || preview.Plan.Remove.BatchCount != 3 || preview.Plan.Remove.RowCount != 6 || preview.Plan.EstimatedRetained.BatchCount != 2 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.Plan.CutoffAt == nil || !preview.Plan.CutoffAt.Equal(evaluatedAt.Add(-3*time.Hour)) || preview.Plan.EstimatedRetained.OldestBatchAt == nil || !preview.Plan.EstimatedRetained.OldestBatchAt.Equal(evaluatedAt.Add(-2*time.Hour)) {
		t.Errorf("preview boundaries = %#v", preview.Plan)
	}

	first, err := history.CleanupRetention(ctx, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 2})
	if err != nil {
		t.Fatalf("CleanupRetention(first) error = %v", err)
	}
	if first.Deleted.BatchCount != 2 || first.Deleted.RowCount != 4 || first.Retained.BatchCount != 3 || first.RemainingRemoval.BatchCount != 1 || first.Complete {
		t.Fatalf("first cleanup = %#v", first)
	}
	afterFirst, err := history.Retention(ctx, logger.ID, evaluatedAt)
	if err != nil || afterFirst.LastRun == nil || afterFirst.LastRun.Result == nil || afterFirst.LastRun.Error != nil || afterFirst.LastRun.Result.Deleted.BatchCount != 2 {
		t.Fatalf("status after first cleanup = %#v, %v", afterFirst, err)
	}

	second, err := history.CleanupRetention(ctx, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 2})
	if err != nil || second.Deleted.BatchCount != 1 || second.Retained.BatchCount != 2 || second.RemainingRemoval.BatchCount != 0 || !second.Complete {
		t.Fatalf("second cleanup = %#v, %v", second, err)
	}
	idempotent, err := history.CleanupRetention(ctx, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 2})
	if err != nil || idempotent.Deleted.BatchCount != 0 || idempotent.Retained.BatchCount != 2 || !idempotent.Complete {
		t.Fatalf("idempotent cleanup = %#v, %v", idempotent, err)
	}
	var rawRows, accountingRows, latestRows int64
	if err := db.Table("tag_values_raw").Where("logger_id = ?", logger.ID).Count(&rawRows).Error; err != nil {
		t.Fatalf("counting raw rows: %v", err)
	}
	if err := db.Table("data_logger_batches").Where("logger_id = ?", logger.ID).Count(&accountingRows).Error; err != nil {
		t.Fatalf("counting accounting rows: %v", err)
	}
	if err := db.Table("tag_values_latest").Where("tag_id = ?", tagIDs[0]).Count(&latestRows).Error; err != nil {
		t.Fatalf("counting latest Tag values: %v", err)
	}
	if rawRows != 4 || accountingRows != 2 || latestRows != 1 {
		t.Errorf("retained raw/accounting/latest = %d/%d/%d", rawRows, accountingRows, latestRows)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := history.CleanupRetention(canceledContext, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupRetention(canceled) error = %v", err)
	}
	failedStatus, err := history.Retention(ctx, logger.ID, evaluatedAt)
	if err != nil || failedStatus.LastRun == nil || failedStatus.LastRun.Result != nil || failedStatus.LastRun.Error == nil || *failedStatus.LastRun.Error != "retention cleanup canceled" {
		t.Errorf("failed cleanup status = %#v, %v", failedStatus, err)
	}
}

func TestHistoryRepositoryRollsBackWholeBatchCleanupAndRecordsSanitizedFailure_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	maxAgeSeconds := int64(60)
	logger := datalogger.Logger{Name: "Rollback Retention Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: evaluatedAt.Add(-time.Hour), MaxAgeSeconds: &maxAgeSeconds, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	batchAt := evaluatedAt.Add(-2 * time.Minute)
	if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
		{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: 1.0, Quality: datalogger.RawQualityGood},
		{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "float64", Value: 2.0, Quality: datalogger.RawQualityGood},
	}}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if err := db.Exec(`
		CREATE FUNCTION reject_retention_accounting_delete() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'fixture accounting delete failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_retention_accounting_delete
		BEFORE DELETE ON data_logger_batches
		FOR EACH ROW EXECUTE FUNCTION reject_retention_accounting_delete();
	`).Error; err != nil {
		t.Fatalf("creating rollback fixture trigger: %v", err)
	}
	if _, err := history.CleanupRetention(ctx, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 10}); !errors.Is(err, datalogger.ErrRetentionCleanup) || strings.Contains(err.Error(), "fixture accounting delete failure") {
		t.Fatalf("CleanupRetention() error = %v", err)
	}
	var rawRows, accountingRows int64
	if err := db.Table("tag_values_raw").Where("logger_id = ?", logger.ID).Count(&rawRows).Error; err != nil {
		t.Fatalf("counting raw rows: %v", err)
	}
	if err := db.Table("data_logger_batches").Where("logger_id = ?", logger.ID).Count(&accountingRows).Error; err != nil {
		t.Fatalf("counting accounting rows: %v", err)
	}
	if rawRows != 2 || accountingRows != 1 {
		t.Errorf("rolled-back raw/accounting rows = %d/%d", rawRows, accountingRows)
	}
	status, err := history.Retention(ctx, logger.ID, evaluatedAt)
	if err != nil || status.LastRun == nil || status.LastRun.Result != nil || status.LastRun.Error == nil || *status.LastRun.Error != "retention cleanup failed" {
		t.Errorf("failure status = %#v, %v", status, err)
	}
}

func TestHistoryRepositorySerializesCaptureAndCleanupOnLoggerPolicyLock_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	evaluatedAt := time.Now().UTC().Truncate(time.Second)
	maxAgeSeconds := int64(60)
	logger := datalogger.Logger{Name: "Serialized Retention Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: evaluatedAt.Add(-time.Hour), MaxAgeSeconds: &maxAgeSeconds, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	locked := db.Begin()
	if locked.Error != nil {
		t.Fatalf("beginning lock transaction: %v", locked.Error)
	}
	var lockedID string
	if err := locked.Raw("SELECT id FROM data_loggers WHERE id = ? FOR UPDATE", logger.ID).Scan(&lockedID).Error; err != nil {
		_ = locked.Rollback().Error
		t.Fatalf("locking Logger: %v", err)
	}
	operationContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cleanupDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		_, err := history.CleanupRetention(operationContext, logger.ID, datalogger.RetentionCleanupInput{EvaluatedAt: evaluatedAt, BatchLimit: 10})
		cleanupDone <- err
	}()
	go func() {
		writeDone <- history.WriteBatch(operationContext, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: evaluatedAt, Samples: []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: evaluatedAt, DataType: "float64", Value: 42.5, Quality: datalogger.RawQualityGood},
		}})
	}()
	for name, done := range map[string]<-chan error{"cleanup": cleanupDone, "capture": writeDone} {
		select {
		case err := <-done:
			_ = locked.Rollback().Error
			t.Fatalf("%s completed before Logger lock release: %v", name, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := locked.Commit().Error; err != nil {
		t.Fatalf("releasing Logger lock: %v", err)
	}
	for name, done := range map[string]<-chan error{"cleanup": cleanupDone, "capture": writeDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after lock release error = %v", name, err)
			}
		case <-operationContext.Done():
			t.Fatalf("%s did not complete after Logger lock release: %v", name, operationContext.Err())
		}
	}
	latest, err := history.LatestBatch(ctx, logger.ID)
	if err != nil || !latest.BatchAt.Equal(evaluatedAt) || len(latest.Samples) != 1 {
		t.Errorf("latest batch after serialized operations = %#v, %v", latest, err)
	}
}

func TestHistoryRepositoryRejectsInvalidAndNonSelectedBatchesAtomically_Integration(t *testing.T) {
	db, _ := newRepositoryDatabase(t)
	tagIDs := insertHistoryTags(t, db)
	definitionRepository := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	startAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Validation Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: []byte(`{"interval_seconds":60}`)}
	if err := definitionRepository.Create(ctx, &logger, tagIDs[:2]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	validBool := datalogger.RawSample{TagID: tagIDs[0], ObservedAt: startAt, DataType: "bool", Value: true, Quality: datalogger.RawQualityGood}
	tests := []struct {
		name  string
		batch datalogger.RawBatch
		want  error
	}{
		{name: "nil logger", batch: datalogger.RawBatch{BatchAt: startAt, Samples: []datalogger.RawSample{validBool}}, want: datalogger.ErrInvalidRawBatch},
		{name: "zero batch time", batch: datalogger.RawBatch{LoggerID: logger.ID, Samples: []datalogger.RawSample{validBool}}, want: datalogger.ErrInvalidRawBatch},
		{name: "empty samples", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt}, want: datalogger.ErrInvalidRawBatch},
		{name: "missing logger", batch: datalogger.RawBatch{LoggerID: uuid.New(), BatchAt: startAt, Samples: []datalogger.RawSample{validBool}}, want: datalogger.ErrLoggerNotFound},
		{name: "duplicate Tag", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{validBool, validBool}}, want: datalogger.ErrInvalidRawBatch},
		{name: "unselected Tag", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[2], ObservedAt: startAt, DataType: "uint16", Value: uint16(1), Quality: datalogger.RawQualityGood}}}, want: datalogger.ErrRawTagNotSelected},
		{name: "data type mismatch", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: startAt, DataType: "float64", Value: 1.0, Quality: datalogger.RawQualityGood}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "zero observation", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], DataType: "bool", Value: true, Quality: datalogger.RawQualityGood}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "unsupported type", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: startAt, DataType: "string", Value: "x", Quality: datalogger.RawQualityGood}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "good error", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: startAt, DataType: "bool", Value: true, Quality: datalogger.RawQualityGood, Error: "stale"}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "bad value", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: startAt, DataType: "bool", Value: true, Quality: datalogger.RawQualityBad, Error: "failed"}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "bad no error", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[0], ObservedAt: startAt, DataType: "bool", Quality: datalogger.RawQualityBad}}}, want: datalogger.ErrInvalidRawBatch},
		{name: "non-finite", batch: datalogger.RawBatch{LoggerID: logger.ID, BatchAt: startAt, Samples: []datalogger.RawSample{{TagID: tagIDs[1], ObservedAt: startAt, DataType: "int16", Value: math.Inf(1), Quality: datalogger.RawQualityGood}}}, want: datalogger.ErrInvalidRawBatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := history.WriteBatch(ctx, test.batch); !errors.Is(err, test.want) {
				t.Fatalf("WriteBatch() error = %v, want %v", err, test.want)
			}
		})
	}

	atomicAt := startAt.Add(time.Hour)
	err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: atomicAt, Samples: []datalogger.RawSample{
		validBool,
		{TagID: tagIDs[1], ObservedAt: startAt, DataType: "int16", Value: 32768, Quality: datalogger.RawQualityGood},
	}})
	if !errors.Is(err, datalogger.ErrInvalidRawBatch) {
		t.Fatalf("WriteBatch(invalid payload) error = %v", err)
	}
	result, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID})
	if err != nil || result.Total != 0 {
		t.Errorf("history after rolled back batch = %#v, error = %v", result, err)
	}
	if _, err := history.ListValues(ctx, datalogger.RawValueListInput{}); !errors.Is(err, datalogger.ErrInvalidRawBatch) {
		t.Errorf("ListValues(nil logger) error = %v", err)
	}
	from, to := startAt.Add(time.Hour), startAt
	if _, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID, From: &from, To: &to}); !errors.Is(err, datalogger.ErrInvalidRawBatch) {
		t.Errorf("ListValues(invalid range) error = %v", err)
	}
	if _, err := history.ListValues(ctx, datalogger.RawValueListInput{LoggerID: logger.ID, PerPage: 501}); !errors.Is(err, datalogger.ErrInvalidRawBatch) {
		t.Errorf("ListValues(per page) error = %v", err)
	}
}

func insertHistoryTags(t *testing.T, db *gorm.DB) []uuid.UUID {
	t.Helper()
	dataTypes := []string{"bool", "int16", "uint16", "int32", "uint32", "float32", "float64"}
	tagIDs := make([]uuid.UUID, 0, len(dataTypes))
	for index, dataType := range dataTypes {
		tagID := uuid.New()
		if err := db.Exec(`INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,'constant',?,true,'{}')`, tagID, "History Tag "+string(rune('A'+index)), dataType).Error; err != nil {
			t.Fatalf("inserting %s Tag: %v", dataType, err)
		}
		tagIDs = append(tagIDs, tagID)
	}
	return tagIDs
}

func assertPartitionRows(t *testing.T, db *gorm.DB, table string, want int) {
	t.Helper()
	var count int64
	if err := db.Table(table).Count(&count).Error; err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	if count != int64(want) {
		t.Errorf("%s rows = %d, want %d", table, count, want)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
