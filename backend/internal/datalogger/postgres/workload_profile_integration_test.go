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
)

const (
	workloadConfiguredTags = 500
	workloadActiveTags     = 100
	workloadSeconds        = 600
)

func TestMeasuredWorkloadProfile_Integration(t *testing.T) {
	if os.Getenv("RUN_WORKLOAD_PROFILE_TESTS") != "1" {
		t.Skip("set RUN_WORKLOAD_PROFILE_TESTS=1 to run the measured workload profile")
	}
	previousCPUs := runtime.GOMAXPROCS(2)
	previousMemoryLimit := debug.SetMemoryLimit(768 << 20)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousCPUs); debug.SetMemoryLimit(previousMemoryLimit) })

	database, _ := newRepositoryDatabase(t)
	if err := database.Exec("DELETE FROM tags").Error; err != nil {
		t.Fatalf("clearing helper Tags: %v", err)
	}
	configuredAt := time.Now()
	tagIDs := make([]uuid.UUID, workloadConfiguredTags)
	for index := range tagIDs {
		tagIDs[index] = uuid.New()
		if err := database.Exec("INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?, 'constant','float64',true,'{}')", tagIDs[index], fmt.Sprintf("Workload Tag %03d", index+1)).Error; err != nil {
			t.Fatalf("inserting configured Tag %d: %v", index+1, err)
		}
	}
	configureElapsed := time.Since(configuredAt)

	from := time.Date(2026, time.August, 28, 0, 0, 0, 0, time.UTC)
	to := from.Add(workloadSeconds * time.Second)
	logger := datalogger.Logger{Name: "Measured 1-second Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: from, Config: []byte(`{"interval_seconds":1}`)}
	if err := NewRepository(database).Create(context.Background(), &logger, tagIDs[:workloadActiveTags]); err != nil {
		t.Fatalf("creating workload Logger: %v", err)
	}
	if err := database.Exec("CREATE TABLE tag_values_raw_2026_08 PARTITION OF tag_values_raw FOR VALUES FROM ('2026-08-01T00:00:00Z') TO ('2026-09-01T00:00:00Z')").Error; err != nil {
		t.Fatalf("creating workload partition: %v", err)
	}

	seededAt := time.Now()
	if err := database.Exec(`INSERT INTO data_logger_batches (logger_id,batch_at,row_count,estimated_size_bytes)
		SELECT ?, batch_at, ?, ? FROM generate_series(?::timestamptz, ?::timestamptz, '1 second'::interval) AS batch_at`,
		logger.ID, workloadActiveTags, workloadActiveTags*256, from, to.Add(-time.Second)).Error; err != nil {
		t.Fatalf("inserting workload batches: %v", err)
	}
	for index, tagID := range tagIDs[:workloadActiveTags] {
		if err := database.Exec(`INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality)
			SELECT ?, ?, batch_at, batch_at, 'float64', to_jsonb((?::double precision + MOD(EXTRACT(EPOCH FROM batch_at)::bigint,100))::double precision), 'good'
			FROM generate_series(?::timestamptz, ?::timestamptz, '1 second'::interval) AS batch_at`,
			logger.ID, tagID, float64(index), from, to.Add(-time.Second)).Error; err != nil {
			t.Fatalf("inserting active Tag %d: %v", index+1, err)
		}
	}
	seedElapsed := time.Since(seededAt)
	if err := database.Exec("ANALYZE data_logger_batches; ANALYZE tag_values_raw").Error; err != nil {
		t.Fatalf("analyzing workload: %v", err)
	}

	history := NewHistoryRepository(database)
	rawInput := datalogger.QueryInput{LoggerID: logger.ID, TagIDs: tagIDs[:workloadActiveTags], From: from, To: to, Mode: datalogger.QueryModeRaw, Page: 1, PerPage: 500}
	rawAt := time.Now()
	raw, err := history.Query(context.Background(), rawInput)
	rawElapsed := time.Since(rawAt)
	if err != nil || len(raw.Data) != 500 || raw.Total != workloadSeconds {
		t.Fatalf("raw query rows/total/error = %d/%d/%v", len(raw.Data), raw.Total, err)
	}
	aggregateInput := rawInput
	aggregateInput.Mode = datalogger.QueryModeAggregate
	aggregateInput.Bucket = datalogger.QueryBucket1Minute
	aggregateInput.Aggregate = datalogger.AggregateAvg
	aggregateAt := time.Now()
	aggregate, err := history.Query(context.Background(), aggregateInput)
	aggregateElapsed := time.Since(aggregateAt)
	if err != nil || len(aggregate.Data) != 10 || aggregate.Total != 10 {
		t.Fatalf("aggregate query rows/total/error = %d/%d/%v", len(aggregate.Data), aggregate.Total, err)
	}

	exportAt := time.Now()
	exported := 0
	for page := 1; ; page++ {
		exportInput := rawInput
		exportInput.Page = page
		result, queryErr := history.Query(context.Background(), exportInput)
		if queryErr != nil {
			t.Fatalf("export page %d: %v", page, queryErr)
		}
		exported += len(result.Data)
		if int64(exported) >= result.Total {
			break
		}
	}
	exportElapsed := time.Since(exportAt)
	var databaseBytes int64
	if err := database.Raw("SELECT pg_total_relation_size('tag_values_raw_2026_08') + pg_total_relation_size('data_logger_batches')").Scan(&databaseBytes).Error; err != nil {
		t.Fatalf("measuring database footprint: %v", err)
	}

	t.Logf("WORKLOAD configured_tags=%d active_tags=%d interval=1s batches=%d samples=%d configure_ms=%d seed_ms=%d raw_ms=%d aggregate_ms=%d export_ms=%d database_bytes=%d",
		workloadConfiguredTags, workloadActiveTags, workloadSeconds, workloadSeconds*workloadActiveTags, configureElapsed.Milliseconds(), seedElapsed.Milliseconds(), rawElapsed.Milliseconds(), aggregateElapsed.Milliseconds(), exportElapsed.Milliseconds(), databaseBytes)
	if configureElapsed > 10*time.Second || seedElapsed > 30*time.Second || rawElapsed > 5*time.Second || aggregateElapsed > 8*time.Second || exportElapsed > 10*time.Second {
		t.Error("measured workload exceeded its conservative Windows timing gate")
	}
	if databaseBytes > 512<<20 {
		t.Errorf("database footprint %d exceeds 512 MiB gate", databaseBytes)
	}
}
