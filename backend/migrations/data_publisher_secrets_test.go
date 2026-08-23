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

func TestDataPublisherSecretsMigrationUsesCiphertextOnlyAndCascades_Integration(t *testing.T) {
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

	for _, relation := range []string{"data_publisher_secrets", "idx_data_publisher_secrets_publisher"} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}
	var secretRevisionDefault string
	if err := connection.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publishers' AND column_name='secret_revision'`, schema).Scan(&secretRevisionDefault); err != nil || secretRevisionDefault != "0" {
		t.Errorf("secret_revision default = %q, %v", secretRevisionDefault, err)
	}
	var plaintextColumns int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publisher_secrets' AND column_name IN ('plaintext','value','secret','password','private_key','certificate')`, schema).Scan(&plaintextColumns); err != nil {
		t.Fatalf("checking plaintext columns: %v", err)
	}
	if plaintextColumns != 0 {
		t.Fatalf("secret table has %d plaintext-oriented columns", plaintextColumns)
	}

	var publisherID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO data_publishers (type,name,config_version) VALUES ('mqtt','Secret Test',2) RETURNING id`).Scan(&publisherID); err != nil {
		t.Fatalf("creating Publisher: %v", err)
	}
	validCiphertext := make([]byte, 32)
	if _, err := connection.ExecContext(ctx, `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.password','opaque','master-v1',$2)`, publisherID, validCiphertext); err != nil {
		t.Fatalf("creating encrypted secret: %v", err)
	}
	for _, invalid := range []struct {
		name      string
		statement string
		args      []any
	}{
		{name: "unsafe name", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'Upper','opaque','master-v1',$2)`, args: []any{publisherID, validCiphertext}},
		{name: "unsupported kind", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'future','future','master-v1',$2)`, args: []any{publisherID, validCiphertext}},
		{name: "blank key ID", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'blank.key','opaque','',$2)`, args: []any{publisherID, validCiphertext}},
		{name: "short ciphertext", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'short','opaque','master-v1',$2)`, args: []any{publisherID, []byte{1}}},
		{name: "zero revision", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext,revision) VALUES ($1,'zero','opaque','master-v1',$2,0)`, args: []any{publisherID, validCiphertext}},
		{name: "duplicate reference", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'mqtt.password','opaque','master-v1',$2)`, args: []any{publisherID, validCiphertext}},
		{name: "missing Publisher", statement: `INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext) VALUES ($1,'missing','opaque','master-v1',$2)`, args: []any{uuid.New(), validCiphertext}},
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
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM data_publisher_secrets WHERE publisher_id=$1`, publisherID).Scan(&secretCount); err != nil || secretCount != 0 {
		t.Errorf("secrets after Publisher delete = %d, %v", secretCount, err)
	}

	applyMigrationFile(t, ctx, connection, "000013_create_data_publisher_secrets.down.sql")
	if relationExists(t, ctx, connection, schema, "data_publisher_secrets") {
		t.Error("data_publisher_secrets remains after down migration")
	}
	var revisionColumns int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='data_publishers' AND column_name='secret_revision'`, schema).Scan(&revisionColumns); err != nil || revisionColumns != 0 {
		t.Errorf("secret_revision columns after down = %d, %v", revisionColumns, err)
	}
}
