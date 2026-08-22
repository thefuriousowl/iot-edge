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

func TestCreateVGatewaysMigration_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
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

	schemaName := "vgateway_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	applyMigrationFile(t, ctx, conn, "000002_create_vgateways.up.sql")

	for _, relationName := range []string{
		"vgateways",
		"vgateway_stats",
		"idx_vgateways_type",
		"idx_vgateways_enabled",
		"idx_vgateway_stats_vgateway",
		"idx_vgateway_stats_recorded",
	} {
		if !relationExists(t, ctx, conn, schemaName, relationName) {
			t.Errorf("relation %q was not created", relationName)
		}
	}

	var (
		gatewayID string
		enabled   bool
	)
	err = conn.QueryRowContext(ctx, `
		INSERT INTO vgateways (name, type, config)
		VALUES ($1, $2, $3)
		RETURNING id, enabled
	`, "Main PLC Gateway", "modbus_tcp", `{"host":"192.168.1.100","port":502}`).Scan(
		&gatewayID,
		&enabled,
	)
	if err != nil {
		t.Fatalf("inserting vGateway with database defaults: %v", err)
	}
	if !enabled {
		t.Error("new vGateway enabled = false, want true")
	}

	invalidGateways := []struct {
		name        string
		gatewayName string
		gatewayType string
		config      string
	}{
		{
			name:        "duplicate name",
			gatewayName: "Main PLC Gateway",
			gatewayType: "modbus_tcp",
			config:      `{"host":"192.168.1.101","port":502}`,
		},
		{
			name:        "unsupported type",
			gatewayName: "Future Gateway",
			gatewayType: "mqtt",
			config:      `{"broker":"mqtt.example.com"}`,
		},
		{
			name:        "non-object config",
			gatewayName: "Invalid Config Gateway",
			gatewayType: "modbus_tcp",
			config:      `[]`,
		},
	}
	for _, testCase := range invalidGateways {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO vgateways (name, type, config)
				VALUES ($1, $2, $3)
			`, testCase.gatewayName, testCase.gatewayType, testCase.config); err == nil {
				t.Errorf("inserting invalid vGateway succeeded: %s", testCase.name)
			}
		})
	}

	var (
		statsID       string
		requestCount  int64
		errorCount    int64
		bytesReceived int64
	)
	err = conn.QueryRowContext(ctx, `
		INSERT INTO vgateway_stats (vgateway_id)
		VALUES ($1)
		RETURNING id, request_count, error_count, bytes_received
	`, gatewayID).Scan(&statsID, &requestCount, &errorCount, &bytesReceived)
	if err != nil {
		t.Fatalf("inserting vGateway stats with database defaults: %v", err)
	}
	if requestCount != 0 || errorCount != 0 || bytesReceived != 0 {
		t.Errorf(
			"new stats counters = (%d, %d, %d), want all zero",
			requestCount,
			errorCount,
			bytesReceived,
		)
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO vgateway_stats (vgateway_id, request_count)
		VALUES ($1, -1)
	`, gatewayID); err == nil {
		t.Error("inserting negative request_count succeeded")
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO vgateway_stats (vgateway_id, avg_latency_ms)
		VALUES ($1, -0.01)
	`, gatewayID); err == nil {
		t.Error("inserting negative avg_latency_ms succeeded")
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO vgateway_stats (vgateway_id)
		VALUES ($1)
	`, uuid.NewString()); err == nil {
		t.Error("inserting stats for an unknown vGateway succeeded")
	}

	if _, err := conn.ExecContext(ctx, "DELETE FROM vgateways WHERE id = $1", gatewayID); err != nil {
		t.Fatalf("deleting vGateway: %v", err)
	}
	var statsCount int
	if err := conn.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM vgateway_stats WHERE id = $1",
		statsID,
	).Scan(&statsCount); err != nil {
		t.Fatalf("counting cascaded vGateway stats: %v", err)
	}
	if statsCount != 0 {
		t.Errorf("stats rows after deleting vGateway = %d, want 0", statsCount)
	}

	applyMigrationFile(t, ctx, conn, "000002_create_vgateways.down.sql")

	for _, tableName := range []string{"vgateway_stats", "vgateways"} {
		if relationExists(t, ctx, conn, schemaName, tableName) {
			t.Errorf("table %q still exists after down migration", tableName)
		}
	}
}

func TestCreateDevicesDatasourcesMigration_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
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
	schemaName := "device_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated schema: %v", err)
		}
	})
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schemaName+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, conn, "000002_create_vgateways.up.sql")
	applyMigrationFile(t, ctx, conn, "000003_create_devices_datasources.up.sql")
	for _, relation := range []string{"devices", "datasources", "idx_devices_vgateway", "idx_devices_type", "idx_datasources_device", "idx_datasources_type"} {
		if !relationExists(t, ctx, conn, schemaName, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	var gatewayID, deviceID, datasourceID string
	if err := conn.QueryRowContext(ctx, `INSERT INTO vgateways (name,type,config) VALUES ('PLC','modbus_tcp','{}') RETURNING id`).Scan(&gatewayID); err != nil {
		t.Fatalf("inserting gateway: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO devices (vgateway_id,name,type,config) VALUES ($1,'Meter','modbus_device','{"unit_id":1}') RETURNING id`, gatewayID).Scan(&deviceID); err != nil {
		t.Fatalf("inserting device: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO datasources (device_id,name,type,config) VALUES ($1,'Registers','modbus_read','{"function_code":3}') RETURNING id`, deviceID).Scan(&datasourceID); err != nil {
		t.Fatalf("inserting datasource: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO devices (vgateway_id,name,type,config) VALUES ($1,'Meter','future_device','{}')`, gatewayID); err == nil {
		t.Error("duplicate device name succeeded")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO datasources (device_id,name,type,config) VALUES ($1,'Invalid','future_source','[]')`, deviceID); err == nil {
		t.Error("non-object datasource config succeeded")
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM devices WHERE id = $1`, deviceID); err != nil {
		t.Fatalf("deleting device: %v", err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM datasources WHERE id = $1`, datasourceID).Scan(&count); err != nil {
		t.Fatalf("counting datasource: %v", err)
	}
	if count != 0 {
		t.Errorf("datasources after cascade = %d", count)
	}
	applyMigrationFile(t, ctx, conn, "000003_create_devices_datasources.down.sql")
	for _, table := range []string{"datasources", "devices"} {
		if relationExists(t, ctx, conn, schemaName, table) {
			t.Errorf("table %q remains after down", table)
		}
	}
}

func TestCreateTagsMigration_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
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
	schemaName := "tag_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated schema: %v", err)
		}
	})
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schemaName+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	applyMigrationFile(t, ctx, conn, "000002_create_vgateways.up.sql")
	applyMigrationFile(t, ctx, conn, "000003_create_devices_datasources.up.sql")
	applyMigrationFile(t, ctx, conn, "000004_create_tags.up.sql")

	for _, relation := range []string{"tags", "tag_dependencies", "idx_tags_datasource", "idx_tags_type", "idx_tags_enabled", "idx_tag_dependencies_dependency"} {
		if !relationExists(t, ctx, conn, schemaName, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	var gatewayID, deviceID, datasourceID string
	if err := conn.QueryRowContext(ctx, `INSERT INTO vgateways (name,type,config) VALUES ('Tag PLC','modbus_tcp','{}') RETURNING id`).Scan(&gatewayID); err != nil {
		t.Fatalf("inserting gateway: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO devices (vgateway_id,name,type,config) VALUES ($1,'Tag meter','modbus_device','{}') RETURNING id`, gatewayID).Scan(&deviceID); err != nil {
		t.Fatalf("inserting device: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO datasources (device_id,name,type,config) VALUES ($1,'Tag registers','modbus_read','{}') RETURNING id`, deviceID).Scan(&datasourceID); err != nil {
		t.Fatalf("inserting datasource: %v", err)
	}

	var readingID, constantID, calculatedID string
	var enabled bool
	if err := conn.QueryRowContext(ctx, `INSERT INTO tags (datasource_id,name,type,data_type,config) VALUES ($1,'Line voltage','reading','float32','{"decoder":{"type":"binary_numeric","config":{"byte_offset":0,"byte_order":"big_endian"}}}') RETURNING id,enabled`, datasourceID).Scan(&readingID, &enabled); err != nil {
		t.Fatalf("inserting reading tag: %v", err)
	}
	if !enabled {
		t.Error("reading tag enabled default = false, want true")
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO tags (name,type,data_type,config) VALUES ('Nominal voltage','constant','float64','{"value":230}') RETURNING id`).Scan(&constantID); err != nil {
		t.Fatalf("inserting constant tag: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `INSERT INTO tags (name,type,data_type,config) VALUES ('Voltage delta','calculated','float64','{"expression":"reading - constant"}') RETURNING id`).Scan(&calculatedID); err != nil {
		t.Fatalf("inserting calculated tag: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO tag_dependencies (tag_id,depends_on_tag_id) VALUES ($1,$2),($1,$3)`, calculatedID, readingID, constantID); err != nil {
		t.Fatalf("inserting dependencies: %v", err)
	}

	expectExecFailure(t, ctx, conn, "duplicate tag name", `INSERT INTO tags (name,type,data_type,config) VALUES ('Nominal voltage','constant','float64','{}')`)
	expectExecFailure(t, ctx, conn, "unsupported tag type", `INSERT INTO tags (name,type,data_type,config) VALUES ('Bad type','future','float64','{}')`)
	expectExecFailure(t, ctx, conn, "unsupported data type", `INSERT INTO tags (name,type,data_type,config) VALUES ('Bad data','constant','decimal128','{}')`)
	expectExecFailure(t, ctx, conn, "non-object config", `INSERT INTO tags (name,type,data_type,config) VALUES ('Bad config','constant','float64','[]')`)
	expectExecFailure(t, ctx, conn, "reading without datasource", `INSERT INTO tags (name,type,data_type,config) VALUES ('Missing source','reading','uint16','{}')`)
	expectExecFailure(t, ctx, conn, "constant with datasource", `INSERT INTO tags (datasource_id,name,type,data_type,config) VALUES ($1,'Unexpected source','constant','float64','{}')`, datasourceID)
	expectExecFailure(t, ctx, conn, "self dependency", `INSERT INTO tag_dependencies (tag_id,depends_on_tag_id) VALUES ($1,$1)`, calculatedID)
	expectExecFailure(t, ctx, conn, "duplicate dependency", `INSERT INTO tag_dependencies (tag_id,depends_on_tag_id) VALUES ($1,$2)`, calculatedID, readingID)

	if _, err := conn.ExecContext(ctx, `DELETE FROM datasources WHERE id = $1`, datasourceID); err != nil {
		t.Fatalf("deleting datasource: %v", err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = $1`, readingID).Scan(&count); err != nil {
		t.Fatalf("counting cascaded reading tag: %v", err)
	}
	if count != 0 {
		t.Errorf("reading tags after datasource cascade = %d, want 0", count)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM tag_dependencies WHERE depends_on_tag_id = $1`, readingID).Scan(&count); err != nil {
		t.Fatalf("counting cascaded source dependencies: %v", err)
	}
	if count != 0 {
		t.Errorf("dependencies after reading tag cascade = %d, want 0", count)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = $1`, calculatedID).Scan(&count); err != nil {
		t.Fatalf("counting calculated tag after source cascade: %v", err)
	}
	if count != 1 {
		t.Errorf("calculated tags after source cascade = %d, want 1", count)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM tags WHERE id = $1`, calculatedID); err != nil {
		t.Fatalf("deleting calculated tag: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM tag_dependencies WHERE tag_id = $1`, calculatedID).Scan(&count); err != nil {
		t.Fatalf("counting cascaded calculated dependencies: %v", err)
	}
	if count != 0 {
		t.Errorf("dependencies after calculated tag delete = %d, want 0", count)
	}

	applyMigrationFile(t, ctx, conn, "000004_create_tags.down.sql")
	for _, table := range []string{"tag_dependencies", "tags"} {
		if relationExists(t, ctx, conn, schemaName, table) {
			t.Errorf("table %q remains after down", table)
		}
	}
}

func expectExecFailure(t *testing.T, ctx context.Context, conn *sql.Conn, name, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, query, args...); err == nil {
		t.Errorf("%s succeeded", name)
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
