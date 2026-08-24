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

func TestDataLoggerRetentionStatusMigration_Integration(t *testing.T) {
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
	schema := "data_logger_retention_status_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		"000015_add_data_logger_age_retention.up.sql",
		"000016_create_data_logger_retention_status.up.sql",
	} {
		applyMigrationFile(t, ctx, connection, migration)
	}

	insertLogger := func(name string) uuid.UUID {
		t.Helper()
		var loggerID uuid.UUID
		if err := connection.QueryRowContext(ctx, `
			INSERT INTO data_loggers (name, timezone, mode, start_at, config)
			VALUES ($1, 'UTC', 'interval', CURRENT_TIMESTAMP, '{"interval_seconds":60}')
			RETURNING id
		`, name).Scan(&loggerID); err != nil {
			t.Fatalf("inserting Logger: %v", err)
		}
		return loggerID
	}
	loggerID := insertLogger("Retention status")
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO data_logger_retention_status (
			logger_id, last_started_at, last_completed_at, last_result
		) VALUES ($1, '2026-08-24T12:00:00Z', '2026-08-24T12:00:01Z', '{"complete":true}')
	`, loggerID); err != nil {
		t.Fatalf("inserting successful status: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `
		UPDATE data_logger_retention_status
		SET last_result=NULL, last_error='retention cleanup canceled'
		WHERE logger_id=$1
	`, loggerID); err != nil {
		t.Fatalf("updating failed status: %v", err)
	}
	for name, statement := range map[string]string{
		"missing outcome": `UPDATE data_logger_retention_status SET last_result=NULL, last_error=NULL WHERE logger_id=$1`,
		"two outcomes":    `UPDATE data_logger_retention_status SET last_result='{}', last_error='failed' WHERE logger_id=$1`,
		"invalid result":  `UPDATE data_logger_retention_status SET last_result='[]', last_error=NULL WHERE logger_id=$1`,
		"invalid time":    `UPDATE data_logger_retention_status SET last_started_at='2026-08-24T12:00:02Z', last_completed_at='2026-08-24T12:00:01Z' WHERE logger_id=$1`,
	} {
		if _, err := connection.ExecContext(ctx, statement, loggerID); err == nil {
			t.Errorf("%s succeeded", name)
		}
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM data_loggers WHERE id=$1`, loggerID); err != nil {
		t.Fatalf("deleting Logger: %v", err)
	}
	var statusCount int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM data_logger_retention_status WHERE logger_id=$1`, loggerID).Scan(&statusCount); err != nil || statusCount != 0 {
		t.Errorf("cascade status count = %d, %v", statusCount, err)
	}

	preservedLoggerID := insertLogger("Preserved after rollback")
	applyMigrationFile(t, ctx, connection, "000016_create_data_logger_retention_status.down.sql")
	var tableCount int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema=$1 AND table_name='data_logger_retention_status'
	`, schema).Scan(&tableCount); err != nil || tableCount != 0 {
		t.Errorf("status table after rollback = %d, %v", tableCount, err)
	}
	var loggerCount int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM data_loggers WHERE id=$1`, preservedLoggerID).Scan(&loggerCount); err != nil || loggerCount != 1 {
		t.Errorf("Logger after rollback = %d, %v", loggerCount, err)
	}
}
