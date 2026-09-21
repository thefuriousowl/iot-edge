package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnergyMeasurementRunsMigrationConstraintsAndDown_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("getting connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	schema := "energy_runs_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000010_create_plugin_instances.up.sql")
	applyMigrationFile(t, ctx, connection, "000020_create_energy_measurement_runs.up.sql")

	for _, relation := range []string{"energy_measurement_runs", "idx_energy_measurement_runs_active", "idx_energy_measurement_runs_archive"} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	energyID := insertRunTestPlugin(t, ctx, connection, "energy_management", "Portable Energy")
	otherID := insertRunTestPlugin(t, ctx, connection, "future_plugin", "Other")
	startedAt := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	var runID uuid.UUID
	if err := connection.QueryRowContext(ctx, `
		INSERT INTO energy_measurement_runs (plugin_instance_id,name,status,started_at,config_version,config_snapshot)
		VALUES ($1,'Point A','active',$2,1,'{}') RETURNING id
	`, energyID, startedAt).Scan(&runID); err != nil {
		t.Fatalf("creating active measurement run: %v", err)
	}
	invalidSQL(t, ctx, connection, `INSERT INTO energy_measurement_runs (plugin_instance_id,name,status,started_at,config_version,config_snapshot) VALUES ($1,'Duplicate','active',$2,1,'{}')`, energyID, startedAt)
	invalidSQL(t, ctx, connection, `INSERT INTO energy_measurement_runs (plugin_instance_id,name,status,started_at,config_version,config_snapshot) VALUES ($1,'Wrong type','active',$2,1,'{}')`, otherID, startedAt)
	invalidSQL(t, ctx, connection, `UPDATE energy_measurement_runs SET status='archived',ended_at=$2,archived_at=$2 WHERE id=$1`, runID, startedAt.Add(time.Hour))

	checksum := strings.Repeat("a", 64)
	endedAt := startedAt.Add(time.Hour)
	if _, err := connection.ExecContext(ctx, `
		UPDATE energy_measurement_runs SET status='archived',ended_at=$2,archived_at=$2,
		archive_payload='{"schema_version":1,"series":[]}',archive_sha256=$3 WHERE id=$1
	`, runID, endedAt, checksum); err != nil {
		t.Fatalf("archiving run: %v", err)
	}
	invalidSQL(t, ctx, connection, `UPDATE energy_measurement_runs SET name='Changed' WHERE id=$1`, runID)
	invalidSQL(t, ctx, connection, `DELETE FROM energy_measurement_runs WHERE id=$1`, runID)

	applyMigrationFile(t, ctx, connection, "000020_create_energy_measurement_runs.down.sql")
	if relationExists(t, ctx, connection, schema, "energy_measurement_runs") {
		t.Error("measurement run table remains after down migration")
	}
	if !relationExists(t, ctx, connection, schema, "plugin_instances") {
		t.Error("measurement run down migration removed Plugin instances")
	}
}

func insertRunTestPlugin(t *testing.T, ctx context.Context, connection *sql.Conn, pluginType, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO plugin_instances (type,name,config_version) VALUES ($1,$2,1) RETURNING id`, pluginType, name).Scan(&id); err != nil {
		t.Fatalf("creating Plugin instance: %v", err)
	}
	return id
}
