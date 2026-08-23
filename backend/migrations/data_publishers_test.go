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

func TestDataPublishersMigrationConstraintsOwnershipAndDown_Integration(t *testing.T) {
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
	schema := "data_publishers_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{
		"000002_create_vgateways.up.sql",
		"000003_create_devices_datasources.up.sql",
		"000004_create_tags.up.sql",
		"000010_create_plugin_instances.up.sql",
		"000012_create_data_publishers.up.sql",
	} {
		applyMigrationFile(t, ctx, connection, migration)
	}

	for _, relation := range []string{
		"data_publishers", "data_publisher_sources", "idx_data_publishers_name_ci",
		"idx_data_publishers_type", "idx_data_publishers_enabled", "idx_data_publishers_type_enabled",
		"idx_data_publisher_sources_tag_unique", "idx_data_publisher_sources_plugin_output_unique",
		"idx_data_publisher_sources_tag", "idx_data_publisher_sources_plugin",
	} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	var runtimeColumns int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publishers' AND column_name IN ('status','runtime_status','last_error')`, schema).Scan(&runtimeColumns); err != nil {
		t.Fatalf("checking runtime columns: %v", err)
	}
	if runtimeColumns != 0 {
		t.Errorf("data_publishers has %d runtime-state columns", runtimeColumns)
	}

	var tagID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO tags (name,type,data_type,config) VALUES ('Voltage','constant','float64','{"value":230}') RETURNING id`).Scan(&tagID); err != nil {
		t.Fatalf("creating Tag fixture: %v", err)
	}
	var pluginID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Plant Energy',1) RETURNING id`).Scan(&pluginID); err != nil {
		t.Fatalf("creating Plugin fixture: %v", err)
	}
	var (
		publisherID uuid.UUID
		enabled     bool
		config      string
	)
	err = connection.QueryRowContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('http_server','Plant API',1) RETURNING id,enabled,config::text`).Scan(&publisherID, &enabled, &config)
	if err != nil {
		t.Fatalf("creating Publisher: %v", err)
	}
	if publisherID == uuid.Nil || enabled || config != "{}" {
		t.Errorf("Publisher defaults = id %s, enabled %t, config %q", publisherID, enabled, config)
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id) VALUES ($1,0,'voltage','tag',$2)`, publisherID, tagID); err != nil {
		t.Fatalf("inserting Tag source: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,plugin_instance_id,output_key) VALUES ($1,1,'energy','plugin_output',$2,'today.energy_kwh')`, publisherID, pluginID); err != nil {
		t.Fatalf("inserting Plugin output source: %v", err)
	}

	invalidStatements := []struct {
		name      string
		statement string
		args      []any
	}{
		{name: "unsupported type", statement: `INSERT INTO data_publishers (type,name,config_version) VALUES ('opcua','Future',1)`},
		{name: "blank name", statement: `INSERT INTO data_publishers (type,name,config_version) VALUES ('mqtt','',1)`},
		{name: "untrimmed name", statement: `INSERT INTO data_publishers (type,name,config_version) VALUES ('mqtt',' Untrimmed ',1)`},
		{name: "blank description", statement: `INSERT INTO data_publishers (type,name,description,config_version) VALUES ('mqtt','Blank Description',' ',1)`},
		{name: "array config", statement: `INSERT INTO data_publishers (type,name,config,config_version) VALUES ('mqtt','Array','[]',1)`},
		{name: "zero version", statement: `INSERT INTO data_publishers (type,name,config_version) VALUES ('mqtt','Zero Version',0)`},
		{name: "case insensitive name", statement: `INSERT INTO data_publishers (type,name,config_version) VALUES ('mqtt','plant api',1)`},
		{name: "negative position", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id) VALUES ($1,-1,'negative','tag',$2)`, args: []any{publisherID, tagID}},
		{name: "unsafe alias", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id) VALUES ($1,2,'line voltage','tag',$2)`, args: []any{publisherID, tagID}},
		{name: "Tag shape with output", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id,output_key) VALUES ($1,2,'shape','tag',$2,'metric')`, args: []any{publisherID, tagID}},
		{name: "Plugin shape without key", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,plugin_instance_id) VALUES ($1,2,'shape','plugin_output',$2)`, args: []any{publisherID, pluginID}},
		{name: "unsafe output key", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,plugin_instance_id,output_key) VALUES ($1,2,'key','plugin_output',$2,'Invalid Key')`, args: []any{publisherID, pluginID}},
		{name: "duplicate alias", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,plugin_instance_id,output_key) VALUES ($1,2,'energy','plugin_output',$2,'today.cop')`, args: []any{publisherID, pluginID}},
		{name: "duplicate Tag", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id) VALUES ($1,2,'voltage_again','tag',$2)`, args: []any{publisherID, tagID}},
		{name: "duplicate output", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,plugin_instance_id,output_key) VALUES ($1,2,'energy_again','plugin_output',$2,'today.energy_kwh')`, args: []any{publisherID, pluginID}},
		{name: "missing Tag", statement: `INSERT INTO data_publisher_sources (publisher_id,position,alias,kind,tag_id) VALUES ($1,2,'missing','tag',$2)`, args: []any{publisherID, uuid.New()}},
	}
	for _, invalid := range invalidStatements {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := connection.ExecContext(ctx, invalid.statement, invalid.args...); err == nil {
				t.Errorf("invalid statement succeeded: %s", invalid.statement)
			}
		})
	}

	if _, err := connection.ExecContext(ctx, `DELETE FROM tags WHERE id=$1`, tagID); err == nil {
		t.Error("deleting selected Tag succeeded")
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM plugin_instances WHERE id=$1`, pluginID); err == nil {
		t.Error("deleting selected Plugin instance succeeded")
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM data_publishers WHERE id=$1`, publisherID); err != nil {
		t.Fatalf("deleting Publisher: %v", err)
	}
	var sourceCount int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM data_publisher_sources WHERE publisher_id=$1`, publisherID).Scan(&sourceCount); err != nil || sourceCount != 0 {
		t.Errorf("source rows after Publisher delete = %d, %v", sourceCount, err)
	}
	for tableName, id := range map[string]uuid.UUID{"tags": tagID, "plugin_instances": pluginID} {
		var count int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tableName+` WHERE id=$1`, id).Scan(&count); err != nil || count != 1 {
			t.Errorf("%s source after Publisher delete = %d, %v", tableName, count, err)
		}
	}

	applyMigrationFile(t, ctx, connection, "000012_create_data_publishers.down.sql")
	for _, relation := range []string{"data_publisher_sources", "data_publishers"} {
		if relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q remains after down migration", relation)
		}
	}
}
