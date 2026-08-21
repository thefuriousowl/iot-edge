package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func TestVGatewayServiceUpdateAppliesPartialFields(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "update-1")
	description := "  Updated production PLC  "
	name := "  Updated Gateway  "
	enabled := false
	keepAlive := false
	existing := existingVGateway()
	wantUpdatedAt := existing.UpdatedAt.Add(time.Minute)

	findCalls := 0
	updateCalls := 0
	var persisted *domain.VGateway
	repo := &stubVGatewayRepository{
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*domain.VGateway, error) {
			findCalls++
			if gotCtx.Value(requestKey) != "update-1" {
				t.Fatalf("find context value = %v, want update-1", gotCtx.Value(requestKey))
			}
			if id != existing.ID {
				t.Fatalf("find ID = %v, want %v", id, existing.ID)
			}
			return existing, nil
		},
		updateFunc: func(gotCtx context.Context, gateway *domain.VGateway) error {
			updateCalls++
			if gotCtx.Value(requestKey) != "update-1" {
				t.Fatalf("update context value = %v, want update-1", gotCtx.Value(requestKey))
			}
			persisted = gateway
			gateway.UpdatedAt = wantUpdatedAt
			return nil
		},
	}
	factoryCalls := 0
	service := newTestVGatewayService(t, repo, func(protocol.ModbusTCPConfig) (protocol.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	})

	got, err := service.Update(ctx, existing.ID, UpdateVGatewayInput{
		Name: &name,
		Description: OptionalDescription{
			Set:   true,
			Value: &description,
		},
		Enabled: &enabled,
		Config: rawConfigPointer(t, protocol.ModbusTCPConfigInput{
			Host:      "  updated-plc.example.local  ",
			KeepAlive: &keepAlive,
		}),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if findCalls != 1 || updateCalls != 1 {
		t.Fatalf("repository calls = find %d update %d, want 1 and 1", findCalls, updateCalls)
	}
	if persisted == nil || got.ID != persisted.ID {
		t.Fatal("Update() did not return repository-updated gateway")
	}
	if got.ID != existing.ID || got.Type != domain.VGatewayTypeModbusTCP || !got.CreatedAt.Equal(existing.CreatedAt) {
		t.Fatalf("immutable fields changed: %#v", got.VGateway)
	}
	if got.Name != "Updated Gateway" {
		t.Fatalf("Name = %q, want trimmed updated name", got.Name)
	}
	if got.Description == nil || *got.Description != "Updated production PLC" {
		t.Fatalf("Description = %v, want trimmed updated description", got.Description)
	}
	if got.Enabled {
		t.Fatal("Enabled = true, want explicit false")
	}
	wantConfig := protocol.ModbusTCPConfig{
		Host:              "updated-plc.example.local",
		Port:              protocol.DefaultModbusTCPPort,
		Timeout:           protocol.DefaultModbusTCPTimeout,
		RetryCount:        protocol.DefaultModbusTCPRetryCount,
		RetryDelay:        protocol.DefaultModbusTCPRetryDelay,
		KeepAlive:         false,
		ReconnectInterval: protocol.DefaultModbusTCPReconnectInterval,
	}
	if gotConfig := decodeModbusConfig(t, got.Config); !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("Config = %#v, want %#v", gotConfig, wantConfig)
	}
	if !got.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, wantUpdatedAt)
	}
	if got.Status != domain.VGatewayStatusStopped {
		t.Fatalf("Status = %q, want %q", got.Status, domain.VGatewayStatusStopped)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}

	name = "caller mutation"
	description = "caller mutation"
	if got.Name != "Updated Gateway" || *got.Description != "Updated production PLC" {
		t.Fatal("updated gateway aliases caller string pointers")
	}
}

func TestVGatewayServiceUpdateDescriptionSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description OptionalDescription
		want        *string
	}{
		{
			name: "omitted preserves description",
			description: OptionalDescription{
				Set: false,
			},
			want: stringPointer("Existing description"),
		},
		{
			name: "explicit null clears description",
			description: OptionalDescription{
				Set:   true,
				Value: nil,
			},
			want: nil,
		},
		{
			name: "whitespace clears description",
			description: OptionalDescription{
				Set:   true,
				Value: stringPointer("  \t"),
			},
			want: nil,
		},
		{
			name: "value is trimmed",
			description: OptionalDescription{
				Set:   true,
				Value: stringPointer("  New description  "),
			},
			want: stringPointer("New description"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existing := existingVGateway()
			updateCalls := 0
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
					return existing, nil
				},
				updateFunc: func(context.Context, *domain.VGateway) error {
					updateCalls++
					return nil
				},
			}, nil)

			got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
				Description: tt.description,
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if !equalOptionalString(got.Description, tt.want) {
				t.Fatalf("Description = %v, want %v", got.Description, tt.want)
			}
			wantCalls := 1
			if !tt.description.Set {
				wantCalls = 0
			}
			if updateCalls != wantCalls {
				t.Fatalf("repository Update() calls = %d, want %d", updateCalls, wantCalls)
			}
		})
	}
}

func TestVGatewayServiceUpdateEmptyInputIsReadOnly(t *testing.T) {
	t.Parallel()

	existing := existingVGateway()
	updateCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
		updateFunc: func(context.Context, *domain.VGateway) error {
			updateCalls++
			return nil
		},
	}, nil)
	service.runtimes[existing.ID] = &vGatewayRuntime{
		status: domain.VGatewayStatusConnected,
	}

	got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updateCalls != 0 {
		t.Fatalf("repository Update() calls = %d, want 0", updateCalls)
	}
	if got.Status != domain.VGatewayStatusConnected {
		t.Fatalf("Status = %q, want %q", got.Status, domain.VGatewayStatusConnected)
	}
}

func TestVGatewayServiceUpdateValidatesBeforeRepositoryUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     UpdateVGatewayInput
		wantError error
	}{
		{
			name: "empty name",
			input: UpdateVGatewayInput{
				Name: stringPointer("  "),
			},
			wantError: ErrInvalidVGatewayName,
		},
		{
			name: "name above 100 Unicode characters",
			input: UpdateVGatewayInput{
				Name: stringPointer(strings.Repeat("ก", 101)),
			},
			wantError: ErrInvalidVGatewayName,
		},
		{
			name: "invalid config",
			input: UpdateVGatewayInput{
				Config: rawConfigPointer(t, protocol.ModbusTCPConfigInput{}),
			},
			wantError: ErrInvalidVGatewayConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existing := existingVGateway()
			updateCalls := 0
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
					return existing, nil
				},
				updateFunc: func(context.Context, *domain.VGateway) error {
					updateCalls++
					return nil
				},
			}, nil)

			view, err := service.Update(context.Background(), existing.ID, tt.input)
			if view != nil {
				t.Fatalf("Update() view = %#v, want nil", view)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantError)
			}
			if updateCalls != 0 {
				t.Fatalf("repository Update() calls = %d, want 0", updateCalls)
			}
		})
	}
}

func TestVGatewayServiceUpdateMapsRepositoryErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name        string
		findError   error
		updateError error
		wantError   error
		wantText    string
	}{
		{
			name:      "find not found",
			findError: repository.ErrVGatewayNotFound,
			wantError: ErrVGatewayNotFound,
		},
		{
			name:      "find unexpected error",
			findError: databaseErr,
			wantError: databaseErr,
			wantText:  "find vGateway for update",
		},
		{
			name:        "update not found race",
			updateError: repository.ErrVGatewayNotFound,
			wantError:   ErrVGatewayNotFound,
		},
		{
			name:        "duplicate name",
			updateError: repository.ErrVGatewayNameExists,
			wantError:   ErrVGatewayNameExists,
		},
		{
			name:        "update unexpected error",
			updateError: databaseErr,
			wantError:   databaseErr,
			wantText:    "update vGateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existing := existingVGateway()
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
					if tt.findError != nil {
						return nil, tt.findError
					}
					return existing, nil
				},
				updateFunc: func(context.Context, *domain.VGateway) error {
					return tt.updateError
				},
			}, nil)

			view, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
				Name: stringPointer("Updated Gateway"),
			})
			if view != nil {
				t.Fatalf("Update() view = %#v, want nil", view)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Update() error = %q, want text %q", err, tt.wantText)
			}
		})
	}
}

func TestVGatewayServiceUpdatePreservesRuntimeForMetadataChanges(t *testing.T) {
	t.Parallel()

	existing := existingVGateway()
	client := &stubModbusClient{connected: true}
	connectedAt := existing.CreatedAt.Add(time.Minute)
	lastActivity := connectedAt.Add(time.Minute)
	runtime := &vGatewayRuntime{
		client:        client,
		status:        domain.VGatewayStatusConnected,
		connectedAt:   &connectedAt,
		lastActivity:  &lastActivity,
		requestCount:  12,
		errorCount:    2,
		bytesReceived: 48,
		totalLatency:  75 * time.Millisecond,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
	}, nil)
	service.runtimes[existing.ID] = runtime
	name := "Metadata Update"

	got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
		Name: &name,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Status != domain.VGatewayStatusConnected {
		t.Fatalf("Status = %q, want connected", got.Status)
	}
	if client.disconnectCallCount() != 0 {
		t.Fatalf("Disconnect() calls = %d, want 0", client.disconnectCallCount())
	}
	if runtime.client != client || runtime.connectedAt != &connectedAt || runtime.lastActivity != &lastActivity {
		t.Fatal("metadata update mutated active runtime")
	}
}

func TestVGatewayServiceUpdatePreservesRuntimeForIdenticalConfig(t *testing.T) {
	t.Parallel()

	existing := existingVGateway()
	// Simulate JSONB returning a semantically identical document with a
	// different key order than the driver's canonical encoding.
	existing.Config = domain.VGatewayConfig(`{
		"timeout":5000,
		"host":"existing-plc.example.local",
		"keep_alive":true,
		"port":502,
		"retry_delay":1000,
		"retry_count":3,
		"reconnect_interval":30
	}`)
	client := &stubModbusClient{connected: true}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
	}, nil)
	service.runtimes[existing.ID] = &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	input := modbusConfigInputFrom(existing.Config)

	got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
		Config: rawConfigPointer(t, input),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Status != domain.VGatewayStatusConnected {
		t.Fatalf("Status = %q, want connected", got.Status)
	}
	if client.disconnectCallCount() != 0 {
		t.Fatalf("Disconnect() calls = %d, want 0", client.disconnectCallCount())
	}
}

func TestVGatewayServiceUpdateInvalidatesRuntimeForConfigChange(t *testing.T) {
	t.Parallel()

	existing := existingVGateway()
	client := &stubModbusClient{connected: true}
	connectedAt := existing.CreatedAt.Add(time.Minute)
	lastActivity := connectedAt.Add(time.Minute)
	runtime := &vGatewayRuntime{
		client:        client,
		status:        domain.VGatewayStatusConnected,
		connectedAt:   &connectedAt,
		lastActivity:  &lastActivity,
		requestCount:  12,
		errorCount:    2,
		bytesReceived: 48,
		totalLatency:  75 * time.Millisecond,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
	}, nil)
	service.runtimes[existing.ID] = runtime
	config := modbusConfigInputFrom(existing.Config)
	config.Host = "replacement-plc.example.local"

	got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
		Config: rawConfigPointer(t, config),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Status != domain.VGatewayStatusDisconnected {
		t.Fatalf("Status = %q, want disconnected", got.Status)
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("Disconnect() calls = %d, want 1", client.disconnectCallCount())
	}
	if runtime.client != nil || runtime.connectedAt != nil || runtime.lastError != nil {
		t.Fatalf("invalidated runtime = %#v, want cleared client/connection/error", runtime)
	}
	if runtime.lastActivity != &lastActivity {
		t.Fatal("config invalidation cleared last activity")
	}
	if runtime.requestCount != 12 || runtime.errorCount != 2 || runtime.bytesReceived != 48 || runtime.totalLatency != 75*time.Millisecond {
		t.Fatal("config invalidation reset runtime statistics")
	}
}

func TestVGatewayServiceUpdateReconcilesEnabledState(t *testing.T) {
	t.Parallel()

	t.Run("disable disconnects and stops runtime", func(t *testing.T) {
		t.Parallel()

		existing := existingVGateway()
		client := &stubModbusClient{connected: true}
		connectedAt := existing.CreatedAt.Add(time.Minute)
		runtime := &vGatewayRuntime{
			client:      client,
			status:      domain.VGatewayStatusConnected,
			connectedAt: &connectedAt,
		}
		service := newTestVGatewayService(t, &stubVGatewayRepository{
			findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
				return existing, nil
			},
		}, nil)
		service.runtimes[existing.ID] = runtime
		enabled := false

		got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
			Enabled: &enabled,
		})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if got.Status != domain.VGatewayStatusStopped || runtime.status != domain.VGatewayStatusStopped {
			t.Fatalf("statuses = (%q, %q), want stopped", got.Status, runtime.status)
		}
		if client.disconnectCallCount() != 1 || runtime.client != nil || runtime.connectedAt != nil {
			t.Fatal("disable did not disconnect and clear runtime client")
		}
	})

	t.Run("enable resets stopped runtime to disconnected", func(t *testing.T) {
		t.Parallel()

		existing := existingVGateway()
		existing.Enabled = false
		runtime := &vGatewayRuntime{status: domain.VGatewayStatusStopped}
		service := newTestVGatewayService(t, &stubVGatewayRepository{
			findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
				return existing, nil
			},
		}, nil)
		service.runtimes[existing.ID] = runtime
		enabled := true

		got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
			Enabled: &enabled,
		})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if got.Status != domain.VGatewayStatusDisconnected || runtime.status != domain.VGatewayStatusDisconnected {
			t.Fatalf("statuses = (%q, %q), want disconnected", got.Status, runtime.status)
		}
	})
}

func TestVGatewayServiceUpdateRecordsRuntimeDisconnectFailure(t *testing.T) {
	t.Parallel()

	disconnectErr := errors.New("close failed")
	existing := existingVGateway()
	client := &stubModbusClient{
		connected:     true,
		disconnectErr: disconnectErr,
	}
	runtime := &vGatewayRuntime{
		client:     client,
		status:     domain.VGatewayStatusConnected,
		errorCount: 2,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
	}, nil)
	service.runtimes[existing.ID] = runtime
	config := modbusConfigInputFrom(existing.Config)
	config.Host = "replacement-plc.example.local"

	got, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
		Config: rawConfigPointer(t, config),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Status != domain.VGatewayStatusError || runtime.status != domain.VGatewayStatusError {
		t.Fatalf("statuses = (%q, %q), want error", got.Status, runtime.status)
	}
	if !errors.Is(runtime.lastError, disconnectErr) {
		t.Fatalf("runtime error = %v, want %v", runtime.lastError, disconnectErr)
	}
	if runtime.errorCount != 3 {
		t.Fatalf("runtime error count = %d, want 3", runtime.errorCount)
	}
	if runtime.client != nil {
		t.Fatal("runtime retained client after failed disconnect")
	}
}

func TestVGatewayServiceUpdateDoesNotInvalidateRuntimeWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	existing := existingVGateway()
	client := &stubModbusClient{connected: true}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return existing, nil
		},
		updateFunc: func(context.Context, *domain.VGateway) error {
			return databaseErr
		},
	}, nil)
	service.runtimes[existing.ID] = runtime
	config := modbusConfigInputFrom(existing.Config)
	config.Host = "replacement-plc.example.local"

	view, err := service.Update(context.Background(), existing.ID, UpdateVGatewayInput{
		Config: rawConfigPointer(t, config),
	})
	if view != nil {
		t.Fatalf("Update() view = %#v, want nil", view)
	}
	if !errors.Is(err, databaseErr) {
		t.Fatalf("Update() error = %v, want %v", err, databaseErr)
	}
	if client.disconnectCallCount() != 0 || runtime.client != client || runtime.status != domain.VGatewayStatusConnected {
		t.Fatal("failed persistence mutated active runtime")
	}
}

func existingVGateway() *domain.VGateway {
	description := "Existing description"
	createdAt := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC)
	return &domain.VGateway{
		ID:          uuid.New(),
		Name:        "Existing Gateway",
		Type:        domain.VGatewayTypeModbusTCP,
		Description: &description,
		Enabled:     true,
		Config: rawConfigValue(protocol.ModbusTCPConfig{
			Host:              "existing-plc.example.local",
			Port:              502,
			Timeout:           5000,
			RetryCount:        3,
			RetryDelay:        1000,
			KeepAlive:         true,
			ReconnectInterval: 30,
		}),
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func stringPointer(value string) *string {
	return &value
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func modbusConfigInputFrom(raw domain.VGatewayConfig) protocol.ModbusTCPConfigInput {
	var config protocol.ModbusTCPConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		panic(err)
	}
	return protocol.ModbusTCPConfigInput{
		Host:              config.Host,
		Port:              &config.Port,
		Timeout:           &config.Timeout,
		RetryCount:        &config.RetryCount,
		RetryDelay:        &config.RetryDelay,
		KeepAlive:         &config.KeepAlive,
		ReconnectInterval: &config.ReconnectInterval,
	}
}

type stubModbusClient struct {
	mu              sync.Mutex
	connected       bool
	connectCalls    int
	connectErr      error
	connectStarted  chan<- struct{}
	connectRelease  <-chan struct{}
	disconnectCalls int
	disconnectErr   error
	disconnectStart chan<- struct{}
	disconnectWait  <-chan struct{}
	readCalls       int
	readRequest     protocol.ReadRequest
	readErr         error
}

var _ protocol.ModbusClient = (*stubModbusClient)(nil)

func (c *stubModbusClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	c.connectCalls++
	connectErr := c.connectErr
	connectStarted := c.connectStarted
	connectRelease := c.connectRelease
	c.mu.Unlock()

	if connectStarted != nil {
		connectStarted <- struct{}{}
	}
	if connectRelease != nil {
		select {
		case <-connectRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if connectErr != nil {
		return connectErr
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return nil
}

func (c *stubModbusClient) connectCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectCalls
}

func (c *stubModbusClient) setConnectError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectErr = err
}

func (c *stubModbusClient) Disconnect() error {
	c.mu.Lock()
	c.disconnectCalls++
	c.connected = false
	disconnectErr := c.disconnectErr
	disconnectStart := c.disconnectStart
	disconnectWait := c.disconnectWait
	c.mu.Unlock()

	if disconnectStart != nil {
		disconnectStart <- struct{}{}
	}
	if disconnectWait != nil {
		<-disconnectWait
	}
	return disconnectErr
}

func (c *stubModbusClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *stubModbusClient) Read(
	_ context.Context,
	request protocol.ReadRequest,
) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readCalls++
	c.readRequest = request
	return nil, c.readErr
}

func (c *stubModbusClient) readCallState() (int, protocol.ReadRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readCalls, c.readRequest
}

func (c *stubModbusClient) disconnectCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnectCalls
}
