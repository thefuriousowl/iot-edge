package pluginhttp

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
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestHandlerContracts(t *testing.T) {
	t.Parallel()
	instanceID := uuid.New()
	createdAt := time.Date(2026, time.August, 22, 1, 2, 3, 0, time.UTC)
	startedAt := createdAt.Add(time.Minute)
	instance := &plugin.Instance{
		ID: instanceID, Type: "energy_management", Name: "Plant Energy", Enabled: false,
		Config: plugin.Config(`{"logger_id":"logger-1"}`), ConfigVersion: 2, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	service := &handlerService{
		manifests: []plugin.Manifest{{Type: "energy_management", Name: "Energy Management", Version: "0.1.0", ConfigVersion: 2}},
		instance:  instance,
		listResult: &plugin.ListResult{
			Data: []plugin.Instance{*instance}, Page: 2, PerPage: 5, Total: 6, TotalPages: 2,
		},
	}
	manager := &handlerManager{runtimeStatus: plugin.RuntimeStatus{
		InstanceID: instanceID, Type: instance.Type, State: plugin.RuntimeStateRunning,
		StartedAt: &startedAt, LastTransitionAt: startedAt,
	}}
	app := newHandlerApp(service, manager)

	response := request(t, app, http.MethodGet, "/api/plugin-types", "")
	assertStatus(t, response, fiber.StatusOK)
	var typesBody struct {
		Data []plugin.Manifest `json:"data"`
	}
	decodeResponse(t, response, &typesBody)
	if len(typesBody.Data) != 1 || typesBody.Data[0].Type != "energy_management" {
		t.Errorf("types response = %#v", typesBody)
	}

	response = request(t, app, http.MethodPost, "/api/plugins/", `{"type":"energy_management","name":"Plant Energy","enabled":false,"config":{"logger_id":"logger-1"}}`)
	assertStatus(t, response, fiber.StatusCreated)
	var created map[string]any
	decodeResponse(t, response, &created)
	if service.createInput.Type != "energy_management" || service.createInput.Name != "Plant Energy" || service.createInput.Enabled == nil || *service.createInput.Enabled || string(service.createInput.Config) != `{"logger_id":"logger-1"}` {
		t.Errorf("Create() input = %#v", service.createInput)
	}
	assertDetailResponse(t, created, instanceID, true)

	query := url.Values{"type": {"energy_management"}, "enabled": {"false"}, "search": {"plant"}, "page": {"2"}, "per_page": {"5"}}
	response = request(t, app, http.MethodGet, "/api/plugins/?"+query.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var listed struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			TotalPages int   `json:"total_pages"`
			Total      int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &listed)
	if service.listInput.Type == nil || *service.listInput.Type != "energy_management" || service.listInput.Enabled == nil || *service.listInput.Enabled || service.listInput.Search != "plant" || service.listInput.Page != 2 || service.listInput.PerPage != 5 {
		t.Errorf("List() input = %#v", service.listInput)
	}
	if len(listed.Data) != 1 || listed.Pagination.Page != 2 || listed.Pagination.PerPage != 5 || listed.Pagination.Total != 6 || listed.Pagination.TotalPages != 2 {
		t.Fatalf("list response = %#v", listed)
	}
	if _, exposed := listed.Data[0]["config"]; exposed {
		t.Error("list response exposes Plugin config")
	}

	response = request(t, app, http.MethodGet, "/api/plugins/"+instanceID.String(), "")
	assertStatus(t, response, fiber.StatusOK)
	var detail map[string]any
	decodeResponse(t, response, &detail)
	assertDetailResponse(t, detail, instanceID, true)

	response = request(t, app, http.MethodPut, "/api/plugins/"+instanceID.String(), `{"name":"Updated Energy","enabled":true,"config":{"logger_id":"logger-2"}}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updateID != instanceID || service.updateInput.Name == nil || *service.updateInput.Name != "Updated Energy" || service.updateInput.Enabled == nil || !*service.updateInput.Enabled || string(service.updateInput.Config) != `{"logger_id":"logger-2"}` {
		t.Errorf("Update() input = %#v", service.updateInput)
	}

	response = request(t, app, http.MethodPost, "/api/plugins/"+instanceID.String()+"/enable", `{}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.enabledID != instanceID || service.enabled == nil || !*service.enabled {
		t.Errorf("SetEnabled() = %s, %v", service.enabledID, service.enabled)
	}

	response = request(t, app, http.MethodPost, "/api/plugins/"+instanceID.String()+"/disable", "")
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.enabled == nil || *service.enabled {
		t.Errorf("SetEnabled(false) = %v", service.enabled)
	}

	instance.Enabled = true
	response = request(t, app, http.MethodPost, "/api/plugins/"+instanceID.String()+"/restart", `{}`)
	assertStatus(t, response, fiber.StatusOK)
	var restarted map[string]any
	decodeResponse(t, response, &restarted)
	if manager.restartID != instanceID {
		t.Errorf("Restart() ID = %s", manager.restartID)
	}
	if _, exposed := restarted["config"]; exposed {
		t.Error("restart response exposes Plugin config")
	}

	response = request(t, app, http.MethodGet, "/api/plugins/"+instanceID.String()+"/status", "")
	assertStatus(t, response, fiber.StatusOK)
	var status map[string]any
	decodeResponse(t, response, &status)
	if _, exposed := status["config"]; exposed {
		t.Error("status response exposes Plugin config")
	}
	runtime, ok := status["runtime"].(map[string]any)
	if !ok || runtime["state"] != string(plugin.RuntimeStateRunning) || runtime["started_at"] != startedAt.Format(time.RFC3339) {
		t.Errorf("runtime response = %#v", status["runtime"])
	}

	response = request(t, app, http.MethodDelete, "/api/plugins/"+instanceID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	if service.deletedID != instanceID {
		t.Errorf("Delete() ID = %s", service.deletedID)
	}
	if manager.reconcileCalls != 5 {
		t.Errorf("Reconcile() calls = %d, want 5", manager.reconcileCalls)
	}
}

func TestHandlerRejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	app := newHandlerApp(&handlerService{}, &handlerManager{})
	id := uuid.NewString()
	tests := []struct{ name, method, path, body string }{
		{name: "invalid get id", method: http.MethodGet, path: "/api/plugins/invalid"},
		{name: "nil delete id", method: http.MethodDelete, path: "/api/plugins/00000000-0000-0000-0000-000000000000"},
		{name: "unknown create field", method: http.MethodPost, path: "/api/plugins/", body: `{"unknown":true}`},
		{name: "multiple create documents", method: http.MethodPost, path: "/api/plugins/", body: "{}\n{}"},
		{name: "invalid update type", method: http.MethodPut, path: "/api/plugins/" + id, body: `{"name":1}`},
		{name: "unknown update field", method: http.MethodPut, path: "/api/plugins/" + id, body: `{"type":"energy_management"}`},
		{name: "invalid enabled query", method: http.MethodGet, path: "/api/plugins/?enabled=yes"},
		{name: "zero page", method: http.MethodGet, path: "/api/plugins/?page=0"},
		{name: "negative per page", method: http.MethodGet, path: "/api/plugins/?per_page=-1"},
		{name: "enable payload", method: http.MethodPost, path: "/api/plugins/" + id + "/enable", body: `{"enabled":true}`},
		{name: "disable multiple documents", method: http.MethodPost, path: "/api/plugins/" + id + "/disable", body: "{}\n{}"},
		{name: "restart array", method: http.MethodPost, path: "/api/plugins/" + id + "/restart", body: `[]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, app, test.method, test.path, test.body)
			assertError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestHandlerMapsServiceErrorsWithoutLeakingDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: plugin.ErrInstanceNotFound, status: fiber.StatusNotFound, code: "PLG001"},
		{name: "duplicate", err: plugin.ErrInstanceNameExists, status: fiber.StatusConflict, code: "PLG002"},
		{name: "type unavailable", err: plugin.ErrNotRegistered, status: fiber.StatusBadRequest, code: "PLG003"},
		{name: "single instance", err: plugin.ErrMultipleInstancesNotAllowed, status: fiber.StatusConflict, code: "PLG004"},
		{name: "config version", err: plugin.ErrConfigVersionMismatch, status: fiber.StatusConflict, code: "PLG005"},
		{name: "manager unavailable", err: plugin.ErrManagerNotStarted, status: fiber.StatusServiceUnavailable, code: "PLG006"},
		{name: "validation unavailable", err: plugin.ErrConfigValidationUnavailable, status: fiber.StatusServiceUnavailable, code: "PLG009"},
		{name: "invalid input", err: plugin.ErrInvalidInput, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid config", err: plugin.ErrInvalidConfig, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unexpected", err: errors.New("password=secret"), status: fiber.StatusInternalServerError, code: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newHandlerApp(&handlerService{err: test.err}, &handlerManager{})
			response := request(t, app, http.MethodGet, "/api/plugins/"+uuid.NewString(), "")
			assertStatus(t, response, test.status)
			var body struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			decodeResponse(t, response, &body)
			if body.Error.Code != test.code || strings.Contains(body.Error.Message, "secret") {
				t.Errorf("error response = %#v", body.Error)
			}
		})
	}
}

func TestHandlerReportsPersistedReconciliationFailureSafely(t *testing.T) {
	t.Parallel()
	service := &handlerService{instance: &plugin.Instance{ID: uuid.New(), Type: "energy_management", Config: plugin.Config(`{}`)}}
	manager := &handlerManager{reconcileErr: errors.New("postgres password=secret")}
	response := request(t, newHandlerApp(service, manager), http.MethodPost, "/api/plugins/", `{"type":"energy_management","name":"Energy"}`)
	assertStatus(t, response, fiber.StatusServiceUnavailable)
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != "PLG008" || strings.Contains(body.Error.Message, "secret") {
		t.Errorf("error response = %#v", body.Error)
	}
}

func TestHandlerRejectsRestartForDisabledInstance(t *testing.T) {
	t.Parallel()
	instance := &plugin.Instance{ID: uuid.New(), Type: "energy_management", Enabled: false, Config: plugin.Config(`{}`)}
	manager := &handlerManager{}
	response := request(t, newHandlerApp(&handlerService{instance: instance}, manager), http.MethodPost, "/api/plugins/"+instance.ID.String()+"/restart", "")
	assertError(t, response, fiber.StatusConflict, "PLG007")
	if manager.restartCalls != 0 {
		t.Errorf("Restart() calls = %d, want 0", manager.restartCalls)
	}
}

func TestRuntimeErrorsAreSanitized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "config version", err: plugin.ErrConfigVersionMismatch, code: "PLUGIN_CONFIG_VERSION"},
		{name: "capability", err: plugin.ErrCapabilityUnavailable, code: "PLUGIN_CAPABILITY_UNAVAILABLE"},
		{name: "type", err: plugin.ErrNotRegistered, code: "PLUGIN_TYPE_UNAVAILABLE"},
		{name: "config", err: plugin.ErrInvalidConfig, code: "PLUGIN_CONFIG_INVALID"},
		{name: "validation unavailable", err: plugin.ErrConfigValidationUnavailable, code: "PLUGIN_VALIDATION_UNAVAILABLE"},
		{name: "runtime unavailable", err: plugin.ErrRuntimeUnavailable, code: "PLUGIN_RUNTIME_UNAVAILABLE"},
		{name: "panic", err: plugin.ErrRuntimePanicked, code: "PLUGIN_RUNTIME_STOPPED"},
		{name: "exit", err: plugin.ErrRuntimeExited, code: "PLUGIN_RUNTIME_STOPPED"},
		{name: "unknown", err: errors.New("token=secret"), code: "PLUGIN_RUNTIME_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := &plugin.Instance{ID: uuid.New(), Type: "energy_management", Enabled: true, Config: plugin.Config(`{}`)}
			manager := &handlerManager{runtimeStatus: plugin.RuntimeStatus{State: plugin.RuntimeStateError, LastError: test.err}}
			response := request(t, newHandlerApp(&handlerService{instance: instance}, manager), http.MethodGet, "/api/plugins/"+instance.ID.String()+"/status", "")
			assertStatus(t, response, fiber.StatusOK)
			var body struct {
				Runtime struct {
					Error *runtimeErrorResponse `json:"error"`
				} `json:"runtime"`
			}
			decodeResponse(t, response, &body)
			if body.Runtime.Error == nil || body.Runtime.Error.Code != test.code || strings.Contains(body.Runtime.Error.Message, "secret") {
				t.Errorf("runtime error = %#v", body.Runtime.Error)
			}
		})
	}
}

type handlerService struct {
	manifests   []plugin.Manifest
	instance    *plugin.Instance
	listResult  *plugin.ListResult
	err         error
	createInput plugin.CreateInput
	listInput   plugin.ListInput
	gotID       uuid.UUID
	updateID    uuid.UUID
	updateInput plugin.UpdateInput
	enabledID   uuid.UUID
	enabled     *bool
	deletedID   uuid.UUID
}

func (service *handlerService) Types() []plugin.Manifest { return service.manifests }

func (service *handlerService) Create(_ context.Context, input plugin.CreateInput) (*plugin.Instance, error) {
	service.createInput = input
	return service.result()
}

func (service *handlerService) Get(_ context.Context, id uuid.UUID) (*plugin.Instance, error) {
	service.gotID = id
	return service.result()
}

func (service *handlerService) List(_ context.Context, input plugin.ListInput) (*plugin.ListResult, error) {
	service.listInput = input
	if service.err != nil {
		return nil, service.err
	}
	if service.listResult == nil {
		return &plugin.ListResult{}, nil
	}
	return service.listResult, nil
}

func (service *handlerService) Update(_ context.Context, id uuid.UUID, input plugin.UpdateInput) (*plugin.Instance, error) {
	service.updateID, service.updateInput = id, input
	return service.result()
}

func (service *handlerService) SetEnabled(_ context.Context, id uuid.UUID, enabled bool) (*plugin.Instance, error) {
	service.enabledID, service.enabled = id, &enabled
	if service.instance != nil {
		service.instance.Enabled = enabled
	}
	return service.result()
}

func (service *handlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deletedID = id
	return service.err
}

func (service *handlerService) result() (*plugin.Instance, error) {
	if service.err != nil {
		return nil, service.err
	}
	if service.instance == nil {
		return &plugin.Instance{}, nil
	}
	return service.instance, nil
}

type handlerManager struct {
	runtimeStatus  plugin.RuntimeStatus
	reconcileErr   error
	reconcileCalls int
	restartErr     error
	restartID      uuid.UUID
	restartCalls   int
}

func (manager *handlerManager) Reconcile(context.Context) error {
	manager.reconcileCalls++
	return manager.reconcileErr
}

func (manager *handlerManager) Restart(_ context.Context, id uuid.UUID) error {
	manager.restartID = id
	manager.restartCalls++
	return manager.restartErr
}

func (manager *handlerManager) Status(uuid.UUID) plugin.RuntimeStatus { return manager.runtimeStatus }

func newHandlerApp(service Service, manager RuntimeManager) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service, manager))
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

func assertDetailResponse(t *testing.T, body map[string]any, id uuid.UUID, wantConfig bool) {
	t.Helper()
	if body["id"] != id.String() || body["type"] != "energy_management" || body["name"] != "Plant Energy" {
		t.Errorf("detail response = %#v", body)
	}
	_, hasConfig := body["config"]
	if hasConfig != wantConfig {
		t.Errorf("config presence = %t, want %t", hasConfig, wantConfig)
	}
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
var _ RuntimeManager = (*handlerManager)(nil)
