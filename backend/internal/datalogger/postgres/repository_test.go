package dataloggerpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCRUDSelectionsAndFilters_Integration(t *testing.T) {
	db, tags := newRepositoryDatabase(t)
	repository := NewRepository(db)
	ctx := context.Background()
	startAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	description := "Main logger"
	interval := datalogger.Logger{Name: "Plant % logger", Description: &description, Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: json.RawMessage(`{"interval_seconds":60}`)}
	if err := repository.Create(ctx, &interval, []uuid.UUID{tags[1], tags[0]}); err != nil {
		t.Fatalf("Create(interval) error = %v", err)
	}
	found, err := repository.Find(ctx, interval.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.ID != interval.ID || found.Description == nil || *found.Description != description || found.TagCount != 2 || len(found.Tags) != 2 || found.Tags[0].ID != tags[1] || found.Tags[1].ID != tags[0] || found.Tags[0].Position != 0 || found.Tags[1].Position != 1 {
		t.Errorf("Find() = %#v", found)
	}

	schedule := datalogger.Logger{Name: "Nightly", Enabled: false, Timezone: "Asia/Bangkok", Mode: datalogger.ModeSchedule, StartAt: startAt, Config: json.RawMessage(`{"unit":"day","every":1,"times":["23:00"]}`)}
	if err := repository.Create(ctx, &schedule, []uuid.UUID{tags[0]}); err != nil {
		t.Fatalf("Create(schedule with shared Tag) error = %v", err)
	}
	assertList(t, repository, datalogger.ListInput{Mode: modePointer(datalogger.ModeInterval), Page: 1, PerPage: 20}, 1, interval.ID, 2)
	assertList(t, repository, datalogger.ListInput{Enabled: boolPointer(false), Page: 1, PerPage: 20}, 1, schedule.ID, 1)
	assertList(t, repository, datalogger.ListInput{Search: "PLANT", Page: 1, PerPage: 20}, 1, interval.ID, 2)
	assertList(t, repository, datalogger.ListInput{Search: "%", Page: 1, PerPage: 20}, 1, interval.ID, 2)
	assertList(t, repository, datalogger.ListInput{Search: "_", Page: 1, PerPage: 20}, 0, uuid.Nil, 0)
	page, err := repository.List(ctx, datalogger.ListInput{Page: 1, PerPage: 1})
	if err != nil {
		t.Fatalf("List(paginated) error = %v", err)
	}
	if len(page.Data) != 1 || page.Total != 2 || page.TotalPages != 2 || page.Page != 1 || page.PerPage != 1 {
		t.Errorf("List(paginated) = %#v", page)
	}

	interval.Name = "Updated"
	interval.Description = nil
	interval.Enabled = false
	interval.Timezone = "Asia/Bangkok"
	interval.Mode = datalogger.ModeSchedule
	endAt := startAt.Add(48 * time.Hour)
	interval.EndAt = &endAt
	interval.Config = json.RawMessage(`{"unit":"week","every":1,"times":["08:00"],"weekdays":[1]}`)
	if err := repository.Update(ctx, &interval, []uuid.UUID{tags[2], tags[0]}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repository.Find(ctx, interval.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	if updated.Name != "Updated" || updated.Description != nil || updated.Enabled || updated.Timezone != "Asia/Bangkok" || updated.Mode != datalogger.ModeSchedule || updated.EndAt == nil || !updated.EndAt.Equal(endAt) || len(updated.Tags) != 2 || updated.Tags[0].ID != tags[2] {
		t.Errorf("updated = %#v", updated)
	}

	interval.Name = "Must roll back"
	if err := repository.Update(ctx, &interval, []uuid.UUID{uuid.New()}); !errors.Is(err, datalogger.ErrLoggerTagNotFound) {
		t.Fatalf("Update(missing Tag) error = %v", err)
	}
	rolledBack, err := repository.Find(ctx, interval.ID)
	if err != nil {
		t.Fatalf("Find(rolled back) error = %v", err)
	}
	if rolledBack.Name != "Updated" || len(rolledBack.Tags) != 2 || rolledBack.Tags[0].ID != tags[2] {
		t.Errorf("rolled back = %#v", rolledBack)
	}

	duplicate := datalogger.Logger{Name: schedule.Name, Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: json.RawMessage(`{"interval_seconds":1}`)}
	if err := repository.Create(ctx, &duplicate, []uuid.UUID{tags[0]}); !errors.Is(err, datalogger.ErrLoggerNameExists) {
		t.Fatalf("Create(duplicate name) error = %v", err)
	}
	missingTag := datalogger.Logger{Name: "Missing Tag", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: json.RawMessage(`{"interval_seconds":1}`)}
	if err := repository.Create(ctx, &missingTag, []uuid.UUID{uuid.New()}); !errors.Is(err, datalogger.ErrLoggerTagNotFound) {
		t.Fatalf("Create(missing Tag) error = %v", err)
	}
	if _, err := repository.Find(ctx, missingTag.ID); !errors.Is(err, datalogger.ErrLoggerNotFound) {
		t.Fatalf("Find(rolled back create) error = %v", err)
	}
	invalid := datalogger.Logger{Name: "Invalid", Enabled: true, Timezone: "UTC", Mode: "event", StartAt: startAt, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &invalid, []uuid.UUID{tags[0]}); !errors.Is(err, datalogger.ErrInvalidLogger) {
		t.Fatalf("Create(invalid logger) error = %v", err)
	}

	if err := repository.Delete(ctx, interval.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repository.Find(ctx, interval.ID); !errors.Is(err, datalogger.ErrLoggerNotFound) {
		t.Fatalf("Find(deleted) error = %v", err)
	}
	if err := repository.Delete(ctx, interval.ID); !errors.Is(err, datalogger.ErrLoggerNotFound) {
		t.Fatalf("Delete(missing) error = %v", err)
	}
}

func assertList(t *testing.T, repository datalogger.Repository, input datalogger.ListInput, wantCount int, wantID uuid.UUID, wantTagCount int) {
	t.Helper()
	result, err := repository.List(context.Background(), input)
	if err != nil {
		t.Fatalf("List(%#v) error = %v", input, err)
	}
	if len(result.Data) != wantCount || result.Total != int64(wantCount) {
		t.Fatalf("List(%#v) = %#v", input, result)
	}
	if wantCount == 1 && (result.Data[0].ID != wantID || result.Data[0].TagCount != wantTagCount || result.Data[0].Tags != nil) {
		t.Errorf("List(%#v) entity = %#v", input, result.Data[0])
	}
}

func newRepositoryDatabase(t *testing.T) (*gorm.DB, []uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Data Logger repository integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "data_logger_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
			t.Errorf("dropping schema: %v", err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening GORM: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting SQL DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, migrationPath := range []string{"../../../migrations/000002_create_vgateways.up.sql", "../../../migrations/000003_create_devices_datasources.up.sql", "../../../migrations/000004_create_tags.up.sql", "../../../migrations/000006_create_data_loggers.up.sql", "../../../migrations/000007_create_tag_values_raw.up.sql"} {
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("reading migration: %v", err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration: %v", err)
		}
	}
	tagIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for index, tagID := range tagIDs {
		if err := db.Exec(`INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,?,'float64',true,'{}')`, tagID, "Logger Tag "+string(rune('A'+index)), "constant").Error; err != nil {
			t.Fatalf("inserting Tag: %v", err)
		}
	}
	return db, tagIDs
}

func modePointer(value datalogger.Mode) *datalogger.Mode { return &value }
func boolPointer(value bool) *bool                       { return &value }
