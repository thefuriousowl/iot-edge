package migrations

import (
	"strings"
	"testing"
)

func TestLoadMigrationsReturnsEveryVersionInOrder(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(migrations) != 18 {
		t.Fatalf("loadMigrations() count = %d, want 18", len(migrations))
	}
	if migrations[0].name != "000001_create_users.up.sql" || migrations[len(migrations)-1].name != "000018_enforce_report_aggregate_bucket.up.sql" {
		t.Fatalf("migration bounds = %q..%q", migrations[0].name, migrations[len(migrations)-1].name)
	}
	for _, migration := range migrations {
		if len(migration.checksum) != 64 {
			t.Errorf("migration %s checksum length = %d", migration.name, len(migration.checksum))
		}
		if strings.HasPrefix(migration.contents, "BEGIN;") || strings.HasSuffix(migration.contents, "COMMIT;") {
			t.Errorf("migration %s retained an outer transaction", migration.name)
		}
	}
}

func TestStripOuterTransaction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "wrapped", input: "\nBEGIN;\nSELECT 1;\nCOMMIT;\n", want: "SELECT 1;"},
		{name: "unwrapped", input: "\nSELECT 1;\n", want: "SELECT 1;"},
		{name: "internal words", input: "SELECT 'BEGIN; COMMIT;';", want: "SELECT 'BEGIN; COMMIT;';"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stripOuterTransaction(test.input); got != test.want {
				t.Fatalf("stripOuterTransaction() = %q, want %q", got, test.want)
			}
		})
	}
}
