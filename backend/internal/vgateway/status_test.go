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

func TestVGatewayServiceStatusReturnsDefaultsWithoutCreatingRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enabled    bool
		wantStatus VGatewayConnectionStatus
	}{
		{
			name:       "enabled gateway is disconnected",
			enabled:    true,
			wantStatus: VGatewayStatusDisconnected,
		},
		{
			name:       "disabled gateway is stopped",
			enabled:    false,
			wantStatus: VGatewayStatusStopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			type contextKey string
			const requestKey contextKey = "request"
			ctx := context.WithValue(
				context.Background(),
				requestKey,
				"status-1",
			)
			gateway := existingVGateway()
			gateway.Enabled = tt.enabled
			factoryCalls := 0
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(gotCtx context.Context, gotID uuid.UUID) (*VGateway, error) {
					if gotCtx.Value(requestKey) != "status-1" {
						t.Fatalf(
							"repository context value = %v, want status-1",
							gotCtx.Value(requestKey),
						)
					}
					if gotID != gateway.ID {
						t.Fatalf("repository ID = %s, want %s", gotID, gateway.ID)
					}
					return gateway, nil
				},
			}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
				factoryCalls++
				return nil, nil
			})

			got, err := service.Status(ctx, gateway.ID)
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if got.ID != gateway.ID || got.Status != tt.wantStatus {
				t.Fatalf(
					"Status() identity/status = (%s, %q), want (%s, %q)",
					got.ID,
					got.Status,
					gateway.ID,
					tt.wantStatus,
				)
			}
			if got.ConnectedAt != nil || got.LastActivity != nil {
				t.Fatalf(
					"Status() timestamps = (%v, %v), want nil",
					got.ConnectedAt,
					got.LastActivity,
				)
			}
			if got.Statistics.RequestCount != 0 ||
				got.Statistics.ErrorCount != 0 ||
				got.Statistics.BytesReceived != 0 ||
				got.Statistics.AvgLatencyMS != nil {
				t.Fatalf("Status() statistics = %#v, want zero values", got.Statistics)
			}
			assertUnknownVGatewayHealth(t, got.Health)
			if factoryCalls != 0 {
				t.Fatalf("client factory calls = %d, want 0", factoryCalls)
			}
			if len(service.runtimes) != 0 {
				t.Fatalf(
					"runtime registry length = %d, want 0",
					len(service.runtimes),
				)
			}
		})
	}
}

func TestVGatewayServiceStatusSnapshotsRuntimeStatistics(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	connectedAt := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	lastActivity := connectedAt.Add(5 * time.Minute)
	runtime := &vGatewayRuntime{
		client:        &statusPanicGatewayClient{},
		status:        VGatewayStatusConnected,
		connectedAt:   &connectedAt,
		lastActivity:  &lastActivity,
		requestCount:  2,
		errorCount:    1,
		bytesReceived: 128,
		totalLatency:  25 * time.Millisecond,
	}
	factoryCalls := 0
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		factoryCalls++
		return nil, nil
	})
	service.runtimes[gateway.ID] = runtime

	got, err := service.Status(context.Background(), gateway.ID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got.Status != VGatewayStatusConnected {
		t.Fatalf("Status() status = %q, want connected", got.Status)
	}
	if got.ConnectedAt == nil || !got.ConnectedAt.Equal(connectedAt) ||
		got.LastActivity == nil || !got.LastActivity.Equal(lastActivity) {
		t.Fatalf(
			"Status() timestamps = (%v, %v), want (%v, %v)",
			got.ConnectedAt,
			got.LastActivity,
			connectedAt,
			lastActivity,
		)
	}
	if got.ConnectedAt == runtime.connectedAt || got.LastActivity == runtime.lastActivity {
		t.Fatal("Status() returned pointers that alias runtime timestamps")
	}
	if got.Statistics.RequestCount != 2 ||
		got.Statistics.ErrorCount != 1 ||
		got.Statistics.BytesReceived != 128 ||
		got.Statistics.AvgLatencyMS == nil ||
		*got.Statistics.AvgLatencyMS != 12.5 {
		t.Fatalf("Status() statistics = %#v, want average latency 12.5ms", got.Statistics)
	}
	assertUnknownVGatewayHealth(t, got.Health)
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}

	*got.ConnectedAt = got.ConnectedAt.Add(time.Hour)
	*got.LastActivity = got.LastActivity.Add(time.Hour)
	if !runtime.connectedAt.Equal(connectedAt) ||
		!runtime.lastActivity.Equal(lastActivity) {
		t.Fatal("caller timestamp mutation escaped into runtime state")
	}
}

func TestVGatewayServiceStatusProjectsEffectiveState(t *testing.T) {
	t.Parallel()

	connectedAt := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	lastActivity := connectedAt.Add(time.Minute)
	tests := []struct {
		name          string
		enabled       bool
		runtimeStatus VGatewayConnectionStatus
		wantStatus    VGatewayConnectionStatus
	}{
		{
			name:          "disabled gateway overrides stale connected runtime",
			enabled:       false,
			runtimeStatus: VGatewayStatusConnected,
			wantStatus:    VGatewayStatusStopped,
		},
		{
			name:          "blank runtime status falls back to disconnected",
			enabled:       true,
			runtimeStatus: "",
			wantStatus:    VGatewayStatusDisconnected,
		},
		{
			name:          "error runtime remains error",
			enabled:       true,
			runtimeStatus: VGatewayStatusError,
			wantStatus:    VGatewayStatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gateway := existingVGateway()
			gateway.Enabled = tt.enabled
			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return gateway, nil
				},
			}, nil)
			service.runtimes[gateway.ID] = &vGatewayRuntime{
				status:        tt.runtimeStatus,
				connectedAt:   &connectedAt,
				lastActivity:  &lastActivity,
				requestCount:  4,
				errorCount:    2,
				bytesReceived: 64,
				totalLatency:  40 * time.Millisecond,
			}

			got, err := service.Status(context.Background(), gateway.ID)
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("Status() status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.ConnectedAt != nil {
				t.Fatalf(
					"Status() connected at = %v for %q state, want nil",
					got.ConnectedAt,
					got.Status,
				)
			}
			if got.LastActivity == nil || !got.LastActivity.Equal(lastActivity) {
				t.Fatalf("Status() last activity = %v, want %v", got.LastActivity, lastActivity)
			}
			if got.Statistics.RequestCount != 4 ||
				got.Statistics.ErrorCount != 2 ||
				got.Statistics.BytesReceived != 64 ||
				got.Statistics.AvgLatencyMS == nil ||
				*got.Statistics.AvgLatencyMS != 10 {
				t.Fatalf("Status() statistics = %#v, want preserved values", got.Statistics)
			}
			assertUnknownVGatewayHealth(t, got.Health)
		})
	}
}

func TestVGatewayServiceStatusMapsRepositoryErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name       string
		gateway    *VGateway
		repoError  error
		wantError  error
		wantDetail string
	}{
		{
			name:      "not found",
			repoError: ErrVGatewayNotFound,
			wantError: ErrVGatewayNotFound,
		},
		{
			name:       "database failure",
			repoError:  databaseErr,
			wantError:  databaseErr,
			wantDetail: "get vGateway status",
		},
		{
			name:       "nil repository result",
			gateway:    nil,
			wantDetail: "repository returned nil gateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestVGatewayService(t, &stubVGatewayRepository{
				findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
					return tt.gateway, tt.repoError
				},
			}, nil)
			result, err := service.Status(context.Background(), uuid.New())
			if result != nil {
				t.Fatalf("Status() result = %#v, want nil", result)
			}
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Fatalf("Status() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantDetail != "" && (err == nil || !strings.Contains(err.Error(), tt.wantDetail)) {
				t.Fatalf("Status() error = %v, want detail %q", err, tt.wantDetail)
			}
			if err == nil {
				t.Fatal("Status() error = nil, want repository result error")
			}
		})
	}
}

func TestVGatewayServiceStatusObservesConnectingWithoutWaitingForLifecycle(t *testing.T) {
	t.Parallel()

	gateway := existingVGateway()
	connectStarted := make(chan struct{}, 1)
	connectRelease := make(chan struct{})
	client := &stubModbusClient{
		connectStarted: connectStarted,
		connectRelease: connectRelease,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		return client, nil
	})

	connectResult := make(chan error, 1)
	go func() {
		connectResult <- service.Connect(context.Background(), gateway.ID)
	}()

	select {
	case <-connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("client Connect() did not start")
	}

	statusResult := make(chan *VGatewayStatusResult, 1)
	statusError := make(chan error, 1)
	go func() {
		result, err := service.Status(context.Background(), gateway.ID)
		statusResult <- result
		statusError <- err
	}()

	select {
	case result := <-statusResult:
		if err := <-statusError; err != nil {
			close(connectRelease)
			<-connectResult
			t.Fatalf("Status() error = %v", err)
		}
		if result.Status != VGatewayStatusConnecting {
			close(connectRelease)
			<-connectResult
			t.Fatalf("Status() status = %q, want connecting", result.Status)
		}
	case <-time.After(2 * time.Second):
		close(connectRelease)
		<-connectResult
		t.Fatal("Status() waited for the lifecycle operation")
	}

	close(connectRelease)
	if err := <-connectResult; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	connected, err := service.Status(context.Background(), gateway.ID)
	if err != nil {
		t.Fatalf("final Status() error = %v", err)
	}
	if connected.Status != VGatewayStatusConnected || connected.ConnectedAt == nil {
		t.Fatalf("final Status() = %#v, want connected with timestamp", connected)
	}
}

func TestVGatewayServiceStatusReturnsCoherentConcurrentSnapshots(t *testing.T) {
	gateway := existingVGateway()
	firstConnectedAt := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	firstActivity := firstConnectedAt.Add(time.Minute)
	secondActivity := firstActivity.Add(time.Minute)
	runtime := &vGatewayRuntime{
		status:        VGatewayStatusConnected,
		connectedAt:   &firstConnectedAt,
		lastActivity:  &firstActivity,
		requestCount:  1,
		bytesReceived: 10,
		totalLatency:  10 * time.Millisecond,
	}
	service := newTestVGatewayService(t, &stubVGatewayRepository{
		findByIDFunc: func(context.Context, uuid.UUID) (*VGateway, error) {
			return gateway, nil
		},
	}, nil)
	service.runtimes[gateway.ID] = runtime

	const iterations = 2000
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for index := range iterations {
			runtime.mu.Lock()
			if index%2 == 0 {
				runtime.status = VGatewayStatusConnected
				runtime.connectedAt = &firstConnectedAt
				runtime.lastActivity = &firstActivity
				runtime.requestCount = 1
				runtime.errorCount = 0
				runtime.bytesReceived = 10
				runtime.totalLatency = 10 * time.Millisecond
			} else {
				runtime.status = VGatewayStatusError
				runtime.connectedAt = nil
				runtime.lastActivity = &secondActivity
				runtime.requestCount = 2
				runtime.errorCount = 1
				runtime.bytesReceived = 20
				runtime.totalLatency = 40 * time.Millisecond
			}
			runtime.mu.Unlock()
		}
	}()

	for range iterations {
		got, err := service.Status(context.Background(), gateway.ID)
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		switch got.Statistics.RequestCount {
		case 1:
			if got.Status != VGatewayStatusConnected ||
				got.ConnectedAt == nil ||
				got.LastActivity == nil || !got.LastActivity.Equal(firstActivity) ||
				got.Statistics.ErrorCount != 0 ||
				got.Statistics.BytesReceived != 10 ||
				got.Statistics.AvgLatencyMS == nil ||
				*got.Statistics.AvgLatencyMS != 10 {
				t.Fatalf("Status() returned torn first snapshot: %#v", got)
			}
		case 2:
			if got.Status != VGatewayStatusError ||
				got.ConnectedAt != nil ||
				got.LastActivity == nil || !got.LastActivity.Equal(secondActivity) ||
				got.Statistics.ErrorCount != 1 ||
				got.Statistics.BytesReceived != 20 ||
				got.Statistics.AvgLatencyMS == nil ||
				*got.Statistics.AvgLatencyMS != 20 {
				t.Fatalf("Status() returned torn second snapshot: %#v", got)
			}
		default:
			t.Fatalf(
				"Status() request count = %d, want coherent 1 or 2",
				got.Statistics.RequestCount,
			)
		}
	}
	writer.Wait()
}

func TestAverageLatencyMilliseconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		totalLatency time.Duration
		requestCount int64
		want         *float64
	}{
		{name: "no requests", totalLatency: time.Second, requestCount: 0},
		{name: "negative requests", totalLatency: time.Second, requestCount: -1},
		{name: "negative latency", totalLatency: -time.Millisecond, requestCount: 1},
		{name: "zero latency", requestCount: 2, want: float64Pointer(0)},
		{
			name:         "fractional milliseconds",
			totalLatency: 25 * time.Millisecond,
			requestCount: 2,
			want:         float64Pointer(12.5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := averageLatencyMilliseconds(tt.totalLatency, tt.requestCount)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("averageLatencyMilliseconds() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("averageLatencyMilliseconds() = %v, want %v", got, *tt.want)
			}
		})
	}
}

func TestVGatewayStatusResultJSONContract(t *testing.T) {
	t.Parallel()

	result := VGatewayStatusResult{
		ID:     uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Status: VGatewayStatusDisconnected,
		Statistics: VGatewayStatusStatistics{
			RequestCount: 3,
		},
		Health: VGatewayHealth{Status: VGatewayHealthUnknown},
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, field := range []string{
		"id",
		"status",
		"connected_at",
		"last_activity",
		"statistics",
		"health",
	} {
		if _, exists := decoded[field]; !exists {
			t.Fatalf("JSON field %q is missing from %s", field, payload)
		}
	}
	statistics, ok := decoded["statistics"].(map[string]any)
	if !ok {
		t.Fatalf("JSON statistics = %#v, want object", decoded["statistics"])
	}
	for _, field := range []string{
		"request_count",
		"error_count",
		"bytes_received",
		"avg_latency_ms",
	} {
		if _, exists := statistics[field]; !exists {
			t.Fatalf("statistics field %q is missing from %s", field, payload)
		}
	}
	health, ok := decoded["health"].(map[string]any)
	if !ok {
		t.Fatalf("JSON health = %#v, want object", decoded["health"])
	}
	if health["status"] != string(VGatewayHealthUnknown) ||
		health["last_check"] != nil || health["latency_ms"] != nil {
		t.Fatalf("JSON health = %#v, want unknown with null measurements", health)
	}
}

func assertUnknownVGatewayHealth(t *testing.T, health VGatewayHealth) {
	t.Helper()
	if health.Status != VGatewayHealthUnknown ||
		health.LastCheck != nil || health.LatencyMS != nil {
		t.Fatalf("health = %#v, want unknown with nil measurements", health)
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}

type statusPanicGatewayClient struct{}

var _ protocol.GatewayClient = (*statusPanicGatewayClient)(nil)

func (*statusPanicGatewayClient) Connect(context.Context) error {
	panic("Status called GatewayClient.Connect")
}

func (*statusPanicGatewayClient) Disconnect() error {
	panic("Status called GatewayClient.Disconnect")
}

func (*statusPanicGatewayClient) IsConnected() bool {
	panic("Status called GatewayClient.IsConnected")
}
