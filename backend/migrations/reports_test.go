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

func TestReportsMigrationConstraintsAndDown_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("getting connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	schema := "reports_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql", "000006_create_data_loggers.up.sql", "000007_create_tag_values_raw.up.sql", "000008_add_data_logger_storage_limits.up.sql", "000009_create_reports.up.sql"} {
		applyMigrationFile(t, ctx, conn, migration)
	}
	loggerID, tagID := insertRawMigrationFixture(t, ctx, conn)
	secondTagID := uuid.New()
	if _, err := conn.ExecContext(ctx, `INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES ($1,'Second Report Tag','constant','float64',true,'{"value":2}')`, secondTagID); err != nil {
		t.Fatalf("inserting second selected Tag: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,1)`, loggerID, secondTagID); err != nil {
		t.Fatalf("selecting second Report Tag: %v", err)
	}
	reportID := uuid.New()
	if _, err := conn.ExecContext(ctx, `INSERT INTO reports (id,name,logger_id,timezone,mode,bucket) VALUES ($1,'Energy','`+loggerID.String()+`','UTC','aggregate','1h')`, reportID); err != nil {
		t.Fatalf("inserting Report: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO report_columns (report_id,logger_id,tag_id,position,name,aggregate) VALUES ($1,$2,$3,0,'Power','avg')`, reportID, loggerID, tagID); err != nil {
		t.Fatalf("inserting column: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO report_columns (report_id,logger_id,tag_id,position,name,aggregate) VALUES ($1,$2,$3,1,' power ','max')`, reportID, loggerID, secondTagID); err == nil {
		t.Error("case-insensitive duplicate column name succeeded")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO report_columns (report_id,logger_id,tag_id,position,name,aggregate) VALUES ($1,$2,$3,1,'Missing','max')`, reportID, loggerID, uuid.New()); err == nil {
		t.Error("unselected Report Tag succeeded")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO reports (name,logger_id,timezone,mode,bucket) VALUES ('Bad raw',$1,'UTC','raw','1h')`, loggerID); err == nil {
		t.Error("raw Report with bucket succeeded")
	}
	applyMigrationFile(t, ctx, conn, "000009_create_reports.down.sql")
	if relationExists(t, ctx, conn, schema, "reports") || relationExists(t, ctx, conn, schema, "report_columns") {
		t.Error("Report tables remain after down migration")
	}
}
