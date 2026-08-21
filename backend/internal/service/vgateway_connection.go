package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

func (s *vGatewayService) Connect(
	ctx context.Context,
	id uuid.UUID,
) error {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrVGatewayNotFound) {
			return ErrVGatewayNotFound
		}
		return fmt.Errorf("find vGateway for connect: %w", err)
	}
	if !gateway.Enabled {
		return ErrVGatewayDisabled
	}

	driver, err := s.driverFor(gateway.Type)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("connect vGateway: %w", err)
	}

	runtime := s.runtimeForGateway(id)
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("connect vGateway: %w", err)
	}

	runtime.mu.Lock()
	client := runtime.client
	if !gatewayClientIsNil(client) && client.IsConnected() {
		runtime.status = domain.VGatewayStatusConnected
		runtime.lastError = nil
		if runtime.connectedAt == nil {
			connectedAt := s.now()
			runtime.connectedAt = &connectedAt
		}
		runtime.mu.Unlock()
		return nil
	}
	runtime.status = domain.VGatewayStatusConnecting
	runtime.lastError = nil
	runtime.mu.Unlock()

	if gatewayClientIsNil(client) {
		client, err = driver.NewClient(
			cloneVGatewayConfig(gateway.Config),
		)
		if err != nil {
			recordRuntimeConnectionError(runtime, err)
			return fmt.Errorf(
				"create %s vGateway client: %w",
				gateway.Type,
				err,
			)
		}
		if gatewayClientIsNil(client) {
			err = protocol.ErrGatewayClientRequired
			recordRuntimeConnectionError(runtime, err)
			return fmt.Errorf(
				"create %s vGateway client: %w",
				gateway.Type,
				err,
			)
		}

		runtime.mu.Lock()
		runtime.client = client
		runtime.mu.Unlock()
	}

	if err := client.Connect(ctx); err != nil {
		recordRuntimeConnectionError(runtime, err)
		return fmt.Errorf("connect vGateway: %w", err)
	}

	connectedAt := s.now()
	runtime.mu.Lock()
	runtime.status = domain.VGatewayStatusConnected
	runtime.connectedAt = &connectedAt
	runtime.lastError = nil
	runtime.mu.Unlock()
	return nil
}

func (s *vGatewayService) Disconnect(
	ctx context.Context,
	id uuid.UUID,
) error {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrVGatewayNotFound) {
			return ErrVGatewayNotFound
		}
		return fmt.Errorf("find vGateway for disconnect: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("disconnect vGateway: %w", err)
	}

	s.mu.RLock()
	runtime := s.runtimes[id]
	s.mu.RUnlock()
	if runtime == nil {
		return nil
	}

	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("disconnect vGateway: %w", err)
	}

	runtime.mu.Lock()
	client := runtime.client
	runtime.mu.Unlock()

	var disconnectErr error
	if !gatewayClientIsNil(client) {
		disconnectErr = client.Disconnect()
	}

	runtime.mu.Lock()
	runtime.client = nil
	runtime.connectedAt = nil
	runtime.lastError = disconnectErr
	if disconnectErr != nil {
		runtime.status = domain.VGatewayStatusError
		runtime.errorCount++
	} else if gateway.Enabled {
		runtime.status = domain.VGatewayStatusDisconnected
	} else {
		runtime.status = domain.VGatewayStatusStopped
	}
	runtime.mu.Unlock()

	if disconnectErr != nil {
		return fmt.Errorf("disconnect vGateway: %w", disconnectErr)
	}
	return nil
}

func (s *vGatewayService) TestConnection(
	ctx context.Context,
	id uuid.UUID,
	options json.RawMessage,
) (*VGatewayConnectionTestResult, error) {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrVGatewayNotFound) {
			return nil, ErrVGatewayNotFound
		}
		return nil, fmt.Errorf(
			"find vGateway for connection test: %w",
			err,
		)
	}

	driver, err := s.driverFor(gateway.Type)
	if err != nil {
		return nil, err
	}
	probe, err := driver.PrepareConnectionTest(
		append(json.RawMessage(nil), options...),
	)
	if err != nil {
		if errors.Is(err, protocol.ErrInvalidGatewayTestOptions) {
			return nil, fmt.Errorf(
				"%w: %w",
				ErrInvalidVGatewayTestInput,
				err,
			)
		}
		return nil, fmt.Errorf(
			"prepare %s vGateway connection test: %w",
			gateway.Type,
			err,
		)
	}
	if probe == nil {
		return nil, fmt.Errorf(
			"prepare %s vGateway connection test: probe is required",
			gateway.Type,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("test vGateway connection: %w", err)
	}

	startedAt := s.now()
	client, clientErr := driver.NewClient(
		cloneVGatewayConfig(gateway.Config),
	)
	if clientErr != nil {
		clientErr = fmt.Errorf(
			"create temporary %s vGateway client: %w",
			gateway.Type,
			clientErr,
		)
		return failedConnectionTestResult(
			startedAt,
			s.now(),
			clientErr,
		), nil
	}
	if gatewayClientIsNil(client) {
		clientErr = fmt.Errorf(
			"create temporary %s vGateway client: %w",
			gateway.Type,
			protocol.ErrGatewayClientRequired,
		)
		return failedConnectionTestResult(
			startedAt,
			s.now(),
			clientErr,
		), nil
	}

	testErr := client.Connect(ctx)
	if testErr != nil {
		testErr = fmt.Errorf(
			"connect temporary vGateway test client: %w",
			testErr,
		)
	}
	if testErr == nil {
		testErr = probe(ctx, client)
	}
	finishedAt := s.now()
	disconnectErr := client.Disconnect()
	if disconnectErr != nil {
		disconnectErr = fmt.Errorf(
			"close temporary vGateway test client: %w",
			disconnectErr,
		)
		testErr = errors.Join(testErr, disconnectErr)
	}

	if testErr != nil {
		return failedConnectionTestResult(
			startedAt,
			finishedAt,
			testErr,
		), nil
	}
	return &VGatewayConnectionTestResult{
		Success: true,
		Latency: connectionTestLatency(startedAt, finishedAt),
	}, nil
}

func failedConnectionTestResult(
	startedAt time.Time,
	finishedAt time.Time,
	err error,
) *VGatewayConnectionTestResult {
	return &VGatewayConnectionTestResult{
		Success: false,
		Latency: connectionTestLatency(startedAt, finishedAt),
		Error:   err,
	}
}

func connectionTestLatency(
	startedAt time.Time,
	finishedAt time.Time,
) time.Duration {
	latency := finishedAt.Sub(startedAt)
	if latency < 0 {
		return 0
	}
	return latency
}
