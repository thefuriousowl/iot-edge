package vgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
)

func TestVGatewayServiceConnectCreatesAndConnectsProtocolClient(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "connect-1")
	gateway := existingVGateway()
	client := &stubModbusClient{}
	factoryCalls := 0
	connectedAt := time.Date(2026, time.August, 21, 14, 0, 0, 0, time.UTC)
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*VGateway, error) {
			if gotCtx.Value(requestKey) != "connect-1" {
				t.Fatalf("FindByID() context value = %v, want connect-1", gotCtx.Value(requestKey))
			}
			if id != gateway.ID {
				t.Fatalf("FindByID() ID = %v, want %v", id, gateway.ID)
			}
			return gateway, nil
		},
	}, func(config modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		want := decodeModbusConfig(t, gateway.Config)
		if config != want {
			t.Fatalf("factory config = %#v, want %#v", config, want)
		}
		return client, nil
	})
	service.now = func() time.Time { return connectedAt }

	if err := service.Connect(ctx, gateway.ID); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if factoryCalls != 1 || client.connectCallCount() != 1 {
		t.Fatalf("factory/connect calls = (%d, %d), want (1, 1)", factoryCalls, client.connectCallCount())
	}
	if !client.IsConnected() {
		t.Fatal("client is not connected after Connect()")
	}
	runtime := service.runtimes[gateway.ID]
	if runtime == nil {
		t.Fatal("Connect() did not register runtime")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.client != client || runtime.status != VGatewayStatusConnected {
		t.Fatalf("runtime client/status = (%T, %q), want connected client", runtime.client, runtime.status)
	}
	if runtime.connectedAt == nil || !runtime.connectedAt.Equal(connectedAt) {
		t.Fatalf("connected at = %v, want %v", runtime.connectedAt, connectedAt)
	}
	if runtime.lastError != nil || runtime.errorCount != 0 {
		t.Fatalf("runtime error state = (%v, %d), want clear", runtime.lastError, runtime.errorCount)
	}
}

func TestVGatewayServiceConnectRejectsDisabledGatewayWithoutRuntime(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	gateway.Enabled = false
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return &stubModbusClient{}, nil
	})

	err := service.Connect(context.Background(), gateway.ID)
	if !errors.Is(err, ErrVGatewayDisabled) {
		t.Fatalf("Connect() error = %v, want ErrVGatewayDisabled", err)
	}
	if factoryCalls != 0 || len(service.runtimes) != 0 {
		t.Fatalf("disabled connect side effects = factory %d runtimes %d, want zero", factoryCalls, len(service.runtimes))
	}
}

func TestVGatewayServiceConnectMapsLookupAndTypeErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name      string
		gateway   *VGateway
		findError error
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
			wantText:  "find vGateway for connect",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return tt.gateway, tt.findError
				},
			}, nil)
			err := service.Connect(context.Background(), uuid.New())
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Connect() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Connect() error = %q, want detail %q", err, tt.wantText)
			}
			if len(service.runtimes) != 0 {
				t.Fatalf("runtime registry length = %d, want 0", len(service.runtimes))
			}
		})
	}
}

func TestVGatewayServiceConnectRecordsClientFactoryFailure(t *testing.T) {
	t.Parallel()

	factoryErr := errors.New("client construction failed")
	gateway := existingVGateway()
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return nil, factoryErr
	})

	err := service.Connect(context.Background(), gateway.ID)
	if !errors.Is(err, factoryErr) {
		t.Fatalf("Connect() error = %v, want factory error", err)
	}
	if !strings.Contains(err.Error(), "create modbus_tcp vGateway client") {
		t.Fatalf("Connect() error = %q, want factory operation detail", err)
	}
	assertRuntimeConnectionError(t, service.runtimes[gateway.ID], factoryErr, 1)
}

func TestVGatewayServiceConnectRejectsNilDriverClient(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return nil, nil
	})

	err := service.Connect(context.Background(), gateway.ID)
	if !errors.Is(err, protocol.ErrGatewayClientRequired) {
		t.Fatalf("Connect() error = %v, want client-required error", err)
	}
	assertRuntimeConnectionError(
		t,
		service.runtimes[gateway.ID],
		protocol.ErrGatewayClientRequired,
		1,
	)
}

func TestVGatewayServiceConnectRetriesExistingClientAfterFailure(t *testing.T) {
	t.Parallel()

	connectErr := errors.New("connection refused")
	gateway := existingVGateway()
	client := &stubModbusClient{connectErr: connectErr}
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return client, nil
	})
	connectedAt := time.Date(2026, time.August, 21, 14, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return connectedAt }

	err := service.Connect(context.Background(), gateway.ID)
	if !errors.Is(err, connectErr) {
		t.Fatalf("first Connect() error = %v, want connection error", err)
	}
	assertRuntimeConnectionError(t, service.runtimes[gateway.ID], connectErr, 1)

	client.setConnectError(nil)
	if err := service.Connect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if factoryCalls != 1 || client.connectCallCount() != 2 {
		t.Fatalf("factory/connect calls = (%d, %d), want (1, 2)", factoryCalls, client.connectCallCount())
	}
	runtime := service.runtimes[gateway.ID]
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.status != VGatewayStatusConnected || runtime.lastError != nil {
		t.Fatalf("recovered runtime state = (%q, %v), want connected/clear", runtime.status, runtime.lastError)
	}
	if runtime.errorCount != 1 {
		t.Fatalf("runtime error count = %d, want preserved 1", runtime.errorCount)
	}
	if runtime.connectedAt == nil || !runtime.connectedAt.Equal(connectedAt) {
		t.Fatalf("connected at = %v, want %v", runtime.connectedAt, connectedAt)
	}
}

func TestVGatewayServiceConnectIsIdempotent(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	client := &stubModbusClient{}
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return client, nil
	})
	firstConnectedAt := time.Date(2026, time.August, 21, 15, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return firstConnectedAt }

	if err := service.Connect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("first Connect() error = %v", err)
	}
	service.now = func() time.Time { return firstConnectedAt.Add(time.Hour) }
	if err := service.Connect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if factoryCalls != 1 || client.connectCallCount() != 1 {
		t.Fatalf("factory/connect calls = (%d, %d), want idempotent (1, 1)", factoryCalls, client.connectCallCount())
	}
	runtime := service.runtimes[gateway.ID]
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.connectedAt == nil || !runtime.connectedAt.Equal(firstConnectedAt) {
		t.Fatalf("connected at = %v, want original %v", runtime.connectedAt, firstConnectedAt)
	}
}

func TestVGatewayServiceConnectSerializesConcurrentCallsAndPublishesConnecting(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	connectStarted := make(chan struct{}, 1)
	connectRelease := make(chan struct{})
	client := &stubModbusClient{
		connectStarted: connectStarted,
		connectRelease: connectRelease,
	}
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return client, nil
	})

	const callers = 12
	errorsByCaller := make([]error, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := range callers {
		go func() {
			defer waitGroup.Done()
			errorsByCaller[index] = service.Connect(
				context.Background(),
				gateway.ID,
			)
		}()
	}

	select {
	case <-connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("client Connect() did not start")
	}
	if status := service.statusForGateway(gateway.ID, true); status != VGatewayStatusConnecting {
		t.Fatalf("status during Connect() = %q, want connecting", status)
	}
	close(connectRelease)
	waitGroup.Wait()

	for index, err := range errorsByCaller {
		if err != nil {
			t.Fatalf("Connect() caller %d error = %v", index, err)
		}
	}
	if factoryCalls != 1 || client.connectCallCount() != 1 {
		t.Fatalf("factory/connect calls = (%d, %d), want serialized (1, 1)", factoryCalls, client.connectCallCount())
	}
	if status := service.statusForGateway(gateway.ID, true); status != VGatewayStatusConnected {
		t.Fatalf("final status = %q, want connected", status)
	}
}

func TestVGatewayServiceConnectPropagatesContextCancellation(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- service.Connect(ctx, gateway.ID)
	}()

	select {
	case <-connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("client Connect() did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect() did not return after context cancellation")
	}
	assertRuntimeConnectionError(
		t,
		service.runtimes[gateway.ID],
		context.Canceled,
		1,
	)
}

func TestVGatewayServiceConnectDispatchesRegisteredDriverWithOwnedConfig(t *testing.T) {
	t.Parallel()

	customType := VGatewayType("mqtt")
	gateway := existingVGateway()
	gateway.Type = customType
	gateway.Config = VGatewayConfig(
		`{"broker":"mqtt.example.local"}`,
	)
	originalConfig := string(gateway.Config)
	client := &stubModbusClient{}
	newClientCalls := 0
	driver := &stubGatewayDriver{
		newClientFunc: func(config json.RawMessage) (protocol.GatewayClient, error) {
			newClientCalls++
			config[0] = '['
			return client, nil
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

	if err := service.Connect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if newClientCalls != 1 || client.connectCallCount() != 1 {
		t.Fatalf("driver/client calls = (%d, %d), want (1, 1)", newClientCalls, client.connectCallCount())
	}
	if string(gateway.Config) != originalConfig {
		t.Fatal("driver mutation escaped into repository-owned config")
	}
}

func assertRuntimeConnectionError(
	t *testing.T,
	runtime *vGatewayRuntime,
	wantError error,
	wantCount int64,
) {
	t.Helper()
	if runtime == nil {
		t.Fatal("runtime = nil, want recorded error state")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.status != VGatewayStatusError {
		t.Fatalf("runtime status = %q, want error", runtime.status)
	}
	if !errors.Is(runtime.lastError, wantError) {
		t.Fatalf("runtime last error = %v, want %v", runtime.lastError, wantError)
	}
	if runtime.errorCount != wantCount {
		t.Fatalf("runtime error count = %d, want %d", runtime.errorCount, wantCount)
	}
	if runtime.connectedAt != nil {
		t.Fatalf("runtime connected at = %v, want nil", runtime.connectedAt)
	}
}
