package reporthttp

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/thefuriousowl/iot-edge/internal/report"
	reportpostgres "github.com/thefuriousowl/iot-edge/internal/report/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestReportCRUDQueryAndFullCSVExport_EndToEnd(t *testing.T) {
	db, tagIDs := newReportDatabase(t)
	loggerRepository := dataloggerpostgres.NewRepository(db)
	history := dataloggerpostgres.NewHistoryRepository(db)
	start := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Report source", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: start, Config: json.RawMessage(`{"interval_seconds":60}`)}
	if err := loggerRepository.Create(t.Context(), &logger, tagIDs); err != nil {
		t.Fatalf("creating Data Logger: %v", err)
	}
	for minute, numeric := range []float64{10, 20, 30} {
		batchAt := start.Add(time.Duration(minute) * time.Minute)
		if err := history.WriteBatch(t.Context(), datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: numeric, Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "bool", Value: minute%2 == 0, Quality: datalogger.RawQualityGood},
		}}); err != nil {
			t.Fatalf("writing batch %d: %v", minute, err)
		}
	}
	service, err := report.NewService(reportpostgres.NewRepository(db), loggerRepository, history)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	t.Cleanup(func() { _ = app.Shutdown() })

	body := `{"name":"Shift summary","description":"Saved report","logger_id":"` + logger.ID.String() + `","timezone":"Asia/Bangkok","mode":"aggregate","bucket":"5m","columns":[{"tag_id":"` + tagIDs[0].String() + `","name":"Average kW","aggregate":"avg"},{"tag_id":"` + tagIDs[1].String() + `","name":"Running samples","aggregate":"count"}]}`
	response := reportRequest(t, app, http.MethodPost, "/api/reports/", body)
	assertReportStatus(t, response, fiber.StatusCreated)
	var created report.Report
	decodeReportResponse(t, response, &created)
	if created.ID == uuid.Nil || created.LoggerName != "Report source" || created.ColumnCount != 2 || created.Columns[0].Name != "Average kW" {
		t.Fatalf("created = %#v", created)
	}

	response = reportRequest(t, app, http.MethodGet, "/api/reports/?search=source&mode=aggregate", "")
	assertReportStatus(t, response, fiber.StatusOK)
	var list struct {
		Data       []report.Report `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeReportResponse(t, response, &list)
	if len(list.Data) != 1 || list.Data[0].ColumnCount != 2 || list.Pagination.Total != 1 {
		t.Fatalf("list = %#v", list)
	}

	rangeQuery := url.Values{"from": {start.Format(time.RFC3339)}, "to": {start.Add(10 * time.Minute).Format(time.RFC3339)}, "page": {"1"}, "per_page": {"10"}}
	response = reportRequest(t, app, http.MethodGet, "/api/reports/"+created.ID.String()+"/query?"+rangeQuery.Encode(), "")
	assertReportStatus(t, response, fiber.StatusOK)
	var query struct {
		Data       []datalogger.QueryRow `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeReportResponse(t, response, &query)
	if len(query.Data) != 1 || query.Pagination.Total != 1 || query.Data[0].Values[tagIDs[0].String()].Value != float64(20) || query.Data[0].Values[tagIDs[1].String()].Value != float64(3) {
		t.Fatalf("query = %#v", query)
	}

	response = reportRequest(t, app, http.MethodGet, "/api/reports/"+created.ID.String()+"/export.csv?"+rangeQuery.Encode(), "")
	assertReportStatus(t, response, fiber.StatusOK)
	exported, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading CSV: %v", err)
	}
	_ = response.Body.Close()
	if response.Header.Get("Content-Type") != "text/csv; charset=utf-8" || string(exported) != "timestamp,Average kW,Running samples\n2026-08-23T07:00:00+07:00,20,3\n" {
		t.Fatalf("CSV headers=%q body=%q", response.Header, exported)
	}

	duplicateColumns := `{"name":"Invalid aliases","logger_id":"` + logger.ID.String() + `","timezone":"UTC","mode":"raw","columns":[{"tag_id":"` + tagIDs[0].String() + `","name":"Value"},{"tag_id":"` + tagIDs[1].String() + `","name":" value "}]}`
	response = reportRequest(t, app, http.MethodPost, "/api/reports/", duplicateColumns)
	assertReportError(t, response, fiber.StatusBadRequest, "RPT004")

	secondLogger := datalogger.Logger{Name: "Second report source", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: start, Config: json.RawMessage(`{"interval_seconds":60}`)}
	if err := loggerRepository.Create(t.Context(), &secondLogger, []uuid.UUID{tagIDs[0]}); err != nil {
		t.Fatalf("creating second Data Logger: %v", err)
	}
	updatedBody := `{"name":"Raw shift","logger_id":"` + secondLogger.ID.String() + `","timezone":"UTC","mode":"raw","columns":[{"tag_id":"` + tagIDs[0].String() + `","name":"Demand"}]}`
	response = reportRequest(t, app, http.MethodPut, "/api/reports/"+created.ID.String(), updatedBody)
	assertReportStatus(t, response, fiber.StatusOK)
	var updated report.Report
	decodeReportResponse(t, response, &updated)
	if updated.Name != "Raw shift" || updated.LoggerID != secondLogger.ID || updated.Mode != datalogger.QueryModeRaw || updated.Bucket != "" || len(updated.Columns) != 1 {
		t.Fatalf("updated = %#v", updated)
	}

	response = reportRequest(t, app, http.MethodDelete, "/api/reports/"+created.ID.String(), "")
	assertReportStatus(t, response, fiber.StatusNoContent)
	_ = response.Body.Close()
	response = reportRequest(t, app, http.MethodGet, "/api/reports/"+created.ID.String(), "")
	assertReportError(t, response, fiber.StatusNotFound, "RPT001")
}

func newReportDatabase(t *testing.T) (*gorm.DB, []uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Report HTTP integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "report_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
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
	for _, path := range []string{"../../../migrations/000002_create_vgateways.up.sql", "../../../migrations/000003_create_devices_datasources.up.sql", "../../../migrations/000004_create_tags.up.sql", "../../../migrations/000006_create_data_loggers.up.sql", "../../../migrations/000007_create_tag_values_raw.up.sql", "../../../migrations/000008_add_data_logger_storage_limits.up.sql", "../../../migrations/000009_create_reports.up.sql", "../../../migrations/000015_add_data_logger_age_retention.up.sql"} {
		migration, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("reading migration %s: %v", path, readErr)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", path, err)
		}
	}
	tagIDs := []uuid.UUID{uuid.New(), uuid.New()}
	if err := db.Exec(`INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,'constant','float64',true,'{"value":0}'),(?,?,'constant','bool',true,'{"value":true}')`, tagIDs[0], "Power", tagIDs[1], "Running").Error; err != nil {
		t.Fatalf("inserting Tags: %v", err)
	}
	return db, tagIDs
}

func reportRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return response
}

func assertReportStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, want, body)
	}
}

func decodeReportResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func assertReportError(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	assertReportStatus(t, response, status)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeReportResponse(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q", body.Error.Code, code)
	}
}
