package devicehttp

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/device"
	devicepostgres "github.com/thefuriousowl/iot-edge/internal/device/postgres"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDeviceDatasourceCRUDAndPreview_EndToEnd(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("set TEST_DATABASE_URL to run device HTTP integration tests")
	}
	server := newModbusReadServer(t)
	host, portText, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting simulator address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing simulator port: %v", err)
	}
	app, gatewayID, countDevices, db := newDeviceIntegrationApp(t, host, port)

	var createdDevice device.Device
	requestJSON(t, app, http.MethodPost, "/api/vgateways/"+gatewayID.String()+"/devices", `{"name":" Meter 7 ","type":"modbus_device","config":{"unit_id":7,"poll_interval_ms":100}}`, fiber.StatusCreated, &createdDevice)
	if createdDevice.ID == uuid.Nil || createdDevice.Name != "Meter 7" || string(createdDevice.Config) != `{"unit_id":7,"poll_interval_ms":100,"request_timeout_ms":null}` {
		t.Errorf("created device = %#v", createdDevice)
	}
	counts, err := countDevices(context.Background(), []uuid.UUID{gatewayID})
	if err != nil {
		t.Fatalf("CountByVGatewayIDs() error = %v", err)
	}
	if counts[gatewayID] != 1 {
		t.Errorf("device count = %d, want 1", counts[gatewayID])
	}
	var fetchedDevice device.DeviceView
	requestJSON(t, app, http.MethodGet, "/api/devices/"+createdDevice.ID.String(), "", fiber.StatusOK, &fetchedDevice)
	if fetchedDevice.ID != createdDevice.ID || fetchedDevice.DatasourceCount != 0 {
		t.Errorf("fetched device = %#v", fetchedDevice)
	}
	var updatedDevice device.Device
	requestJSON(t, app, http.MethodPut, "/api/devices/"+createdDevice.ID.String(), `{"name":"Meter 7 updated","description":"Production meter","enabled":true,"config":{"unit_id":7,"poll_interval_ms":100}}`, fiber.StatusOK, &updatedDevice)
	if updatedDevice.Name != "Meter 7 updated" || updatedDevice.Description == nil || *updatedDevice.Description != "Production meter" || !updatedDevice.Enabled {
		t.Errorf("updated device = %#v", updatedDevice)
	}

	previewBody := `{"type":"modbus_read","config":{"function_code":3,"start_address":10,"quantity":2}}`
	var unsaved device.DatasourceSample
	requestJSON(t, app, http.MethodPost, "/api/devices/"+createdDevice.ID.String()+"/datasources/preview", previewBody, fiber.StatusOK, &unsaved)
	assertIntegrationSample(t, unsaved, uuid.Nil)

	var failedPreview struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Protocol      string `json:"protocol"`
				ExceptionCode int    `json:"exception_code"`
				ExceptionName string `json:"exception_name"`
				StartAddress  int    `json:"start_address"`
				Quantity      int    `json:"quantity"`
			} `json:"details"`
		} `json:"error"`
	}
	requestJSON(t, app, http.MethodPost, "/api/devices/"+createdDevice.ID.String()+"/datasources/preview", `{"type":"modbus_read","config":{"function_code":3,"start_address":99,"quantity":2}}`, fiber.StatusBadGateway, &failedPreview)
	if failedPreview.Error.Code != "DS005" || !strings.Contains(failedPreview.Error.Message, "Modbus exception 0x02: Illegal Data Address") {
		t.Errorf("failed preview error = %#v", failedPreview.Error)
	}
	if failedPreview.Error.Details.Protocol != "modbus_tcp" || failedPreview.Error.Details.ExceptionCode != 2 || failedPreview.Error.Details.ExceptionName != "ILLEGAL_DATA_ADDRESS" || failedPreview.Error.Details.StartAddress != 99 || failedPreview.Error.Details.Quantity != 2 {
		t.Errorf("failed preview details = %#v", failedPreview.Error.Details)
	}

	var createdDatasource device.Datasource
	requestJSON(t, app, http.MethodPost, "/api/devices/"+createdDevice.ID.String()+"/datasources", `{"name":" Voltage ","type":"modbus_read","config":{"function_code":3,"start_address":10,"quantity":2}}`, fiber.StatusCreated, &createdDatasource)
	if createdDatasource.ID == uuid.Nil || createdDatasource.Name != "Voltage" {
		t.Errorf("created datasource = %#v", createdDatasource)
	}
	if err := db.Exec(`INSERT INTO tags (name,type,data_type,datasource_id,config) VALUES (?,?,?,?,CAST(? AS jsonb))`, "Meter voltage", "reading", "uint16", createdDatasource.ID, `{"decoder":{"type":"binary_numeric","config":{"byte_offset":0,"byte_order":"big_endian"}}}`).Error; err != nil {
		t.Fatalf("inserting reading tag: %v", err)
	}
	var inventory struct {
		Data       []device.DeviceInventoryItem `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	inventoryPath := fmt.Sprintf("/api/devices?vgateway_id=%s&type=modbus_device&enabled=true&search=simulator&page=1&per_page=20", gatewayID)
	requestJSON(t, app, http.MethodGet, inventoryPath, "", fiber.StatusOK, &inventory)
	if len(inventory.Data) != 1 || inventory.Data[0].ID != createdDevice.ID || inventory.Data[0].VGatewayName != "Simulator" || inventory.Data[0].VGatewayType != "modbus_tcp" || !inventory.Data[0].VGatewayEnabled || inventory.Data[0].DatasourceCount != 1 || inventory.Data[0].TagCount != 1 {
		t.Errorf("device inventory = %#v", inventory.Data)
	}
	if inventory.Pagination.Page != 1 || inventory.Pagination.PerPage != 20 || inventory.Pagination.Total != 1 || inventory.Pagination.TotalPages != 1 {
		t.Errorf("device inventory pagination = %#v", inventory.Pagination)
	}
	var escapedSearch struct {
		Data       []device.DeviceInventoryItem `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	requestJSON(t, app, http.MethodGet, "/api/devices?search=%25", "", fiber.StatusOK, &escapedSearch)
	if len(escapedSearch.Data) != 0 || escapedSearch.Pagination.Total != 0 {
		t.Errorf("escaped wildcard search = %#v", escapedSearch)
	}
	var invalidQuery map[string]any
	requestJSON(t, app, http.MethodGet, "/api/devices?enabled=maybe", "", fiber.StatusBadRequest, &invalidQuery)
	requestJSON(t, app, http.MethodGet, "/api/devices?type=mqtt_device", "", fiber.StatusBadRequest, &invalidQuery)
	requestJSON(t, app, http.MethodGet, "/api/devices/"+createdDevice.ID.String(), "", fiber.StatusOK, &fetchedDevice)
	if fetchedDevice.DatasourceCount != 1 {
		t.Errorf("device datasource count = %d, want 1", fetchedDevice.DatasourceCount)
	}
	var fetchedDatasource device.DatasourceView
	requestJSON(t, app, http.MethodGet, "/api/datasources/"+createdDatasource.ID.String(), "", fiber.StatusOK, &fetchedDatasource)
	if fetchedDatasource.ID != createdDatasource.ID || fetchedDatasource.Status != "idle" {
		t.Errorf("fetched datasource = %#v", fetchedDatasource)
	}
	var updatedDatasource device.DatasourceView
	requestJSON(t, app, http.MethodPut, "/api/datasources/"+createdDatasource.ID.String(), `{"name":"Voltage updated","description":"Line voltage","enabled":true,"config":{"function_code":3,"start_address":10,"quantity":2,"poll_interval_ms":100}}`, fiber.StatusOK, &updatedDatasource)
	if updatedDatasource.Name != "Voltage updated" || updatedDatasource.Description == nil || *updatedDatasource.Description != "Line voltage" || updatedDatasource.Status != "idle" {
		t.Errorf("updated datasource = %#v", updatedDatasource)
	}

	var saved device.DatasourceSample
	requestJSON(t, app, http.MethodPost, "/api/datasources/"+createdDatasource.ID.String()+"/preview", "", fiber.StatusOK, &saved)
	assertIntegrationSample(t, saved, createdDatasource.ID)
	server.assertRequests(t, 3)

	var listed struct {
		Data []device.DatasourceView `json:"data"`
	}
	requestJSON(t, app, http.MethodGet, "/api/devices/"+createdDevice.ID.String()+"/datasources", "", fiber.StatusOK, &listed)
	if len(listed.Data) != 1 || listed.Data[0].ID != createdDatasource.ID || listed.Data[0].Name != "Voltage updated" || listed.Data[0].Status != "idle" {
		t.Errorf("listed datasources = %#v", listed.Data)
	}

	requestEmpty(t, app, http.MethodDelete, "/api/devices/"+createdDevice.ID.String(), fiber.StatusNoContent)
	counts, err = countDevices(context.Background(), []uuid.UUID{gatewayID})
	if err != nil {
		t.Fatalf("CountByVGatewayIDs() after delete error = %v", err)
	}
	if counts[gatewayID] != 0 {
		t.Errorf("device count after delete = %d, want 0", counts[gatewayID])
	}
	response := request(t, app, http.MethodGet, "/api/datasources/"+createdDatasource.ID.String(), "")
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusNotFound {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("GET deleted datasource status = %d; body=%s", response.StatusCode, body)
	}
}

func assertIntegrationSample(t *testing.T, sample device.DatasourceSample, datasourceID uuid.UUID) {
	t.Helper()
	if sample.DatasourceID != datasourceID || sample.Quality != "good" || sample.RawHex != "002A1234" || sample.Sequence == 0 {
		t.Errorf("sample = %#v", sample)
	}
	var data struct {
		Registers []struct {
			Address int    `json:"address"`
			Value   int    `json:"value"`
			Hex     string `json:"hex"`
		} `json:"registers"`
	}
	if err := json.Unmarshal(sample.Data, &data); err != nil {
		t.Fatalf("decoding sample data: %v", err)
	}
	if len(data.Registers) != 2 || data.Registers[0].Address != 10 || data.Registers[0].Value != 42 || data.Registers[1].Hex != "1234" {
		t.Errorf("registers = %#v", data.Registers)
	}
}

func newDeviceIntegrationApp(t *testing.T, host string, port int) (*fiber.App, uuid.UUID, func(context.Context, []uuid.UUID) (map[uuid.UUID]int64, error), *gorm.DB) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run device HTTP integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "device_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	for _, migrationPath := range []string{"../../../migrations/000002_create_vgateways.up.sql", "../../../migrations/000003_create_devices_datasources.up.sql", "../../../migrations/000004_create_tags.up.sql"} {
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("reading migration: %v", err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration: %v", err)
		}
	}
	gatewayID := uuid.New()
	gatewayConfig := fmt.Sprintf(`{"host":%q,"port":%d,"timeout":1000,"retry_count":0,"retry_delay":0,"keep_alive":false,"reconnect_interval":0}`, host, port)
	if err := db.Exec(`INSERT INTO vgateways (id,name,type,config) VALUES (?,?,?,?)`, gatewayID, "Simulator", "modbus_tcp", gatewayConfig).Error; err != nil {
		t.Fatalf("inserting gateway: %v", err)
	}
	driver := modbus.NewDefaultModbusTCPDriver()
	repository := devicepostgres.NewRepository(db)
	service, err := device.NewService(repository, driver)
	if err != nil {
		t.Fatalf("creating service: %v", err)
	}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	t.Cleanup(func() { _ = app.Shutdown() })
	return app, gatewayID, repository.CountByVGatewayIDs, db
}

type modbusReadServer struct {
	listener net.Listener
	mu       sync.Mutex
	requests int
	errors   chan error
}

func newModbusReadServer(t *testing.T) *modbusReadServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	server := &modbusReadServer{listener: listener, errors: make(chan error, 8)}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}
func (s *modbusReadServer) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.errors <- err
			}
			return
		}
		go s.handle(connection)
	}
}
func (s *modbusReadServer) handle(connection net.Conn) {
	defer connection.Close()
	request := make([]byte, 12)
	if _, err := io.ReadFull(connection, request); err != nil {
		s.errors <- err
		return
	}
	address := binary.BigEndian.Uint16(request[8:10])
	quantity := binary.BigEndian.Uint16(request[10:12])
	if request[6] != 7 || request[7] != 3 {
		s.errors <- fmt.Errorf("unexpected request %x", request)
		return
	}
	if address == 99 && quantity == 2 {
		response := make([]byte, 9)
		copy(response[:2], request[:2])
		binary.BigEndian.PutUint16(response[4:6], 3)
		response[6] = request[6]
		response[7] = request[7] | 0x80
		response[8] = 2
		if _, err := connection.Write(response); err != nil {
			s.errors <- err
			return
		}
		s.mu.Lock()
		s.requests++
		s.mu.Unlock()
		return
	}
	if address != 10 || quantity != 2 {
		s.errors <- fmt.Errorf("unexpected request %x", request)
		return
	}
	response := make([]byte, 13)
	copy(response[:2], request[:2])
	binary.BigEndian.PutUint16(response[4:6], 7)
	response[6] = request[6]
	response[7] = request[7]
	response[8] = 4
	copy(response[9:], []byte{0, 42, 0x12, 0x34})
	if _, err := connection.Write(response); err != nil {
		s.errors <- err
		return
	}
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
}
func (s *modbusReadServer) assertRequests(t *testing.T, want int) {
	t.Helper()
	s.mu.Lock()
	got := s.requests
	s.mu.Unlock()
	if got != want {
		t.Errorf("simulator requests = %d, want %d", got, want)
	}
	select {
	case err := <-s.errors:
		t.Errorf("simulator error: %v", err)
	default:
	}
}

func requestJSON(t *testing.T, app *fiber.App, method, path, body string, status int, destination any) {
	t.Helper()
	response := request(t, app, method, path, body)
	defer response.Body.Close()
	if response.StatusCode != status {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, status, payload)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}
func requestEmpty(t *testing.T, app *fiber.App, method, path string, status int) {
	t.Helper()
	response := request(t, app, method, path, "")
	defer response.Body.Close()
	if response.StatusCode != status {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, status, payload)
	}
}
func request(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	return response
}
