package vgatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
)

type serviceStub struct {
	create         func(context.Context, vgateway.CreateVGatewayInput) (*vgateway.VGateway, error)
	get            func(context.Context, uuid.UUID) (*vgateway.VGatewayView, error)
	list           func(context.Context, vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error)
	update         func(context.Context, uuid.UUID, vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error)
	delete         func(context.Context, uuid.UUID) error
	connect        func(context.Context, uuid.UUID) error
	disconnect     func(context.Context, uuid.UUID) error
	testConnection func(context.Context, uuid.UUID, json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error)
	status         func(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error)
}

func (stub *serviceStub) Create(ctx context.Context, input vgateway.CreateVGatewayInput) (*vgateway.VGateway, error) {
	return stub.create(ctx, input)
}

func (stub *serviceStub) Get(ctx context.Context, id uuid.UUID) (*vgateway.VGatewayView, error) {
	return stub.get(ctx, id)
}

func (stub *serviceStub) List(ctx context.Context, input vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error) {
	return stub.list(ctx, input)
}

func (stub *serviceStub) Update(ctx context.Context, id uuid.UUID, input vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error) {
	return stub.update(ctx, id, input)
}

func (stub *serviceStub) Delete(ctx context.Context, id uuid.UUID) error {
	return stub.delete(ctx, id)
}

func (stub *serviceStub) Connect(ctx context.Context, id uuid.UUID) error {
	return stub.connect(ctx, id)
}

func (stub *serviceStub) Disconnect(ctx context.Context, id uuid.UUID) error {
	return stub.disconnect(ctx, id)
}

func (stub *serviceStub) TestConnection(ctx context.Context, id uuid.UUID, options json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error) {
	return stub.testConnection(ctx, id, options)
}

func (stub *serviceStub) Status(ctx context.Context, id uuid.UUID) (*vgateway.VGatewayStatusResult, error) {
	return stub.status(ctx, id)
}

func TestList_ReturnsFilteredPaginatedItemsWithoutConfig(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, time.August, 21, 10, 30, 0, 0, time.UTC)
	stub := &serviceStub{}
	stub.list = func(_ context.Context, input vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error) {
		if input.Type == nil || *input.Type != vgateway.VGatewayTypeModbusTCP {
			t.Errorf("type = %v, want modbus_tcp", input.Type)
		}
		if input.Enabled == nil || !*input.Enabled {
			t.Errorf("enabled = %v, want true", input.Enabled)
		}
		if input.Page != 2 || input.PerPage != 10 {
			t.Errorf("pagination input = (%d, %d), want (2, 10)", input.Page, input.PerPage)
		}
		return &vgateway.VGatewayListResult{
			Data: []vgateway.VGatewayView{{
				VGateway: vgateway.VGateway{
					ID:        id,
					Name:      "Main PLC Gateway",
					Type:      vgateway.VGatewayTypeModbusTCP,
					Enabled:   true,
					Config:    json.RawMessage(`{"host":"192.0.2.10"}`),
					CreatedAt: now,
					UpdatedAt: now,
				},
				Status: vgateway.VGatewayStatusConnected,
			}},
			Page:       2,
			PerPage:    10,
			Total:      11,
			TotalPages: 2,
		}, nil
	}

	response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways?type=modbus_tcp&enabled=true&page=2&per_page=10", "")
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &body)
	if len(body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(body.Data))
	}
	item := body.Data[0]
	if item["id"] != id.String() || item["name"] != "Main PLC Gateway" || item["status"] != "connected" {
		t.Errorf("item = %#v, want expected gateway identity and status", item)
	}
	if item["device_count"] != float64(0) || item["last_activity"] != nil {
		t.Errorf("future device fields = %#v, want zero count and null activity", item)
	}
	if _, exists := item["config"]; exists {
		t.Error("list item exposes config")
	}
	if body.Pagination.Page != 2 || body.Pagination.PerPage != 10 || body.Pagination.Total != 11 || body.Pagination.TotalPages != 2 {
		t.Errorf("pagination = %#v, want page 2 of 2 with total 11", body.Pagination)
	}
}

func TestList_RejectsInvalidQueryParameters(t *testing.T) {
	tests := []string{
		"enabled=maybe",
		"page=zero",
		"page=0",
		"page=-1",
		"per_page=0",
	}

	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			stub := &serviceStub{list: func(context.Context, vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error) {
				t.Fatal("service List called for invalid query")
				return nil, nil
			}}
			response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways?"+query, "")
			defer response.Body.Close()
			assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestCreate_ForwardsInputAndReturnsDerivedStatus(t *testing.T) {
	id := uuid.New()
	description := "Factory floor"
	stub := &serviceStub{}
	stub.create = func(_ context.Context, input vgateway.CreateVGatewayInput) (*vgateway.VGateway, error) {
		if input.Name != "Main PLC" || input.Type != vgateway.VGatewayTypeModbusTCP {
			t.Errorf("identity input = %#v, want Main PLC/modbus_tcp", input)
		}
		if input.Description == nil || *input.Description != description {
			t.Errorf("description = %v, want %q", input.Description, description)
		}
		if input.Enabled == nil || *input.Enabled {
			t.Errorf("enabled = %v, want false", input.Enabled)
		}
		if string(input.Config) != `{"host":"192.0.2.10","port":502}` {
			t.Errorf("config = %s, want original JSON", input.Config)
		}
		return &vgateway.VGateway{
			ID:          id,
			Name:        input.Name,
			Type:        input.Type,
			Description: input.Description,
			Enabled:     false,
			Config:      input.Config,
		}, nil
	}
	body := `{"name":"Main PLC","type":"modbus_tcp","description":"Factory floor","enabled":false,"config":{"host":"192.0.2.10","port":502}}`

	response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways", body)
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusCreated)
	}
	var result map[string]any
	decodeResponse(t, response, &result)
	if result["id"] != id.String() || result["status"] != "stopped" {
		t.Errorf("response = %#v, want created ID with stopped status", result)
	}
	if _, exists := result["config"]; !exists {
		t.Error("create response omits full config")
	}
}

func TestCreate_RejectsMalformedAndUnknownFields(t *testing.T) {
	tests := []string{
		`{"name":`,
		`{"name":"PLC","type":"modbus_tcp","config":{},"unexpected":true}`,
		`{"name":"PLC","type":"modbus_tcp","config":{}} {}`,
	}

	for _, body := range tests {
		stub := &serviceStub{create: func(context.Context, vgateway.CreateVGatewayInput) (*vgateway.VGateway, error) {
			t.Fatal("service Create called for invalid body")
			return nil, nil
		}}
		response := performRequest(t, testApp(stub), http.MethodPost, "/api/vgateways", body)
		defer response.Body.Close()
		assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
	}
}

func TestGet_ValidatesIDAndMapsNotFound(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid ID", path: "/api/vgateways/not-a-uuid", wantStatus: fiber.StatusBadRequest, wantCode: "VALIDATION_ERROR"},
		{name: "not found", path: "/api/vgateways/" + id.String(), serviceErr: vgateway.ErrVGatewayNotFound, wantStatus: fiber.StatusNotFound, wantCode: "NOT_FOUND"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{get: func(_ context.Context, gotID uuid.UUID) (*vgateway.VGatewayView, error) {
				if gotID != id {
					t.Errorf("id = %s, want %s", gotID, id)
				}
				return nil, test.serviceErr
			}}
			response := performRequest(t, testApp(stub), http.MethodGet, test.path, "")
			defer response.Body.Close()
			assertAPIError(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func TestGet_ReturnsFullGateway(t *testing.T) {
	id := uuid.New()
	stub := &serviceStub{get: func(_ context.Context, gotID uuid.UUID) (*vgateway.VGatewayView, error) {
		if gotID != id {
			t.Errorf("id = %s, want %s", gotID, id)
		}
		return &vgateway.VGatewayView{
			VGateway: vgateway.VGateway{ID: id, Name: "Main PLC", Type: vgateway.VGatewayTypeModbusTCP, Config: json.RawMessage(`{"host":"192.0.2.10"}`)},
			Status:   vgateway.VGatewayStatusDisconnected,
		}, nil
	}}

	response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways/"+id.String(), "")
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var result map[string]any
	decodeResponse(t, response, &result)
	if result["id"] != id.String() || result["status"] != "disconnected" {
		t.Errorf("response = %#v, want full gateway", result)
	}
	if _, exists := result["config"]; !exists {
		t.Error("detail response omits config")
	}
}

func TestUpdate_PreservesDescriptionPresenceSemantics(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name      string
		body      string
		wantSet   bool
		wantValue *string
	}{
		{name: "omitted", body: `{"enabled":true}`},
		{name: "explicit null", body: `{"description":null}`, wantSet: true},
		{name: "string", body: `{"description":"Updated"}`, wantSet: true, wantValue: stringPointer("Updated")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{update: func(_ context.Context, gotID uuid.UUID, input vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error) {
				if gotID != id {
					t.Errorf("id = %s, want %s", gotID, id)
				}
				if input.Description.Set != test.wantSet {
					t.Errorf("description Set = %t, want %t", input.Description.Set, test.wantSet)
				}
				if !equalOptionalString(input.Description.Value, test.wantValue) {
					t.Errorf("description Value = %v, want %v", input.Description.Value, test.wantValue)
				}
				return &vgateway.VGatewayView{VGateway: vgateway.VGateway{ID: id}, Status: vgateway.VGatewayStatusDisconnected}, nil
			}}
			response := performRequest(t, testApp(stub), http.MethodPut, "/api/vgateways/"+id.String(), test.body)
			defer response.Body.Close()
			if response.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
			}
		})
	}
}

func TestUpdate_ForwardsConfigAndMapsDuplicateName(t *testing.T) {
	id := uuid.New()
	stub := &serviceStub{update: func(_ context.Context, _ uuid.UUID, input vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error) {
		if input.Config == nil || string(*input.Config) != `{"host":"192.0.2.20"}` {
			t.Errorf("config = %v, want forwarded JSON", input.Config)
		}
		return nil, errors.Join(errors.New("update failed"), vgateway.ErrVGatewayNameExists)
	}}
	response := performRequest(t, testApp(stub), http.MethodPut, "/api/vgateways/"+id.String(), `{"name":"Duplicate","config":{"host":"192.0.2.20"}}`)
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusConflict, "VGW011")
}

func TestUpdate_RejectsInvalidDescriptionType(t *testing.T) {
	id := uuid.New()
	stub := &serviceStub{update: func(context.Context, uuid.UUID, vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error) {
		t.Fatal("service Update called for invalid description")
		return nil, nil
	}}
	response := performRequest(t, testApp(stub), http.MethodPut, "/api/vgateways/"+id.String(), `{"description":42}`)
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
}

func TestDelete_ReturnsNoContentAndMapsErrors(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "deleted", wantStatus: fiber.StatusNoContent},
		{name: "not found", serviceErr: vgateway.ErrVGatewayNotFound, wantStatus: fiber.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "internal", serviceErr: errors.New("database unavailable"), wantStatus: fiber.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{delete: func(_ context.Context, gotID uuid.UUID) error {
				if gotID != id {
					t.Errorf("id = %s, want %s", gotID, id)
				}
				return test.serviceErr
			}}
			response := performRequest(t, testApp(stub), http.MethodDelete, "/api/vgateways/"+id.String(), "")
			defer response.Body.Close()
			if test.wantCode == "" {
				if response.StatusCode != test.wantStatus {
					t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
				}
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatalf("reading response body: %v", err)
				}
				if len(body) != 0 {
					t.Errorf("body = %q, want empty", body)
				}
				return
			}
			assertAPIError(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func TestServiceErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid name", err: vgateway.ErrInvalidVGatewayName, wantStatus: fiber.StatusBadRequest, wantCode: "VGW010"},
		{name: "invalid config", err: vgateway.ErrInvalidVGatewayConfig, wantStatus: fiber.StatusBadRequest, wantCode: "VGW010"},
		{name: "unsupported type", err: vgateway.ErrUnsupportedVGatewayType, wantStatus: fiber.StatusBadRequest, wantCode: "VGW010"},
		{name: "duplicate name", err: vgateway.ErrVGatewayNameExists, wantStatus: fiber.StatusConflict, wantCode: "VGW011"},
		{name: "internal", err: errors.New("unexpected"), wantStatus: fiber.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{list: func(context.Context, vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error) {
				return nil, test.err
			}}
			response := performRequest(t, testApp(stub), http.MethodGet, "/api/vgateways", "")
			defer response.Body.Close()
			assertAPIError(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func testApp(service Service) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	return app
}

func performRequest(t *testing.T, app *fiber.App, method string, path string, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func assertAPIError(t *testing.T, response *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", response.StatusCode, wantStatus)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty")
	}
}

func stringPointer(value string) *string {
	return &value
}

func equalOptionalString(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
