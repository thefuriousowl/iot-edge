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

func TestHTTPPublisherMigrationReplacesDormantModbusTypeAndExpandsCredentials_Integration(t *testing.T) {
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
		t.Fatalf("getting connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	schema := "http_publishers_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{
		"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql",
		"000010_create_plugin_instances.up.sql", "000012_create_data_publishers.up.sql", "000013_create_data_publisher_secrets.up.sql",
	} {
		applyMigrationFile(t, ctx, connection, migration)
	}

	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('modbus_tcp_server','Obsolete Modbus',2)`); err != nil {
		t.Fatalf("creating obsolete Publisher fixture: %v", err)
	}
	up, err := os.ReadFile("000014_replace_modbus_publisher_with_http_client.up.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	if _, err := connection.ExecContext(ctx, string(up)); err == nil {
		t.Fatal("migration accepted an obsolete Modbus Publisher")
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM data_publishers WHERE type='modbus_tcp_server'`); err != nil {
		t.Fatalf("removing obsolete fixture: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000014_replace_modbus_publisher_with_http_client.up.sql")

	var publisherID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('http_client','Plant Receiver',1) RETURNING id`).Scan(&publisherID); err != nil {
		t.Fatalf("creating HTTP Client Publisher: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('modbus_tcp_server','Removed Type',2)`); err == nil {
		t.Fatal("removed Modbus Publisher type remains accepted")
	}
	var credentialID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO credential_profiles (type,name) VALUES ('http','Plant HTTP') RETURNING id`).Scan(&credentialID); err != nil {
		t.Fatalf("creating HTTP Credential Profile: %v", err)
	}
	ciphertext := make([]byte, 32)
	for name, kind := range map[string]string{
		"http.username": "opaque", "http.password": "opaque", "http.api_key": "opaque", "http.bearer_token": "opaque",
		"http.oauth_client_secret": "opaque", "http.custom_ca": "ca_certificate", "http.client_identity": "client_identity",
	} {
		if _, err := connection.ExecContext(ctx, `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,$2,$3,'master-v1',$4)`, credentialID, name, kind, ciphertext); err != nil {
			t.Fatalf("creating %s secret: %v", name, err)
		}
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'http.api_key','ca_certificate','master-v1',$2)`, credentialID, ciphertext); err == nil {
		t.Fatal("HTTP secret kind mismatch succeeded")
	}

	down, err := os.ReadFile("000014_replace_modbus_publisher_with_http_client.down.sql")
	if err != nil {
		t.Fatalf("reading down migration: %v", err)
	}
	if _, err := connection.ExecContext(ctx, string(down)); err == nil {
		t.Fatal("down migration accepted active HTTP rows")
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM data_publishers WHERE id=$1`, publisherID); err != nil {
		t.Fatalf("deleting HTTP Client Publisher: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM credential_profiles WHERE id=$1`, credentialID); err != nil {
		t.Fatalf("deleting HTTP Credential Profile: %v", err)
	}
	applyMigrationFile(t, ctx, connection, "000014_replace_modbus_publisher_with_http_client.down.sql")
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('modbus_tcp_server','Restored Legacy Type',2)`); err != nil {
		t.Fatalf("down migration did not restore the old type constraint: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('http_client','Removed HTTP Client',1)`); err == nil {
		t.Fatal("down migration retained HTTP Client type")
	}
}
