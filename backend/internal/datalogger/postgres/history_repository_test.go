package dataloggerpostgres

import (
	"context"
	"errors"
	"math"
	"reflect"
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
