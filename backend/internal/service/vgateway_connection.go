package service

import (
	"context"
	"errors"
	"fmt"

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

	runtime := s.runtimeForGateway(id)
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("connect vGateway: %w", err)
	}

	runtime.mu.Lock()
	client := runtime.client
	if client != nil && client.IsConnected() {
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

	if client == nil {
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
		if client == nil {
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
