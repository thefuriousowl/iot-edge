package vgateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
)

func TestVGatewayServiceGetReturnsGatewayWithDefaultStatus(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "get-1")
	gateway := &VGateway{
		ID:      uuid.New(),
		Name:    "Factory Gateway",
		Type:    VGatewayTypeModbusTCP,
		Enabled: true,
		Config: rawConfigValue(modbus.ModbusTCPConfig{
			Host:    "plc.example.local",
			Port:    502,
			Timeout: 5000,
		}),
	}
	factoryCalls := 0
	repo := &stubVGatewayRepository{
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*VGateway, error) {
			if gotCtx.Value(requestKey) != "get-1" {
				t.Fatalf("repository context value = %v, want get-1", gotCtx.Value(requestKey))
			}
			if id != gateway.ID {
				t.Fatalf("repository ID = %v, want %v", id, gateway.ID)
			}
			return gateway, nil
		},
	}
	service := newTestVGatewayService(t, repo, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	})

	got, err := service.Get(ctx, gateway.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != gateway.ID || got.Name != gateway.Name ||
		!equalVGatewayConfig(got.Config, gateway.Config) {
		t.Fatalf("Get() gateway = %#v, want %#v", got.VGateway, *gateway)
	}
	if got.Status != VGatewayStatusDisconnected {
		t.Fatalf("Get() status = %q, want %q", got.Status, VGatewayStatusDisconnected)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}
	if len(service.runtimes) != 0 {
		t.Fatalf("runtime registry length = %d, want 0", len(service.runtimes))
	}

	got.Name = "Caller Mutation"
	if gateway.Name != "Factory Gateway" {
		t.Fatalf("repository gateway name changed to %q through view value", gateway.Name)
	}
	originalConfig := string(gateway.Config)
	got.Config[0] = '['
	if string(gateway.Config) != originalConfig {
		t.Fatal("repository gateway config changed through view byte slice")
	}
}

func TestVGatewayServiceGetProjectsStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		enabled       bool
		runtimeExists bool
		runtimeStatus VGatewayConnectionStatus
		wantStatus    VGatewayConnectionStatus
	}{
		{
			name:          "disabled gateway is stopped even with connected runtime",
			enabled:       false,
			runtimeExists: true,
			runtimeStatus: VGatewayStatusConnected,
			wantStatus:    VGatewayStatusStopped,
		},
		{
			name:       "enabled gateway without runtime is disconnected",
			enabled:    true,
			wantStatus: VGatewayStatusDisconnected,
		},
		{
			name:          "empty runtime status falls back to disconnected",
			enabled:       true,
			runtimeExists: true,
			wantStatus:    VGatewayStatusDisconnected,
		},
		{
			name:          "connecting runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: VGatewayStatusConnecting,
			wantStatus:    VGatewayStatusConnecting,
		},
		{
			name:          "connected runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: VGatewayStatusConnected,
			wantStatus:    VGatewayStatusConnected,
		},
		{
			name:          "error runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: VGatewayStatusError,
			wantStatus:    VGatewayStatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gateway := &VGateway{
				ID:      uuid.New(),
				Name:    "Gateway",
				Type:    VGatewayTypeModbusTCP,
				Enabled: tt.enabled,
			}
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return gateway, nil
				},
			}, nil)
			if tt.runtimeExists {
				service.runtimes[gateway.ID] = &vGatewayRuntime{status: tt.runtimeStatus}
			}

			got, err := service.Get(context.Background(), gateway.ID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("Get() status = %q, want %q", got.Status, tt.wantStatus)
			}
		})
	}
}

func TestVGatewayServiceGetMapsRepositoryErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name      string
		repoError error
		wantError error
		wantText  string
	}{
		{
			name:      "not found",
			repoError: ErrVGatewayNotFound,
			wantError: ErrVGatewayNotFound,
		},
		{
			name:      "unexpected repository error",
			repoError: databaseErr,
			wantError: databaseErr,
			wantText:  "get vGateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return nil, tt.repoError
				},
			}, nil)

			view, err := service.Get(context.Background(), uuid.New())
			if view != nil {
				t.Fatalf("Get() view = %#v, want nil", view)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Get() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Get() error = %q, want text %q", err, tt.wantText)
			}
		})
	}
}
