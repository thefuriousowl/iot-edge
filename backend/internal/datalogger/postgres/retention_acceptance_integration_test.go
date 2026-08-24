package dataloggerpostgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"gorm.io/gorm"
)

func TestRetentionAcceptancePoliciesPreserveWholeBatchesWithoutReadingTagSnapshots_Integration(t *testing.T) {
	db, tagIDs := newRepositoryDatabase(t)
	definitions := NewRepository(db)
	history := NewHistoryRepository(db)
	ctx := context.Background()
	evaluatedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	batchTimes := []time.Time{
		evaluatedAt.Add(-4 * time.Hour),
		evaluatedAt.Add(-3 * time.Hour),
		evaluatedAt.Add(-2 * time.Hour),
		evaluatedAt.Add(-time.Hour),
	}
	if err := db.Exec(`ALTER TABLE data_loggers DROP CONSTRAINT data_loggers_max_size_check`).Error; err != nil {
		t.Fatalf("dropping storage constraint for compact acceptance fixtures: %v", err)
	}

	tests := []struct {
		name              string
		policy            func(int64) datalogger.RetentionPolicy
		wantCutoff        *time.Time
		wantRemove        int64
		wantRetained      int64
		cleanupBatchLimit int
	}{
		{
			name: "unlimited",
			policy: func(int64) datalogger.RetentionPolicy {
				return datalogger.RetentionPolicy{}
			},
			wantRemove: 0, wantRetained: 4, cleanupBatchLimit: 1,
		},
		{
			name: "age only keeps exact cutoff",
			policy: func(int64) datalogger.RetentionPolicy {
				maxAgeSeconds := int64((2 * time.Hour) / time.Second)
				return datalogger.RetentionPolicy{MaxAgeSeconds: &maxAgeSeconds}
			},
			wantCutoff: timePointer(evaluatedAt.Add(-2 * time.Hour)),
			wantRemove: 2, wantRetained: 2, cleanupBatchLimit: 10,
		},
		{
			name: "size only keeps newest capacity",
			policy: func(batchSize int64) datalogger.RetentionPolicy {
				maxSizeBytes := batchSize * 2
				return datalogger.RetentionPolicy{MaxSizeBytes: &maxSizeBytes}
			},
			wantRemove: 2, wantRetained: 2, cleanupBatchLimit: 10,
		},
		{
			name: "combined policy applies stricter age limit in bounded passes",
			policy: func(batchSize int64) datalogger.RetentionPolicy {
				maxSizeBytes := batchSize * 3
				maxAgeSeconds := int64((2 * time.Hour) / time.Second)
				return datalogger.RetentionPolicy{MaxSizeBytes: &maxSizeBytes, MaxAgeSeconds: &maxAgeSeconds}
			},
			wantCutoff: timePointer(evaluatedAt.Add(-2 * time.Hour)),
			wantRemove: 2, wantRetained: 2, cleanupBatchLimit: 1,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := datalogger.Logger{
				Name:     fmt.Sprintf("Retention acceptance %d", index),
				Enabled:  true,
				Timezone: "UTC",
				Mode:     datalogger.ModeInterval,
				StartAt:  evaluatedAt.Add(-24 * time.Hour),
				Config:   []byte(`{"interval_seconds":60}`),
			}
			if err := definitions.Create(ctx, &logger, tagIDs[:2]); err != nil {
				t.Fatalf("Create(logger) error = %v", err)
			}
			stored, err := definitions.Find(ctx, logger.ID)
			if err != nil {
				t.Fatalf("Find(logger) error = %v", err)
			}
			snapshots := &retentionAcceptanceSnapshots{values: map[uuid.UUID]datalogger.SnapshotValue{
				tagIDs[0]: {DataType: "float64", Value: 42.5, Quality: datalogger.RawQualityGood},
				tagIDs[1]: {DataType: "float64", Value: 7.25, Quality: datalogger.RawQualityGood},
			}}
			scheduler, err := datalogger.NewIntervalScheduler(snapshots, history)
			if err != nil {
				t.Fatalf("NewIntervalScheduler() error = %v", err)
			}
			for _, batchAt := range batchTimes {
				snapshots.observedAt = batchAt
				if err := scheduler.Capture(ctx, *stored, batchAt); err != nil {
					t.Fatalf("Capture(%s) error = %v", batchAt, err)
				}
			}
			captureReads := snapshots.calls
			if captureReads != len(batchTimes) {
				t.Fatalf("Tag snapshot reads after capture = %d, want %d", captureReads, len(batchTimes))
			}

			batchSize := acceptanceBatchSize(t, db, logger.ID)
			policy := test.policy(batchSize)
			if err := db.Model(&datalogger.Logger{}).Where("id = ?", logger.ID).Updates(map[string]any{
				"max_size_bytes":  policy.MaxSizeBytes,
				"max_age_seconds": policy.MaxAgeSeconds,
			}).Error; err != nil {
				t.Fatalf("setting retention policy: %v", err)
			}

			status, err := history.Retention(ctx, logger.ID, evaluatedAt)
			if err != nil {
				t.Fatalf("Retention() error = %v", err)
			}
			if status.Plan.Current.BatchCount != int64(len(batchTimes)) || status.Plan.Remove.BatchCount != test.wantRemove || status.Plan.EstimatedRetained.BatchCount != test.wantRetained {
				t.Fatalf("retention preview = %#v", status.Plan)
			}
			if !equalAcceptanceTime(status.Plan.CutoffAt, test.wantCutoff) {
				t.Errorf("cutoff = %v, want %v", status.Plan.CutoffAt, test.wantCutoff)
			}
			if snapshots.calls != captureReads {
				t.Fatalf("preview read Tag snapshots: calls %d -> %d", captureReads, snapshots.calls)
			}

			var deletedBatches int64
			for pass := 1; pass <= len(batchTimes)+1; pass++ {
				result, cleanupErr := history.CleanupRetention(ctx, logger.ID, datalogger.RetentionCleanupInput{
					EvaluatedAt: evaluatedAt,
					BatchLimit:  test.cleanupBatchLimit,
				})
				if cleanupErr != nil {
					t.Fatalf("CleanupRetention(pass %d) error = %v", pass, cleanupErr)
				}
				deletedBatches += result.Deleted.BatchCount
				if snapshots.calls != captureReads {
					t.Fatalf("cleanup pass %d read Tag snapshots: calls %d -> %d", pass, captureReads, snapshots.calls)
				}
				if result.Complete {
					break
				}
				if pass == len(batchTimes)+1 {
					t.Fatal("cleanup did not complete within the fixture batch count")
				}
			}
			if deletedBatches != test.wantRemove {
				t.Errorf("deleted batches = %d, want %d", deletedBatches, test.wantRemove)
			}
			assertAcceptanceWholeBatches(t, db, logger.ID, test.wantRetained, int64(len(stored.Tags)))

			finalStatus, err := history.Retention(ctx, logger.ID, evaluatedAt)
			if err != nil || finalStatus.Plan.Remove.BatchCount != 0 || finalStatus.Plan.Current.BatchCount != test.wantRetained || finalStatus.LastRun == nil || finalStatus.LastRun.Result == nil || !finalStatus.LastRun.Result.Complete || finalStatus.LastRun.Error != nil {
				t.Errorf("final retention status = %#v, %v", finalStatus, err)
			}
			if test.wantCutoff != nil {
				assertAcceptanceBatchExists(t, db, logger.ID, *test.wantCutoff)
			}
		})
	}
}

type retentionAcceptanceSnapshots struct {
	calls      int
	observedAt time.Time
	values     map[uuid.UUID]datalogger.SnapshotValue
}

func (snapshots *retentionAcceptanceSnapshots) Snapshot(tagIDs []uuid.UUID) map[uuid.UUID]datalogger.SnapshotValue {
	snapshots.calls++
	result := make(map[uuid.UUID]datalogger.SnapshotValue, len(tagIDs))
	for _, tagID := range tagIDs {
		value, exists := snapshots.values[tagID]
		if !exists {
			continue
		}
		value.ObservedAt = snapshots.observedAt
		result[tagID] = value
	}
	return result
}

func acceptanceBatchSize(t *testing.T, db *gorm.DB, loggerID uuid.UUID) int64 {
	t.Helper()
	var size int64
	if err := db.Table("data_logger_batches").Select("estimated_size_bytes").Where("logger_id = ?", loggerID).Order("batch_at").Limit(1).Scan(&size).Error; err != nil || size <= 0 {
		t.Fatalf("acceptance batch size = %d, %v", size, err)
	}
	return size
}

func assertAcceptanceWholeBatches(t *testing.T, db *gorm.DB, loggerID uuid.UUID, wantBatches, tagsPerBatch int64) {
	t.Helper()
	type batchCount struct {
		BatchAt  time.Time
		RowCount int64
	}
	var batches []batchCount
	if err := db.Table("tag_values_raw").Select("batch_at, COUNT(*) AS row_count").Where("logger_id = ?", loggerID).Group("batch_at").Order("batch_at").Scan(&batches).Error; err != nil {
		t.Fatalf("grouping retained batches: %v", err)
	}
	if int64(len(batches)) != wantBatches {
		t.Fatalf("retained raw batches = %d, want %d", len(batches), wantBatches)
	}
	for _, batch := range batches {
		if batch.RowCount != tagsPerBatch {
			t.Errorf("retained batch %s has %d rows, want complete %d", batch.BatchAt, batch.RowCount, tagsPerBatch)
		}
	}
	var accountingRows int64
	if err := db.Table("data_logger_batches").Where("logger_id = ?", loggerID).Count(&accountingRows).Error; err != nil {
		t.Fatalf("counting retained accounting rows: %v", err)
	}
	if accountingRows != wantBatches {
		t.Errorf("retained accounting batches = %d, want %d", accountingRows, wantBatches)
	}
}

func assertAcceptanceBatchExists(t *testing.T, db *gorm.DB, loggerID uuid.UUID, batchAt time.Time) {
	t.Helper()
	var count int64
	if err := db.Table("data_logger_batches").Where("logger_id = ? AND batch_at = ?", loggerID, batchAt).Count(&count).Error; err != nil {
		t.Fatalf("counting exact-cutoff batch: %v", err)
	}
	if count != 1 {
		t.Errorf("exact-cutoff batch count = %d, want 1", count)
	}
}

func equalAcceptanceTime(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}
