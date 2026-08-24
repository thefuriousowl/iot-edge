package dataloggerpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestRetentionRuntimeReconcilesDisabledLoggerAcrossBoundedRestarts_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	repository := NewRepository(db)
	history := NewHistoryRepository(db)
	service, err := datalogger.NewService(repository, history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	logger := datalogger.Logger{Name: "Restart Retention Logger", Enabled: false, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: now.Add(-time.Hour), Config: []byte(`{"interval_seconds":60}`)}
	if err := repository.Create(ctx, &logger, tagIDs[:1]); err != nil {
		t.Fatalf("Create(logger) error = %v", err)
	}
	for minutesAgo := 4; minutesAgo >= 2; minutesAgo-- {
		batchAt := now.Add(-time.Duration(minutesAgo) * time.Minute)
		if err := history.WriteBatch(ctx, datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: float64(minutesAgo), Quality: datalogger.RawQualityGood},
		}}); err != nil {
			t.Fatalf("WriteBatch(%s) error = %v", batchAt, err)
		}
	}
	maxAgeSeconds := int64(60)
	if err := db.Model(&datalogger.Logger{}).Where("id = ?", logger.ID).Update("max_age_seconds", maxAgeSeconds).Error; err != nil {
		t.Fatalf("setting age policy: %v", err)
	}

	for run, wantBatches := range []int64{2, 1, 0} {
		runtime, err := datalogger.NewRetentionRuntime(
			repository,
			service,
			datalogger.WithRetentionRuntimeInterval(0),
			datalogger.WithRetentionRuntimeBatchLimit(1),
		)
		if err != nil {
			t.Fatalf("NewRetentionRuntime(run %d) error = %v", run+1, err)
		}
		if err := runtime.Start(ctx); err != nil {
			t.Fatalf("Start(run %d) error = %v", run+1, err)
		}
		awaitRetentionBatchCount(t, history, logger.ID, wantBatches)
		runtime.Stop()
		status, err := history.Retention(ctx, logger.ID, time.Now().UTC())
		if err != nil || status.LastRun == nil || status.LastRun.Result == nil || status.LastRun.Error != nil || status.LastRun.Result.Deleted.BatchCount != 1 {
			t.Fatalf("retention status after run %d = %#v, %v", run+1, status, err)
		}
		if status.LastRun.Result.Complete != (wantBatches == 0) {
			t.Errorf("run %d complete = %t, want %t", run+1, status.LastRun.Result.Complete, wantBatches == 0)
		}
	}
}

func awaitRetentionBatchCount(t *testing.T, history datalogger.HistoryRepository, loggerID uuid.UUID, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		storage, err := history.Storage(context.Background(), loggerID, 1, nil)
		if err == nil && storage.BatchCount == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	storage, err := history.Storage(context.Background(), loggerID, 1, nil)
	t.Fatalf("retention batch count = %#v, %v; want %d", storage, err, want)
}
