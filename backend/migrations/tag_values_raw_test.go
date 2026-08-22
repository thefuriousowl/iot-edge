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

func TestCreateTagValuesRawMigration_Integration(t *testing.T) {
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
	schema := "tag_value_raw_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	for _, migration := range []string{
		"000002_create_vgateways.up.sql",
		"000003_create_devices_datasources.up.sql",
		"000004_create_tags.up.sql",
		"000006_create_data_loggers.up.sql",
		"000007_create_tag_values_raw.up.sql",
	} {
		applyMigrationFile(t, ctx, conn, migration)
	}
	for _, relation := range []string{"tag_values_raw", "tag_values_raw_default", "idx_tag_values_raw_logger_batch", "idx_tag_values_raw_tag_batch", "idx_tag_values_raw_batch"} {
		if !relationExists(t, ctx, conn, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	var relationKind string
	if err := conn.QueryRowContext(ctx, `SELECT relkind FROM pg_class WHERE oid=$1::regclass`, schema+".tag_values_raw").Scan(&relationKind); err != nil || relationKind != "p" {
		t.Errorf("tag_values_raw relkind = %q, error = %v", relationKind, err)
	}

	loggerID, tagID := insertRawMigrationFixture(t, ctx, conn)
	batchAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality)
		VALUES ($1,$2,$3,$3,'float64','12.5','good')
	`, loggerID, tagID, batchAt); err != nil {
		t.Fatalf("inserting good raw value: %v", err)
	}
	var partition string
	if err := conn.QueryRowContext(ctx, `SELECT tableoid::regclass::text FROM tag_values_raw WHERE logger_id=$1 AND tag_id=$2`, loggerID, tagID).Scan(&partition); err != nil || !strings.HasSuffix(partition, "tag_values_raw_default") {
		t.Errorf("raw value partition = %q, error = %v", partition, err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality,error_message)
		VALUES ($1,$2,$3,$3,'float64',NULL,'bad','connection lost')
	`, loggerID, tagID, batchAt.Add(time.Second)); err != nil {
		t.Fatalf("inserting bad raw value: %v", err)
	}

	invalidValues := []struct {
		name, dataType, value, quality string
		errorMessage                   any
	}{
		{name: "unsupported data type", dataType: "string", value: `1`, quality: "good"},
		{name: "integer overflow", dataType: "int16", value: `32768`, quality: "good"},
		{name: "fractional integer", dataType: "uint32", value: `1.5`, quality: "good"},
		{name: "wrong bool type", dataType: "bool", value: `1`, quality: "good"},
		{name: "unsupported quality", dataType: "float64", value: `1`, quality: "uncertain"},
		{name: "good with error", dataType: "float64", value: `1`, quality: "good", errorMessage: "stale"},
		{name: "bad with value", dataType: "float64", value: `1`, quality: "bad", errorMessage: "failed"},
		{name: "bad without error", dataType: "float64", quality: "bad", errorMessage: " "},
	}
	for index, test := range invalidValues {
		t.Run(test.name, func(t *testing.T) {
			var value any
			if test.value != "" {
				value = test.value
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality,error_message)
				VALUES ($1,$2,$3,$3,$4,CAST($5 AS JSONB),$6,$7)
			`, loggerID, tagID, batchAt.Add(time.Duration(index+2)*time.Second), test.dataType, value, test.quality, test.errorMessage); err == nil {
				t.Error("invalid raw value insert succeeded")
			}
		})
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality)
		VALUES ($1,$2,$3,$3,'float64','13.5','good')
	`, loggerID, tagID, batchAt); err == nil {
		t.Error("duplicate logger/Tag/batch insert succeeded")
	}

	if _, err := conn.ExecContext(ctx, "DELETE FROM tags WHERE id=$1", tagID); err != nil {
		t.Fatalf("deleting Tag: %v", err)
	}
	assertMigrationCount(t, ctx, conn, "SELECT COUNT(*) FROM tag_values_raw", 0)
	applyMigrationFile(t, ctx, conn, "000007_create_tag_values_raw.down.sql")
	for _, relation := range []string{"tag_values_raw", "tag_values_raw_default"} {
		if relationExists(t, ctx, conn, schema, relation) {
			t.Errorf("%s still exists after down migration", relation)
		}
	}
}

func insertRawMigrationFixture(t *testing.T, ctx context.Context, conn *sql.Conn) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tagID := insertTagValueMigrationFixture(t, ctx, conn)
	loggerID := uuid.New()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO data_loggers (id,name,timezone,mode,start_at,config)
		VALUES ($1,'Raw Logger','UTC','interval','2026-08-23T00:00:00Z','{"interval_seconds":60}')
	`, loggerID); err != nil {
		t.Fatalf("inserting Data Logger: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,0)`, loggerID, tagID); err != nil {
		t.Fatalf("selecting Tag: %v", err)
	}
	return loggerID, tagID
}
