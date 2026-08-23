package energy

import (
	"context"
	"fmt"
	"os"
	goruntime "runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	pluginpostgres "github.com/thefuriousowl/iot-edge/internal/plugin/postgres"
	"gorm.io/gorm"
)

const (
	energyPerformanceInterval       = time.Minute
	energyPerformanceMax30Day       = 2 * time.Second
	energyPerformanceMax366Day      = 8 * time.Second
	energyPerformanceMaxHeap30Day   = 256 << 20
	energyPerformanceMaxHeap366Day  = 768 << 20
	energyPerformanceMaxAlloc30Day  = 1 << 30
	energyPerformanceMaxAlloc366Day = 8 << 30
	energyPerformanceMemoryLimit    = 768 << 20
	energyPerformanceCPUs           = 2
)

func TestEnergyRawHistoryPerformance_Integration(t *testing.T) {
	if os.Getenv("RUN_ENERGY_PERFORMANCE_TESTS") != "1" {
		t.Skip("set RUN_ENERGY_PERFORMANCE_TESTS=1 to run the Energy performance gate")
	}
	previousCPUs := goruntime.GOMAXPROCS(energyPerformanceCPUs)
	previousMemoryLimit := debug.SetMemoryLimit(energyPerformanceMemoryLimit)
	t.Cleanup(func() {
		goruntime.GOMAXPROCS(previousCPUs)
		debug.SetMemoryLimit(previousMemoryLimit)
	})
	database := newEnergyIntegrationDatabase(t)
	to := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	from := to.Add(-366 * 24 * time.Hour)
	loggerID, instanceID := seedEnergyPerformanceHistory(t, database, from.Add(-energyPerformanceInterval), to.Add(energyPerformanceInterval))
	history := dataloggerpostgres.NewHistoryRepository(database)
	service, err := NewService(pluginpostgres.NewRepository(database), history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tests := []struct {
		name       string
		from       time.Time
		maxElapsed time.Duration
		maxHeap    uint64
		maxAlloc   uint64
		wantRows   int
	}{
		{name: "30 days", from: to.Add(-30 * 24 * time.Hour), maxElapsed: energyPerformanceMax30Day, maxHeap: energyPerformanceMaxHeap30Day, maxAlloc: energyPerformanceMaxAlloc30Day, wantRows: 30},
		{name: "366 days", from: from, maxElapsed: energyPerformanceMax366Day, maxHeap: energyPerformanceMaxHeap366Day, maxAlloc: energyPerformanceMaxAlloc366Day, wantRows: 366},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			goruntime.GC()
			var before goruntime.MemStats
			goruntime.ReadMemStats(&before)
			startedAt := time.Now()
			result, queryErr := service.History(context.Background(), instanceID, HistoryInput{
				From: test.from, To: to, Bucket: datalogger.QueryBucket1Day, Page: 1, PerPage: 500,
			})
			elapsed := time.Since(startedAt)
			var after goruntime.MemStats
			goruntime.ReadMemStats(&after)
			if queryErr != nil {
				t.Fatalf("History() error = %v", queryErr)
			}
			if result.LoggerID != loggerID || len(result.Data) != test.wantRows || result.Total != test.wantRows {
				t.Fatalf("History() result = logger %s, rows %d, total %d", result.LoggerID, len(result.Data), result.Total)
			}
			heapGrowth := positiveDifference(after.HeapAlloc, before.HeapAlloc)
			allocated := positiveDifference(after.TotalAlloc, before.TotalAlloc)
			t.Logf("raw Energy history %s: elapsed=%s heap_growth=%.1fMiB allocated=%.1fMiB", test.name, elapsed, bytesToMiB(heapGrowth), bytesToMiB(allocated))
			if elapsed > test.maxElapsed {
				t.Errorf("elapsed %s exceeds %s raw-history gate", elapsed, test.maxElapsed)
			}
			if heapGrowth > test.maxHeap {
				t.Errorf("heap growth %.1fMiB exceeds %.1fMiB raw-history gate", bytesToMiB(heapGrowth), bytesToMiB(test.maxHeap))
			}
			if allocated > test.maxAlloc {
				t.Errorf("allocated %.1fMiB exceeds %.1fMiB raw-history gate", bytesToMiB(allocated), bytesToMiB(test.maxAlloc))
			}
		})
	}
}

func seedEnergyPerformanceHistory(t *testing.T, database *gorm.DB, from, to time.Time) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tagIDs := make([]uuid.UUID, 3)
	for index := range tagIDs {
		tagIDs[index] = uuid.New()
		if err := database.Exec("INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,'constant','float64',true,'{}')", tagIDs[index], fmt.Sprintf("Performance Energy Tag %d", index+1)).Error; err != nil {
			t.Fatalf("inserting performance Tag %d: %v", index+1, err)
		}
	}
	logger := datalogger.Logger{
		Name: "Energy Performance Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval,
		StartAt: from, Config: []byte(`{"interval_seconds":60}`),
	}
	if err := dataloggerpostgres.NewRepository(database).Create(ctx, &logger, tagIDs); err != nil {
		t.Fatalf("creating performance Logger: %v", err)
	}
	createEnergyPerformancePartitions(t, database, from, to)
	if err := database.Exec(`
		INSERT INTO data_logger_batches (logger_id, batch_at, row_count, estimated_size_bytes)
		SELECT ?, batch_at, ?, ?
		FROM generate_series(?::timestamptz, ?::timestamptz, '1 minute'::interval) AS batch_at`,
		logger.ID, len(tagIDs), len(tagIDs)*256, from, to,
	).Error; err != nil {
		t.Fatalf("inserting performance batches: %v", err)
	}
	for index, tagID := range tagIDs {
		if err := database.Exec(`
			INSERT INTO tag_values_raw (logger_id, tag_id, batch_at, observed_at, data_type, value, quality)
			SELECT ?, ?, batch_at, batch_at, 'float64',
				to_jsonb((?::double precision + MOD(EXTRACT(EPOCH FROM batch_at)::bigint / 60, 100))::double precision),
				'good'
			FROM generate_series(?::timestamptz, ?::timestamptz, '1 minute'::interval) AS batch_at`,
			logger.ID, tagID, float64((index+1)*10), from, to,
		).Error; err != nil {
			t.Fatalf("inserting performance values for Tag %d: %v", index+1, err)
		}
	}
	if err := database.Exec("ANALYZE data_logger_batches").Error; err != nil {
		t.Fatalf("analyzing performance batches: %v", err)
	}
	if err := database.Exec("ANALYZE tag_values_raw").Error; err != nil {
		t.Fatalf("analyzing performance values: %v", err)
	}
	config := Config{
		LoggerID:            logger.ID,
		ElectricalPowerTags: []PowerTag{{TagID: tagIDs[0], Unit: PowerUnitKW}, {TagID: tagIDs[1], Unit: PowerUnitKW}},
		ThermalPowerTags:    []PowerTag{{TagID: tagIDs[2], Unit: PowerUnitKW}},
		Timezone:            "UTC", MaxGapSeconds: 120,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4.5},
	}
	instance := plugin.Instance{Type: PluginType, Name: "Energy Performance", Config: encodeEnergyConfig(t, config), ConfigVersion: 1}
	if err := pluginpostgres.NewRepository(database).Create(ctx, &instance); err != nil {
		t.Fatalf("creating performance Plugin: %v", err)
	}
	return logger.ID, instance.ID
}

func createEnergyPerformancePartitions(t *testing.T, database *gorm.DB, from, to time.Time) {
	t.Helper()
	month := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !month.After(to) {
		next := month.AddDate(0, 1, 0)
		name := fmt.Sprintf("tag_values_raw_%04d_%02d", month.Year(), month.Month())
		statement := fmt.Sprintf(
			`CREATE TABLE %s PARTITION OF tag_values_raw FOR VALUES FROM ('%s') TO ('%s')`,
			name, month.Format(time.RFC3339), next.Format(time.RFC3339),
		)
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("creating partition %s: %v", name, err)
		}
		month = next
	}
}

func positiveDifference(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func bytesToMiB(value uint64) float64 {
	return float64(value) / (1 << 20)
}
