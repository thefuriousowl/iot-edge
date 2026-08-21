package vgatewayhttp

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
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
	vgatewaypostgres "github.com/thefuriousowl/iot-edge/internal/vgateway/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVGatewayCRUD_EndToEnd(t *testing.T) {
	app := newVGatewayIntegrationApp(t)

	createBody := `{
		"name":" Main PLC Gateway ",
		"type":"modbus_tcp",
		"description":"Factory floor",
		"enabled":true,
		"config":{"host":"192.0.2.10"}
	}`
	var created integrationGatewayResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodPost,
		"/api/vgateways",
		createBody,
		fiber.StatusCreated,
		&created,
	)

	if created.ID == uuid.Nil {
		t.Fatal("created gateway ID is empty")
	}
	if created.Name != "Main PLC Gateway" || created.Type != vgateway.VGatewayTypeModbusTCP {
		t.Errorf("created gateway identity = (%q, %q)", created.Name, created.Type)
	}
	if created.Description == nil || *created.Description != "Factory floor" {
		t.Errorf("created description = %v, want Factory floor", created.Description)
	}
	if !created.Enabled || created.Status != vgateway.VGatewayStatusDisconnected {
		t.Errorf("created state = (enabled %t, status %q)", created.Enabled, created.Status)
	}
	assertIntegrationModbusConfig(t, created.Config, modbus.ModbusTCPConfig{
		Host:              "192.0.2.10",
		Port:              modbus.DefaultModbusTCPPort,
		Timeout:           modbus.DefaultModbusTCPTimeout,
		RetryCount:        modbus.DefaultModbusTCPRetryCount,
		RetryDelay:        modbus.DefaultModbusTCPRetryDelay,
		KeepAlive:         true,
		ReconnectInterval: modbus.DefaultModbusTCPReconnectInterval,
	})
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("created timestamps = (%v, %v), want database timestamps", created.CreatedAt, created.UpdatedAt)
	}

	var listed struct {
		Data []struct {
			ID          uuid.UUID                         `json:"id"`
			Name        string                            `json:"name"`
			Description *string                           `json:"description"`
			Enabled     bool                              `json:"enabled"`
			Status      vgateway.VGatewayConnectionStatus `json:"status"`
			Config      json.RawMessage                   `json:"config"`
		} `json:"data"`
		Pagination struct {
			Page       int   `json:"page"`
			PerPage    int   `json:"per_page"`
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways?type=modbus_tcp&enabled=true&page=1&per_page=10",
		"",
		fiber.StatusOK,
		&listed,
	)
	if len(listed.Data) != 1 || listed.Data[0].ID != created.ID {
		t.Fatalf("listed gateways = %#v, want created gateway", listed.Data)
	}
	if listed.Data[0].Name != created.Name || listed.Data[0].Status != vgateway.VGatewayStatusDisconnected {
		t.Errorf("listed gateway = %#v, want created identity and disconnected status", listed.Data[0])
	}
	if listed.Data[0].Config != nil {
		t.Errorf("list response config = %s, want omitted", listed.Data[0].Config)
	}
	if listed.Pagination.Page != 1 || listed.Pagination.PerPage != 10 || listed.Pagination.Total != 1 || listed.Pagination.TotalPages != 1 {
		t.Errorf("pagination = %#v, want one result on page one", listed.Pagination)
	}

	var fetched integrationGatewayResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways/"+created.ID.String(),
		"",
		fiber.StatusOK,
		&fetched,
	)
	if fetched.ID != created.ID || fetched.Name != created.Name || fetched.Config.Host != "192.0.2.10" {
		t.Errorf("fetched gateway = %#v, want persisted created gateway", fetched)
	}

	updateBody := `{
		"name":"Backup PLC Gateway",
		"description":null,
		"enabled":false,
		"config":{
			"host":"127.0.0.1",
			"port":1502,
			"timeout":750,
			"retry_count":0,
			"retry_delay":0,
			"keep_alive":false,
			"reconnect_interval":0
		}
	}`
	var updated integrationGatewayResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodPut,
		"/api/vgateways/"+created.ID.String(),
		updateBody,
		fiber.StatusOK,
		&updated,
	)
	if updated.ID != created.ID || updated.Name != "Backup PLC Gateway" {
		t.Errorf("updated gateway identity = (%s, %q)", updated.ID, updated.Name)
	}
	if updated.Description != nil || updated.Enabled || updated.Status != vgateway.VGatewayStatusStopped {
		t.Errorf("updated state = (description %v, enabled %t, status %q)", updated.Description, updated.Enabled, updated.Status)
	}
	assertIntegrationModbusConfig(t, updated.Config, modbus.ModbusTCPConfig{
		Host:              "127.0.0.1",
		Port:              1502,
		Timeout:           750,
		RetryCount:        0,
		RetryDelay:        0,
		KeepAlive:         false,
		ReconnectInterval: 0,
	})

	var persisted integrationGatewayResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways/"+created.ID.String(),
		"",
		fiber.StatusOK,
		&persisted,
	)
	if persisted.Name != updated.Name || persisted.Description != nil || persisted.Enabled {
		t.Errorf("persisted update = %#v, want updated fields", persisted)
	}
	assertIntegrationModbusConfig(t, persisted.Config, updated.Config)

	integrationEmptyRequest(
		t,
		app,
		http.MethodDelete,
		"/api/vgateways/"+created.ID.String(),
		fiber.StatusNoContent,
	)

	response := performRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways/"+created.ID.String(),
		"",
	)
	defer response.Body.Close()
	assertAPIError(t, response, fiber.StatusNotFound, "NOT_FOUND")
}

func TestVGatewayModbusTCP_SimulatedDeviceEndToEnd(t *testing.T) {
	app := newVGatewayIntegrationApp(t)
	server := newSimulatedModbusTCPServer(t)

	host, portText, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting simulated Modbus address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing simulated Modbus port: %v", err)
	}

	configBody := fmt.Sprintf(`{
		"host":%q,
		"port":%d,
		"timeout":1000,
		"retry_count":0,
		"retry_delay":0,
		"keep_alive":false,
		"reconnect_interval":0
	}`, host, port)
	preSaveTestBody := fmt.Sprintf(`{
		"type":"modbus_tcp",
		"config":%s,
		"options":{"unit_id":7}
	}`, configBody)
	var connectionTest struct {
		Success   bool    `json:"success"`
		LatencyMS float64 `json:"latency_ms"`
		Message   string  `json:"message"`
	}
	integrationJSONRequest(
		t,
		app,
		http.MethodPost,
		"/api/vgateways/test",
		preSaveTestBody,
		fiber.StatusOK,
		&connectionTest,
	)
	if !connectionTest.Success || connectionTest.LatencyMS < 0 || connectionTest.Message != "Connection successful" {
		t.Errorf("connection test response = %#v", connectionTest)
	}
	server.waitForAcceptedConnection(t)
	server.waitForProbe(t)

	var emptyList struct {
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways",
		"",
		fiber.StatusOK,
		&emptyList,
	)
	if emptyList.Pagination.Total != 0 {
		t.Errorf("gateway total after pre-save test = %d, want 0", emptyList.Pagination.Total)
	}

	createBody := fmt.Sprintf(`{
		"name":"Simulated Modbus Gateway",
		"type":"modbus_tcp",
		"enabled":true,
		"config":%s
	}`, configBody)
	var created integrationGatewayResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodPost,
		"/api/vgateways",
		createBody,
		fiber.StatusCreated,
		&created,
	)

	var connected struct {
		Message string                            `json:"message"`
		Status  vgateway.VGatewayConnectionStatus `json:"status"`
	}
	integrationJSONRequest(
		t,
		app,
		http.MethodPost,
		"/api/vgateways/"+created.ID.String()+"/connect",
		"",
		fiber.StatusOK,
		&connected,
	)
	if connected.Status != vgateway.VGatewayStatusConnected || connected.Message != "Connected successfully" {
		t.Errorf("connect response = %#v", connected)
	}
	server.waitForAcceptedConnection(t)

	var connectedStatus integrationStatusResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways/"+created.ID.String()+"/status",
		"",
		fiber.StatusOK,
		&connectedStatus,
	)
	if connectedStatus.ID != created.ID || connectedStatus.Status != vgateway.VGatewayStatusConnected || connectedStatus.ConnectedAt == nil {
		t.Errorf("connected status = %#v", connectedStatus)
	}
	assertEmptyIntegrationStatistics(t, connectedStatus)

	var disconnected struct {
		Message string                            `json:"message"`
		Status  vgateway.VGatewayConnectionStatus `json:"status"`
	}
	integrationJSONRequest(
		t,
		app,
		http.MethodPost,
		"/api/vgateways/"+created.ID.String()+"/disconnect",
		"",
		fiber.StatusOK,
		&disconnected,
	)
	if disconnected.Status != vgateway.VGatewayStatusDisconnected || disconnected.Message != "Disconnected successfully" {
		t.Errorf("disconnect response = %#v", disconnected)
	}

	var disconnectedStatus integrationStatusResponse
	integrationJSONRequest(
		t,
		app,
		http.MethodGet,
		"/api/vgateways/"+created.ID.String()+"/status",
		"",
		fiber.StatusOK,
		&disconnectedStatus,
	)
	if disconnectedStatus.Status != vgateway.VGatewayStatusDisconnected || disconnectedStatus.ConnectedAt != nil {
		t.Errorf("disconnected status = %#v", disconnectedStatus)
	}
	assertEmptyIntegrationStatistics(t, disconnectedStatus)
}

type integrationGatewayResponse struct {
	ID          uuid.UUID                         `json:"id"`
	Name        string                            `json:"name"`
	Type        vgateway.VGatewayType             `json:"type"`
	Description *string                           `json:"description"`
	Enabled     bool                              `json:"enabled"`
	Config      modbus.ModbusTCPConfig            `json:"config"`
	Status      vgateway.VGatewayConnectionStatus `json:"status"`
	CreatedAt   time.Time                         `json:"created_at"`
	UpdatedAt   time.Time                         `json:"updated_at"`
}

type integrationStatusResponse struct {
	ID          uuid.UUID                         `json:"id"`
	Status      vgateway.VGatewayConnectionStatus `json:"status"`
	ConnectedAt *time.Time                        `json:"connected_at"`
	Statistics  vgateway.VGatewayStatusStatistics `json:"statistics"`
	Health      vgateway.VGatewayHealth           `json:"health"`
}

func newVGatewayIntegrationApp(t *testing.T) *fiber.App {
	t.Helper()

	repository := newVGatewayIntegrationRepository(t)
	service, err := vgateway.NewVGatewayService(
		repository,
		vgateway.GatewayDriverRegistry{
			vgateway.VGatewayTypeModbusTCP: modbus.NewDefaultModbusTCPDriver(),
		},
	)
	if err != nil {
		t.Fatalf("creating vGateway service: %v", err)
	}

	app := testApp(service)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shutting down Fiber app: %v", err)
		}
	})
	return app
}

func newVGatewayIntegrationRepository(t *testing.T) vgateway.VGatewayRepository {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the vGateway HTTP integration tests")
	}

	ctx := context.Background()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Close(); err != nil {
			t.Errorf("closing PostgreSQL connection: %v", err)
		}
	})

	schemaName := "vgateway_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated test schema: %v", err)
		}
	})

	testDatabaseURL, err := integrationDatabaseURLWithSearchPath(databaseURL, schemaName)
	if err != nil {
		t.Fatalf("adding test schema to database URL: %v", err)
	}
	db, err := gorm.Open(gormpostgres.Open(testDatabaseURL), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening isolated GORM connection: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting isolated SQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing isolated GORM connection: %v", err)
		}
	})

	migration, err := os.ReadFile("../../../migrations/000002_create_vgateways.up.sql")
	if err != nil {
		t.Fatalf("reading vGateway migration: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying vGateway migration: %v", err)
	}

	return vgatewaypostgres.NewVGatewayRepository(db)
}

func integrationDatabaseURLWithSearchPath(databaseURL, schemaName string) (string, error) {
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}

	query := parsedURL.Query()
	query.Set("search_path", schemaName)
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}

func integrationJSONRequest(
	t *testing.T,
	app *fiber.App,
	method string,
	path string,
	body string,
	wantStatus int,
	destination any,
) {
	t.Helper()

	response := performRequest(t, app, method, path, body)
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status = %d, want %d; body = %s", method, path, response.StatusCode, wantStatus, responseBody)
	}
	decodeResponse(t, response, destination)
}

func integrationEmptyRequest(
	t *testing.T,
	app *fiber.App,
	method string,
	path string,
	wantStatus int,
) {
	t.Helper()

	response := performRequest(t, app, method, path, "")
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status = %d, want %d; body = %s", method, path, response.StatusCode, wantStatus, responseBody)
	}
}

func assertIntegrationModbusConfig(t *testing.T, got, want modbus.ModbusTCPConfig) {
	t.Helper()
	if got != want {
		t.Errorf("Modbus config = %#v, want %#v", got, want)
	}
}

func assertEmptyIntegrationStatistics(t *testing.T, status integrationStatusResponse) {
	t.Helper()
	statistics := status.Statistics
	if statistics.RequestCount != 0 || statistics.ErrorCount != 0 || statistics.BytesReceived != 0 || statistics.AvgLatencyMS != nil {
		t.Errorf("status statistics = %#v, want zero values", statistics)
	}
	if status.Health.Status != vgateway.VGatewayHealthUnknown || status.Health.LastCheck != nil || status.Health.LatencyMS != nil {
		t.Errorf("status health = %#v, want unknown without measurements", status.Health)
	}
}

type simulatedModbusTCPServer struct {
	listener net.Listener

	mu          sync.Mutex
	connections map[net.Conn]struct{}
	waitGroup   sync.WaitGroup
	accepted    chan struct{}
	probeResult chan error
	errors      chan error
}

func newSimulatedModbusTCPServer(t *testing.T) *simulatedModbusTCPServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("starting simulated Modbus TCP server: %v", err)
	}

	server := &simulatedModbusTCPServer{
		listener:    listener,
		connections: make(map[net.Conn]struct{}),
		accepted:    make(chan struct{}, 4),
		probeResult: make(chan error, 4),
		errors:      make(chan error, 4),
	}
	server.waitGroup.Add(1)
	go server.acceptConnections()
	t.Cleanup(func() {
		server.close()
		server.assertNoErrors(t)
	})
	return server
}

func (s *simulatedModbusTCPServer) acceptConnections() {
	defer s.waitGroup.Done()

	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.reportError(fmt.Errorf("accept connection: %w", err))
			return
		}

		s.mu.Lock()
		s.connections[connection] = struct{}{}
		s.mu.Unlock()
		s.accepted <- struct{}{}

		s.waitGroup.Add(1)
		go s.serveConnection(connection)
	}
}

func (s *simulatedModbusTCPServer) serveConnection(connection net.Conn) {
	defer s.waitGroup.Done()
	defer func() {
		s.mu.Lock()
		delete(s.connections, connection)
		s.mu.Unlock()
		_ = connection.Close()
	}()

	request := make([]byte, 12)
	if _, err := io.ReadFull(connection, request); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return
		}
		s.reportError(fmt.Errorf("read Modbus request: %w", err))
		return
	}

	if err := validateSimulatedModbusRequest(request); err != nil {
		s.probeResult <- err
		return
	}

	responseData := []byte{0x00, 0x2a}
	response := make([]byte, 9+len(responseData))
	copy(response[0:2], request[0:2])
	binary.BigEndian.PutUint16(response[4:6], uint16(3+len(responseData)))
	response[6] = request[6]
	response[7] = request[7]
	response[8] = byte(len(responseData))
	copy(response[9:], responseData)

	if _, err := connection.Write(response); err != nil {
		s.probeResult <- fmt.Errorf("write Modbus response: %w", err)
		return
	}
	s.probeResult <- nil
}

func validateSimulatedModbusRequest(request []byte) error {
	if got := binary.BigEndian.Uint16(request[2:4]); got != 0 {
		return fmt.Errorf("protocol ID = %d, want 0", got)
	}
	if got := binary.BigEndian.Uint16(request[4:6]); got != 6 {
		return fmt.Errorf("request length = %d, want 6", got)
	}
	if request[6] != 7 {
		return fmt.Errorf("unit ID = %d, want 7", request[6])
	}
	if request[7] != byte(modbus.FunctionReadHoldingRegisters) {
		return fmt.Errorf("function code = %d, want %d", request[7], modbus.FunctionReadHoldingRegisters)
	}
	if got := binary.BigEndian.Uint16(request[8:10]); got != 0 {
		return fmt.Errorf("address = %d, want 0", got)
	}
	if got := binary.BigEndian.Uint16(request[10:12]); got != 1 {
		return fmt.Errorf("quantity = %d, want 1", got)
	}
	return nil
}

func (s *simulatedModbusTCPServer) waitForAcceptedConnection(t *testing.T) {
	t.Helper()
	select {
	case <-s.accepted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for simulated Modbus connection")
	}
}

func (s *simulatedModbusTCPServer) waitForProbe(t *testing.T) {
	t.Helper()
	select {
	case err := <-s.probeResult:
		if err != nil {
			t.Fatalf("simulated Modbus probe: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for simulated Modbus probe")
	}
}

func (s *simulatedModbusTCPServer) close() {
	_ = s.listener.Close()

	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}

	s.waitGroup.Wait()
}

func (s *simulatedModbusTCPServer) reportError(err error) {
	select {
	case s.errors <- err:
	default:
	}
}

func (s *simulatedModbusTCPServer) assertNoErrors(t *testing.T) {
	t.Helper()
	for {
		select {
		case err := <-s.errors:
			t.Errorf("simulated Modbus server: %v", err)
		default:
			return
		}
	}
}
