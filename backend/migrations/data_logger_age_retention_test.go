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

func TestDataLoggerAgeRetentionMigration_Integration(t *testing.T) {
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
		t.Fatalf("getting dedicated connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	schema := "data_logger_age_retention_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		"000006_create_data_loggers.up.sql",
		"000007_create_tag_values_raw.up.sql",
		"000008_add_data_logger_storage_limits.up.sql",
	} {
		applyMigrationFile(t, ctx, connection, migration)
	}

	var loggerID uuid.UUID
	if err := connection.QueryRowContext(ctx, `
		INSERT INTO data_loggers (name, timezone, mode, start_at, max_size_bytes, config)
		VALUES ('Age retention', 'UTC', 'interval', CURRENT_TIMESTAMP, 1048576, '{"interval_seconds":60}')
		RETURNING id
	`).Scan(&loggerID); err != nil {
		t.Fatalf("inserting pre-migration Logger: %v", err)
	}

	applyMigrationFile(t, ctx, connection, "000015_add_data_logger_age_retention.up.sql")
	var maxAge sql.NullInt64
	var maxSize int64
	if err := connection.QueryRowContext(ctx, `SELECT max_age_seconds, max_size_bytes FROM data_loggers WHERE id=$1`, loggerID).Scan(&maxAge, &maxSize); err != nil {
		t.Fatalf("reading migrated Logger: %v", err)
	}
	if maxAge.Valid || maxSize != 1048576 {
		t.Fatalf("migrated retention = age %#v, size %d", maxAge, maxSize)
	}
	for _, value := range []int64{1, 315360000} {
		if _, err := connection.ExecContext(ctx, `UPDATE data_loggers SET max_age_seconds=$1 WHERE id=$2`, value, loggerID); err != nil {
			t.Errorf("accepted age %d failed: %v", value, err)
		}
	}
	for _, value := range []int64{0, 315360001} {
		if _, err := connection.ExecContext(ctx, `UPDATE data_loggers SET max_age_seconds=$1 WHERE id=$2`, value, loggerID); err == nil {
			t.Errorf("invalid age %d succeeded", value)
		}
	}
	if _, err := connection.ExecContext(ctx, `UPDATE data_loggers SET max_age_seconds=NULL WHERE id=$1`, loggerID); err != nil {
		t.Fatalf("clearing age retention: %v", err)
	}

	applyMigrationFile(t, ctx, connection, "000015_add_data_logger_age_retention.down.sql")
	var ageColumnCount int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='data_loggers' AND column_name='max_age_seconds'
	`, schema).Scan(&ageColumnCount); err != nil {
		t.Fatalf("checking removed age column: %v", err)
	}
	if ageColumnCount != 0 {
		t.Errorf("max_age_seconds remains after down migration")
	}
	if err := connection.QueryRowContext(ctx, `SELECT max_size_bytes FROM data_loggers WHERE id=$1`, loggerID).Scan(&maxSize); err != nil || maxSize != 1048576 {
		t.Errorf("existing storage retention after down = %d, %v", maxSize, err)
	}
}
