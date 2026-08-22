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
	"time"

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

func TestTagHandlerReturnsRuntimeHistoryAndStreamsValues(t *testing.T) {
	t.Parallel()
	tagID := uuid.New()
	entity := tag.Tag{ID: tagID, Name: "Pressure", Type: tag.TypeReading, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)}
	observedAt := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	first := tag.TagValue{TagID: tagID, Sequence: 10, ObservedAt: observedAt, StoredAt: observedAt, Quality: tag.ValueQualityGood, DataType: tag.DataTypeFloat64, Value: float64(12.5)}
	second := tag.TagValue{TagID: tagID, Sequence: 11, ObservedAt: observedAt.Add(time.Second), StoredAt: observedAt.Add(time.Second), Quality: tag.ValueQualityBad, DataType: tag.DataTypeFloat64, Error: "connection lost"}
	monitor := &handlerValueMonitor{latest: second, history: []tag.TagValue{first, second}, stream: make(chan tag.TagValue, 1)}
	monitor.stream <- second
	close(monitor.stream)
	app := newHandlerTestApp(&handlerService{entity: &entity}, WithValueMonitor(monitor))

	response := performRequest(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/values?limit=2", "")
	assertStatus(t, response, fiber.StatusOK)
	var values struct {
		Latest           *tag.TagValue  `json:"latest"`
		History          []tag.TagValue `json:"history"`
		LatestRetention  string         `json:"latest_retention"`
		HistoryRetention string         `json:"history_retention"`
	}
	decodeResponse(t, response, &values)
	if values.Latest == nil || values.Latest.Sequence != second.Sequence || len(values.History) != 2 || values.LatestRetention != "persistent" || values.HistoryRetention != "runtime_memory" || monitor.limit != 2 {
		t.Errorf("values = %#v, limit = %d", values, monitor.limit)
	}

	response = performRequest(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/stream", "")
	assertStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	closeResponse(t, response)
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	if !strings.Contains(string(body), "event: tag_value") || !strings.Contains(string(body), `"sequence":11`) {
		t.Errorf("stream body = %s", body)
	}
}

func TestTagHandlerStreamsAllValuesWithSequenceResume(t *testing.T) {
	t.Parallel()
	firstID := uuid.New()
	secondID := uuid.New()
	observedAt := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	first := tag.TagValue{TagID: firstID, Sequence: 10, ObservedAt: observedAt, StoredAt: observedAt, Quality: tag.ValueQualityGood, DataType: tag.DataTypeUInt16, Value: uint16(42)}
	bad := tag.TagValue{TagID: secondID, Sequence: 11, ObservedAt: observedAt.Add(time.Second), StoredAt: observedAt.Add(time.Second), Quality: tag.ValueQualityBad, DataType: tag.DataTypeFloat64, Error: "Modbus exception 0x02: Illegal Data Address"}
	live := tag.TagValue{TagID: firstID, Sequence: 12, ObservedAt: observedAt.Add(2 * time.Second), StoredAt: observedAt.Add(2 * time.Second), Quality: tag.ValueQualityGood, DataType: tag.DataTypeUInt16, Value: uint16(43)}
	stream := make(chan tag.TagValue, 1)
	stream <- live
	close(stream)
	monitor := &handlerValueMonitor{subscription: tag.ValueSubscription{Replay: []tag.TagValue{first, bad}, Stream: stream}}
	app := newHandlerTestApp(&handlerService{}, WithValueMonitor(monitor))
	request := httptest.NewRequest(http.MethodGet, "/api/sse/tags", nil)
	request.Header.Set("Last-Event-ID", "9")
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("stream request error = %v", err)
	}
	assertStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	closeResponse(t, response)
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	contents := string(body)
	for _, expected := range []string{"retry: 3000", "id: 10", "id: 11", "id: 12", `"quality":"bad"`, `"error":"Modbus exception 0x02: Illegal Data Address"`} {
		if !strings.Contains(contents, expected) {
			t.Errorf("stream body missing %q: %s", expected, contents)
		}
	}
	if strings.Index(contents, "id: 10") > strings.Index(contents, "id: 11") || strings.Index(contents, "id: 11") > strings.Index(contents, "id: 12") {
		t.Errorf("stream sequence order = %s", contents)
	}
	if monitor.afterSequence != 9 || len(monitor.tagIDs) != 0 || !monitor.unsubscribed {
		t.Errorf("subscription = after %d, tags %v, unsubscribed %v", monitor.afterSequence, monitor.tagIDs, monitor.unsubscribed)
	}
	if response.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q", response.Header.Get("X-Accel-Buffering"))
	}
}

func TestTagHandlerValidatesGlobalStreamCursorAndAvailability(t *testing.T) {
	t.Parallel()
	app := newHandlerTestApp(&handlerService{})
	response := performRequest(t, app, http.MethodGet, "/api/sse/tags", "")
	assertAPIError(t, response, fiber.StatusServiceUnavailable, "TAG009")

	monitor := &handlerValueMonitor{}
	app = newHandlerTestApp(&handlerService{}, WithValueMonitor(monitor))
	request := httptest.NewRequest(http.MethodGet, "/api/sse/tags", nil)
	request.Header.Set("Last-Event-ID", "not-a-sequence")
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("invalid cursor request error = %v", err)
	}
	assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
	if monitor.subscribeCalls != 0 {
		t.Errorf("SubscribeValues() calls = %d, want 0", monitor.subscribeCalls)
	}
}

func TestTagSSERouteHonorsProtectedRouterMiddleware(t *testing.T) {
	t.Parallel()
	monitor := &handlerValueMonitor{}
	app := fiber.New()
	protected := app.Group("/api", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusUnauthorized)
	})
	RegisterRoutes(protected, NewHandler(&handlerService{}, WithValueMonitor(monitor)))
	response := performRequest(t, app, http.MethodGet, "/api/sse/tags", "")
	assertStatus(t, response, fiber.StatusUnauthorized)
	closeResponse(t, response)
	if monitor.subscribeCalls != 0 {
		t.Errorf("SubscribeValues() calls = %d, want 0", monitor.subscribeCalls)
	}
}

func TestTagHandlerValidatesValueMonitoringAvailabilityAndLimit(t *testing.T) {
	t.Parallel()
	tagID := uuid.New()
	entity := tag.Tag{ID: tagID, Name: "Pressure", Type: tag.TypeReading, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)}
	app := newHandlerTestApp(&handlerService{entity: &entity})
	response := performRequest(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/values", "")
	assertAPIError(t, response, fiber.StatusServiceUnavailable, "TAG009")

	app = newHandlerTestApp(&handlerService{entity: &entity}, WithValueMonitor(&handlerValueMonitor{}))
	response = performRequest(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/values", "")
	assertStatus(t, response, fiber.StatusOK)
	var emptyValues struct {
		History json.RawMessage `json:"history"`
	}
	decodeResponse(t, response, &emptyValues)
	if string(emptyValues.History) != "[]" {
		t.Errorf("empty history JSON = %s, want []", emptyValues.History)
	}
	for _, limit := range []string{"0", "11", "invalid"} {
		response = performRequest(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/values?limit="+limit, "")
		assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
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
		{name: "snapshot required", err: tag.ErrCalculatedSnapshotRequired, status: fiber.StatusConflict, code: "TAG008"},
		{name: "source failure", err: tag.ErrTagSourceReadFailed, status: fiber.StatusBadGateway, code: "TAG007"},
		{name: "invalid", err: tag.ErrInvalidTagInput, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported type", err: tag.ErrUnsupportedTagType, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported data type", err: tag.ErrUnsupportedTagDataType, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported decoder", err: tag.ErrUnsupportedDecoder, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "decoder config", err: tag.ErrInvalidDecoderConfig, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "transform config", err: tag.ErrInvalidTransformConfig, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "unsupported transform", err: tag.ErrUnsupportedTransform, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
		{name: "invalid trigger", err: tag.ErrCalculatedTriggerInvalid, status: fiber.StatusBadRequest, code: "VALIDATION_ERROR"},
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

type handlerValueMonitor struct {
	latest         tag.TagValue
	history        []tag.TagValue
	stream         chan tag.TagValue
	subscription   tag.ValueSubscription
	limit          int
	afterSequence  uint64
	tagIDs         []uuid.UUID
	subscribeCalls int
	unsubscribed   bool
}

func (monitor *handlerValueMonitor) Latest(uuid.UUID) (tag.TagValue, bool) {
	return monitor.latest, monitor.latest.TagID != uuid.Nil
}

func (monitor *handlerValueMonitor) History(_ uuid.UUID, limit int) []tag.TagValue {
	monitor.limit = limit
	return append([]tag.TagValue(nil), monitor.history...)
}

func (monitor *handlerValueMonitor) Subscribe(context.Context, []uuid.UUID) (<-chan tag.TagValue, func()) {
	if monitor.stream == nil {
		monitor.stream = make(chan tag.TagValue)
		close(monitor.stream)
	}
	return monitor.stream, func() {}
}

func (monitor *handlerValueMonitor) SubscribeValues(_ context.Context, tagIDs []uuid.UUID, afterSequence uint64) tag.ValueSubscription {
	monitor.subscribeCalls++
	monitor.tagIDs = append([]uuid.UUID(nil), tagIDs...)
	monitor.afterSequence = afterSequence
	if monitor.subscription.Stream == nil {
		stream := make(chan tag.TagValue)
		close(stream)
		monitor.subscription.Stream = stream
	}
	monitor.subscription.Unsubscribe = func() { monitor.unsubscribed = true }
	return monitor.subscription
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

func newHandlerTestApp(service Service, options ...HandlerOption) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service, options...))
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
