package vgateway

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
)

func TestVGatewayServiceCreateNormalizesAndPersistsGateway(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "create-1")
	description := "  Main production PLC  "
	wantID := uuid.New()
	wantCreatedAt := time.Date(2026, time.August, 21, 10, 30, 0, 0, time.UTC)

	var persisted *VGateway
	repo := &stubVGatewayRepository{
		createFunc: func(gotCtx context.Context, gateway *VGateway) error {
			if gotCtx.Value(requestKey) != "create-1" {
				t.Fatalf("repository context value = %v, want create-1", gotCtx.Value(requestKey))
			}
			persisted = gateway
			gateway.ID = wantID
			gateway.CreatedAt = wantCreatedAt
			gateway.UpdatedAt = wantCreatedAt
			return nil
		},
	}
	factoryCalls := 0
	service := newTestVGatewayService(t, repo, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	})

	got, err := service.Create(ctx, CreateVGatewayInput{
		Name:        "  Factory Gateway  ",
		Type:        VGatewayTypeModbusTCP,
		Description: &description,
		Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
			Host: "  plc.example.local  ",
		}),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got != persisted {
		t.Fatal("Create() did not return the gateway populated by repository")
	}
	if got.ID != wantID || !got.CreatedAt.Equal(wantCreatedAt) || !got.UpdatedAt.Equal(wantCreatedAt) {
		t.Fatalf("repository-populated fields = (%v, %v, %v), want (%v, %v, %v)", got.ID, got.CreatedAt, got.UpdatedAt, wantID, wantCreatedAt, wantCreatedAt)
	}
	if got.Name != "Factory Gateway" {
		t.Fatalf("Name = %q, want %q", got.Name, "Factory Gateway")
	}
	if got.Description == nil || *got.Description != "Main production PLC" {
		t.Fatalf("Description = %v, want trimmed description", got.Description)
	}
	if !got.Enabled {
		t.Fatal("Enabled = false, want default true")
	}
	wantConfig := modbus.ModbusTCPConfig{
		Host:              "plc.example.local",
		Port:              modbus.DefaultModbusTCPPort,
		Timeout:           modbus.DefaultModbusTCPTimeout,
		RetryCount:        modbus.DefaultModbusTCPRetryCount,
		RetryDelay:        modbus.DefaultModbusTCPRetryDelay,
		KeepAlive:         true,
		ReconnectInterval: modbus.DefaultModbusTCPReconnectInterval,
	}
	if gotConfig := decodeModbusConfig(t, got.Config); !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("Config = %#v, want %#v", gotConfig, wantConfig)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}
	if len(service.runtimes) != 0 {
		t.Fatalf("runtime registry length = %d, want 0", len(service.runtimes))
	}

	description = "caller mutation"
	if *got.Description != "Main production PLC" {
		t.Fatalf("Description changed through caller pointer to %q", *got.Description)
	}
}

func TestVGatewayServiceCreatePreservesExplicitDisabledAndEmptyDescription(t *testing.T) {
	t.Parallel()

	enabled := false
	description := "  "
	var persisted *VGateway
	repo := &stubVGatewayRepository{
		createFunc: func(_ context.Context, gateway *VGateway) error {
			persisted = gateway
			return nil
		},
	}
	service := newTestVGatewayService(t, repo, nil)

	got, err := service.Create(context.Background(), CreateVGatewayInput{
		Name:        "Disabled Gateway",
		Type:        VGatewayTypeModbusTCP,
		Description: &description,
		Enabled:     &enabled,
		Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
			Host: "192.0.2.50",
		}),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got != persisted {
		t.Fatal("Create() result and persisted gateway differ")
	}
	if got.Enabled {
		t.Fatal("Enabled = true, want explicit false")
	}
	if got.Description != nil {
		t.Fatalf("Description = %q, want nil", *got.Description)
	}
}

func TestVGatewayServiceCreateValidatesBeforeRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     CreateVGatewayInput
		wantError error
	}{
		{
			name: "empty name",
			input: CreateVGatewayInput{
				Name: "  ",
				Type: VGatewayTypeModbusTCP,
				Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
					Host: "plc.example.local",
				}),
			},
			wantError: ErrInvalidVGatewayName,
		},
		{
			name: "name above 100 Unicode characters",
			input: CreateVGatewayInput{
				Name: strings.Repeat("ก", 101),
				Type: VGatewayTypeModbusTCP,
				Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
					Host: "plc.example.local",
				}),
			},
			wantError: ErrInvalidVGatewayName,
		},
		{
			name: "unsupported type",
			input: CreateVGatewayInput{
				Name: "Gateway",
				Type: VGatewayType("modbus_rtu"),
				Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
					Host: "plc.example.local",
				}),
			},
			wantError: ErrUnsupportedVGatewayType,
		},
		{
			name: "invalid config",
			input: CreateVGatewayInput{
				Name:   "Gateway",
				Type:   VGatewayTypeModbusTCP,
				Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{}),
			},
			wantError: ErrInvalidVGatewayConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repositoryCalls := 0
			factoryCalls := 0
			repo := &stubVGatewayRepository{
				createFunc: func(context.Context, *VGateway) error {
					repositoryCalls++
					return nil
				},
			}
			service := newTestVGatewayService(t, repo, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
				factoryCalls++
				return nil, nil
			})

			gateway, err := service.Create(context.Background(), tt.input)
			if gateway != nil {
				t.Fatalf("Create() gateway = %#v, want nil", gateway)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantError)
			}
			if repositoryCalls != 0 {
				t.Fatalf("repository calls = %d, want 0", repositoryCalls)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
		})
	}
}

func TestVGatewayServiceCreateAcceptsExactly100UnicodeCharacters(t *testing.T) {
	t.Parallel()

	repositoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		createFunc: func(context.Context, *VGateway) error {
			repositoryCalls++
			return nil
		},
	}, nil)

	_, err := service.Create(context.Background(), CreateVGatewayInput{
		Name: strings.Repeat("ก", 100),
		Type: VGatewayTypeModbusTCP,
		Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
			Host: "plc.example.local",
		}),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if repositoryCalls != 1 {
		t.Fatalf("repository calls = %d, want 1", repositoryCalls)
	}
}

func TestVGatewayServiceCreateMapsRepositoryErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name      string
		repoError error
		wantError error
	}{
		{
			name:      "duplicate name",
			repoError: ErrVGatewayNameExists,
			wantError: ErrVGatewayNameExists,
		},
		{
			name:      "unexpected repository error",
			repoError: databaseErr,
			wantError: databaseErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestVGatewayService(t, &stubVGatewayRepository{
				createFunc: func(context.Context, *VGateway) error {
					return tt.repoError
				},
			}, nil)

			gateway, err := service.Create(context.Background(), CreateVGatewayInput{
				Name: "Gateway",
				Type: VGatewayTypeModbusTCP,
				Config: mustRawConfig(t, modbus.ModbusTCPConfigInput{
					Host: "plc.example.local",
				}),
			})
			if gateway != nil {
				t.Fatalf("Create() gateway = %#v, want nil", gateway)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestVGatewayServiceCreateDispatchesRegisteredProtocolDriver(t *testing.T) {
	t.Parallel()

	customType := VGatewayType("mqtt")
	inputConfig := VGatewayConfig(`{"broker":"mqtt.example.local"}`)
	canonicalConfig := VGatewayConfig(
		`{"broker":"mqtt.example.local","qos":1}`,
	)
	normalizeCalls := 0
	clientCalls := 0
	driver := &stubGatewayDriver{
		normalizeConfigFunc: func(raw json.RawMessage) (json.RawMessage, error) {
			normalizeCalls++
			if string(raw) != string(inputConfig) {
				t.Fatalf("driver config = %s, want %s", raw, inputConfig)
			}
			return canonicalConfig, nil
		},
		newClientFunc: func(json.RawMessage) (protocol.GatewayClient, error) {
			clientCalls++
			return nil, nil
		},
	}
	var persisted *VGateway
	repo := &stubVGatewayRepository{
		createFunc: func(_ context.Context, gateway *VGateway) error {
			persisted = gateway
			return nil
		},
	}
	service, err := NewVGatewayService(repo, GatewayDriverRegistry{
		customType: driver,
	})
	if err != nil {
		t.Fatalf("NewVGatewayService() error = %v", err)
	}

	gateway, err := service.Create(context.Background(), CreateVGatewayInput{
		Name:   "MQTT Gateway",
		Type:   customType,
		Config: inputConfig,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gateway != persisted || gateway.Type != customType {
		t.Fatalf("Create() gateway = %#v, want persisted custom type", gateway)
	}
	if string(gateway.Config) != string(canonicalConfig) {
		t.Fatalf("persisted config = %s, want %s", gateway.Config, canonicalConfig)
	}
	if normalizeCalls != 1 {
		t.Fatalf("NormalizeConfig() calls = %d, want 1", normalizeCalls)
	}
	if clientCalls != 0 {
		t.Fatalf("NewClient() calls = %d, want lazy zero", clientCalls)
	}
}

func newTestVGatewayService(
	t *testing.T,
	repo VGatewayRepository,
	factory modbus.ModbusClientFactory,
) *vGatewayService {
	t.Helper()

	if factory == nil {
		factory = func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
			return nil, nil
		}
	}
	driver, err := modbus.NewModbusTCPDriver(factory)
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	service, err := NewVGatewayService(repo, GatewayDriverRegistry{
		VGatewayTypeModbusTCP: driver,
	})
	if err != nil {
		t.Fatalf("NewVGatewayService() error = %v", err)
	}
	return service
}
