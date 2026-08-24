package dataloggerhttp

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDataLoggerCRUD_EndToEnd(t *testing.T) {
	db, tags := newHTTPIntegrationDatabase(t)
	historyRepository := dataloggerpostgres.NewHistoryRepository(db)
	service, err := datalogger.NewService(dataloggerpostgres.NewRepository(db), historyRepository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	app := newHandlerApp(service)
	t.Cleanup(func() { _ = app.Shutdown() })

	response := request(t, app, http.MethodPost, "/api/data-loggers/", `{"name":"Main Logger","description":"Plant values","timezone":"UTC","mode":"interval","start_at":"2026-08-23T00:00:00Z","max_age_seconds":86400,"config":{},"tag_ids":["`+tags[1].String()+`","`+tags[0].String()+`"]}`)
	assertStatus(t, response, fiber.StatusCreated)
	var created datalogger.Logger
	decodeResponse(t, response, &created)
	if created.ID == uuid.Nil || created.Name != "Main Logger" || !created.Enabled || created.MaxAgeSeconds == nil || *created.MaxAgeSeconds != 86400 || string(created.Config) != `{"interval_seconds":60}` || created.TagCount != 2 || len(created.Tags) != 2 || created.Tags[0].ID != tags[1] {
		t.Errorf("created = %#v", created)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/?mode=interval&enabled=true&search=main&page=1&per_page=10", "")
	assertStatus(t, response, fiber.StatusOK)
	var list struct {
		Data       []datalogger.Logger `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &list)
	if len(list.Data) != 1 || list.Data[0].ID != created.ID || list.Data[0].TagCount != 2 || list.Data[0].Tags != nil || list.Pagination.Total != 1 {
		t.Errorf("list = %#v", list)
	}

	response = request(t, app, http.MethodPut, "/api/data-loggers/"+created.ID.String(), `{"description":null,"timezone":"Asia/Bangkok","mode":"schedule","end_at":"2026-08-30T00:00:00Z","max_age_seconds":null,"config":{"unit":"week","times":["17:30","08:00"],"weekdays":[5,1]},"tag_ids":["`+tags[0].String()+`"]}`)
	assertStatus(t, response, fiber.StatusOK)
	var updated datalogger.Logger
	decodeResponse(t, response, &updated)
	if updated.Description != nil || updated.Timezone != "Asia/Bangkok" || updated.Mode != datalogger.ModeSchedule || updated.MaxAgeSeconds != nil || string(updated.Config) != `{"unit":"week","every":1,"times":["08:00","17:30"],"weekdays":[1,5]}` || updated.TagCount != 1 || updated.Tags[0].ID != tags[0] {
		t.Errorf("updated = %#v", updated)
	}

	response = request(t, app, http.MethodPost, "/api/data-loggers/", `{"name":"Main Logger","timezone":"UTC","mode":"interval","start_at":"2026-08-23T00:00:00Z","tag_ids":["`+tags[0].String()+`"]}`)
	assertError(t, response, fiber.StatusConflict, "DLG002")
	response = request(t, app, http.MethodPut, "/api/data-loggers/"+created.ID.String(), `{"tag_ids":["`+uuid.NewString()+`"]}`)
	assertError(t, response, fiber.StatusBadRequest, "DLG003")
	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String(), "")
	assertStatus(t, response, fiber.StatusOK)
	var rolledBack datalogger.Logger
	decodeResponse(t, response, &rolledBack)
	if rolledBack.TagCount != 1 || rolledBack.Tags[0].ID != tags[0] {
		t.Errorf("rolled back = %#v", rolledBack)
	}

	batchAt := time.Date(2026, time.August, 23, 1, 0, 0, 0, time.UTC)
	if err := historyRepository.WriteBatch(t.Context(), datalogger.RawBatch{LoggerID: created.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{{TagID: tags[0], ObservedAt: batchAt.Add(-time.Second), DataType: "float64", Value: 42.5, Quality: datalogger.RawQualityGood}}}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	historyQuery := url.Values{"tag_id": {tags[0].String()}, "from": {batchAt.Add(-time.Hour).Format(time.RFC3339)}, "to": {batchAt.Add(time.Hour).Format(time.RFC3339)}, "page": {"1"}, "per_page": {"10"}}
	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String()+"/history?"+historyQuery.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var history struct {
		Data        []datalogger.RawValue `json:"data"`
		LastBatchAt *time.Time            `json:"last_batch_at"`
		Pagination  struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &history)
	if len(history.Data) != 1 || history.Data[0].Value != 42.5 || history.LastBatchAt == nil || !history.LastBatchAt.Equal(batchAt) || history.Pagination.Total != 1 {
		t.Errorf("history = %#v", history)
	}

	response = request(t, app, http.MethodDelete, "/api/data-loggers/"+created.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String(), "")
	assertError(t, response, fiber.StatusNotFound, "DLG001")
}

func TestDataManagementRetentionEndpoints_EndToEnd(t *testing.T) {
	db, tags := newHTTPIntegrationDatabase(t)
	historyRepository := dataloggerpostgres.NewHistoryRepository(db)
	service, err := datalogger.NewService(dataloggerpostgres.NewRepository(db), historyRepository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	app := newHandlerApp(service)
	t.Cleanup(func() { _ = app.Shutdown() })

	now := time.Now().UTC().Truncate(time.Second)
	response := request(t, app, http.MethodPost, "/api/data-loggers/", `{"name":"Retention Logger","timezone":"UTC","mode":"interval","start_at":"`+now.Add(-24*time.Hour).Format(time.RFC3339)+`","config":{"interval_seconds":60},"tag_ids":["`+tags[0].String()+`"]}`)
	assertStatus(t, response, fiber.StatusCreated)
	var created datalogger.Logger
	decodeResponse(t, response, &created)

	for index, batchAt := range []time.Time{now.Add(-3 * time.Hour), now.Add(-2 * time.Hour), now.Add(-30 * time.Minute)} {
		if err := historyRepository.WriteBatch(t.Context(), datalogger.RawBatch{LoggerID: created.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{{TagID: tags[0], ObservedAt: batchAt, DataType: "float64", Value: float64(index + 1), Quality: datalogger.RawQualityGood}}}); err != nil {
			t.Fatalf("WriteBatch(%d) error = %v", index, err)
		}
	}
	if err := db.Exec("UPDATE data_loggers SET max_age_seconds = 3600 WHERE id = ?", created.ID).Error; err != nil {
		t.Fatalf("setting retention policy: %v", err)
	}

	response = request(t, app, http.MethodGet, "/api/data-management/overview", "")
	assertStatus(t, response, fiber.StatusOK)
	var overview datalogger.DataManagementOverview
	decodeResponse(t, response, &overview)
	if overview.EvaluatedAt.IsZero() || overview.LoggerCount != 1 || overview.EnabledLoggerCount != 1 || overview.PolicyLoggerCount != 1 || overview.LogicalHistory.RowCount != 3 || overview.LogicalHistory.BatchCount != 3 || overview.LogicalHistory.EstimatedSizeBytes <= 0 || overview.PostgreSQLPhysicalAllocation.RawHistoryBytes <= 0 || overview.PostgreSQLPhysicalAllocation.BatchAccountingBytes <= 0 || overview.PostgreSQLPhysicalAllocation.TotalBytes != overview.PostgreSQLPhysicalAllocation.RawHistoryBytes+overview.PostgreSQLPhysicalAllocation.BatchAccountingBytes {
		t.Errorf("management overview = %#v", overview)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String()+"/retention", "")
	assertStatus(t, response, fiber.StatusOK)
	var status datalogger.RetentionStatus
	decodeResponse(t, response, &status)
	if status.LastRun != nil || status.Plan.Policy.MaxAgeSeconds == nil || *status.Plan.Policy.MaxAgeSeconds != 3600 || status.Plan.Current.BatchCount != 3 || status.Plan.Remove.BatchCount != 2 || status.Plan.EstimatedRetained.BatchCount != 1 {
		t.Errorf("retention status = %#v", status)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String()+"/retention/preview", "")
	assertStatus(t, response, fiber.StatusOK)
	var preview datalogger.RetentionPlan
	decodeResponse(t, response, &preview)
	if preview.Remove.BatchCount != 2 || preview.EstimatedRetained.BatchCount != 1 {
		t.Errorf("retention preview = %#v", preview)
	}
	assertBatchCount(t, db, created.ID, 3)

	response = request(t, app, http.MethodPost, "/api/data-loggers/"+created.ID.String()+"/retention/cleanup", `{"confirm":false,"batch_limit":1}`)
	assertError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
	assertBatchCount(t, db, created.ID, 3)
	response = request(t, app, http.MethodPost, "/api/data-loggers/"+created.ID.String()+"/retention/cleanup", `{"confirm":true,"batch_limit":10001}`)
	assertError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
	assertBatchCount(t, db, created.ID, 3)

	response = request(t, app, http.MethodPost, "/api/data-loggers/"+created.ID.String()+"/retention/cleanup", `{"confirm":true,"batch_limit":1}`)
	assertStatus(t, response, fiber.StatusOK)
	var firstCleanup datalogger.RetentionCleanupResult
	decodeResponse(t, response, &firstCleanup)
	if firstCleanup.Deleted.BatchCount != 1 || firstCleanup.Retained.BatchCount != 2 || firstCleanup.RemainingRemoval.BatchCount != 1 || firstCleanup.Complete {
		t.Errorf("first cleanup = %#v", firstCleanup)
	}
	assertBatchCount(t, db, created.ID, 2)

	response = request(t, app, http.MethodPost, "/api/data-loggers/"+created.ID.String()+"/retention/cleanup", `{"confirm":true,"batch_limit":10}`)
	assertStatus(t, response, fiber.StatusOK)
	var secondCleanup datalogger.RetentionCleanupResult
	decodeResponse(t, response, &secondCleanup)
	if secondCleanup.Deleted.BatchCount != 1 || secondCleanup.Retained.BatchCount != 1 || secondCleanup.RemainingRemoval.BatchCount != 0 || !secondCleanup.Complete {
		t.Errorf("second cleanup = %#v", secondCleanup)
	}
	assertBatchCount(t, db, created.ID, 1)

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+created.ID.String()+"/retention", "")
	assertStatus(t, response, fiber.StatusOK)
	decodeResponse(t, response, &status)
	if status.Plan.Current.BatchCount != 1 || status.Plan.Remove.BatchCount != 0 || status.LastRun == nil || status.LastRun.Result == nil || !status.LastRun.Result.Complete || status.LastRun.Error != nil {
		t.Errorf("retention status after cleanup = %#v", status)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+uuid.NewString()+"/retention", "")
	assertError(t, response, fiber.StatusNotFound, "DLG001")
}

func assertBatchCount(t *testing.T, db *gorm.DB, loggerID uuid.UUID, want int64) {
	t.Helper()
	var count int64
	if err := db.Table("data_logger_batches").Where("logger_id = ?", loggerID).Count(&count).Error; err != nil {
		t.Fatalf("counting Data Logger batches: %v", err)
	}
	if count != want {
		t.Fatalf("batch count = %d, want %d", count, want)
	}
}

func newHTTPIntegrationDatabase(t *testing.T) (*gorm.DB, []uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Data Logger HTTP integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "data_logger_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	for _, migrationPath := range []string{"../../../migrations/000002_create_vgateways.up.sql", "../../../migrations/000003_create_devices_datasources.up.sql", "../../../migrations/000004_create_tags.up.sql", "../../../migrations/000006_create_data_loggers.up.sql", "../../../migrations/000007_create_tag_values_raw.up.sql", "../../../migrations/000008_add_data_logger_storage_limits.up.sql", "../../../migrations/000015_add_data_logger_age_retention.up.sql", "../../../migrations/000016_create_data_logger_retention_status.up.sql"} {
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("reading migration: %v", err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration: %v", err)
		}
	}
	tagIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for index, tagID := range tagIDs {
		config, _ := json.Marshal(map[string]any{"value": index + 1})
		if err := db.Exec(`INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,'constant','float64',true,?)`, tagID, "HTTP Logger Tag "+string(rune('A'+index)), config).Error; err != nil {
			t.Fatalf("inserting Tag: %v", err)
		}
	}
	return db, tagIDs
}
