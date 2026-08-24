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

func TestAuthSessionVersionMigration_Integration(t *testing.T) {
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
	schema := "auth_session_version_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000001_create_users.up.sql")
	var userID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO users (username,password_hash) VALUES ('owner','original-hash') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("inserting pre-migration user: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000017_add_auth_session_version.up.sql")
	var version int64
	if err := connection.QueryRowContext(ctx, `SELECT session_version FROM users WHERE id=$1`, userID).Scan(&version); err != nil || version != 0 {
		t.Fatalf("migrated session version = %d, %v", version, err)
	}
	if _, err := connection.ExecContext(ctx, `UPDATE users SET session_version=-1 WHERE id=$1`, userID); err == nil {
		t.Error("negative session version succeeded")
	}
	if _, err := connection.ExecContext(ctx, `UPDATE users SET session_version=1 WHERE id=$1`, userID); err != nil {
		t.Fatalf("updating session version: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000017_add_auth_session_version.down.sql")
	var columnCount int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='users' AND column_name='session_version'`, schema).Scan(&columnCount); err != nil || columnCount != 0 {
		t.Errorf("session_version after rollback = %d, %v", columnCount, err)
	}
	var passwordHash string
	if err := connection.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&passwordHash); err != nil || passwordHash != "original-hash" {
		t.Errorf("user after rollback = %q, %v", passwordHash, err)
	}
}
