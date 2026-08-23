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

func TestCredentialProfilesMigrationUsesCiphertextOnlyAndCascades_Integration(t *testing.T) {
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
	schema := "data_publisher_secrets_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

	for _, relation := range []string{"credential_profiles", "credential_secrets", "idx_credential_secrets_credential", "idx_data_publishers_credential"} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	var secretRevisionDefault string
	if err := connection.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publishers' AND column_name='secret_revision'`, schema).Scan(&secretRevisionDefault); err != nil || secretRevisionDefault != "0" {
		t.Errorf("secret_revision default = %q, %v", secretRevisionDefault, err)
	}
	var plaintextColumns int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='credential_secrets' AND column_name IN ('plaintext','value','secret','password','private_key','certificate')`, schema).Scan(&plaintextColumns); err != nil {
		t.Fatalf("checking plaintext columns: %v", err)
	}
	if plaintextColumns != 0 {
		t.Fatalf("secret table has %d plaintext-oriented columns", plaintextColumns)
	}

	var credentialID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO credential_profiles (type,name) VALUES ('mqtt','Test Broker') RETURNING id`).Scan(&credentialID); err != nil {
		t.Fatalf("creating Credential Profile: %v", err)
	}
	var publisherID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO data_publishers (type,name,config_version,credential_id) VALUES ('mqtt','Secret Test',2,$1) RETURNING id`, credentialID).Scan(&publisherID); err != nil {
		t.Fatalf("creating Publisher: %v", err)
	}
	validCiphertext := make([]byte, 32)
	if _, err := connection.ExecContext(ctx, `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.password','opaque','master-v1',$2)`, credentialID, validCiphertext); err != nil {
		t.Fatalf("creating encrypted secret: %v", err)
	}
	for _, invalid := range []struct {
		name      string
		statement string
		args      []any
	}{
		{name: "unsafe slot", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'Upper','opaque','master-v1',$2)`, args: []any{credentialID, validCiphertext}},
		{name: "slot kind mismatch", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.username','ca_certificate','master-v1',$2)`, args: []any{credentialID, validCiphertext}},
		{name: "blank key ID", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.username','opaque','',$2)`, args: []any{credentialID, validCiphertext}},
		{name: "short ciphertext", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.username','opaque','master-v1',$2)`, args: []any{credentialID, []byte{1}}},
		{name: "zero revision", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext,revision) VALUES ($1,'mqtt.username','opaque','master-v1',$2,0)`, args: []any{credentialID, validCiphertext}},
		{name: "duplicate slot", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.password','opaque','master-v1',$2)`, args: []any{credentialID, validCiphertext}},
		{name: "missing Credential", statement: `INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.username','opaque','master-v1',$2)`, args: []any{uuid.New(), validCiphertext}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := connection.ExecContext(ctx, invalid.statement, invalid.args...); err == nil {
				t.Errorf("invalid statement succeeded: %s", invalid.statement)
			}
		})
	}
	if _, err := connection.ExecContext(ctx, `UPDATE data_publishers SET secret_revision=-1 WHERE id=$1`, publisherID); err == nil {
		t.Error("negative secret revision succeeded")
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM data_publishers WHERE id=$1`, publisherID); err != nil {
		t.Fatalf("deleting Publisher: %v", err)
	}
	var secretCount int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_secrets WHERE credential_id=$1`, credentialID).Scan(&secretCount); err != nil || secretCount != 1 {
		t.Errorf("secrets after Publisher delete = %d, %v", secretCount, err)
	}
	if _, err := connection.ExecContext(ctx, `DELETE FROM credential_profiles WHERE id=$1`, credentialID); err != nil {
		t.Fatalf("deleting Credential Profile: %v", err)
	}
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_secrets WHERE credential_id=$1`, credentialID).Scan(&secretCount); err != nil || secretCount != 0 {
		t.Errorf("secrets after Credential delete = %d, %v", secretCount, err)
	}

	applyMigrationFile(t, ctx, connection, "000013_create_data_publisher_secrets.down.sql")
	if relationExists(t, ctx, connection, schema, "credential_secrets") || relationExists(t, ctx, connection, schema, "credential_profiles") {
		t.Error("Credential relations remain after down migration")
	}
	var revisionColumns int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publishers' AND column_name IN ('secret_revision','credential_id')`, schema).Scan(&revisionColumns); err != nil || revisionColumns != 0 {
		t.Errorf("secret_revision columns after down = %d, %v", revisionColumns, err)
	}
}
