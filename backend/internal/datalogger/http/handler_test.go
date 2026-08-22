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
	service := &handlerService{entity: &entity, listResult: &datalogger.ListResult{Data: []datalogger.Logger{entity}, Page: 2, PerPage: 5, Total: 6, TotalPages: 2}}
	app := newHandlerApp(service)

	query := url.Values{"mode": {"interval"}, "enabled": {"false"}, "search": {"plant"}, "page": {"2"}, "per_page": {"5"}}
	response := request(t, app, http.MethodGet, "/api/data-loggers/?"+query.Encode(), "")
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

	response = request(t, app, http.MethodPost, "/api/data-loggers/", `{"name":"Plant","description":"Main","enabled":false,"timezone":"Asia/Bangkok","mode":"interval","start_at":"2026-08-23T00:00:00Z","end_at":"2026-08-24T00:00:00Z","config":{"interval_seconds":15},"tag_ids":["`+tagA.String()+`","`+tagB.String()+`"]}`)
	assertStatus(t, response, fiber.StatusCreated)
	closeBody(t, response)
	if service.createInput.Name != "Plant" || service.createInput.Description == nil || *service.createInput.Description != "Main" || service.createInput.Enabled == nil || *service.createInput.Enabled || service.createInput.Timezone != "Asia/Bangkok" || service.createInput.Mode != datalogger.ModeInterval || !service.createInput.StartAt.Equal(startAt) || service.createInput.EndAt == nil || !service.createInput.EndAt.Equal(endAt) || string(service.createInput.Config) != `{"interval_seconds":15}` || len(service.createInput.TagIDs) != 2 || service.createInput.TagIDs[1] != tagB {
		t.Errorf("Create() input = %#v", service.createInput)
	}

	response = request(t, app, http.MethodGet, "/api/data-loggers/"+loggerID.String(), "")
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.gotID != loggerID {
		t.Errorf("Get() ID = %s", service.gotID)
	}

	response = request(t, app, http.MethodPut, "/api/data-loggers/"+loggerID.String(), `{"description":null,"end_at":null,"enabled":false,"tag_ids":["`+tagB.String()+`"]}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updatedID != loggerID || !service.updateInput.Description.Set || service.updateInput.Description.Value != nil || !service.updateInput.EndAt.Set || service.updateInput.EndAt.Value != nil || service.updateInput.Enabled == nil || *service.updateInput.Enabled || service.updateInput.TagIDs == nil || len(*service.updateInput.TagIDs) != 1 || (*service.updateInput.TagIDs)[0] != tagB {
		t.Errorf("Update() input = %#v", service.updateInput)
	}

	response = request(t, app, http.MethodPut, "/api/data-loggers/"+loggerID.String(), `{"description":"Updated","end_at":"2026-08-24T00:00:00Z"}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updateInput.Description.Value == nil || *service.updateInput.Description.Value != "Updated" || service.updateInput.EndAt.Value == nil || !service.updateInput.EndAt.Value.Equal(endAt) {
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
		{name: "invalid enabled", method: http.MethodGet, path: "/api/data-loggers/?enabled=yes"},
		{name: "zero page", method: http.MethodGet, path: "/api/data-loggers/?page=0"},
		{name: "negative per page", method: http.MethodGet, path: "/api/data-loggers/?per_page=-1"},
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
		{name: "invalid input", err: datalogger.ErrInvalidInput, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid logger", err: datalogger.ErrInvalidLogger, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid tag", err: datalogger.ErrInvalidLoggerTag, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
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
	entity      *datalogger.Logger
	listResult  *datalogger.ListResult
	err         error
	createInput datalogger.CreateInput
	listInput   datalogger.ListInput
	gotID       uuid.UUID
	updatedID   uuid.UUID
	updateInput datalogger.UpdateInput
	deletedID   uuid.UUID
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
