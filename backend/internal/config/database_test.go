package config

import (
	"os"
	"testing"
)

func TestConnectDatabase_InvalidURL_ReturnsError(t *testing.T) {
	// Arrange
	cfg := &Config{
		DatabaseURL: "not-a-postgres-url",
	}

	// Act
	db, err := ConnectDatabase(cfg)

	// Assert
	if err == nil {
		t.Fatal("expected an error for an invalid database URL, got nil")
	}
	if db != nil {
		t.Fatal("expected a nil database when connection setup fails")
	}
}

func TestConnectDatabase_Integration_ConnectsAndConfiguresPool(t *testing.T) {
	// Arrange
	// Keep this integration test opt-in so the unit-test suite does not depend on
	// a locally running PostgreSQL instance or contain database credentials.
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL integration test")
	}

	cfg := &Config{
		DatabaseURL: databaseURL,
	}

	// Act
	db, err := ConnectDatabase(cfg)
	if err != nil {
		t.Fatalf("ConnectDatabase() returned an unexpected error: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() returned an unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing database connection: %v", err)
		}
	})

	// Assert
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("database ping failed: %v", err)
	}

	if got := sqlDB.Stats().MaxOpenConnections; got != 10 {
		t.Errorf("MaxOpenConns = %d, want 10", got)
	}
}
