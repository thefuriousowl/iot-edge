package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPluginOutputLatestMigrationConstraintsCascadeAndDown_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("getting connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	schema := "plugin_output_latest_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000010_create_plugin_instances.up.sql")
	applyMigrationFile(t, ctx, connection, "000011_create_plugin_output_latest.up.sql")
	for _, relation := range []string{"plugin_output_latest", "idx_plugin_output_latest_published_at"} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	var instanceID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Plant Energy',1) RETURNING id`).Scan(&instanceID); err != nil {
		t.Fatalf("inserting Plugin instance: %v", err)
	}
	sourceAt := time.Date(2026, time.August, 23, 9, 0, 0, 0, time.UTC)
	publishedAt := sourceAt.Add(time.Second)
	if _, err := connection.ExecContext(ctx, `INSERT INTO plugin_output_latest (plugin_instance_id,sequence,source_at,published_at,values) VALUES ($1,1,$2,$3,'[{"key":"demand_kw"}]')`, instanceID, sourceAt, publishedAt); err != nil {
		t.Fatalf("inserting latest output: %v", err)
	}

	invalidStatements := []struct {
		name      string
		statement string
		args      []any
	}{
		{name: "missing Plugin", statement: `INSERT INTO plugin_output_latest (plugin_instance_id,sequence,source_at,published_at,values) VALUES ($1,1,$2,$3,'[{"key":"metric"}]')`, args: []any{uuid.New(), sourceAt, publishedAt}},
		{name: "zero sequence", statement: `UPDATE plugin_output_latest SET sequence=0 WHERE plugin_instance_id=$1`, args: []any{instanceID}},
		{name: "negative sequence", statement: `UPDATE plugin_output_latest SET sequence=-1 WHERE plugin_instance_id=$1`, args: []any{instanceID}},
		{name: "source after publish", statement: `UPDATE plugin_output_latest SET source_at=published_at + INTERVAL '1 second' WHERE plugin_instance_id=$1`, args: []any{instanceID}},
		{name: "object values", statement: `UPDATE plugin_output_latest SET values='{}' WHERE plugin_instance_id=$1`, args: []any{instanceID}},
		{name: "empty values", statement: `UPDATE plugin_output_latest SET values='[]' WHERE plugin_instance_id=$1`, args: []any{instanceID}},
		{name: "too many values", statement: `UPDATE plugin_output_latest SET values=(SELECT jsonb_agg(value) FROM generate_series(1,129) value) WHERE plugin_instance_id=$1`, args: []any{instanceID}},
	}
	for _, invalid := range invalidStatements {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := connection.ExecContext(ctx, invalid.statement, invalid.args...); err == nil {
				t.Errorf("invalid statement succeeded: %s", invalid.statement)
			}
		})
	}

	if _, err := connection.ExecContext(ctx, `DELETE FROM plugin_instances WHERE id=$1`, instanceID); err != nil {
		t.Fatalf("deleting Plugin instance: %v", err)
	}
	var count int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_output_latest WHERE plugin_instance_id=$1`, instanceID).Scan(&count); err != nil || count != 0 {
		t.Errorf("cascaded output count = %d, %v", count, err)
	}

	applyMigrationFile(t, ctx, connection, "000011_create_plugin_output_latest.down.sql")
	if relationExists(t, ctx, connection, schema, "plugin_output_latest") {
		t.Error("plugin_output_latest remains after down migration")
	}
	if !relationExists(t, ctx, connection, schema, "plugin_instances") {
		t.Error("Plugin output down migration removed plugin_instances")
	}
}
