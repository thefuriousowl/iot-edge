package dataloggerpostgres

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"gorm.io/gorm"
)

const (
	queryPerformanceTagCount     = 16
	queryPerformanceRawMax       = 5 * time.Second
	queryPerformanceAggregateMax = 8 * time.Second
	queryPerformanceMaxHeap      = 256 << 20
	queryPerformanceMaxAllocated = 1 << 30
	queryPerformanceMemoryLimit  = 768 << 20
	queryPerformanceCPUs         = 2
)

func TestHistoryQueryPerformance_Integration(t *testing.T) {
	if os.Getenv("RUN_DATALOGGER_PERFORMANCE_TESTS") != "1" {
		t.Skip("set RUN_DATALOGGER_PERFORMANCE_TESTS=1 to run the Data Logger query performance gate")
	}
	previousCPUs := runtime.GOMAXPROCS(queryPerformanceCPUs)
	previousMemoryLimit := debug.SetMemoryLimit(queryPerformanceMemoryLimit)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousCPUs)
		debug.SetMemoryLimit(previousMemoryLimit)
	})
	database, _ := newRepositoryDatabase(t)
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	loggerID, tagIDs := seedQueryPerformanceHistory(t, database, from, to)
	history := NewHistoryRepository(database)

	tests := []struct {
		name       string
		input      datalogger.QueryInput
		maxElapsed time.Duration
		wantRows   int
		wantTotal  int64
	}{
		{
			name: "raw 31 days", maxElapsed: queryPerformanceRawMax, wantRows: 100, wantTotal: 31 * 24 * 60,
			input: datalogger.QueryInput{LoggerID: loggerID, TagIDs: tagIDs, From: from, To: to, Mode: datalogger.QueryModeRaw, Page: 1, PerPage: 100},
		},
		{
			name: "hourly aggregate 31 days", maxElapsed: queryPerformanceAggregateMax, wantRows: 500, wantTotal: 31 * 24,
			input: datalogger.QueryInput{LoggerID: loggerID, TagIDs: tagIDs, From: from, To: to, Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket1Hour, Aggregate: datalogger.AggregateAvg, Page: 1, PerPage: 500},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := history.Query(context.Background(), test.input); err != nil {
				t.Fatalf("warming Query() error = %v", err)
			}
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			startedAt := time.Now()
			result, err := history.Query(context.Background(), test.input)
			elapsed := time.Since(startedAt)
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}
			if len(result.Data) != test.wantRows || result.Total != test.wantTotal {
				t.Fatalf("Query() rows/total = %d/%d, want %d/%d", len(result.Data), result.Total, test.wantRows, test.wantTotal)
			}
			heapGrowth := positiveQueryDifference(after.HeapAlloc, before.HeapAlloc)
			allocated := positiveQueryDifference(after.TotalAlloc, before.TotalAlloc)
			t.Logf("%s: elapsed=%s heap_growth=%.1fMiB allocated=%.1fMiB", test.name, elapsed, queryBytesToMiB(heapGrowth), queryBytesToMiB(allocated))
			if elapsed > test.maxElapsed {
				t.Errorf("elapsed %s exceeds %s query gate", elapsed, test.maxElapsed)
			}
			if heapGrowth > queryPerformanceMaxHeap {
				t.Errorf("heap growth %.1fMiB exceeds %.1fMiB query gate", queryBytesToMiB(heapGrowth), queryBytesToMiB(queryPerformanceMaxHeap))
			}
			if allocated > queryPerformanceMaxAllocated {
				t.Errorf("allocated %.1fMiB exceeds %.1fMiB query gate", queryBytesToMiB(allocated), queryBytesToMiB(queryPerformanceMaxAllocated))
			}
		})
	}
}

func seedQueryPerformanceHistory(t *testing.T, database *gorm.DB, from, to time.Time) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	tagIDs := make([]uuid.UUID, queryPerformanceTagCount)
	for index := range tagIDs {
		tagIDs[index] = uuid.New()
		if err := database.Exec("INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?, 'constant','float64',true,'{}')", tagIDs[index], fmt.Sprintf("Query Performance Tag %02d", index+1)).Error; err != nil {
			t.Fatalf("inserting performance Tag %d: %v", index+1, err)
		}
	}
	logger := datalogger.Logger{Name: "Query Performance Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: from, Config: []byte(`{"interval_seconds":60}`)}
	if err := NewRepository(database).Create(context.Background(), &logger, tagIDs); err != nil {
		t.Fatalf("creating performance Logger: %v", err)
	}
	partitionName := "tag_values_raw_2026_07"
	if err := database.Exec(fmt.Sprintf(
		"CREATE TABLE %s PARTITION OF tag_values_raw FOR VALUES FROM ('%s') TO ('%s')",
		partitionName, from.Format(time.RFC3339), to.Format(time.RFC3339),
	)).Error; err != nil {
		t.Fatalf("creating performance partition: %v", err)
	}
	lastBatchAt := to.Add(-time.Minute)
	if err := database.Exec(`
		INSERT INTO data_logger_batches (logger_id, batch_at, row_count, estimated_size_bytes)
		SELECT ?, batch_at, ?, ?
		FROM generate_series(?::timestamptz, ?::timestamptz, '1 minute'::interval) AS batch_at
	`, logger.ID, len(tagIDs), len(tagIDs)*256, from, lastBatchAt).Error; err != nil {
		t.Fatalf("inserting performance batches: %v", err)
	}
	for index, tagID := range tagIDs {
		if err := database.Exec(`
			INSERT INTO tag_values_raw (logger_id, tag_id, batch_at, observed_at, data_type, value, quality)
			SELECT ?, ?, batch_at, batch_at, 'float64',
				to_jsonb((?::double precision + MOD(EXTRACT(EPOCH FROM batch_at)::bigint / 60, 100))::double precision),
				'good'
			FROM generate_series(?::timestamptz, ?::timestamptz, '1 minute'::interval) AS batch_at
		`, logger.ID, tagID, float64((index+1)*10), from, lastBatchAt).Error; err != nil {
			t.Fatalf("inserting performance values for Tag %d: %v", index+1, err)
		}
	}
	if err := database.Exec("ANALYZE data_logger_batches").Error; err != nil {
		t.Fatalf("analyzing performance batches: %v", err)
	}
	if err := database.Exec("ANALYZE tag_values_raw").Error; err != nil {
		t.Fatalf("analyzing performance values: %v", err)
	}
	return logger.ID, tagIDs
}

func positiveQueryDifference(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func queryBytesToMiB(value uint64) float64 {
	return float64(value) / (1 << 20)
}
