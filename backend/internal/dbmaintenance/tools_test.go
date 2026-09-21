package dbmaintenance

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCommandConnectionRemovesPassword(t *testing.T) {
	public, password, database, err := commandConnection("postgres://iot_edge:p%40ss@127.0.0.1:5432/iot_edge?sslmode=require")
	if err != nil {
		t.Fatalf("commandConnection() error = %v", err)
	}
	defer clear(password)
	if database != "iot_edge" || string(password) != "p@ss" {
		t.Fatalf("database/password = %q/%q", database, password)
	}
	if public != "postgres://iot_edge@127.0.0.1:5432/iot_edge?sslmode=require" {
		t.Fatalf("public URL = %q", public)
	}
}

func TestBackupRestoreDedicatedDatabaseIntegration(t *testing.T) {
	databaseURL, pgDump, pgRestore := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_PG_DUMP"), os.Getenv("TEST_PG_RESTORE")
	if databaseURL == "" || pgDump == "" || pgRestore == "" {
		t.Skip("set TEST_DATABASE_URL, TEST_PG_DUMP and TEST_PG_RESTORE for integration test")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	target, err := ValidateDedicated(context.Background(), database)
	if err != nil {
		t.Fatalf("ValidateDedicated() error = %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE maintenance_probe (value TEXT NOT NULL); INSERT INTO maintenance_probe VALUES ('before')`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "acceptance.dump")
	if _, err := Backup(context.Background(), databaseURL, pgDump, path, target.Database); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	if _, err := database.Exec(`UPDATE maintenance_probe SET value = 'after'`); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), databaseURL, pgRestore, path, target.Database); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM maintenance_probe`).Scan(&value); err != nil || value != "before" {
		t.Fatalf("restored value = %q, error = %v", value, err)
	}
}

func TestVerifyBackupRejectsChecksumMismatch(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "sample.dump")
	if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".json", []byte(`{"format":1,"database":"iot_edge","created_at":"now","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dump_format":"postgresql-custom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(path, "iot_edge"); err == nil {
		t.Fatal("checksum mismatch unexpectedly succeeded")
	}
}
