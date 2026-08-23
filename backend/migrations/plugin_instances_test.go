package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPluginInstancesMigrationConstraintsAndDown_Integration(t *testing.T) {
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
	schema := "plugin_instances_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000010_create_plugin_instances.up.sql")
	for _, relation := range []string{"plugin_instances", "idx_plugin_instances_name_ci", "idx_plugin_instances_type", "idx_plugin_instances_enabled", "idx_plugin_instances_type_enabled"} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	var (
		instanceID uuid.UUID
		enabled    bool
		config     string
	)
	err = connection.QueryRowContext(ctx, `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Plant Energy',1) RETURNING id,enabled,config::text`).Scan(&instanceID, &enabled, &config)
	if err != nil {
		t.Fatalf("inserting Plugin instance with defaults: %v", err)
	}
	if instanceID == uuid.Nil || enabled || config != "{}" {
		t.Errorf("defaults = id %s, enabled %t, config %q", instanceID, enabled, config)
	}

	invalidStatements := []struct {
		name      string
		statement string
	}{
		{name: "uppercase type", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('Energy','Uppercase Type',1)`},
		{name: "spaced type", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy plugin','Spaced Type',1)`},
		{name: "blank name", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','',1)`},
		{name: "untrimmed name", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management',' Untrimmed ',1)`},
		{name: "array config", statement: `INSERT INTO plugin_instances (type,name,config,config_version) VALUES ('energy_management','Array Config','[]',1)`},
		{name: "null config", statement: `INSERT INTO plugin_instances (type,name,config,config_version) VALUES ('energy_management','Null Config',NULL,1)`},
		{name: "zero config version", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Zero Version',0)`},
		{name: "negative config version", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Negative Version',-1)`},
		{name: "case insensitive duplicate name", statement: `INSERT INTO plugin_instances (type,name,config_version) VALUES ('mqtt_publisher','plant energy',1)`},
	}
	for _, invalid := range invalidStatements {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := connection.ExecContext(ctx, invalid.statement); err == nil {
				t.Errorf("invalid statement succeeded: %s", invalid.statement)
			}
		})
	}

	applyMigrationFile(t, ctx, connection, "000010_create_plugin_instances.down.sql")
	if relationExists(t, ctx, connection, schema, "plugin_instances") {
		t.Error("plugin_instances remains after down migration")
	}
}
