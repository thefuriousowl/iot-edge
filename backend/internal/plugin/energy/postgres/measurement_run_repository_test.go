package energypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMeasurementRunRepositoryAtomicLifecycle_Integration(t *testing.T) {
	database := newMeasurementRunDatabase(t)
	pluginID := createEnergyPlugin(t, database)
	repository := NewMeasurementRunRepository(database)
	ctx := context.Background()
	startedAt := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	config := json.RawMessage(`{"logger_id":"11111111-1111-1111-1111-111111111111"}`)

	current, err := repository.Start(ctx, energy.StartMeasurementRunInput{
		PluginInstanceID: pluginID, Name: "Point A", StartedAt: startedAt,
		ConfigVersion: 1, ConfigSnapshot: config,
	})
	if err != nil {
		t.Fatalf("starting run: %v", err)
	}
	if _, err := repository.Start(ctx, energy.StartMeasurementRunInput{
		PluginInstanceID: pluginID, Name: "Duplicate", StartedAt: startedAt,
		ConfigVersion: 1, ConfigSnapshot: config,
	}); !errors.Is(err, energy.ErrMeasurementRunConflict) {
		t.Fatalf("duplicate active run error = %v, want conflict", err)
	}

	cutoff := startedAt.Add(37 * time.Minute)
	archive := json.RawMessage(`{"schema_version":1,"summary":{"electrical_kwh":0.148},"series":[]}`)
	checksum := strings.Repeat("b", 64)
	next, err := repository.ArchiveAndStart(ctx, energy.ResetMeasurementRunInput{
		PluginInstanceID: pluginID, ExpectedRunID: current.ID, Name: "Point B", Reason: "moved by operator",
		Cutoff: cutoff, ConfigVersion: 1, ConfigSnapshot: config,
		ArchivePayload: archive, ArchiveSHA256: checksum,
	})
	if err != nil {
		t.Fatalf("resetting run: %v", err)
	}
	if next.StartedAt != cutoff || next.Name != "Point B" {
		t.Errorf("next run = %+v, want Point B at cutoff", next)
	}
	active, err := repository.FindActive(ctx, pluginID)
	if err != nil || active.ID != next.ID {
		t.Fatalf("active run = %+v, %v; want next run", active, err)
	}
	var archived energy.MeasurementRun
	if err := database.First(&archived, "id = ?", current.ID).Error; err != nil {
		t.Fatalf("loading archive: %v", err)
	}
	if archived.Status != energy.MeasurementRunArchived || archived.EndedAt == nil || !archived.EndedAt.Equal(cutoff) || !jsonEqual(archived.ArchivePayload, archive) {
		t.Errorf("archive = %+v payload=%s", archived, archived.ArchivePayload)
	}

	if _, err := repository.ArchiveAndStart(ctx, energy.ResetMeasurementRunInput{
		PluginInstanceID: pluginID, ExpectedRunID: current.ID, Name: "Point C", Cutoff: cutoff,
		ConfigVersion: 1, ConfigSnapshot: config, ArchivePayload: archive, ArchiveSHA256: checksum,
	}); !errors.Is(err, energy.ErrMeasurementRunConflict) {
		t.Fatalf("stale reset error = %v, want conflict", err)
	}
	var count int64
	if err := database.Model(&energy.MeasurementRun{}).Where("plugin_instance_id = ?", pluginID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("run count = %d, %v; want 2", count, err)
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}

func newMeasurementRunDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Energy measurement-run repository tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "energy_run_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDatabase.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = adminDatabase.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	database, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening GORM: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatalf("getting SQL database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	for _, migration := range []string{"../../../../migrations/000010_create_plugin_instances.up.sql", "../../../../migrations/000020_create_energy_measurement_runs.up.sql"} {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatalf("reading migration %s: %v", migration, err)
		}
		if err := database.Exec(string(contents)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", migration, err)
		}
	}
	return database
}

func createEnergyPlugin(t *testing.T, database *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := database.Exec(`INSERT INTO plugin_instances (id,type,name,config_version) VALUES (?,'energy_management','Portable Energy',1)`, id).Error; err != nil {
		t.Fatalf("creating Energy Plugin: %v", err)
	}
	return id
}
