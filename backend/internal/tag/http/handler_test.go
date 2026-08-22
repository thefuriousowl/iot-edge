package taghttp

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

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

func TestTagHandlerContracts(t *testing.T) {
	t.Parallel()
	datasourceID := uuid.New()
	tagID := uuid.New()
	entity := tag.Tag{ID: tagID, Name: "Pressure", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)}
	service := &handlerService{entity: &entity, listResult: &tag.ListResult{Data: []tag.Tag{entity}, Page: 2, PerPage: 5, Total: 6, TotalPages: 2}}
	app := newHandlerTestApp(service)

	query := url.Values{"type": {"reading"}, "data_type": {"uint16"}, "enabled": {"true"}, "datasource_id": {datasourceID.String()}, "search": {"main pump"}, "page": {"2"}, "per_page": {"5"}}
	response := performRequest(t, app, http.MethodGet, "/api/tags/?"+query.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var listed struct {
		Data       []tag.Tag `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &listed)
	if service.listInput.Type == nil || *service.listInput.Type != tag.TypeReading || service.listInput.DataType == nil || *service.listInput.DataType != tag.DataTypeUInt16 || service.listInput.Enabled == nil || !*service.listInput.Enabled || service.listInput.DatasourceID == nil || *service.listInput.DatasourceID != datasourceID || service.listInput.Search != "main pump" || service.listInput.Page != 2 || service.listInput.PerPage != 5 {
		t.Errorf("List() input = %#v", service.listInput)
	}
	if len(listed.Data) != 1 || listed.Pagination.Total != 6 || listed.Pagination.TotalPages != 2 {
		t.Errorf("list response = %#v", listed)
	}

	description := "Line pressure"
	response = performRequest(t, app, http.MethodPost, "/api/tags/", `{"datasource_id":"`+datasourceID.String()+`","name":"Pressure","type":"reading","data_type":"uint16","description":"Line pressure","enabled":true,"config":{"decoder":{"type":"binary_numeric"}}}`)
	assertStatus(t, response, fiber.StatusCreated)
	closeResponse(t, response)
	if service.createInput.DatasourceID == nil || *service.createInput.DatasourceID != datasourceID || service.createInput.Name != "Pressure" || service.createInput.Type != tag.TypeReading || service.createInput.DataType != tag.DataTypeUInt16 || service.createInput.Description == nil || *service.createInput.Description != description || service.createInput.Enabled == nil || !*service.createInput.Enabled {
		t.Errorf("Create() input = %#v", service.createInput)
	}

	response = performRequest(t, app, http.MethodPut, "/api/tags/"+tagID.String(), `{"datasource_id":null,"name":"Static pressure","data_type":"float64","description":null,"enabled":false,"config":{"value":4.5}}`)
	assertStatus(t, response, fiber.StatusOK)
	closeResponse(t, response)
	if service.updatedID != tagID || !service.updateInput.DatasourceID.Set || service.updateInput.DatasourceID.Value != nil || service.updateInput.Name == nil || *service.updateInput.Name != "Static pressure" || service.updateInput.DataType == nil || *service.updateInput.DataType != tag.DataTypeFloat64 || !service.updateInput.Description.Set || service.updateInput.Description.Value != nil || service.updateInput.Enabled == nil || *service.updateInput.Enabled || service.updateInput.Config == nil || string(*service.updateInput.Config) != `{"value":4.5}` {
		t.Errorf("Update() input = %#v", service.updateInput)
	}

	response = performRequest(t, app, http.MethodPost, "/api/tags/preview", `{"type":"constant","data_type":"bool","config":{"value":true}}`)
	assertStatus(t, response, fiber.StatusOK)
	closeResponse(t, response)
	if service.previewInput.Type != tag.TypeConstant || service.previewInput.DataType != tag.DataTypeBool || string(service.previewInput.Config) != `{"value":true}` {
		t.Errorf("Preview() input = %#v", service.previewInput)
	}

	response = performRequest(t, app, http.MethodPost, "/api/tags/"+tagID.String()+"/preview", "")
	assertStatus(t, response, fiber.StatusOK)
	closeResponse(t, response)
	if service.previewSavedID != tagID {
		t.Errorf("PreviewSaved() ID = %s", service.previewSavedID)
	}

	expression := "${" + tagID.String() + "} + 1"
	response = performRequest(t, app, http.MethodPost, "/api/tags/validate-expression", `{"tag_id":"`+tagID.String()+`","expression":"`+expression+`"}`)
	assertStatus(t, response, fiber.StatusOK)
	var validation struct {
		Valid        bool        `json:"valid"`
		Dependencies []uuid.UUID `json:"dependencies"`
	}
	decodeResponse(t, response, &validation)
	if service.validateID != tagID || service.expression != expression || !validation.Valid || len(validation.Dependencies) != 1 || validation.Dependencies[0] != tagID {
		t.Errorf("ValidateExpression() = %s, %q, %#v", service.validateID, service.expression, validation)
	}

	response = performRequest(t, app, http.MethodDelete, "/api/tags/"+tagID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeResponse(t, response)
	if service.deletedID != tagID {
		t.Errorf("Delete() ID = %s", service.deletedID)
	}
}

func TestTagHandlerRejectsMalformedRequests(t *testing.T) {
	t.Parallel()
	app := newHandlerTestApp(&handlerService{})
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "invalid tag id", method: http.MethodGet, path: "/api/tags/not-a-uuid"},
		{name: "unknown create field", method: http.MethodPost, path: "/api/tags/", body: `{"name":"Tag","unknown":true}`},
		{name: "multiple documents", method: http.MethodPost, path: "/api/tags/", body: `{}` + "\n" + `{}`},
		{name: "invalid nullable datasource", method: http.MethodPut, path: "/api/tags/" + uuid.NewString(), body: `{"datasource_id":"invalid"}`},
		{name: "invalid nullable description", method: http.MethodPut, path: "/api/tags/" + uuid.NewString(), body: `{"description":12}`},
		{name: "invalid enabled query", method: http.MethodGet, path: "/api/tags/?enabled=yes"},
		{name: "invalid datasource query", method: http.MethodGet, path: "/api/tags/?datasource_id=invalid"},
		{name: "zero page", method: http.MethodGet, path: "/api/tags/?page=0"},
		{name: "negative per page", method: http.MethodGet, path: "/api/tags/?per_page=-1"},
		{name: "preview unknown field", method: http.MethodPost, path: "/api/tags/preview", body: `{"unknown":true}`},
		{name: "validate malformed id", method: http.MethodPost, path: "/api/tags/validate-expression", body: `{"tag_id":"invalid","expression":"1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(t, app, test.method, test.path, test.body)
			assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestTagHandlerMapsServiceErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: tag.ErrTagNotFound, status: fiber.StatusNotFound, code: "TAG004"},
		{name: "datasource missing", err: tag.ErrDatasourceMissing, status: fiber.StatusNotFound, code: "DS008"},
		{name: "name conflict", err: tag.ErrTagNameExists, status: fiber.StatusConflict, code: "TAG001"},
		{name: "disabled", err: tag.ErrTagDisabled, status: fiber.StatusConflict, code: "TAG006"},
		{name: "source failure", err: tag.ErrTagSourceReadFailed, status: fiber.StatusBadGateway, code: "TAG007"},
		{name: "invalid", err: tag.ErrInvalidTagInput, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported type", err: tag.ErrUnsupportedTagType, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported data type", err: tag.ErrUnsupportedTagDataType, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported decoder", err: tag.ErrUnsupportedDecoder, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "decoder config", err: tag.ErrInvalidDecoderConfig, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "missing dependency", err: tag.ErrCalculatedDependencyMissing, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "cycle", err: tag.ErrCircularTagDependency, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "deep graph", err: tag.ErrTagDependencyTooDeep, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "complex expression", err: tag.ErrExpressionTooComplex, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unexpected", err: errors.New("database unavailable"), status: fiber.StatusInternalServerError, code: "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newHandlerTestApp(&handlerService{err: test.err})
			response := performRequest(t, app, http.MethodGet, "/api/tags/"+uuid.NewString(), "")
			assertAPIError(t, response, test.status, test.code)
		})
	}
}

func TestTagHandlerPreservesProtocolRequestErrorDetails(t *testing.T) {
	t.Parallel()
	requestFailure := &handlerRequestError{
		message: "Modbus exception 0x02: Illegal Data Address. The requested address range 99-100 is not available on the server.",
		details: map[string]any{"protocol": "modbus_tcp", "exception_code": 2, "exception_name": "ILLEGAL_DATA_ADDRESS"},
	}
	app := newHandlerTestApp(&handlerService{err: errors.Join(tag.ErrTagSourceReadFailed, requestFailure)})
	response := performRequest(t, app, http.MethodPost, "/api/tags/"+uuid.NewString()+"/preview", "")
	assertStatus(t, response, fiber.StatusBadGateway)
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != "TAG007" || body.Error.Message != requestFailure.message {
		t.Errorf("error = %#v", body.Error)
	}
	if body.Error.Details["protocol"] != "modbus_tcp" || body.Error.Details["exception_name"] != "ILLEGAL_DATA_ADDRESS" || body.Error.Details["exception_code"] != float64(2) {
		t.Errorf("details = %#v", body.Error.Details)
	}
}

type handlerRequestError struct {
	message string
	details map[string]any
}

func (e *handlerRequestError) Error() string { return e.message }

func (e *handlerRequestError) PublicMessage() string { return e.message }

func (e *handlerRequestError) PublicDetails() map[string]any { return e.details }

type handlerService struct {
	entity         *tag.Tag
	listResult     *tag.ListResult
	err            error
	createInput    tag.CreateInput
	listInput      tag.ListInput
	updatedID      uuid.UUID
	updateInput    tag.UpdateInput
	deletedID      uuid.UUID
	previewInput   tag.PreviewInput
	previewSavedID uuid.UUID
	validateID     uuid.UUID
	expression     string
}

func (service *handlerService) Create(_ context.Context, input tag.CreateInput) (*tag.Tag, error) {
	service.createInput = input
	return service.result()
}

func (service *handlerService) Get(context.Context, uuid.UUID) (*tag.Tag, error) {
	return service.result()
}

func (service *handlerService) List(_ context.Context, input tag.ListInput) (*tag.ListResult, error) {
	service.listInput = input
	if service.err != nil {
		return nil, service.err
	}
	if service.listResult == nil {
		return &tag.ListResult{}, nil
	}
	return service.listResult, nil
}

func (service *handlerService) Update(_ context.Context, id uuid.UUID, input tag.UpdateInput) (*tag.Tag, error) {
	service.updatedID = id
	service.updateInput = input
	return service.result()
}

func (service *handlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deletedID = id
	return service.err
}

func (service *handlerService) Preview(_ context.Context, input tag.PreviewInput) (*tag.PreviewResult, error) {
	service.previewInput = input
	if service.err != nil {
		return nil, service.err
	}
	return &tag.PreviewResult{DataType: input.DataType, Quality: "good", Value: true}, nil
}

func (service *handlerService) PreviewSaved(_ context.Context, id uuid.UUID) (*tag.PreviewResult, error) {
	service.previewSavedID = id
	if service.err != nil {
		return nil, service.err
	}
	return &tag.PreviewResult{TagID: id, DataType: tag.DataTypeUInt16, Quality: "good", Value: uint16(42)}, nil
}

func (service *handlerService) ValidateCalculatedExpression(_ context.Context, id uuid.UUID, expression string) ([]uuid.UUID, error) {
	service.validateID = id
	service.expression = expression
	if service.err != nil {
		return nil, service.err
	}
	if id == uuid.Nil {
		return nil, nil
	}
	return []uuid.UUID{id}, nil
}

func (service *handlerService) result() (*tag.Tag, error) {
	if service.err != nil {
		return nil, service.err
	}
	if service.entity == nil {
		return &tag.Tag{}, nil
	}
	return service.entity, nil
}

func newHandlerTestApp(service Service) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	return app
}

func performRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("%s %s error: %v", method, path, err)
	}
	return response
}

func assertStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, want, body)
	}
}

func assertAPIError(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	assertStatus(t, response, status)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code {
		t.Errorf("error code = %q, want %q", body.Error.Code, code)
	}
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer closeResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func closeResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("closing response: %v", err)
	}
}
