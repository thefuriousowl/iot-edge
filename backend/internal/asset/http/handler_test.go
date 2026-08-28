package assethttp

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
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

type handlerService struct {
	entity          *asset.Asset
	tree            *asset.TreeNode
	list            *asset.ListResult
	assets          []asset.Asset
	bindings        []asset.MeasurementBinding
	err             error
	createInput     asset.CreateInput
	listInput       asset.ListInput
	updateID        uuid.UUID
	updateInput     asset.UpdateInput
	moveID          uuid.UUID
	moveInput       asset.MoveInput
	deletedID       uuid.UUID
	childrenParent  *uuid.UUID
	bindingAssetID  uuid.UUID
	replaceBindings []asset.MeasurementBinding
}

type measurementHandlerService struct {
	snapshot     *asset.MeasurementSnapshot
	subscription *measurementHandlerSubscription
	err          error
	assetID      uuid.UUID
}

func (service *measurementHandlerService) Snapshot(_ context.Context, id uuid.UUID) (*asset.MeasurementSnapshot, error) {
	service.assetID = id
	return service.snapshot, service.err
}
func (service *measurementHandlerService) Subscribe(_ context.Context, id uuid.UUID) (asset.MeasurementSubscription, error) {
	service.assetID = id
	return service.subscription, service.err
}

type measurementHandlerSubscription struct {
	events chan asset.MeasurementReading
	err    error
	closed bool
}

func (subscription *measurementHandlerSubscription) Events() <-chan asset.MeasurementReading {
	return subscription.events
}
func (subscription *measurementHandlerSubscription) Err() error { return subscription.err }
func (subscription *measurementHandlerSubscription) Close()     { subscription.closed = true }

type connectivityHandlerService struct {
	assetValue *asset.AssetConnectivity
	tagValue   *asset.TagAssetConnectivity
	err        error
	assetID    uuid.UUID
	tagID      uuid.UUID
}

func (service *connectivityHandlerService) Asset(_ context.Context, id uuid.UUID) (*asset.AssetConnectivity, error) {
	service.assetID = id
	return service.assetValue, service.err
}
func (service *connectivityHandlerService) Tag(_ context.Context, id uuid.UUID) (*asset.TagAssetConnectivity, error) {
	service.tagID = id
	return service.tagValue, service.err
}

func (service *handlerService) Create(_ context.Context, input asset.CreateInput) (*asset.Asset, error) {
	service.createInput = input
	return service.entity, service.err
}
func (service *handlerService) Get(_ context.Context, _ uuid.UUID) (*asset.Asset, error) {
	return service.entity, service.err
}
func (service *handlerService) List(_ context.Context, input asset.ListInput) (*asset.ListResult, error) {
	service.listInput = input
	return service.list, service.err
}
func (service *handlerService) Children(_ context.Context, parent *uuid.UUID) ([]asset.Asset, error) {
	service.childrenParent = parent
	return service.assets, service.err
}
func (service *handlerService) Subtree(_ context.Context, _ uuid.UUID) (*asset.TreeNode, error) {
	return service.tree, service.err
}
func (service *handlerService) Ancestors(_ context.Context, _ uuid.UUID) ([]asset.Asset, error) {
	return service.assets, service.err
}
func (service *handlerService) Update(_ context.Context, id uuid.UUID, input asset.UpdateInput) (*asset.Asset, error) {
	service.updateID, service.updateInput = id, input
	return service.entity, service.err
}
func (service *handlerService) Move(_ context.Context, id uuid.UUID, input asset.MoveInput) (*asset.Asset, error) {
	service.moveID, service.moveInput = id, input
	return service.entity, service.err
}
func (service *handlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deletedID = id
	return service.err
}
func (service *handlerService) Bindings(_ context.Context, id uuid.UUID) ([]asset.MeasurementBinding, error) {
	service.bindingAssetID = id
	return service.bindings, service.err
}
func (service *handlerService) ReplaceBindings(_ context.Context, id uuid.UUID, values []asset.MeasurementBinding) ([]asset.MeasurementBinding, error) {
	service.bindingAssetID, service.replaceBindings = id, values
	return service.bindings, service.err
}

func TestHandlerCRUDTreeAndListContracts(t *testing.T) {
	t.Parallel()
	id, parentID := uuid.New(), uuid.New()
	entity := &asset.Asset{ID: id, ParentID: &parentID, Name: "Boiler", Kind: asset.KindEquipment, Enabled: true, Metadata: asset.Metadata(`{}`)}
	service := &handlerService{entity: entity, tree: &asset.TreeNode{Asset: *entity}, assets: []asset.Asset{*entity}, list: &asset.ListResult{Data: []asset.Asset{*entity}, Page: 2, PerPage: 5, Total: 6, TotalPages: 2}}
	app := newTestApp(service)

	response := request(t, app, http.MethodPost, "/api/assets/", `{"parent_id":"`+parentID.String()+`","name":" Boiler ","kind":"equipment","description":"Plant boiler","enabled":true,"timezone":"Asia/Bangkok","position":3,"metadata":{"area":"north"}}`)
	assertStatus(t, response, fiber.StatusCreated)
	closeBody(t, response)
	if service.createInput.ParentID == nil || *service.createInput.ParentID != parentID || service.createInput.Name != " Boiler " || service.createInput.Kind != asset.KindEquipment || service.createInput.Position != 3 || string(service.createInput.Metadata) != `{"area":"north"}` {
		t.Fatalf("Create input = %#v", service.createInput)
	}

	query := url.Values{"search": {"boiler"}, "kind": {"equipment"}, "enabled": {"true"}, "page": {"2"}, "per_page": {"5"}}
	response = request(t, app, http.MethodGet, "/api/assets/?"+query.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var listed struct {
		Data       []asset.Asset `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &listed)
	if service.listInput.Kind == nil || *service.listInput.Kind != asset.KindEquipment || service.listInput.Enabled == nil || !*service.listInput.Enabled || listed.Pagination.Total != 6 {
		t.Fatalf("List input=%#v response=%#v", service.listInput, listed)
	}

	name := "Boiler 2"
	response = request(t, app, http.MethodPut, "/api/assets/"+id.String(), `{"name":"`+name+`","description":null,"timezone":null,"metadata":{"critical":true}}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.updateID != id || service.updateInput.Name == nil || *service.updateInput.Name != name || service.updateInput.Description == nil || *service.updateInput.Description != nil || service.updateInput.Timezone == nil || *service.updateInput.Timezone != nil || string(service.updateInput.Metadata) != `{"critical":true}` {
		t.Fatalf("Update input = %#v", service.updateInput)
	}

	response = request(t, app, http.MethodPost, "/api/assets/"+id.String()+"/move", `{"parent_id":"`+parentID.String()+`","position":4}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if service.moveID != id || service.moveInput.ParentID == nil || *service.moveInput.ParentID != parentID || service.moveInput.Position != 4 {
		t.Fatalf("Move input = %#v", service.moveInput)
	}

	for _, path := range []string{"/api/assets/" + id.String(), "/api/assets/" + id.String() + "/tree", "/api/assets/" + id.String() + "/ancestors", "/api/assets/" + id.String() + "/children", "/api/assets/roots"} {
		response = request(t, app, http.MethodGet, path, "")
		assertStatus(t, response, fiber.StatusOK)
		closeBody(t, response)
	}
	if service.childrenParent != nil {
		t.Fatalf("Roots parent = %v, want nil", service.childrenParent)
	}
	response = request(t, app, http.MethodDelete, "/api/assets/"+id.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	if service.deletedID != id {
		t.Fatalf("deleted ID = %s", service.deletedID)
	}
}

func TestHandlerBindingContracts(t *testing.T) {
	t.Parallel()
	id, boundaryID, tagID := uuid.New(), uuid.New(), uuid.New()
	binding := asset.MeasurementBinding{ID: uuid.New(), BoundaryAssetID: boundaryID, Source: asset.TagSource(tagID), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityEnergy, Unit: asset.UnitKilowattHour, Precision: 3}, MeterRole: asset.MeterRoleMain, RollupPolicy: asset.RollupInclude}
	service := &handlerService{bindings: []asset.MeasurementBinding{binding}}
	app := newTestApp(service)

	payload, err := json.Marshal(bindingsRequest{Bindings: []asset.MeasurementBinding{binding}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, app, http.MethodPut, "/api/assets/"+id.String()+"/bindings", string(payload))
	assertStatus(t, response, fiber.StatusOK)
	var result struct {
		Data []asset.MeasurementBinding `json:"data"`
	}
	decodeResponse(t, response, &result)
	if service.bindingAssetID != id || len(service.replaceBindings) != 1 || service.replaceBindings[0].Source.TagID != tagID || len(result.Data) != 1 {
		t.Fatalf("Replace bindings input=%#v result=%#v", service.replaceBindings, result)
	}
	response = request(t, app, http.MethodGet, "/api/assets/"+id.String()+"/bindings", "")
	assertStatus(t, response, fiber.StatusOK)
	decodeResponse(t, response, &result)
	if len(result.Data) != 1 {
		t.Fatalf("Bindings result = %#v", result)
	}
}

func TestHandlerMeasurementSnapshotAndStreamContracts(t *testing.T) {
	t.Parallel()
	id, tagID := uuid.New(), uuid.New()
	at := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	reading := asset.MeasurementReading{BindingID: uuid.New(), Source: asset.TagSource(tagID), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt}, Available: true, Sequence: 12, Value: 4.5, Quality: "good", ObservedAt: &at}
	snapshot := &asset.MeasurementSnapshot{AssetID: id, CapturedAt: at, Measurements: []asset.MeasurementProjection{{Latest: reading, History: []asset.MeasurementReading{reading}, HistoryRetention: "runtime_memory"}}}
	events := make(chan asset.MeasurementReading, 1)
	events <- reading
	close(events)
	subscription := &measurementHandlerSubscription{events: events}
	measurements := &measurementHandlerService{snapshot: snapshot, subscription: subscription}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(&handlerService{}, WithMeasurements(measurements)))

	response := request(t, app, http.MethodGet, "/api/assets/"+id.String()+"/measurements", "")
	assertStatus(t, response, fiber.StatusOK)
	var result asset.MeasurementSnapshot
	decodeResponse(t, response, &result)
	if measurements.assetID != id || len(result.Measurements) != 1 || result.Measurements[0].Latest.Sequence != 12 || len(result.Measurements[0].History) != 1 {
		t.Fatalf("measurement snapshot = %#v", result)
	}

	response = request(t, app, http.MethodGet, "/api/assets/"+id.String()+"/measurements/stream", "")
	assertStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	closeBody(t, response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "event: asset_measurement") || !strings.Contains(string(body), `"sequence":12`) || !subscription.closed {
		t.Fatalf("stream body=%s closed=%v", body, subscription.closed)
	}
}

func TestHandlerMeasurementMonitoringUnavailable(t *testing.T) {
	t.Parallel()
	app := newTestApp(&handlerService{})
	response := request(t, app, http.MethodGet, "/api/assets/"+uuid.NewString()+"/measurements", "")
	assertAPIError(t, response, fiber.StatusServiceUnavailable, "ASSET_LIVE_UNAVAILABLE")
}

func TestHandlerConnectivityContracts(t *testing.T) {
	t.Parallel()
	assetID, tagID, bindingID := uuid.New(), uuid.New(), uuid.New()
	connectivity := &connectivityHandlerService{
		assetValue: &asset.AssetConnectivity{AssetID: assetID, Links: []asset.MeasurementConnectivity{{BindingID: bindingID, Source: asset.TagSource(tagID), Tag: &asset.ConnectivityEntity{ID: tagID, Name: "Power", Kind: "reading", Enabled: true}}}},
		tagValue:   &asset.TagAssetConnectivity{TagID: tagID, Assets: []asset.TagAssetLink{{BindingID: bindingID, Asset: asset.Asset{ID: assetID, Name: "Meter", Kind: asset.KindMeter}}}},
	}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(&handlerService{}, WithConnectivity(connectivity)))
	response := request(t, app, http.MethodGet, "/api/assets/"+assetID.String()+"/connectivity", "")
	assertStatus(t, response, fiber.StatusOK)
	var assetResult asset.AssetConnectivity
	decodeResponse(t, response, &assetResult)
	if connectivity.assetID != assetID || len(assetResult.Links) != 1 || assetResult.Links[0].Tag == nil || assetResult.Links[0].Tag.ID != tagID {
		t.Fatalf("Asset connectivity = %#v", assetResult)
	}
	response = request(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/assets", "")
	assertStatus(t, response, fiber.StatusOK)
	var tagResult asset.TagAssetConnectivity
	decodeResponse(t, response, &tagResult)
	if connectivity.tagID != tagID || len(tagResult.Assets) != 1 || tagResult.Assets[0].Asset.ID != assetID {
		t.Fatalf("Tag connectivity = %#v", tagResult)
	}
}

func TestHandlerConnectivityUnavailableAndTypedErrors(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	app := newTestApp(&handlerService{})
	response := request(t, app, http.MethodGet, "/api/assets/"+id+"/connectivity", "")
	assertAPIError(t, response, fiber.StatusServiceUnavailable, "ASSET_CONNECTIVITY_UNAVAILABLE")
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{{asset.ErrConnectivitySourceNotFound, fiber.StatusNotFound, "ASSET_CONNECTIVITY_SOURCE004"}, {asset.ErrConnectivityUnavailable, fiber.StatusConflict, "ASSET_CONNECTIVITY_INCOMPLETE"}} {
		connectivity := &connectivityHandlerService{err: test.err}
		app = fiber.New()
		RegisterRoutes(app.Group("/api"), NewHandler(&handlerService{}, WithConnectivity(connectivity)))
		response = request(t, app, http.MethodGet, "/api/assets/"+id+"/connectivity", "")
		assertAPIError(t, response, test.status, test.code)
	}
}

func TestHandlerRejectsMalformedBodiesAndQueries(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	app := newTestApp(&handlerService{})
	tests := []struct{ method, path, body string }{
		{http.MethodGet, "/api/assets/not-a-uuid", ""},
		{http.MethodPost, "/api/assets/", `{"unknown":true}`},
		{http.MethodPost, "/api/assets/", `{}` + "\n" + `{}`},
		{http.MethodGet, "/api/assets/?future=true", ""},
		{http.MethodGet, "/api/assets/?page=1&page=2", ""},
		{http.MethodGet, "/api/assets/?enabled=yes", ""},
		{http.MethodGet, "/api/assets/?page=0", ""},
		{http.MethodGet, "/api/assets/roots?future=true", ""},
		{http.MethodGet, "/api/assets/" + id + "/tree?depth=all", ""},
		{http.MethodGet, "/api/assets/" + id + "?future=true", ""},
		{http.MethodPost, "/api/assets/" + id + "/move?future=true", `{"position":0}`},
		{http.MethodPut, "/api/assets/" + id, `{"parent_id":null}`},
		{http.MethodPut, "/api/assets/" + id, `{"description":12}`},
		{http.MethodPut, "/api/assets/" + id, `{"timezone":false}`},
		{http.MethodPut, "/api/assets/" + id, `{"metadata":null}`},
		{http.MethodPost, "/api/assets/" + id + "/move", `{"position":0,"unknown":true}`},
		{http.MethodPut, "/api/assets/" + id + "/bindings", `{}`},
	}
	for _, test := range tests {
		response := request(t, app, test.method, test.path, test.body)
		assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
	}
	plain := httptest.NewRequest(http.MethodPost, "/api/assets/", strings.NewReader(`{"name":"Site","kind":"site"}`))
	response, err := app.Test(plain, -1)
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
}

func TestHandlerMapsTypedErrorsAndSanitizesInternalErrors(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{asset.ErrAssetNotFound, fiber.StatusNotFound, "ASSET004"},
		{asset.ErrBindingSourceMissing, fiber.StatusNotFound, "ASSET_SOURCE004"},
		{asset.ErrAssetNameExists, fiber.StatusConflict, "ASSET001"},
		{asset.ErrAssetHasDependents, fiber.StatusConflict, "ASSET_DEPENDENTS"},
		{asset.ErrInvalidAggregation, fiber.StatusBadRequest, "VALIDATION_ERROR"},
		{errors.New("database password leaked"), fiber.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		app := newTestApp(&handlerService{err: test.err})
		response := request(t, app, http.MethodGet, "/api/assets/"+id, "")
		body := assertAPIError(t, response, test.status, test.code)
		if strings.Contains(body, "database password") {
			t.Fatalf("internal error leaked: %s", body)
		}
	}
}

func newTestApp(service Service) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	return app
}

func request(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
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

func assertAPIError(t *testing.T, response *http.Response, status int, code string) string {
	t.Helper()
	assertStatus(t, response, status)
	body, err := io.ReadAll(response.Body)
	closeBody(t, response)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != code {
		t.Fatalf("error body = %s, code=%q", body, envelope.Error.Code)
	}
	return string(body)
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer closeBody(t, response)
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func closeBody(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
