package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func TestNewVGatewayServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	driver, err := protocol.NewModbusTCPDriver(
		func(protocol.ModbusTCPConfig) (protocol.ModbusClient, error) {
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	drivers := GatewayDriverRegistry{
		domain.VGatewayTypeModbusTCP: driver,
	}

	tests := []struct {
		name      string
		gateways  repository.VGatewayRepository
		drivers   GatewayDriverRegistry
		wantError error
	}{
		{
			name:      "missing repository is reported first",
			gateways:  nil,
			drivers:   nil,
			wantError: ErrVGatewayRepositoryRequired,
		},
		{
			name:      "missing repository",
			gateways:  nil,
			drivers:   drivers,
			wantError: ErrVGatewayRepositoryRequired,
		},
		{
			name:      "missing driver registry",
			gateways:  &stubVGatewayRepository{},
			drivers:   nil,
			wantError: ErrVGatewayDriversRequired,
		},
		{
			name:     "nil registered driver",
			gateways: &stubVGatewayRepository{},
			drivers: GatewayDriverRegistry{
				domain.VGatewayTypeModbusTCP: nil,
			},
			wantError: ErrVGatewayDriverRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service, err := NewVGatewayService(tt.gateways, tt.drivers)
			if service != nil {
				t.Fatalf("NewVGatewayService() service = %#v, want nil", service)
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("NewVGatewayService() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestNewVGatewayServiceInitializesIsolatedRuntimeRegistry(t *testing.T) {
	t.Parallel()

	repo := &stubVGatewayRepository{}
	factoryCalls := 0
	factory := func(protocol.ModbusTCPConfig) (protocol.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	}

	driver, err := protocol.NewModbusTCPDriver(factory)
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	drivers := GatewayDriverRegistry{
		domain.VGatewayTypeModbusTCP: driver,
	}
	first, err := NewVGatewayService(repo, drivers)
	if err != nil {
		t.Fatalf("first NewVGatewayService() error = %v", err)
	}
	second, err := NewVGatewayService(repo, drivers)
	if err != nil {
		t.Fatalf("second NewVGatewayService() error = %v", err)
	}

	if first.gateways != repo {
		t.Fatal("repository dependency was not preserved")
	}
	if first.drivers[domain.VGatewayTypeModbusTCP] != driver {
		t.Fatal("driver dependency was not preserved")
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls during construction = %d, want 0", factoryCalls)
	}
	if first.runtimes == nil || second.runtimes == nil {
		t.Fatal("runtime registry = nil, want initialized map")
	}
	if len(first.runtimes) != 0 || len(second.runtimes) != 0 {
		t.Fatalf("runtime registry lengths = (%d, %d), want (0, 0)", len(first.runtimes), len(second.runtimes))
	}
	if first.now == nil || second.now == nil {
		t.Fatal("clock function = nil, want initialized clock")
	}

	delete(drivers, domain.VGatewayTypeModbusTCP)
	if _, exists := first.drivers[domain.VGatewayTypeModbusTCP]; !exists {
		t.Fatal("service driver registry aliases caller-owned map")
	}

	gatewayID := uuid.New()
	first.runtimes[gatewayID] = &vGatewayRuntime{
		status: domain.VGatewayStatusConnected,
	}
	if _, exists := second.runtimes[gatewayID]; exists {
		t.Fatal("runtime registry is shared between service instances")
	}
}

func TestNewVGatewayServiceClockUsesCurrentTime(t *testing.T) {
	t.Parallel()

	driver, err := protocol.NewModbusTCPDriver(
		func(protocol.ModbusTCPConfig) (protocol.ModbusClient, error) {
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	service, err := NewVGatewayService(
		&stubVGatewayRepository{},
		GatewayDriverRegistry{domain.VGatewayTypeModbusTCP: driver},
	)
	if err != nil {
		t.Fatalf("NewVGatewayService() error = %v", err)
	}

	before := time.Now()
	got := service.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("clock returned %v, want between %v and %v", got, before, after)
	}
}

func mustRawConfig(t *testing.T, config any) domain.VGatewayConfig {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func rawConfigPointer(t *testing.T, config any) *domain.VGatewayConfig {
	t.Helper()
	raw := mustRawConfig(t, config)
	return &raw
}

func rawConfigValue(config any) domain.VGatewayConfig {
	raw, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	return raw
}

func decodeModbusConfig(
	t *testing.T,
	raw domain.VGatewayConfig,
) protocol.ModbusTCPConfig {
	t.Helper()
	var config protocol.ModbusTCPConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return config
}

type stubVGatewayRepository struct {
	createFunc   func(context.Context, *domain.VGateway) error
	findByIDFunc func(context.Context, uuid.UUID) (*domain.VGateway, error)
	listFunc     func(context.Context, repository.VGatewayListOptions) ([]domain.VGateway, int64, error)
	updateFunc   func(context.Context, *domain.VGateway) error
	deleteFunc   func(context.Context, uuid.UUID) error
}

type stubGatewayDriver struct {
	normalizeConfigFunc func(json.RawMessage) (json.RawMessage, error)
	newClientFunc       func(json.RawMessage) (protocol.GatewayClient, error)
}

var _ protocol.GatewayDriver = (*stubGatewayDriver)(nil)

func (d *stubGatewayDriver) NormalizeConfig(
	raw json.RawMessage,
) (json.RawMessage, error) {
	if d.normalizeConfigFunc != nil {
		return d.normalizeConfigFunc(raw)
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (d *stubGatewayDriver) NewClient(
	config json.RawMessage,
) (protocol.GatewayClient, error) {
	if d.newClientFunc != nil {
		return d.newClientFunc(config)
	}
	return nil, nil
}

var _ repository.VGatewayRepository = (*stubVGatewayRepository)(nil)

func (r *stubVGatewayRepository) Create(ctx context.Context, gateway *domain.VGateway) error {
	if r.createFunc != nil {
		return r.createFunc(ctx, gateway)
	}
	return nil
}

func (r *stubVGatewayRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.VGateway, error) {
	if r.findByIDFunc != nil {
		return r.findByIDFunc(ctx, id)
	}
	return nil, repository.ErrVGatewayNotFound
}

func (r *stubVGatewayRepository) List(
	ctx context.Context,
	options repository.VGatewayListOptions,
) ([]domain.VGateway, int64, error) {
	if r.listFunc != nil {
		return r.listFunc(ctx, options)
	}
	return []domain.VGateway{}, 0, nil
}

func (r *stubVGatewayRepository) Update(ctx context.Context, gateway *domain.VGateway) error {
	if r.updateFunc != nil {
		return r.updateFunc(ctx, gateway)
	}
	return nil
}

func (r *stubVGatewayRepository) Delete(ctx context.Context, id uuid.UUID) error {
	if r.deleteFunc != nil {
		return r.deleteFunc(ctx, id)
	}
	return nil
}
