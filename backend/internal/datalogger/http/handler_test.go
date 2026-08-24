package dataloggerhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

func TestHandlerContracts(t *testing.T) {
	t.Parallel()
	loggerID, tagA, tagB := uuid.New(), uuid.New(), uuid.New()
	startAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	endAt := startAt.Add(24 * time.Hour)
	entity := datalogger.Logger{ID: loggerID, Name: "Plant", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: json.RawMessage(`{"interval_seconds":60}`)}
	lastBatchAt := startAt.Add(time.Hour)
	overview := &datalogger.DataManagementOverview{EvaluatedAt: startAt, LoggerCount: 1, EnabledLoggerCount: 1, PolicyLoggerCount: 1, LogicalHistory: datalogger.RetentionMetrics{RowCount: 3, BatchCount: 2, EstimatedSizeBytes: 2048}, PostgreSQLPhysicalAllocation: datalogger.PostgreSQLPhysicalAllocation{RawHistoryBytes: 8192, BatchAccountingBytes: 4096, TotalBytes: 12288}}
	retentionStatus := &datalogger.RetentionStatus{Plan: datalogger.RetentionPlan{EvaluatedAt: startAt, Current: datalogger.RetentionMetrics{BatchCount: 2}, Remove: datalogger.RetentionMetrics{BatchCount: 1}}}
	cleanupResult := &datalogger.RetentionCleanupResult{EvaluatedAt: startAt, Deleted: datalogger.RetentionMetrics{BatchCount: 1}, Complete: true}
	service := &handlerService{
		entity:             &entity,
		listResult:         &datalogger.ListResult{Data: []datalogger.Logger{entity}, Page: 2, PerPage: 5, Total: 6, TotalPages: 2},
		historyResult:      &datalogger.RawValueListResult{Data: []datalogger.RawValue{{LoggerID: loggerID, TagID: tagA, BatchAt: lastBatchAt, ObservedAt: lastBatchAt, DataType: "float64", Value: 42.5, Quality: datalogger.RawQualityGood, PersistedAt: lastBatchAt}}, Page: 2, PerPage: 25, Total: 26, TotalPages: 2, LastBatchAt: &lastBatchAt},
		queryResult:        &datalogger.QueryResult{Data: []datalogger.QueryRow{{At: lastBatchAt, Values: map[string]datalogger.QueryValue{tagA.String(): {TagID: tagA, DataType: "float64", Value: 42.5, GoodCount: 3, TotalCount: 3}}}}, Mode: datalogger.QueryModeAggregate, Bucket: datalogger.QueryBucket5Minutes, Aggregate: datalogger.AggregateAvg, Page: 2, PerPage: 25, Total: 26, TotalPages: 2},
		managementOverview: overview,
		retentionStatus:    retentionStatus,
		cleanupResult:      cleanupResult,
	}
	app := newHandlerApp(service)

	response := request(t, app, http.MethodGet, "/api/data-management/overview", "")
	assertStatus(t, response, fiber.StatusOK)
	var overviewBody datalogger.DataManagementOverview
	decodeResponse(t, response, &overviewBody)
	if overviewBody.LoggerCount != 1 || overviewBody.LogicalHistory.RowCount != 3 || overviewBody.PostgreSQLPhysicalAllocation.TotalBytes != 12288 {
		t.Errorf("management overview response = %#v", overviewBody)
	}

	query := url.Values{"mode": {"interval"}, "enabled": {"false"}, "search": {"plant"}, "page": {"2"}, "per_page": {"5"}}
	response = request(t, app, http.MethodGet, "/api/data-loggers/?"+query.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var listed struct {
		Data       []datalogger.Logger `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			TotalPages int   `json:"total_pages"`
			Total      int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &listed)
	if service.listInput.Mode == nil || *service.listInput.Mode != datalogger.ModeInterval || service.listInput.Enabled == nil || *service.listInput.Enabled || service.listInput.Search != "plant" || service.listInput.Page != 2 || service.listInput.PerPage != 5 {
		t.Errorf("List() input = %#v", service.listInput)
	}
	if len(listed.Data) != 1 || listed.Pagination.Total != 6 || listed.Pagination.TotalPages != 2 {
		t.Errorf("list response = %#v", listed)
	}

	response = request(t, app, http.MethodPost, "/api/data-loggers/", `{"name":"Plant","description":"Main","enabled":false,"timezone":"Asia/Bangkok","mode":"interval","start_at":"2026-08-23T00:00:00Z","end_at":"2026-08-24T00:00:00Z","max_size_bytes":104857600,"max_age_seconds":86400,"config":{"interval_seconds":15},"tag_ids":["`+tagA.String()+`","`+tagB.String()+`"]}`)
	assertStatus(t, response, fiber.StatusCreated)
	closeBody(t, response)
	if service.createInput.Name != "Plant" || service.createInput.Description == nil || *service.createInput.Description != "Main" || service.createInput.Enabled == nil || *service.createInput.Enabled || service.createInput.Timezone != "Asia/Bangkok" || service.createInput.Mode != datalogger.ModeInterval || !service.createInput.StartAt.Equal(startAt) || service.createInput.EndAt == nil || !service.createInput.EndAt.Equal(endAt) || service.createInput.MaxSizeBytes == nil || *service.createInput.MaxSizeBytes != 104857600 || service.createInput.MaxAgeSeconds == nil || *service.createInput.MaxAgeSeconds != 86400 || string(service.createInput.Config) != `{"interval_seconds":15}` || len(service.createInput.TagIDs) != 2 || service.createInput.TagIDs[1] != tagB {
		t.Errorf("Create() input = %#v", service.createInput)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String(), "")
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.gotID != loggerID {
		t.Errorf("Get() ID = %s", service.gotID)
	}

	historyQuery := url.Values{"tag_id": {tagA.String()}, "from": {startAt.Format(time.RFC3339)}, "to": {endAt.Format(time.RFC3339)}, "page": {"2"}, "per_page": {"25"}}
	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String()+"/history?"+historyQuery.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var historyBody struct {
		Data        []datalogger.RawValue `json:"data"`
		LastBatchAt *time.Time            `json:"last_batch_at"`
		Pagination  struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &historyBody)
	if service.historyID != loggerID || service.historyInput.TagID == nil || *service.historyInput.TagID != tagA || service.historyInput.Page != 2 || service.historyInput.PerPage != 25 || len(historyBody.Data) != 1 || historyBody.LastBatchAt == nil || !historyBody.LastBatchAt.Equal(lastBatchAt) || historyBody.Pagination.Total != 26 {
		t.Errorf("history response/input = %#v / %#v", historyBody, service.historyInput)
	}

	dataQuery := url.Values{"mode": {"aggregate"}, "tag_ids": {tagA.String() + "," + tagB.String()}, "from": {startAt.Format(time.RFC3339)}, "to": {endAt.Format(time.RFC3339)}, "bucket": {"5m"}, "aggregate": {"avg"}, "page": {"2"}, "per_page": {"25"}}
	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String()+"/query?"+dataQuery.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var queryBody struct {
		Data       []datalogger.QueryRow        `json:"data"`
		Mode       datalogger.QueryMode         `json:"mode"`
		Bucket     datalogger.QueryBucket       `json:"bucket"`
		Aggregate  datalogger.AggregateFunction `json:"aggregate"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &queryBody)
	if service.queryID != loggerID || len(service.queryInput.TagIDs) != 2 || service.queryInput.TagIDs[0] != tagA || service.queryInput.TagIDs[1] != tagB || !service.queryInput.From.Equal(startAt) || !service.queryInput.To.Equal(endAt) || service.queryInput.Mode != datalogger.QueryModeAggregate || service.queryInput.Bucket != datalogger.QueryBucket5Minutes || service.queryInput.Aggregate != datalogger.AggregateAvg || service.queryInput.Page != 2 || service.queryInput.PerPage != 25 {
		t.Errorf("Query() input = %#v", service.queryInput)
	}
	if len(queryBody.Data) != 1 || queryBody.Mode != datalogger.QueryModeAggregate || queryBody.Bucket != datalogger.QueryBucket5Minutes || queryBody.Aggregate != datalogger.AggregateAvg || queryBody.Pagination.Total != 26 {
		t.Errorf("query response = %#v", queryBody)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String()+"/retention", "")
	assertStatus(t, response, fiber.StatusOK)
	var statusBody datalogger.RetentionStatus
	decodeResponse(t, response, &statusBody)
	if service.retentionID != loggerID || statusBody.Plan.Current.BatchCount != 2 {
		t.Errorf("retention status response/input = %#v / %s", statusBody, service.retentionID)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String()+"/retention/preview", "")
	assertStatus(t, response, fiber.StatusOK)
	var previewBody datalogger.RetentionPlan
	decodeResponse(t, response, &previewBody)
	if previewBody.Remove.BatchCount != 1 {
		t.Errorf("retention preview response = %#v", previewBody)
	}

	response = request(t, app, http.MethodPost, "/api/data-loggers/"+loggerID.String()+"/retention/cleanup", `{"confirm":true,"batch_limit":25}`)
	assertStatus(t, response, fiber.StatusOK)
	var cleanupBody datalogger.RetentionCleanupResult
	decodeResponse(t, response, &cleanupBody)
	if service.cleanupID != loggerID || service.cleanupInput.BatchLimit != 25 || cleanupBody.Deleted.BatchCount != 1 || !cleanupBody.Complete {
		t.Errorf("retention cleanup response/input = %#v / %#v", cleanupBody, service.cleanupInput)
	}

	response = request(t, app, http.MethodPut, "/api/data-loggers/"+loggerID.String(), `{"description":null,"end_at":null,"max_size_bytes":null,"max_age_seconds":null,"enabled":false,"tag_ids":["`+tagB.String()+`"]}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updatedID != loggerID || !service.updateInput.Description.Set || service.updateInput.Description.Value != nil || !service.updateInput.EndAt.Set || service.updateInput.EndAt.Value != nil || !service.updateInput.MaxSizeBytes.Set || service.updateInput.MaxSizeBytes.Value != nil || !service.updateInput.MaxAgeSeconds.Set || service.updateInput.MaxAgeSeconds.Value != nil || service.updateInput.Enabled == nil || *service.updateInput.Enabled || service.updateInput.TagIDs == nil || len(*service.updateInput.TagIDs) != 1 || (*service.updateInput.TagIDs)[0] != tagB {
		t.Errorf("Update() input = %#v", service.updateInput)
	}

	response = request(t, app, http.MethodPut, "/api/data-loggers/"+loggerID.String(), `{"description":"Updated","end_at":"2026-08-24T00:00:00Z","max_size_bytes":209715200,"max_age_seconds":604800}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updateInput.Description.Value == nil || *service.updateInput.Description.Value != "Updated" || service.updateInput.EndAt.Value == nil || !service.updateInput.EndAt.Value.Equal(endAt) || service.updateInput.MaxSizeBytes.Value == nil || *service.updateInput.MaxSizeBytes.Value != 209715200 || service.updateInput.MaxAgeSeconds.Value == nil || *service.updateInput.MaxAgeSeconds.Value != 604800 {
		t.Errorf("Update(non-null) input = %#v", service.updateInput)
	}

	response = request(t, app, http.MethodDelete, "/api/data-loggers/"+loggerID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	if service.deletedID != loggerID {
		t.Errorf("Delete() ID = %s", service.deletedID)
	}
}

func TestHandlerRejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	app := newHandlerApp(&handlerService{})
	tests := []struct{ name, method, path, body string }{
		{name: "invalid id", method: http.MethodGet, path: "/api/data-loggers/invalid"},
		{name: "nil id", method: http.MethodDelete, path: "/api/data-loggers/00000000-0000-0000-0000-000000000000"},
		{name: "unknown field", method: http.MethodPost, path: "/api/data-loggers/", body: `{"unknown":true}`},
		{name: "multiple documents", method: http.MethodPost, path: "/api/data-loggers/", body: `{}` + "\n" + `{}`},
		{name: "invalid description", method: http.MethodPut, path: "/api/data-loggers/" + uuid.NewString(), body: `{"description":1}`},
		{name: "invalid end", method: http.MethodPut, path: "/api/data-loggers/" + uuid.NewString(), body: `{"end_at":"today"}`},
		{name: "invalid max size", method: http.MethodPut, path: "/api/data-loggers/" + uuid.NewString(), body: `{"max_size_bytes":"large"}`},
		{name: "invalid max age", method: http.MethodPut, path: "/api/data-loggers/" + uuid.NewString(), body: `{"max_age_seconds":"old"}`},
		{name: "invalid enabled", method: http.MethodGet, path: "/api/data-loggers/?enabled=yes"},
		{name: "zero page", method: http.MethodGet, path: "/api/data-loggers/?page=0"},
		{name: "negative per page", method: http.MethodGet, path: "/api/data-loggers/?per_page=-1"},
		{name: "invalid history logger", method: http.MethodGet, path: "/api/data-loggers/invalid/history"},
		{name: "invalid history tag", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/history?tag_id=invalid"},
		{name: "invalid history from", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/history?from=today"},
		{name: "invalid history to", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/history?to=tomorrow"},
		{name: "invalid history page", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/history?page=0"},
		{name: "invalid query logger", method: http.MethodGet, path: "/api/data-loggers/invalid/query?from=2026-08-23T00:00:00Z&to=2026-08-24T00:00:00Z"},
		{name: "missing query range", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/query"},
		{name: "invalid query tag", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/query?tag_ids=invalid&from=2026-08-23T00:00:00Z&to=2026-08-24T00:00:00Z"},
		{name: "invalid query from", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/query?from=today&to=2026-08-24T00:00:00Z"},
		{name: "invalid query to", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/query?from=2026-08-23T00:00:00Z&to=tomorrow"},
		{name: "invalid query page", method: http.MethodGet, path: "/api/data-loggers/" + uuid.NewString() + "/query?from=2026-08-23T00:00:00Z&to=2026-08-24T00:00:00Z&page=0"},
		{name: "invalid retention status logger", method: http.MethodGet, path: "/api/data-loggers/invalid/retention"},
		{name: "invalid retention preview logger", method: http.MethodGet, path: "/api/data-loggers/invalid/retention/preview"},
		{name: "invalid retention cleanup logger", method: http.MethodPost, path: "/api/data-loggers/invalid/retention/cleanup", body: `{"confirm":true}`},
		{name: "missing retention confirmation", method: http.MethodPost, path: "/api/data-loggers/" + uuid.NewString() + "/retention/cleanup", body: `{}`},
		{name: "false retention confirmation", method: http.MethodPost, path: "/api/data-loggers/" + uuid.NewString() + "/retention/cleanup", body: `{"confirm":false}`},
		{name: "unknown retention cleanup field", method: http.MethodPost, path: "/api/data-loggers/" + uuid.NewString() + "/retention/cleanup", body: `{"confirm":true,"force":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, app, test.method, test.path, test.body)
			assertError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestHandlerMapsServiceErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: datalogger.ErrLoggerNotFound, status: fiber.StatusNotFound, code: "DLG001"},
		{name: "duplicate", err: datalogger.ErrLoggerNameExists, status: fiber.StatusConflict, code: "DLG002"},
		{name: "tag missing", err: datalogger.ErrLoggerTagNotFound, status: fiber.StatusBadRequest, code: "DLG003"},
		{name: "storage limit too small", err: datalogger.ErrStorageLimitTooSmall, status: fiber.StatusBadRequest, code: "DLG004"},
		{name: "retention cleanup", err: datalogger.ErrRetentionCleanup, status: fiber.StatusInternalServerError, code: "DLG005"},
		{name: "invalid input", err: datalogger.ErrInvalidInput, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid logger", err: datalogger.ErrInvalidLogger, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid tag", err: datalogger.ErrInvalidLoggerTag, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid history", err: datalogger.ErrInvalidRawBatch, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid query", err: datalogger.ErrInvalidQuery, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unexpected", err: errors.New("password=secret"), status: fiber.StatusInternalServerError, code: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newHandlerApp(&handlerService{err: test.err})
			response := request(t, app, http.MethodGet, "/api/data-loggers/"+uuid.NewString(), "")
			var body struct {
				Error struct {
					Code, Message string
				} `json:"error"`
			}
			assertStatus(t, response, test.status)
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code || strings.Contains(body.Error.Message, "secret") {
				t.Errorf("error response = %#v", body.Error)
			}
		})
	}
}

type handlerService struct {
	entity             *datalogger.Logger
	listResult         *datalogger.ListResult
	err                error
	createInput        datalogger.CreateInput
	listInput          datalogger.ListInput
	gotID              uuid.UUID
	updatedID          uuid.UUID
	updateInput        datalogger.UpdateInput
	deletedID          uuid.UUID
	historyID          uuid.UUID
	historyInput       datalogger.RawValueListInput
	historyResult      *datalogger.RawValueListResult
	queryID            uuid.UUID
	queryInput         datalogger.QueryInput
	queryResult        *datalogger.QueryResult
	managementOverview *datalogger.DataManagementOverview
	retentionID        uuid.UUID
	retentionStatus    *datalogger.RetentionStatus
	cleanupID          uuid.UUID
	cleanupInput       datalogger.RetentionCleanupInput
	cleanupResult      *datalogger.RetentionCleanupResult
}

func (service *handlerService) Create(_ context.Context, input datalogger.CreateInput) (*datalogger.Logger, error) {
	service.createInput = input
	return service.result()
}

func (service *handlerService) Get(_ context.Context, id uuid.UUID) (*datalogger.Logger, error) {
	service.gotID = id
	return service.result()
}

func (service *handlerService) List(_ context.Context, input datalogger.ListInput) (*datalogger.ListResult, error) {
	service.listInput = input
	if service.err != nil {
		return nil, service.err
	}
	if service.listResult == nil {
		return &datalogger.ListResult{}, nil
	}
	return service.listResult, nil
}

func (service *handlerService) ListHistory(_ context.Context, id uuid.UUID, input datalogger.RawValueListInput) (*datalogger.RawValueListResult, error) {
	service.historyID, service.historyInput = id, input
	if service.err != nil {
		return nil, service.err
	}
	if service.historyResult == nil {
		return &datalogger.RawValueListResult{}, nil
	}
	return service.historyResult, nil
}

func (service *handlerService) QueryHistory(_ context.Context, id uuid.UUID, input datalogger.QueryInput) (*datalogger.QueryResult, error) {
	service.queryID, service.queryInput = id, input
	if service.err != nil {
		return nil, service.err
	}
	if service.queryResult == nil {
		return &datalogger.QueryResult{}, nil
	}
	return service.queryResult, nil
}

func (service *handlerService) ManagementOverview(_ context.Context, _ time.Time) (*datalogger.DataManagementOverview, error) {
	if service.err != nil {
		return nil, service.err
	}
	if service.managementOverview == nil {
		return &datalogger.DataManagementOverview{}, nil
	}
	return service.managementOverview, nil
}

func (service *handlerService) Retention(_ context.Context, id uuid.UUID, _ time.Time) (*datalogger.RetentionStatus, error) {
	service.retentionID = id
	if service.err != nil {
		return nil, service.err
	}
	if service.retentionStatus == nil {
		return &datalogger.RetentionStatus{}, nil
	}
	return service.retentionStatus, nil
}

func (service *handlerService) CleanupRetention(_ context.Context, id uuid.UUID, input datalogger.RetentionCleanupInput) (*datalogger.RetentionCleanupResult, error) {
	service.cleanupID, service.cleanupInput = id, input
	if service.err != nil {
		return nil, service.err
	}
	if service.cleanupResult == nil {
		return &datalogger.RetentionCleanupResult{}, nil
	}
	return service.cleanupResult, nil
}

func (service *handlerService) Update(_ context.Context, id uuid.UUID, input datalogger.UpdateInput) (*datalogger.Logger, error) {
	service.updatedID, service.updateInput = id, input
	return service.result()
}

func (service *handlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deletedID = id
	return service.err
}

func (service *handlerService) result() (*datalogger.Logger, error) {
	if service.err != nil {
		return nil, service.err
	}
	if service.entity == nil {
		return &datalogger.Logger{}, nil
	}
	return service.entity, nil
}

func newHandlerApp(service Service) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	return app
}

func request(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("requesting %s %s: %v", method, path, err)
	}
	return response
}

func assertStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, want, body)
	}
}

func assertError(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	assertStatus(t, response, status)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code {
		t.Errorf("error code = %q, want %q", body.Error.Code, code)
	}
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func closeBody(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("closing response: %v", err)
	}
}

var _ Service = (*handlerService)(nil)
