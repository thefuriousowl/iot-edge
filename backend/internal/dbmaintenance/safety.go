package dbmaintenance

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Target struct {
	Database string
	Role     string
}

// ValidateDedicated rejects shared/system databases, superusers, and databases
// not owned by the connected runtime role. Maintenance must never run with an
// administrative PostgreSQL identity.
func ValidateDedicated(ctx context.Context, database *sql.DB) (Target, error) {
	if database == nil {
		return Target{}, errors.New("database is required")
	}
	var target Target
	var owner string
	var superuser bool
	err := database.QueryRowContext(ctx, `
		SELECT current_database(), current_user, pg_get_userbyid(d.datdba), r.rolsuper
		FROM pg_database d
		JOIN pg_roles r ON r.rolname = current_user
		WHERE d.datname = current_database()
	`).Scan(&target.Database, &target.Role, &owner, &superuser)
	if err != nil {
		return Target{}, fmt.Errorf("inspect database target: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(target.Database))
	if name == "postgres" || strings.HasPrefix(name, "template") {
		return Target{}, errors.New("maintenance refuses PostgreSQL system databases")
	}
	if superuser {
		return Target{}, errors.New("maintenance refuses a PostgreSQL superuser")
	}
	if target.Role != owner {
		return Target{}, errors.New("runtime role must own its dedicated database")
	}
	return target, nil
}

func RequireConfirmation(reader io.Reader, expected string) error {
	if reader == nil || strings.TrimSpace(expected) == "" {
		return errors.New("confirmation input and phrase are required")
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 513)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read confirmation: %w", err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line != expected {
		return errors.New("confirmation phrase did not match")
	}
	return nil
}
