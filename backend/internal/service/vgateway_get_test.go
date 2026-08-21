package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func TestVGatewayServiceGetReturnsGatewayWithDefaultStatus(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "get-1")
	gateway := &domain.VGateway{
		ID:      uuid.New(),
		Name:    "Factory Gateway",
		Type:    domain.VGatewayTypeModbusTCP,
		Enabled: true,
		Config: rawConfigValue(protocol.ModbusTCPConfig{
			Host:    "plc.example.local",
			Port:    502,
			Timeout: 5000,
		}),
	}
	factoryCalls := 0
	repo := &stubVGatewayRepository{
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*domain.VGateway, error) {
			if gotCtx.Value(requestKey) != "get-1" {
				t.Fatalf("repository context value = %v, want get-1", gotCtx.Value(requestKey))
			}
			if id != gateway.ID {
				t.Fatalf("repository ID = %v, want %v", id, gateway.ID)
			}
			return gateway, nil
		},
	}
	service := newTestVGatewayService(t, repo, func(protocol.ModbusTCPConfig) (protocol.ModbusClient, error) {
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
	if got.Status != domain.VGatewayStatusDisconnected {
		t.Fatalf("Get() status = %q, want %q", got.Status, domain.VGatewayStatusDisconnected)
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
		runtimeStatus domain.VGatewayConnectionStatus
		wantStatus    domain.VGatewayConnectionStatus
	}{
		{
			name:          "disabled gateway is stopped even with connected runtime",
			enabled:       false,
			runtimeExists: true,
			runtimeStatus: domain.VGatewayStatusConnected,
			wantStatus:    domain.VGatewayStatusStopped,
		},
		{
			name:       "enabled gateway without runtime is disconnected",
			enabled:    true,
			wantStatus: domain.VGatewayStatusDisconnected,
		},
		{
			name:          "empty runtime status falls back to disconnected",
			enabled:       true,
			runtimeExists: true,
			wantStatus:    domain.VGatewayStatusDisconnected,
		},
		{
			name:          "connecting runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: domain.VGatewayStatusConnecting,
			wantStatus:    domain.VGatewayStatusConnecting,
		},
		{
			name:          "connected runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: domain.VGatewayStatusConnected,
			wantStatus:    domain.VGatewayStatusConnected,
		},
		{
			name:          "error runtime",
			enabled:       true,
			runtimeExists: true,
			runtimeStatus: domain.VGatewayStatusError,
			wantStatus:    domain.VGatewayStatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gateway := &domain.VGateway{
				ID:      uuid.New(),
				Name:    "Gateway",
				Type:    domain.VGatewayTypeModbusTCP,
				Enabled: tt.enabled,
			}
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
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
			repoError: repository.ErrVGatewayNotFound,
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
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
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
