package vgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
)

func TestVGatewayServiceTestConnectionUsesTemporaryClientAndOptionalProbe(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "test-1")
	gateway := existingVGateway()
	temporaryClient := &stubModbusClient{}
	factoryCalls := 0
	startedAt := time.Date(2026, time.August, 21, 16, 0, 0, 0, time.UTC)
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*VGateway, error) {
			if gotCtx.Value(requestKey) != "test-1" {
				t.Fatalf("FindByID() context value = %v, want test-1", gotCtx.Value(requestKey))
			}
			if id != gateway.ID {
				t.Fatalf("FindByID() ID = %v, want %v", id, gateway.ID)
			}
			return gateway, nil
		},
	}, func(config modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		if config != decodeModbusConfig(t, gateway.Config) {
			t.Fatalf("temporary client config = %#v", config)
		}
		return temporaryClient, nil
	})
	service.now = clockSequence(
		t,
		startedAt,
		startedAt.Add(15*time.Millisecond),
	)

	result, err := service.TestConnection(
		ctx,
		gateway.ID,
		json.RawMessage(`{"unit_id":7}`),
	)
	if err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if result == nil || !result.Success || result.Error != nil {
		t.Fatalf("TestConnection() result = %#v, want success", result)
	}
	if result.Latency != 15*time.Millisecond {
		t.Fatalf("latency = %v, want 15ms", result.Latency)
	}
	if factoryCalls != 1 || temporaryClient.connectCallCount() != 1 || temporaryClient.disconnectCallCount() != 1 {
		t.Fatalf("factory/connect/disconnect calls = (%d, %d, %d), want (1, 1, 1)", factoryCalls, temporaryClient.connectCallCount(), temporaryClient.disconnectCallCount())
	}
	readCalls, request := temporaryClient.readCallState()
	if readCalls != 1 {
		t.Fatalf("Read() calls = %d, want 1", readCalls)
	}
	wantRequest := modbus.ReadRequest{
		UnitID:       7,
		FunctionCode: modbus.FunctionReadHoldingRegisters,
		Address:      0,
		Quantity:     1,
	}
	if request != wantRequest {
		t.Fatalf("Read() request = %#v, want %#v", request, wantRequest)
	}
	if temporaryClient.IsConnected() {
		t.Fatal("temporary client remains connected after test")
	}
	if len(service.runtimes) != 0 {
		t.Fatalf("runtime registry length = %d, want no persistent side effects", len(service.runtimes))
	}
}

func TestVGatewayServiceTestConnectionAllowsDisabledGateway(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	gateway.Enabled = false
	client := &stubModbusClient{}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return client, nil
	})
	startedAt := time.Now()
	service.now = clockSequence(t, startedAt, startedAt)

	result, err := service.TestConnection(context.Background(), gateway.ID, nil)
	if err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("TestConnection() result = %#v, want success", result)
	}
	if client.connectCallCount() != 1 || client.disconnectCallCount() != 1 {
		t.Fatalf("temporary lifecycle calls = (%d, %d), want (1, 1)", client.connectCallCount(), client.disconnectCallCount())
	}
}

func TestVGatewayServiceTestConnectionDoesNotMutateActiveRuntime(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	persistentClient := &stubModbusClient{connected: true}
	connectedAt := gateway.CreatedAt.Add(time.Minute)
	runtime := &vGatewayRuntime{
		client:       persistentClient,
		status:       VGatewayStatusConnected,
		connectedAt:  &connectedAt,
		requestCount: 8,
	}
	temporaryClient := &stubModbusClient{}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return temporaryClient, nil
	})
	service.runtimes[gateway.ID] = runtime
	startedAt := time.Now()
	service.now = clockSequence(t, startedAt, startedAt.Add(time.Millisecond))

	result, err := service.TestConnection(context.Background(), gateway.ID, nil)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("TestConnection() = (%#v, %v), want success", result, err)
	}
	if service.runtimes[gateway.ID] != runtime ||
		persistentClient.connectCallCount() != 0 ||
		persistentClient.disconnectCallCount() != 0 {
		t.Fatal("connection test touched persistent runtime client")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.status != VGatewayStatusConnected ||
		runtime.connectedAt != &connectedAt ||
		runtime.requestCount != 8 {
		t.Fatal("connection test mutated persistent runtime state")
	}
}

func TestVGatewayServiceTestConnectionRejectsInvalidOptionsBeforeNetwork(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return &stubModbusClient{}, nil
	})

	result, err := service.TestConnection(
		context.Background(),
		gateway.ID,
		json.RawMessage(`{"unit_id":256}`),
	)
	if result != nil {
		t.Fatalf("TestConnection() result = %#v, want nil", result)
	}
	if !errors.Is(err, ErrInvalidVGatewayTestInput) ||
		!errors.Is(err, protocol.ErrInvalidGatewayTestOptions) {
		t.Fatalf("TestConnection() error = %v, want mapped invalid options", err)
	}
	if factoryCalls != 0 || len(service.runtimes) != 0 {
		t.Fatalf("invalid test side effects = factory %d runtimes %d, want zero", factoryCalls, len(service.runtimes))
	}
}

func TestVGatewayServiceTestConnectionMapsLookupTypeAndProbeErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	prepareErr := errors.New("probe unavailable")
	tests := []struct {
		name      string
		gateway   *VGateway
		findError error
		driver    protocol.GatewayDriver
		wantError error
		wantText  string
	}{
		{
			name:      "not found",
			findError: ErrVGatewayNotFound,
			wantError: ErrVGatewayNotFound,
		},
		{
			name:      "unexpected repository error",
			findError: databaseErr,
			wantError: databaseErr,
			wantText:  "find vGateway for connection test",
		},
		{
			name: "unregistered protocol",
			gateway: func() *VGateway {
				gateway := existingVGateway()
				gateway.Type = VGatewayType("mqtt")
				return gateway
			}(),
			wantError: ErrUnsupportedVGatewayType,
		},
		{
			name:    "driver preparation error",
			gateway: existingVGateway(),
			driver: &stubGatewayDriver{
				prepareConnectionTestFunc: func(json.RawMessage) (protocol.ConnectionProbe, error) {
					return nil, prepareErr
				},
			},
			wantError: prepareErr,
			wantText:  "prepare modbus_tcp vGateway connection test",
		},
		{
			name:    "nil driver probe",
			gateway: existingVGateway(),
			driver: &stubGatewayDriver{
				prepareConnectionTestFunc: func(json.RawMessage) (protocol.ConnectionProbe, error) {
					return nil, nil
				},
			},
			wantText: "probe is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			driver := tt.driver
			if driver == nil {
				modbusDriver, err := modbus.NewModbusTCPDriver(
					func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
						return &stubModbusClient{}, nil
					},
				)
				if err != nil {
					t.Fatalf("NewModbusTCPDriver() error = %v", err)
				}
				driver = modbusDriver
			}
			service, err := NewVGatewayService(
				&stubVGatewayRepository{
					findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
						return tt.gateway, tt.findError
					},
				},
				GatewayDriverRegistry{
					VGatewayTypeModbusTCP: driver,
				},
			)
			if err != nil {
				t.Fatalf("NewVGatewayService() error = %v", err)
			}

			result, err := service.TestConnection(
				context.Background(),
				uuid.New(),
				nil,
			)
			if result != nil {
				t.Fatalf("TestConnection() result = %#v, want nil", result)
			}
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Fatalf("TestConnection() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && (err == nil || !strings.Contains(err.Error(), tt.wantText)) {
				t.Fatalf("TestConnection() error = %v, want detail %q", err, tt.wantText)
			}
		})
	}
}

func TestVGatewayServiceTestConnectionReturnsOperationalFailuresAsResults(t *testing.T) {
	t.Parallel()

	factoryErr := errors.New("factory failed")
	connectErr := errors.New("connection refused")
	readErr := errors.New("Modbus exception")
	disconnectErr := errors.New("close failed")
	tests := []struct {
		name            string
		factoryError    error
		client          *stubModbusClient
		options         json.RawMessage
		wantErrors      []error
		wantConnects    int
		wantDisconnects int
		wantReads       int
	}{
		{
			name:         "client factory failure",
			factoryError: factoryErr,
			wantErrors:   []error{factoryErr},
		},
		{
			name:       "nil factory client",
			wantErrors: []error{protocol.ErrGatewayClientRequired},
		},
		{
			name: "connect failure still cleans up",
			client: &stubModbusClient{
				connectErr: connectErr,
			},
			wantErrors:      []error{connectErr},
			wantConnects:    1,
			wantDisconnects: 1,
		},
		{
			name: "probe and cleanup failures are joined",
			client: &stubModbusClient{
				readErr:       readErr,
				disconnectErr: disconnectErr,
			},
			options:         json.RawMessage(`{"unit_id":1}`),
			wantErrors:      []error{readErr, disconnectErr},
			wantConnects:    1,
			wantDisconnects: 1,
			wantReads:       1,
		},
		{
			name: "cleanup failure changes successful probe to failure",
			client: &stubModbusClient{
				disconnectErr: disconnectErr,
			},
			wantErrors:      []error{disconnectErr},
			wantConnects:    1,
			wantDisconnects: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gateway := existingVGateway()
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return gateway, nil
				},
			}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
				return tt.client, tt.factoryError
			})
			startedAt := time.Now()
			service.now = clockSequence(
				t,
				startedAt,
				startedAt.Add(5*time.Millisecond),
			)

			result, err := service.TestConnection(
				context.Background(),
				gateway.ID,
				tt.options,
			)
			if err != nil {
				t.Fatalf("TestConnection() outer error = %v", err)
			}
			if result == nil || result.Success || result.Error == nil {
				t.Fatalf("TestConnection() result = %#v, want operational failure", result)
			}
			if result.Latency != 5*time.Millisecond {
				t.Fatalf("latency = %v, want 5ms", result.Latency)
			}
			for _, wantError := range tt.wantErrors {
				if !errors.Is(result.Error, wantError) {
					t.Fatalf("result error = %v, want wrapped %v", result.Error, wantError)
				}
			}
			if tt.client == nil {
				return
			}
			readCalls, _ := tt.client.readCallState()
			if tt.client.connectCallCount() != tt.wantConnects ||
				tt.client.disconnectCallCount() != tt.wantDisconnects ||
				readCalls != tt.wantReads {
				t.Fatalf(
					"temporary lifecycle calls = connect %d disconnect %d read %d, want %d/%d/%d",
					tt.client.connectCallCount(),
					tt.client.disconnectCallCount(),
					readCalls,
					tt.wantConnects,
					tt.wantDisconnects,
					tt.wantReads,
				)
			}
		})
	}
}

func TestVGatewayServiceTestConnectionDispatchesProtocolNeutralProbe(t *testing.T) {
	t.Parallel()

	customType := VGatewayType("mqtt")
	gateway := existingVGateway()
	gateway.Type = customType
	client := &stubModbusClient{}
	options := json.RawMessage(`{"topic":"health"}`)
	originalOptions := string(options)
	prepareCalls := 0
	probeCalls := 0
	driver := &stubGatewayDriver{
		newClientFunc: func(json.RawMessage) (protocol.GatewayClient, error) {
			return client, nil
		},
		prepareConnectionTestFunc: func(gotOptions json.RawMessage) (protocol.ConnectionProbe, error) {
			prepareCalls++
			if string(gotOptions) != originalOptions {
				t.Fatalf("test options = %s, want %s", gotOptions, originalOptions)
			}
			gotOptions[0] = '['
			return func(_ context.Context, gotClient protocol.GatewayClient) error {
				probeCalls++
				if gotClient != client || !gotClient.IsConnected() {
					t.Fatal("probe did not receive connected temporary client")
				}
				return nil
			}, nil
		},
	}
	service, err := NewVGatewayService(
		&stubVGatewayRepository{
			findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
				return gateway, nil
			},
		},
		GatewayDriverRegistry{customType: driver},
	)
	if err != nil {
		t.Fatalf("NewVGatewayService() error = %v", err)
	}
	startedAt := time.Now()
	service.now = clockSequence(t, startedAt, startedAt.Add(time.Millisecond))

	result, err := service.TestConnection(
		context.Background(),
		gateway.ID,
		options,
	)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("TestConnection() = (%#v, %v), want success", result, err)
	}
	if prepareCalls != 1 || probeCalls != 1 {
		t.Fatalf("prepare/probe calls = (%d, %d), want (1, 1)", prepareCalls, probeCalls)
	}
	if string(options) != originalOptions {
		t.Fatal("driver mutation escaped into caller-owned test options")
	}
}

func TestVGatewayServiceTestConnectionReturnsCancellationAsTestFailure(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	connectStarted := make(chan struct{}, 1)
	client := &stubModbusClient{
		connectStarted: connectStarted,
		connectRelease: make(chan struct{}),
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return client, nil
	})
	startedAt := time.Now()
	service.now = clockSequence(t, startedAt, startedAt.Add(time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	type testResponse struct {
		result *VGatewayConnectionTestResult
		err    error
	}
	response := make(chan testResponse, 1)
	go func() {
		result, err := service.TestConnection(ctx, gateway.ID, nil)
		response <- testResponse{result: result, err: err}
	}()

	select {
	case <-connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("temporary client Connect() did not start")
	}
	cancel()

	select {
	case got := <-response:
		if got.err != nil {
			t.Fatalf("TestConnection() outer error = %v", got.err)
		}
		if got.result == nil || got.result.Success ||
			!errors.Is(got.result.Error, context.Canceled) {
			t.Fatalf("TestConnection() result = %#v, want cancellation failure", got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TestConnection() did not return after cancellation")
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("temporary client Disconnect() calls = %d, want cleanup 1", client.disconnectCallCount())
	}
}

func TestConnectionTestLatencyClampsClockRegression(t *testing.T) {
	t.Parallel()

	startedAt := time.Now()
	if got := connectionTestLatency(startedAt, startedAt.Add(-time.Second)); got != 0 {
		t.Fatalf("connectionTestLatency() = %v, want 0", got)
	}
}

func clockSequence(
	t *testing.T,
	times ...time.Time,
) func() time.Time {
	t.Helper()
	index := 0
	return func() time.Time {
		if index >= len(times) {
			t.Fatalf("clock called %d times, only %d values provided", index+1, len(times))
		}
		value := times[index]
		index++
		return value
	}
}
