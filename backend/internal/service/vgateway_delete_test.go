package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func TestVGatewayServiceDeleteRemovesPersistedGatewayWithoutRuntime(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "delete-1")
	gatewayID := uuid.New()
	deleteCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		deleteFunc: func(gotCtx context.Context, id uuid.UUID) error {
			deleteCalls++
			if gotCtx.Value(requestKey) != "delete-1" {
				t.Fatalf("Delete() context value = %v, want delete-1", gotCtx.Value(requestKey))
			}
			if id != gatewayID {
				t.Fatalf("Delete() ID = %v, want %v", id, gatewayID)
			}
			return nil
		},
	}, nil)

	if err := service.Delete(ctx, gatewayID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("repository Delete() calls = %d, want 1", deleteCalls)
	}
	if len(service.runtimes) != 0 {
		t.Fatalf("runtime registry length = %d, want 0", len(service.runtimes))
	}
}

func TestVGatewayServiceDeleteStopsRuntimeBeforePersistence(t *testing.T) {
	t.Parallel()

	gatewayID := uuid.New()
	client := &stubModbusClient{connected: true}
	connectedAt := time.Now()
	runtime := &vGatewayRuntime{
		client:      client,
		status:      domain.VGatewayStatusConnected,
		connectedAt: &connectedAt,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		deleteFunc: func(context.Context, uuid.UUID) error {
			if client.disconnectCallCount() != 1 {
				t.Fatalf(
					"repository Delete() ran after %d disconnects, want 1",
					client.disconnectCallCount(),
				)
			}
			return nil
		},
	}, nil)
	service.runtimes[gatewayID] = runtime

	if err := service.Delete(context.Background(), gatewayID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("Disconnect() calls = %d, want 1", client.disconnectCallCount())
	}
	if _, exists := service.runtimes[gatewayID]; exists {
		t.Fatal("deleted gateway runtime remains registered")
	}
}

func TestVGatewayServiceDeleteAbortsWhenRuntimeCannotStop(t *testing.T) {
	t.Parallel()

	disconnectErr := errors.New("close failed")
	gatewayID := uuid.New()
	client := &stubModbusClient{
		connected:     true,
		disconnectErr: disconnectErr,
	}
	runtime := &vGatewayRuntime{
		client:     client,
		status:     domain.VGatewayStatusConnected,
		errorCount: 4,
	}
	repositoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		deleteFunc: func(context.Context, uuid.UUID) error {
			repositoryCalls++
			return nil
		},
	}, nil)
	service.runtimes[gatewayID] = runtime

	err := service.Delete(context.Background(), gatewayID)
	if !errors.Is(err, disconnectErr) {
		t.Fatalf("Delete() error = %v, want wrapped disconnect error", err)
	}
	if !strings.Contains(err.Error(), "disconnect vGateway before delete") {
		t.Fatalf("Delete() error = %q, want operation detail", err)
	}
	if repositoryCalls != 0 {
		t.Fatalf("repository Delete() calls = %d, want 0", repositoryCalls)
	}
	if service.runtimes[gatewayID] != runtime {
		t.Fatal("failed disconnect removed runtime registry entry")
	}
	if runtime.client != nil {
		t.Fatal("failed disconnect retained an unusable client")
	}
	if runtime.status != domain.VGatewayStatusError {
		t.Fatalf("runtime status = %q, want error", runtime.status)
	}
	if !errors.Is(runtime.lastError, disconnectErr) {
		t.Fatalf("runtime last error = %v, want %v", runtime.lastError, disconnectErr)
	}
	if runtime.errorCount != 5 {
		t.Fatalf("runtime error count = %d, want 5", runtime.errorCount)
	}
}

func TestVGatewayServiceDeletePreservesStoppedRuntimeWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	gatewayID := uuid.New()
	client := &stubModbusClient{connected: true}
	connectedAt := time.Now()
	runtime := &vGatewayRuntime{
		client:      client,
		status:      domain.VGatewayStatusConnected,
		connectedAt: &connectedAt,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		deleteFunc: func(context.Context, uuid.UUID) error {
			return databaseErr
		},
	}, nil)
	service.runtimes[gatewayID] = runtime

	err := service.Delete(context.Background(), gatewayID)
	if !errors.Is(err, databaseErr) {
		t.Fatalf("Delete() error = %v, want wrapped repository error", err)
	}
	if !strings.Contains(err.Error(), "delete vGateway") {
		t.Fatalf("Delete() error = %q, want operation detail", err)
	}
	if service.runtimes[gatewayID] != runtime {
		t.Fatal("failed persistence removed runtime registry entry")
	}
	if client.disconnectCallCount() != 1 || runtime.client != nil {
		t.Fatal("failed persistence did not leave runtime safely disconnected")
	}
	if runtime.status != domain.VGatewayStatusDisconnected {
		t.Fatalf("runtime status = %q, want disconnected", runtime.status)
	}
	if runtime.connectedAt != nil || runtime.lastError != nil {
		t.Fatalf("runtime connection state = %#v, want clean disconnect", runtime)
	}
}

func TestVGatewayServiceDeleteMapsRepositoryErrorsWithoutRuntime(t *testing.T) {
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
			name:      "unexpected error",
			repoError: databaseErr,
			wantError: databaseErr,
			wantText:  "delete vGateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestVGatewayService(t, &stubVGatewayRepository{
				deleteFunc: func(context.Context, uuid.UUID) error {
					return tt.repoError
				},
			}, nil)

			err := service.Delete(context.Background(), uuid.New())
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Delete() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Delete() error = %q, want detail %q", err, tt.wantText)
			}
		})
	}
}

func TestVGatewayServiceDeleteDoesNotRemoveReplacementRuntime(t *testing.T) {
	t.Parallel()

	gatewayID := uuid.New()
	oldClient := &stubModbusClient{connected: true}
	oldRuntime := &vGatewayRuntime{
		client: oldClient,
		status: domain.VGatewayStatusConnected,
	}
	replacementClient := &stubModbusClient{connected: true}
	replacementRuntime := &vGatewayRuntime{
		client: replacementClient,
		status: domain.VGatewayStatusConnected,
	}

	var service *vGatewayService
	service = newTestVGatewayService(t, &stubVGatewayRepository{
		deleteFunc: func(context.Context, uuid.UUID) error {
			service.mu.Lock()
			service.runtimes[gatewayID] = replacementRuntime
			service.mu.Unlock()
			return nil
		},
	}, nil)
	service.runtimes[gatewayID] = oldRuntime

	if err := service.Delete(context.Background(), gatewayID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if service.runtimes[gatewayID] != replacementRuntime {
		t.Fatal("Delete() removed a runtime registered during persistence")
	}
	if oldClient.disconnectCallCount() != 1 {
		t.Fatalf("old client Disconnect() calls = %d, want 1", oldClient.disconnectCallCount())
	}
	if replacementClient.disconnectCallCount() != 0 {
		t.Fatalf("replacement client Disconnect() calls = %d, want 0", replacementClient.disconnectCallCount())
	}
}
