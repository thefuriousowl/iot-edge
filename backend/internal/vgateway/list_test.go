package vgateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
)

func TestVGatewayServiceListAppliesDefaultsAndProjectsStatuses(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "list-1")
	connectedID := uuid.New()
	disabledID := uuid.New()
	gateways := []VGateway{
		{
			ID:      connectedID,
			Name:    "Connected Gateway",
			Type:    VGatewayTypeModbusTCP,
			Enabled: true,
		},
		{
			ID:      disabledID,
			Name:    "Disabled Gateway",
			Type:    VGatewayTypeModbusTCP,
			Enabled: false,
		},
	}

	var gotOptions VGatewayListOptions
	repo := &stubVGatewayRepository{
		listFunc: func(gotCtx context.Context, options VGatewayListOptions) ([]VGateway, int64, error) {
			if gotCtx.Value(requestKey) != "list-1" {
				t.Fatalf("repository context value = %v, want list-1", gotCtx.Value(requestKey))
			}
			gotOptions = options
			return gateways, 41, nil
		},
	}
	factoryCalls := 0
	service := newTestVGatewayService(t, repo, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	})
	service.runtimes[connectedID] = &vGatewayRuntime{
		status: VGatewayStatusConnected,
	}

	got, err := service.List(ctx, VGatewayListInput{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if gotOptions.Type != nil || gotOptions.Enabled != nil {
		t.Fatalf("repository filters = (%v, %v), want nil filters", gotOptions.Type, gotOptions.Enabled)
	}
	if gotOptions.Limit != DefaultVGatewayPerPage || gotOptions.Offset != 0 {
		t.Fatalf("repository pagination = limit %d offset %d, want limit %d offset 0", gotOptions.Limit, gotOptions.Offset, DefaultVGatewayPerPage)
	}
	if got.Page != DefaultVGatewayPage || got.PerPage != DefaultVGatewayPerPage {
		t.Fatalf("pagination = page %d perPage %d, want page %d perPage %d", got.Page, got.PerPage, DefaultVGatewayPage, DefaultVGatewayPerPage)
	}
	if got.Total != 41 || got.TotalPages != 3 {
		t.Fatalf("totals = total %d pages %d, want total 41 pages 3", got.Total, got.TotalPages)
	}
	if len(got.Data) != 2 {
		t.Fatalf("data length = %d, want 2", len(got.Data))
	}
	if got.Data[0].ID != connectedID || got.Data[0].Status != VGatewayStatusConnected {
		t.Fatalf("first view = %#v, want connected gateway", got.Data[0])
	}
	if got.Data[1].ID != disabledID || got.Data[1].Status != VGatewayStatusStopped {
		t.Fatalf("second view = %#v, want stopped gateway", got.Data[1])
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}
	if len(service.runtimes) != 1 {
		t.Fatalf("runtime registry length = %d, want existing length 1", len(service.runtimes))
	}
}

func TestVGatewayServiceListForwardsFiltersAndPagination(t *testing.T) {
	t.Parallel()

	gatewayType := VGatewayTypeModbusTCP
	enabled := false
	var gotOptions VGatewayListOptions
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		listFunc: func(_ context.Context, options VGatewayListOptions) ([]VGateway, int64, error) {
			gotOptions = options
			return []VGateway{}, 0, nil
		},
	}, nil)

	got, err := service.List(context.Background(), VGatewayListInput{
		Type:    &gatewayType,
		Enabled: &enabled,
		Page:    3,
		PerPage: 15,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if gotOptions.Type == nil || *gotOptions.Type != gatewayType {
		t.Fatalf("repository type filter = %v, want %q", gotOptions.Type, gatewayType)
	}
	if gotOptions.Enabled == nil || *gotOptions.Enabled {
		t.Fatalf("repository enabled filter = %v, want false", gotOptions.Enabled)
	}
	if gotOptions.Limit != 15 || gotOptions.Offset != 30 {
		t.Fatalf("repository pagination = limit %d offset %d, want limit 15 offset 30", gotOptions.Limit, gotOptions.Offset)
	}
	if got.Page != 3 || got.PerPage != 15 || got.TotalPages != 0 {
		t.Fatalf("result pagination = %#v, want page 3 perPage 15 totalPages 0", got)
	}
	if got.Data == nil || len(got.Data) != 0 {
		t.Fatalf("empty data = %#v, want non-nil empty slice", got.Data)
	}
}

func TestVGatewayServiceListCapsPerPage(t *testing.T) {
	t.Parallel()

	var gotOptions VGatewayListOptions
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		listFunc: func(_ context.Context, options VGatewayListOptions) ([]VGateway, int64, error) {
			gotOptions = options
			return []VGateway{}, 250, nil
		},
	}, nil)

	got, err := service.List(context.Background(), VGatewayListInput{
		Page:    2,
		PerPage: MaxVGatewayPerPage + 1,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if gotOptions.Limit != MaxVGatewayPerPage || gotOptions.Offset != MaxVGatewayPerPage {
		t.Fatalf("repository pagination = limit %d offset %d, want capped values %d and %d", gotOptions.Limit, gotOptions.Offset, MaxVGatewayPerPage, MaxVGatewayPerPage)
	}
	if got.PerPage != MaxVGatewayPerPage || got.TotalPages != 3 {
		t.Fatalf("result pagination = perPage %d totalPages %d, want %d and 3", got.PerPage, got.TotalPages, MaxVGatewayPerPage)
	}
}

func TestVGatewayServiceListRejectsUnsupportedTypeBeforeRepository(t *testing.T) {
	t.Parallel()

	unsupported := VGatewayType("modbus_rtu")
	repositoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		listFunc: func(context.Context, VGatewayListOptions) ([]VGateway, int64, error) {
			repositoryCalls++
			return []VGateway{}, 0, nil
		},
	}, nil)

	result, err := service.List(context.Background(), VGatewayListInput{
		Type: &unsupported,
	})
	if result != nil {
		t.Fatalf("List() result = %#v, want nil", result)
	}
	if !errors.Is(err, ErrUnsupportedVGatewayType) {
		t.Fatalf("List() error = %v, want ErrUnsupportedVGatewayType", err)
	}
	if repositoryCalls != 0 {
		t.Fatalf("repository calls = %d, want 0", repositoryCalls)
	}
}

func TestVGatewayServiceListWrapsRepositoryError(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		listFunc: func(context.Context, VGatewayListOptions) ([]VGateway, int64, error) {
			return nil, 0, databaseErr
		},
	}, nil)

	result, err := service.List(context.Background(), VGatewayListInput{})
	if result != nil {
		t.Fatalf("List() result = %#v, want nil", result)
	}
	if !errors.Is(err, databaseErr) {
		t.Fatalf("List() error = %v, want wrapped %v", err, databaseErr)
	}
	if !strings.Contains(err.Error(), "list vGateways") {
		t.Fatalf("List() error = %q, want operation detail", err)
	}
}
