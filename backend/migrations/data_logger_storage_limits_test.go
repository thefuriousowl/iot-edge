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

func TestDataLoggerStorageLimitMigrationBackfillsBatches_Integration(t *testing.T) {
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
	schema := "data_logger_storage_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql", "000006_create_data_loggers.up.sql", "000007_create_tag_values_raw.up.sql"} {
		applyMigrationFile(t, ctx, conn, migration)
	}
	loggerID, tagID := insertRawMigrationFixture(t, ctx, conn)
	batchAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	if _, err := conn.ExecContext(ctx, `INSERT INTO tag_values_raw (logger_id,tag_id,batch_at,observed_at,data_type,value,quality) VALUES ($1,$2,$3,$3,'float64','12.5','good')`, loggerID, tagID, batchAt); err != nil {
		t.Fatalf("inserting pre-migration history: %v", err)
	}

	applyMigrationFile(t, ctx, conn, "000008_add_data_logger_storage_limits.up.sql")
	var rows int
	var estimatedBytes int64
	if err := conn.QueryRowContext(ctx, `SELECT row_count, estimated_size_bytes FROM data_logger_batches WHERE logger_id=$1 AND batch_at=$2`, loggerID, batchAt).Scan(&rows, &estimatedBytes); err != nil || rows != 1 || estimatedBytes <= 192 {
		t.Fatalf("backfilled batch = rows %d, bytes %d, error %v", rows, estimatedBytes, err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE data_loggers SET max_size_bytes=1048575 WHERE id=$1`, loggerID); err == nil {
		t.Error("sub-MiB storage limit update succeeded")
	}
	if _, err := conn.ExecContext(ctx, `UPDATE data_loggers SET max_size_bytes=1048576 WHERE id=$1`, loggerID); err != nil {
		t.Fatalf("minimum storage limit update: %v", err)
	}

	applyMigrationFile(t, ctx, conn, "000008_add_data_logger_storage_limits.down.sql")
	if relationExists(t, ctx, conn, schema, "data_logger_batches") {
		t.Error("data_logger_batches still exists after down migration")
	}
}
