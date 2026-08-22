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

func TestCreateDataLoggersMigration_Integration(t *testing.T) {
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
		t.Fatalf("getting dedicated connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	schema := "data_logger_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("dropping schema: %v", err)
		}
	})
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql", "000006_create_data_loggers.up.sql"} {
		applyMigrationFile(t, ctx, conn, migration)
	}
	for _, relation := range []string{"data_loggers", "data_logger_tags", "idx_data_loggers_enabled", "idx_data_loggers_mode", "idx_data_loggers_start_at", "idx_data_logger_tags_tag"} {
		if !relationExists(t, ctx, conn, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	tagID := insertTagValueMigrationFixture(t, ctx, conn)
	intervalID := uuid.New()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO data_loggers (id,name,timezone,mode,start_at,config)
		VALUES ($1,'Interval','UTC','interval','2026-08-23T00:00:00Z','{"interval_seconds":60}')
	`, intervalID); err != nil {
		t.Fatalf("inserting interval logger: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,0)`, intervalID, tagID); err != nil {
		t.Fatalf("selecting interval Tag: %v", err)
	}
	scheduleID := uuid.New()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO data_loggers (id,name,timezone,mode,start_at,end_at,config)
		VALUES ($1,'Schedule','Asia/Bangkok','schedule','2026-08-23T00:00:00Z','2026-08-24T00:00:00Z','{"unit":"week","every":1,"times":["08:00"],"weekdays":[1]}')
	`, scheduleID); err != nil {
		t.Fatalf("inserting schedule logger: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,0)`, scheduleID, tagID); err != nil {
		t.Fatalf("selecting same Tag in second logger: %v", err)
	}

	invalidLoggers := []struct {
		name, mode, config, endAt string
	}{
		{name: "invalid mode", mode: "event", config: `{"interval_seconds":1}`},
		{name: "non-object config", mode: "interval", config: `[]`},
		{name: "missing interval", mode: "interval", config: `{}`},
		{name: "zero interval", mode: "interval", config: `{"interval_seconds":0}`},
		{name: "fractional interval", mode: "interval", config: `{"interval_seconds":1.5}`},
		{name: "overflowing interval", mode: "interval", config: `{"interval_seconds":9223372037}`},
		{name: "invalid schedule unit", mode: "schedule", config: `{"unit":"month","every":1}`},
		{name: "fractional schedule", mode: "schedule", config: `{"unit":"day","every":1.5}`},
		{name: "overflowing schedule", mode: "schedule", config: `{"unit":"hour","every":2562048}`},
		{name: "end before start", mode: "interval", config: `{"interval_seconds":1}`, endAt: "2026-08-22T00:00:00Z"},
	}
	for _, test := range invalidLoggers {
		t.Run(test.name, func(t *testing.T) {
			var endAt any
			if test.endAt != "" {
				endAt = test.endAt
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO data_loggers (name,timezone,mode,start_at,end_at,config)
				VALUES ($1,'UTC',$2,'2026-08-23T00:00:00Z',$3,CAST($4 AS JSONB))
			`, "Invalid "+test.name, test.mode, endAt, test.config); err == nil {
				t.Error("invalid Data Logger insert succeeded")
			}
		})
	}

	extraTagID, anotherTagID := uuid.New(), uuid.New()
	if _, err := conn.ExecContext(ctx, `INSERT INTO tags (id,name,type,data_type,config) VALUES ($1,'Extra Tag','constant','float64','{}'),($2,'Another Tag','constant','float64','{}')`, extraTagID, anotherTagID); err != nil {
		t.Fatalf("inserting extra Tags: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,-1)`, intervalID, extraTagID); err == nil {
		t.Error("negative Tag position succeeded")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO data_logger_tags (logger_id,tag_id,position) VALUES ($1,$2,0)`, intervalID, anotherTagID); err == nil {
		t.Error("duplicate Tag position succeeded")
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM tags WHERE id=$1", tagID); err != nil {
		t.Fatalf("deleting selected Tag: %v", err)
	}
	assertMigrationCount(t, ctx, conn, "SELECT COUNT(*) FROM data_logger_tags", 0)
	if _, err := conn.ExecContext(ctx, "DELETE FROM data_loggers WHERE id=$1", intervalID); err != nil {
		t.Fatalf("deleting interval logger: %v", err)
	}
	assertMigrationCount(t, ctx, conn, "SELECT COUNT(*) FROM data_loggers", 1)

	applyMigrationFile(t, ctx, conn, "000006_create_data_loggers.down.sql")
	for _, relation := range []string{"data_logger_tags", "data_loggers"} {
		if relationExists(t, ctx, conn, schema, relation) {
			t.Errorf("%s still exists after down migration", relation)
		}
	}
}

func assertMigrationCount(t *testing.T, ctx context.Context, conn *sql.Conn, query string, want int) {
	t.Helper()
	var count int
	if err := conn.QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("querying count: %v", err)
	}
	if count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
}
