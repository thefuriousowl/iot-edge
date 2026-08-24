package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const migrationLockID int64 = 739273844120260824

//go:embed *.up.sql
var migrationFiles embed.FS

type migration struct {
	name     string
	contents string
	checksum string
}

func Up(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return errors.New("migration database is required")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve migration connection: %w", err)
	}
	defer connection.Close()

	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = connection.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := connection.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	applied, err := loadApplied(ctx, connection)
	if err != nil {
		return err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, candidate := range migrations {
		if checksum, ok := applied[candidate.name]; ok {
			if checksum != candidate.checksum {
				return fmt.Errorf("migration %s checksum changed after it was applied", candidate.name)
			}
			continue
		}
		if err := apply(ctx, connection, candidate); err != nil {
			return err
		}
	}
	return nil
}

func loadApplied(ctx context.Context, database *sql.Conn) (map[string]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT name, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var name string
		var checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[name] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFiles, "*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	sort.Strings(entries)

	result := make([]migration, 0, len(entries))
	for _, name := range entries {
		contents, err := migrationFiles.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		digest := sha256.Sum256(contents)
		result = append(result, migration{
			name:     name,
			contents: stripOuterTransaction(string(contents)),
			checksum: hex.EncodeToString(digest[:]),
		})
	}
	return result, nil
}

func apply(ctx context.Context, database *sql.Conn, candidate migration) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", candidate.name, err)
	}
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx, candidate.contents); err != nil {
		return fmt.Errorf("apply migration %s: %w", candidate.name, err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO schema_migrations (name, checksum) VALUES ($1, $2)`,
		candidate.name,
		candidate.checksum,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", candidate.name, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", candidate.name, err)
	}
	return nil
}

func stripOuterTransaction(contents string) string {
	trimmed := strings.TrimSpace(contents)
	if !strings.HasPrefix(trimmed, "BEGIN;") || !strings.HasSuffix(trimmed, "COMMIT;") {
		return trimmed
	}
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "BEGIN;"))
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "COMMIT;"))
	return trimmed
}
