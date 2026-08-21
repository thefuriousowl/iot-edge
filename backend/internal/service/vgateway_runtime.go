package service

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

type vGatewayRuntime struct {
	operationMu sync.Mutex
	mu          sync.Mutex

	client       protocol.GatewayClient
	status       domain.VGatewayConnectionStatus
	connectedAt  *time.Time
	lastActivity *time.Time
	lastError    error

	requestCount  int64
	errorCount    int64
	bytesReceived int64
	totalLatency  time.Duration
}

func (s *vGatewayService) runtimeForGateway(
	id uuid.UUID,
) *vGatewayRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()

	runtime := s.runtimes[id]
	if runtime == nil {
		runtime = &vGatewayRuntime{
			status: domain.VGatewayStatusDisconnected,
		}
		s.runtimes[id] = runtime
	}
	return runtime
}

func (s *vGatewayService) reconcileRuntimeAfterUpdate(
	id uuid.UUID,
	enabled bool,
) {
	s.mu.RLock()
	runtime := s.runtimes[id]
	s.mu.RUnlock()

	if runtime == nil {
		return
	}

	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()

	runtime.mu.Lock()
	client := runtime.client
	runtime.mu.Unlock()

	var disconnectErr error
	if client != nil {
		disconnectErr = client.Disconnect()
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.client = nil
	runtime.connectedAt = nil
	runtime.lastError = disconnectErr

	if !enabled {
		runtime.status = domain.VGatewayStatusStopped
	} else if disconnectErr != nil {
		runtime.status = domain.VGatewayStatusError
	} else {
		runtime.status = domain.VGatewayStatusDisconnected
	}

	if disconnectErr != nil {
		runtime.errorCount++
	}
}

func recordRuntimeConnectionError(
	runtime *vGatewayRuntime,
	err error,
) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.status = domain.VGatewayStatusError
	runtime.connectedAt = nil
	runtime.lastError = err
	runtime.errorCount++
}

func (s *vGatewayService) statusForGateway(
	id uuid.UUID,
	enabled bool,
) domain.VGatewayConnectionStatus {
	if !enabled {
		return domain.VGatewayStatusStopped
	}

	s.mu.RLock()
	runtime := s.runtimes[id]
	s.mu.RUnlock()

	if runtime == nil {
		return domain.VGatewayStatusDisconnected
	}

	runtime.mu.Lock()
	status := runtime.status
	runtime.mu.Unlock()

	if status == "" {
		return domain.VGatewayStatusDisconnected
	}
	return status
}

func (s *vGatewayService) viewForGateway(
	gateway domain.VGateway,
) *VGatewayView {
	gateway.Config = cloneVGatewayConfig(gateway.Config)
	return &VGatewayView{
		VGateway: gateway,
		Status: s.statusForGateway(
			gateway.ID,
			gateway.Enabled,
		),
	}
}
