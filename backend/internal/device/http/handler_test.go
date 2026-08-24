package devicehttp

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
	"github.com/thefuriousowl/iot-edge/internal/device"
)

func TestDeviceHandlerListDeleteAndRawEndpoints(t *testing.T) {
	gatewayID := uuid.New()
	deviceID := uuid.New()
	datasourceID := uuid.New()
	service := &deviceHandlerService{
		devices:     []device.DeviceView{{Device: device.Device{ID: deviceID, VGatewayID: gatewayID, Name: "Meter", Type: device.DeviceTypeModbus, Enabled: true, Config: json.RawMessage(`{}`)}, DatasourceCount: 1, TagCount: 2}},
		inventory:   &device.DeviceInventoryResult{Data: []device.DeviceInventoryItem{{Device: device.Device{ID: deviceID, Name: "Meter"}, VGatewayName: "Factory", DatasourceCount: 1, TagCount: 2}}, Page: 2, PerPage: 10, Total: 11, TotalPages: 2},
		datasources: []device.DatasourceView{{Datasource: device.Datasource{ID: datasourceID, DeviceID: deviceID, Name: "Power"}, Status: "running"}},
		sample:      &device.DatasourceSample{DatasourceID: datasourceID, Sequence: 7, ObservedAt: time.Date(2026, time.August, 24, 1, 2, 3, 0, time.UTC), Quality: "good", RawHex: "1234", Data: json.RawMessage(`{"registers":[{"address":0,"value":4660}]}`)},
	}
	app := newDeviceHandlerTestApp(service)

	response := deviceHandlerRequest(t, app, http.MethodGet, "/api/vgateways/"+gatewayID.String()+"/devices/", "")
	assertDeviceHandlerStatus(t, response, fiber.StatusOK)
	var scoped struct {
		Data []device.DeviceView `json:"data"`
	}
	decodeDeviceHandlerResponse(t, response, &scoped)
	if service.gatewayID != gatewayID || len(scoped.Data) != 1 || scoped.Data[0].TagCount != 2 {
		t.Fatalf("gateway list = %#v, gateway ID = %s", scoped, service.gatewayID)
	}

	response = deviceHandlerRequest(t, app, http.MethodGet, "/api/devices/?vgateway_id="+gatewayID.String()+"&type=modbus_device&enabled=true&search=meter&page=2&per_page=10", "")
	assertDeviceHandlerStatus(t, response, fiber.StatusOK)
	var inventory struct {
		Data       []device.DeviceInventoryItem `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	decodeDeviceHandlerResponse(t, response, &inventory)
	if service.inventoryInput.VGatewayID == nil || *service.inventoryInput.VGatewayID != gatewayID || service.inventoryInput.Type == nil || *service.inventoryInput.Type != device.DeviceTypeModbus || service.inventoryInput.Enabled == nil || !*service.inventoryInput.Enabled || service.inventoryInput.Search != "meter" || service.inventoryInput.Page != 2 || service.inventoryInput.PerPage != 10 || inventory.Pagination.Total != 11 || len(inventory.Data) != 1 {
		t.Fatalf("inventory input/result = %#v / %#v", service.inventoryInput, inventory)
	}

	response = deviceHandlerRequest(t, app, http.MethodGet, "/api/devices/"+deviceID.String()+"/datasources", "")
	assertDeviceHandlerStatus(t, response, fiber.StatusOK)
	var listedSources struct {
		Data []device.DatasourceView `json:"data"`
	}
	decodeDeviceHandlerResponse(t, response, &listedSources)
	if service.deviceID != deviceID || len(listedSources.Data) != 1 || listedSources.Data[0].ID != datasourceID {
		t.Fatalf("datasource list = %#v, device ID = %s", listedSources, service.deviceID)
	}

	response = deviceHandlerRequest(t, app, http.MethodGet, "/api/datasources/"+datasourceID.String()+"/raw", "")
	assertDeviceHandlerStatus(t, response, fiber.StatusOK)
	var sample device.DatasourceSample
	decodeDeviceHandlerResponse(t, response, &sample)
	if service.datasourceID != datasourceID || sample.Sequence != 7 || sample.RawHex != "1234" {
		t.Fatalf("raw sample = %#v, datasource ID = %s", sample, service.datasourceID)
	}

	response = deviceHandlerRequest(t, app, http.MethodDelete, "/api/devices/"+deviceID.String(), "")
	assertDeviceHandlerStatus(t, response, fiber.StatusNoContent)
	closeDeviceHandlerResponse(t, response)
	response = deviceHandlerRequest(t, app, http.MethodDelete, "/api/datasources/"+datasourceID.String(), "")
	assertDeviceHandlerStatus(t, response, fiber.StatusNoContent)
	closeDeviceHandlerResponse(t, response)
	if service.deletedDeviceID != deviceID || service.deletedDatasourceID != datasourceID {
		t.Fatalf("deleted IDs = %s / %s", service.deletedDeviceID, service.deletedDatasourceID)
	}
}

func TestDeviceHandlerRejectsInvalidIDsQueriesAndStrictJSON(t *testing.T) {
	service := &deviceHandlerService{}
	app := newDeviceHandlerTestApp(service)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/vgateways/not-a-uuid/devices/", ""},
		{http.MethodGet, "/api/devices/not-a-uuid", ""},
		{http.MethodDelete, "/api/datasources/not-a-uuid", ""},
		{http.MethodGet, "/api/devices/?enabled=maybe", ""},
		{http.MethodGet, "/api/devices/?page=0", ""},
		{http.MethodPost, "/api/vgateways/" + uuid.NewString() + "/devices/", `{"name":"Meter","type":"modbus_device","config":{},"unknown":true}`},
		{http.MethodPost, "/api/vgateways/" + uuid.NewString() + "/devices/", `{"name":"Meter","type":"modbus_device","config":{}} {"name":"Second"}`},
		{http.MethodPut, "/api/devices/" + uuid.NewString(), `{"description":42}`},
		{http.MethodPost, "/api/devices/" + uuid.NewString() + "/datasources", `{"name":"Read","type":"modbus_read","config":{},"unknown":true}`},
	}
	for _, test := range tests {
		name := test.method + " " + test.path
		t.Run(name, func(t *testing.T) {
			response := deviceHandlerRequest(t, app, test.method, test.path, test.body)
			assertDeviceHandlerError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
	if service.calls != 0 {
		t.Errorf("service calls = %d, want 0", service.calls)
	}
}

func TestDeviceHandlerMapsStableSanitizedErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"device missing", device.ErrDeviceNotFound, fiber.StatusNotFound, "DEV004"},
		{"datasource missing", device.ErrDatasourceNotFound, fiber.StatusNotFound, "DS008"},
		{"gateway missing", device.ErrGatewayNotFound, fiber.StatusNotFound, "VGW004"},
		{"device conflict", device.ErrDeviceNameExists, fiber.StatusConflict, "DEV001"},
		{"datasource conflict", device.ErrDatasourceNameExists, fiber.StatusConflict, "DS001"},
		{"invalid", device.ErrInvalidDatasource, fiber.StatusBadRequest, "VALIDATION_ERROR"},
		{"monitor disabled", device.ErrMonitoringDisabled, fiber.StatusConflict, "DS009"},
		{"sample unavailable", device.ErrDatasourceSampleUnavailable, fiber.StatusNotFound, "DS010"},
		{"read failed", device.ErrDatasourceReadFailed, fiber.StatusBadGateway, "DS005"},
		{"unknown", errors.New("database password=do-not-expose"), fiber.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &deviceHandlerService{err: test.err}
			app := newDeviceHandlerTestApp(service)
			response := deviceHandlerRequest(t, app, http.MethodGet, "/api/datasources/"+uuid.NewString()+"/raw", "")
			body := assertDeviceHandlerError(t, response, test.status, test.code)
			if strings.Contains(body, "do-not-expose") || strings.Contains(body, "password") {
				t.Errorf("response leaked internal error: %s", body)
			}
		})
	}
}

func TestDeviceHandlerStreamsSamplesAndUnsubscribes(t *testing.T) {
	datasourceID := uuid.New()
	stream := make(chan device.DatasourceSample, 1)
	stream <- device.DatasourceSample{DatasourceID: datasourceID, Sequence: 9, ObservedAt: time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC), Quality: "bad", Error: "Illegal Data Address"}
	close(stream)
	service := &deviceHandlerService{stream: stream}
	app := newDeviceHandlerTestApp(service)
	response := deviceHandlerRequest(t, app, http.MethodGet, "/api/datasources/"+datasourceID.String()+"/stream", "")
	assertDeviceHandlerStatus(t, response, fiber.StatusOK)
	body, err := io.ReadAll(response.Body)
	closeDeviceHandlerResponse(t, response)
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	contents := string(body)
	for _, expected := range []string{": connected", "event: sample", `"sequence":9`, `"quality":"bad"`, "Illegal Data Address"} {
		if !strings.Contains(contents, expected) {
			t.Errorf("stream missing %q: %s", expected, contents)
		}
	}
	if response.Header.Get(fiber.HeaderContentType) != "text/event-stream" || response.Header.Get(fiber.HeaderCacheControl) != "no-cache, no-transform" || !service.unsubscribed || service.datasourceID != datasourceID {
		t.Errorf("stream headers/unsubscribe/id = %q / %q / %v / %s", response.Header.Get(fiber.HeaderContentType), response.Header.Get(fiber.HeaderCacheControl), service.unsubscribed, service.datasourceID)
	}

	service = &deviceHandlerService{err: device.ErrMonitoringDisabled}
	app = newDeviceHandlerTestApp(service)
	response = deviceHandlerRequest(t, app, http.MethodGet, "/api/datasources/"+datasourceID.String()+"/stream", "")
	assertDeviceHandlerError(t, response, fiber.StatusConflict, "DS009")
}

type deviceHandlerService struct {
	err                 error
	calls               int
	gatewayID           uuid.UUID
	deviceID            uuid.UUID
	datasourceID        uuid.UUID
	deletedDeviceID     uuid.UUID
	deletedDatasourceID uuid.UUID
	inventoryInput      device.DeviceInventoryInput
	devices             []device.DeviceView
	inventory           *device.DeviceInventoryResult
	datasources         []device.DatasourceView
	sample              *device.DatasourceSample
	stream              <-chan device.DatasourceSample
	unsubscribed        bool
}

func (service *deviceHandlerService) CreateDevice(context.Context, uuid.UUID, device.CreateDeviceInput) (*device.Device, error) {
	service.calls++
	return &device.Device{}, service.err
}
func (service *deviceHandlerService) GetDevice(_ context.Context, id uuid.UUID) (*device.DeviceView, error) {
	service.calls++
	service.deviceID = id
	return &device.DeviceView{}, service.err
}
func (service *deviceHandlerService) ListDevices(_ context.Context, id uuid.UUID) ([]device.DeviceView, error) {
	service.calls++
	service.gatewayID = id
	return service.devices, service.err
}
func (service *deviceHandlerService) ListDeviceInventory(_ context.Context, input device.DeviceInventoryInput) (*device.DeviceInventoryResult, error) {
	service.calls++
	service.inventoryInput = input
	return service.inventory, service.err
}
func (service *deviceHandlerService) UpdateDevice(context.Context, uuid.UUID, device.UpdateDeviceInput) (*device.Device, error) {
	service.calls++
	return &device.Device{}, service.err
}
func (service *deviceHandlerService) DeleteDevice(_ context.Context, id uuid.UUID) error {
	service.calls++
	service.deletedDeviceID = id
	return service.err
}
func (service *deviceHandlerService) CreateDatasource(context.Context, uuid.UUID, device.CreateDatasourceInput) (*device.Datasource, error) {
	service.calls++
	return &device.Datasource{}, service.err
}
func (service *deviceHandlerService) GetDatasource(_ context.Context, id uuid.UUID) (*device.DatasourceView, error) {
	service.calls++
	service.datasourceID = id
	return &device.DatasourceView{}, service.err
}
func (service *deviceHandlerService) ListDatasources(_ context.Context, id uuid.UUID) ([]device.DatasourceView, error) {
	service.calls++
	service.deviceID = id
	return service.datasources, service.err
}
func (service *deviceHandlerService) UpdateDatasource(context.Context, uuid.UUID, device.UpdateDatasourceInput) (*device.DatasourceView, error) {
	service.calls++
	return &device.DatasourceView{}, service.err
}
func (service *deviceHandlerService) DeleteDatasource(_ context.Context, id uuid.UUID) error {
	service.calls++
	service.deletedDatasourceID = id
	return service.err
}
func (service *deviceHandlerService) PreviewDatasource(context.Context, uuid.UUID, device.PreviewDatasourceInput) (*device.DatasourceSample, error) {
	service.calls++
	return service.sample, service.err
}
func (service *deviceHandlerService) PreviewSavedDatasource(_ context.Context, id uuid.UUID) (*device.DatasourceSample, error) {
	service.calls++
	service.datasourceID = id
	return service.sample, service.err
}
func (service *deviceHandlerService) LatestSample(_ context.Context, id uuid.UUID) (*device.DatasourceSample, error) {
	service.calls++
	service.datasourceID = id
	return service.sample, service.err
}
func (service *deviceHandlerService) Subscribe(_ context.Context, id uuid.UUID) (<-chan device.DatasourceSample, func(), error) {
	service.calls++
	service.datasourceID = id
	return service.stream, func() { service.unsubscribed = true }, service.err
}

func newDeviceHandlerTestApp(service Service) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	return app
}

func deviceHandlerRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, path, err)
	}
	return response
}

func assertDeviceHandlerStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		closeDeviceHandlerResponse(t, response)
		t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, want, body)
	}
}

func assertDeviceHandlerError(t *testing.T, response *http.Response, status int, code string) string {
	t.Helper()
	assertDeviceHandlerStatus(t, response, status)
	body, err := io.ReadAll(response.Body)
	closeDeviceHandlerResponse(t, response)
	if err != nil {
		t.Fatalf("reading error response: %v", err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decoding error response: %v; body=%s", err, body)
	}
	if envelope.Error.Code != code {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, code)
	}
	return string(body)
}

func decodeDeviceHandlerResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer closeDeviceHandlerResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func closeDeviceHandlerResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("closing response: %v", err)
	}
}
