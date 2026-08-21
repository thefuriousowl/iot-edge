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

func TestCreateUsersMigration_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration integration test")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing PostgreSQL connection: %v", err)
		}
	})

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("getting dedicated PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("closing dedicated PostgreSQL connection: %v", err)
		}
	})

	schemaName := "migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated test schema: %v", err)
		}
	})

	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schemaName+", public"); err != nil {
		t.Fatalf("setting test schema search path: %v", err)
	}

	applyMigrationFile(t, ctx, conn, "000001_create_users.up.sql")

	for _, tableName := range []string{"users", "password_history", "revoked_tokens"} {
		if !relationExists(t, ctx, conn, schemaName, tableName) {
			t.Errorf("table %q was not created", tableName)
		}
	}
	for _, indexName := range []string{"idx_password_history_user", "idx_revoked_tokens_expires"} {
		if !relationExists(t, ctx, conn, schemaName, indexName) {
			t.Errorf("index %q was not created", indexName)
		}
	}

	var (
		userID         string
		isLocked       bool
		failedAttempts int
	)
	err = conn.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id, is_locked, failed_attempts
	`, "admin", "test-password-hash").Scan(&userID, &isLocked, &failedAttempts)
	if err != nil {
		t.Fatalf("inserting user with database defaults: %v", err)
	}
	if isLocked {
		t.Error("new user is_locked = true, want false")
	}
	if failedAttempts != 0 {
		t.Errorf("new user failed_attempts = %d, want 0", failedAttempts)
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
	`, "admin", "another-test-password-hash"); err == nil {
		t.Error("inserting a duplicate username succeeded, want a unique constraint error")
	}
	if _, err := conn.ExecContext(ctx, `
		UPDATE users SET failed_attempts = -1 WHERE id = $1
	`, userID); err == nil {
		t.Error("setting failed_attempts below zero succeeded, want a check constraint error")
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO password_history (user_id, password_hash)
		VALUES ($1, $2)
	`, userID, "previous-test-password-hash"); err != nil {
		t.Fatalf("inserting password history: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO revoked_tokens (jti, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, "test-token-id", userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("inserting revoked token: %v", err)
	}

	if _, err := conn.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatalf("deleting user: %v", err)
	}
	for _, tableName := range []string{"password_history", "revoked_tokens"} {
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count); err != nil {
			t.Fatalf("counting rows in %s: %v", tableName, err)
		}
		if count != 0 {
			t.Errorf("rows in %s after deleting user = %d, want 0", tableName, count)
		}
	}

	applyMigrationFile(t, ctx, conn, "000001_create_users.down.sql")

	for _, tableName := range []string{"revoked_tokens", "password_history", "users"} {
		if relationExists(t, ctx, conn, schemaName, tableName) {
			t.Errorf("table %q still exists after down migration", tableName)
		}
	}
}

func applyMigrationFile(t *testing.T, ctx context.Context, conn *sql.Conn, filename string) {
	t.Helper()

	migration, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("reading migration %s: %v", filename, err)
	}
	if _, err := conn.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("applying migration %s: %v", filename, err)
	}
}

func relationExists(
	t *testing.T,
	ctx context.Context,
	conn *sql.Conn,
	schemaName string,
	relationName string,
) bool {
	t.Helper()

	var qualifiedName sql.NullString
	if err := conn.QueryRowContext(
		ctx,
		"SELECT to_regclass($1)",
		schemaName+"."+relationName,
	).Scan(&qualifiedName); err != nil {
		t.Fatalf("checking relation %s: %v", relationName, err)
	}
	return qualifiedName.Valid
}
