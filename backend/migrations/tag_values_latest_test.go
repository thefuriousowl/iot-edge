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

func TestCreateTagValuesLatestMigration_Integration(t *testing.T) {
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
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("getting dedicated connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	schema := "tag_value_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("dropping schema: %v", err)
		}
	})
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql", "000005_create_tag_values_latest.up.sql"} {
		applyMigrationFile(t, ctx, conn, migration)
	}
	for _, relation := range []string{"tag_values_latest", "tag_values_latest_sequence_key", "idx_tag_values_latest_observed_at"} {
		if !relationExists(t, ctx, conn, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	tagID := insertTagValueMigrationFixture(t, ctx, conn)
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO tag_values_latest (tag_id, sequence, data_type, value, quality, observed_at, stored_at)
		VALUES ($1, 1, 'int16', '32767'::jsonb, 'good', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, tagID); err != nil {
		t.Fatalf("inserting valid latest value: %v", err)
	}
	invalidValues := []struct {
		name         string
		sequence     int
		dataType     string
		value        any
		quality      string
		errorMessage any
	}{
		{name: "zero sequence", sequence: 0, dataType: "int16", value: "1", quality: "good"},
		{name: "integer overflow", sequence: 2, dataType: "int16", value: "32768", quality: "good"},
		{name: "fractional integer", sequence: 2, dataType: "int16", value: "1.5", quality: "good"},
		{name: "wrong JSON type", sequence: 2, dataType: "bool", value: "1", quality: "good"},
		{name: "bad with value", sequence: 2, dataType: "int16", value: "1", quality: "bad", errorMessage: "failed"},
		{name: "bad without error", sequence: 2, dataType: "int16", quality: "bad", errorMessage: " "},
		{name: "good with error", sequence: 2, dataType: "int16", value: "1", quality: "good", errorMessage: "stale"},
	}
	for _, test := range invalidValues {
		t.Run(test.name, func(t *testing.T) {
			if _, err := conn.ExecContext(ctx, `
				UPDATE tag_values_latest SET sequence=$2, data_type=$3, value=CAST($4 AS JSONB), quality=$5, error_message=$6
				WHERE tag_id=$1
			`, tagID, test.sequence, test.dataType, test.value, test.quality, test.errorMessage); err == nil {
				t.Error("invalid latest value update succeeded")
			}
		})
	}

	if _, err := conn.ExecContext(ctx, "DELETE FROM tags WHERE id=$1", tagID); err != nil {
		t.Fatalf("deleting Tag: %v", err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tag_values_latest").Scan(&count); err != nil || count != 0 {
		t.Errorf("latest rows after Tag delete = %d, error = %v", count, err)
	}
	applyMigrationFile(t, ctx, conn, "000005_create_tag_values_latest.down.sql")
	if relationExists(t, ctx, conn, schema, "tag_values_latest") {
		t.Error("tag_values_latest still exists after down migration")
	}
}

func insertTagValueMigrationFixture(t *testing.T, ctx context.Context, conn *sql.Conn) uuid.UUID {
	t.Helper()
	gatewayID := uuid.New()
	deviceID := uuid.New()
	datasourceID := uuid.New()
	tagID := uuid.New()
	statements := []struct {
		query string
		args  []any
	}{
		{query: `INSERT INTO vgateways (id,name,type,config) VALUES ($1,'Gateway','modbus_tcp','{}')`, args: []any{gatewayID}},
		{query: `INSERT INTO devices (id,vgateway_id,name,type,config) VALUES ($1,$2,'Device','modbus_device','{}')`, args: []any{deviceID, gatewayID}},
		{query: `INSERT INTO datasources (id,device_id,name,type,config) VALUES ($1,$2,'Datasource','modbus_read','{}')`, args: []any{datasourceID, deviceID}},
		{query: `INSERT INTO tags (id,datasource_id,name,type,data_type,config) VALUES ($1,$2,'Tag','reading','int16','{}')`, args: []any{tagID, datasourceID}},
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("inserting fixture: %v", err)
		}
	}
	return tagID
}
