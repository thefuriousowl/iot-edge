package vgateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type vGatewayStatusSnapshot struct {
	status        VGatewayConnectionStatus
	connectedAt   *time.Time
	lastActivity  *time.Time
	requestCount  int64
	errorCount    int64
	bytesReceived int64
	totalLatency  time.Duration
}

func (s *vGatewayService) Status(
	ctx context.Context,
	id uuid.UUID,
) (*VGatewayStatusResult, error) {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrVGatewayNotFound) {
			return nil, ErrVGatewayNotFound
		}
		return nil, fmt.Errorf("get vGateway status: %w", err)
	}
	if gateway == nil {
		return nil, errors.New(
			"get vGateway status: repository returned nil gateway",
		)
	}

	snapshot, found := s.snapshotVGatewayStatus(id)

	status := VGatewayStatusDisconnected
	if !gateway.Enabled {
		status = VGatewayStatusStopped
	} else if found && snapshot.status != "" {
		status = snapshot.status
	}

	var connectedAt *time.Time
	var lastActivity *time.Time
	statistics := VGatewayStatusStatistics{}

	if found {
		if status == VGatewayStatusConnected {
			connectedAt = snapshot.connectedAt
		}
		lastActivity = snapshot.lastActivity
		statistics = VGatewayStatusStatistics{
			RequestCount:  snapshot.requestCount,
			ErrorCount:    snapshot.errorCount,
			BytesReceived: snapshot.bytesReceived,
			AvgLatencyMS: averageLatencyMilliseconds(
				snapshot.totalLatency,
				snapshot.requestCount,
			),
		}
	}

	return &VGatewayStatusResult{
		ID:           gateway.ID,
		Status:       status,
		ConnectedAt:  connectedAt,
		LastActivity: lastActivity,
		Statistics:   statistics,
		Health: VGatewayHealth{
			Status: VGatewayHealthUnknown,
		},
	}, nil
}

func (s *vGatewayService) snapshotVGatewayStatus(
	id uuid.UUID,
) (vGatewayStatusSnapshot, bool) {
	s.mu.RLock()
	runtime := s.runtimes[id]
	s.mu.RUnlock()

	if runtime == nil {
		return vGatewayStatusSnapshot{}, false
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	return vGatewayStatusSnapshot{
		status:        runtime.status,
		connectedAt:   cloneTimePointer(runtime.connectedAt),
		lastActivity:  cloneTimePointer(runtime.lastActivity),
		requestCount:  runtime.requestCount,
		errorCount:    runtime.errorCount,
		bytesReceived: runtime.bytesReceived,
		totalLatency:  runtime.totalLatency,
	}, true
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func averageLatencyMilliseconds(
	totalLatency time.Duration,
	requestCount int64,
) *float64 {
	if requestCount <= 0 || totalLatency < 0 {
		return nil
	}

	average := float64(totalLatency) /
		float64(time.Millisecond) /
		float64(requestCount)

	return &average
}
