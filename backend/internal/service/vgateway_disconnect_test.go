package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func TestVGatewayServiceDisconnectStopsClientAndPreservesStatistics(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestKey contextKey = "request"
	ctx := context.WithValue(context.Background(), requestKey, "disconnect-1")
	gateway := existingVGateway()
	client := &stubModbusClient{connected: true}
	connectedAt := gateway.CreatedAt.Add(time.Minute)
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
		findByIDFunc: func(gotCtx context.Context, id uuid.UUID) (*domain.VGateway, error) {
			if gotCtx.Value(requestKey) != "disconnect-1" {
				t.Fatalf("FindByID() context value = %v, want disconnect-1", gotCtx.Value(requestKey))
			}
			if id != gateway.ID {
				t.Fatalf("FindByID() ID = %v, want %v", id, gateway.ID)
			}
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	if err := service.Disconnect(ctx, gateway.ID); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if client.disconnectCallCount() != 1 || client.IsConnected() {
		t.Fatalf("client disconnect state = calls %d connected %v, want 1/false", client.disconnectCallCount(), client.IsConnected())
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.client != nil || runtime.status != domain.VGatewayStatusDisconnected {
		t.Fatalf("runtime client/status = (%T, %q), want nil/disconnected", runtime.client, runtime.status)
	}
	if runtime.connectedAt != nil || runtime.lastError != nil {
		t.Fatalf("runtime connection state = (%v, %v), want cleared", runtime.connectedAt, runtime.lastError)
	}
	if runtime.lastActivity != &lastActivity ||
		runtime.requestCount != 12 ||
		runtime.errorCount != 2 ||
		runtime.bytesReceived != 48 ||
		runtime.totalLatency != 75*time.Millisecond {
		t.Fatal("Disconnect() reset accumulated runtime statistics")
	}
}

func TestVGatewayServiceDisconnectWithoutRuntimeIsIdempotent(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	findCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			findCalls++
			return gateway, nil
		},
	}, nil)

	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("first Disconnect() error = %v", err)
	}
	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("second Disconnect() error = %v", err)
	}
	if findCalls != 2 {
		t.Fatalf("FindByID() calls = %d, want 2", findCalls)
	}
	if len(service.runtimes) != 0 {
		t.Fatalf("runtime registry length = %d, want 0", len(service.runtimes))
	}
}

func TestVGatewayServiceDisconnectIsIdempotentWithExistingRuntime(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	client := &stubModbusClient{connected: true}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("first Disconnect() error = %v", err)
	}
	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("second Disconnect() error = %v", err)
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("Disconnect() calls = %d, want idempotent 1", client.disconnectCallCount())
	}
}

func TestVGatewayServiceDisconnectProjectsStoppedForDisabledGateway(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	gateway.Enabled = false
	client := &stubModbusClient{connected: true}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.status != domain.VGatewayStatusStopped {
		t.Fatalf("runtime status = %q, want stopped", runtime.status)
	}
}

func TestVGatewayServiceDisconnectRecordsClientFailure(t *testing.T) {
	t.Parallel()

	disconnectErr := errors.New("close failed")
	gateway := existingVGateway()
	client := &stubModbusClient{
		connected:     true,
		disconnectErr: disconnectErr,
	}
	runtime := &vGatewayRuntime{
		client:     client,
		status:     domain.VGatewayStatusConnected,
		errorCount: 3,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	err := service.Disconnect(context.Background(), gateway.ID)
	if !errors.Is(err, disconnectErr) {
		t.Fatalf("Disconnect() error = %v, want wrapped close error", err)
	}
	if !strings.Contains(err.Error(), "disconnect vGateway") {
		t.Fatalf("Disconnect() error = %q, want operation detail", err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.client != nil || runtime.connectedAt != nil {
		t.Fatal("failed Disconnect() retained unusable connection state")
	}
	if runtime.status != domain.VGatewayStatusError ||
		!errors.Is(runtime.lastError, disconnectErr) ||
		runtime.errorCount != 4 {
		t.Fatalf("runtime error state = (%q, %v, %d), want error/close failure/4", runtime.status, runtime.lastError, runtime.errorCount)
	}
}

func TestVGatewayServiceDisconnectMapsLookupErrorsWithoutRuntimeMutation(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name      string
		findError error
		wantError error
		wantText  string
	}{
		{
			name:      "not found",
			findError: repository.ErrVGatewayNotFound,
			wantError: ErrVGatewayNotFound,
		},
		{
			name:      "unexpected error",
			findError: databaseErr,
			wantError: databaseErr,
			wantText:  "find vGateway for disconnect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gatewayID := uuid.New()
			client := &stubModbusClient{connected: true}
			runtime := &vGatewayRuntime{
				client: client,
				status: domain.VGatewayStatusConnected,
			}
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
					return nil, tt.findError
				},
			}, nil)
			service.runtimes[gatewayID] = runtime

			err := service.Disconnect(context.Background(), gatewayID)
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Disconnect() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Disconnect() error = %q, want detail %q", err, tt.wantText)
			}
			if runtime.client != client || client.disconnectCallCount() != 0 {
				t.Fatal("lookup failure mutated active runtime")
			}
		})
	}
}

func TestVGatewayServiceDisconnectSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	disconnectStarted := make(chan struct{}, 1)
	disconnectRelease := make(chan struct{})
	client := &stubModbusClient{
		connected:       true,
		disconnectStart: disconnectStarted,
		disconnectWait:  disconnectRelease,
	}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	const callers = 12
	errorsByCaller := make([]error, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := range callers {
		go func() {
			defer waitGroup.Done()
			errorsByCaller[index] = service.Disconnect(
				context.Background(),
				gateway.ID,
			)
		}()
	}

	select {
	case <-disconnectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("client Disconnect() did not start")
	}
	close(disconnectRelease)
	waitGroup.Wait()

	for index, err := range errorsByCaller {
		if err != nil {
			t.Fatalf("Disconnect() caller %d error = %v", index, err)
		}
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("client Disconnect() calls = %d, want serialized 1", client.disconnectCallCount())
	}
	if status := service.statusForGateway(gateway.ID, true); status != domain.VGatewayStatusDisconnected {
		t.Fatalf("final status = %q, want disconnected", status)
	}
}

func TestVGatewayServiceDisconnectHonorsContextWhileWaitingForLifecycle(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	lookupComplete := make(chan struct{}, 1)
	client := &stubModbusClient{connected: true}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			lookupComplete <- struct{}{}
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime
	runtime.operationMu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- service.Disconnect(ctx, gateway.ID)
	}()
	select {
	case <-lookupComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect() did not finish gateway lookup")
	}
	cancel()
	runtime.operationMu.Unlock()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Disconnect() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect() did not return after lifecycle lock released")
	}
	if client.disconnectCallCount() != 0 || runtime.client != client {
		t.Fatal("canceled Disconnect() mutated active runtime")
	}
}

func TestVGatewayServiceDisconnectDoesNotRequireRegisteredDriver(t *testing.T) {
	t.Parallel()

	customType := domain.VGatewayType("mqtt")
	gateway := existingVGateway()
	gateway.Type = customType
	client := &stubModbusClient{connected: true}
	runtime := &vGatewayRuntime{
		client: client,
		status: domain.VGatewayStatusConnected,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*domain.VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	if err := service.Disconnect(context.Background(), gateway.ID); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if client.disconnectCallCount() != 1 {
		t.Fatalf("client Disconnect() calls = %d, want 1", client.disconnectCallCount())
	}
}
