package vgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func (s *vGatewayService) Create(
	ctx context.Context,
	input CreateVGatewayInput,
) (*VGateway, error) {
	name := strings.TrimSpace(input.Name)
	if err := validateVGatewayName(name); err != nil {
		return nil, err
	}

	driver, err := s.driverFor(input.Type)
	if err != nil {
		return nil, err
	}
	config, err := normalizeVGatewayConfig(driver, input.Config)
	if err != nil {
		return nil, err
	}

	gateway := &VGateway{
		Name:        name,
		Type:        input.Type,
		Description: normalizeVGatewayDescription(input.Description),
		Enabled:     valueOrDefault(input.Enabled, true),
		Config:      config,
	}

	if err := s.gateways.Create(ctx, gateway); err != nil {
		if errors.Is(err, ErrVGatewayNameExists) {
			return nil, fmt.Errorf(
				"%w: %q",
				ErrVGatewayNameExists,
				name,
			)
		}
		return nil, fmt.Errorf("create vGateway: %w", err)
	}

	return gateway, nil
}

func (s *vGatewayService) Get(
	ctx context.Context,
	id uuid.UUID,
) (*VGatewayView, error) {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrVGatewayNotFound) {
			return nil, ErrVGatewayNotFound
		}
		return nil, fmt.Errorf("get vGateway: %w", err)
	}

	return s.viewForGateway(*gateway), nil
}

func (s *vGatewayService) List(
	ctx context.Context,
	input VGatewayListInput,
) (*VGatewayListResult, error) {
	page := input.Page
	if page <= 0 {
		page = DefaultVGatewayPage
	}

	perPage := input.PerPage
	if perPage <= 0 {
		perPage = DefaultVGatewayPerPage
	}
	if perPage > MaxVGatewayPerPage {
		perPage = MaxVGatewayPerPage
	}

	if input.Type != nil {
		if _, err := s.driverFor(*input.Type); err != nil {
			return nil, err
		}
	}

	offset := (page - 1) * perPage
	gateways, total, err := s.gateways.List(
		ctx,
		VGatewayListOptions{
			Type:    input.Type,
			Enabled: input.Enabled,
			Limit:   perPage,
			Offset:  offset,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list vGateways: %w", err)
	}

	views := make([]VGatewayView, 0, len(gateways))
	for _, gateway := range gateways {
		views = append(views, *s.viewForGateway(gateway))
	}

	totalPages := int(
		(total + int64(perPage) - 1) / int64(perPage),
	)

	return &VGatewayListResult{
		Data:       views,
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

func (s *vGatewayService) Update(
	ctx context.Context,
	id uuid.UUID,
	input UpdateVGatewayInput,
) (*VGatewayView, error) {
	gateway, err := s.gateways.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrVGatewayNotFound) {
			return nil, ErrVGatewayNotFound
		}
		return nil, fmt.Errorf("find vGateway for update: %w", err)
	}

	previousConfig := cloneVGatewayConfig(gateway.Config)
	previousEnabled := gateway.Enabled
	changed := false

	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if err := validateVGatewayName(name); err != nil {
			return nil, err
		}
		gateway.Name = name
		changed = true
	}

	if input.Description.Set {
		gateway.Description = normalizeVGatewayDescription(
			input.Description.Value,
		)
		changed = true
	}

	if input.Enabled != nil {
		gateway.Enabled = *input.Enabled
		changed = true
	}

	if input.Config != nil {
		driver, err := s.driverFor(gateway.Type)
		if err != nil {
			return nil, err
		}
		config, err := normalizeVGatewayConfig(driver, *input.Config)
		if err != nil {
			return nil, err
		}
		gateway.Config = config
		changed = true
	}

	configChanged := !equalVGatewayConfig(
		gateway.Config,
		previousConfig,
	)
	enabledChanged := gateway.Enabled != previousEnabled

	if changed {
		if err := s.gateways.Update(ctx, gateway); err != nil {
			if errors.Is(err, ErrVGatewayNotFound) {
				return nil, ErrVGatewayNotFound
			}
			if errors.Is(err, ErrVGatewayNameExists) {
				return nil, fmt.Errorf(
					"%w: %q",
					ErrVGatewayNameExists,
					gateway.Name,
				)
			}
			return nil, fmt.Errorf("update vGateway: %w", err)
		}

		if configChanged || enabledChanged {
			s.reconcileRuntimeAfterUpdate(
				gateway.ID,
				gateway.Enabled,
			)
		}
	}

	return s.viewForGateway(*gateway), nil
}

func (s *vGatewayService) Delete(
	ctx context.Context,
	id uuid.UUID,
) error {
	s.mu.RLock()
	runtime := s.runtimes[id]
	s.mu.RUnlock()

	if runtime != nil {
		runtime.operationMu.Lock()
		defer runtime.operationMu.Unlock()

		runtime.mu.Lock()
		client := runtime.client
		runtime.mu.Unlock()

		if !gatewayClientIsNil(client) {
			disconnectErr := client.Disconnect()
			runtime.mu.Lock()
			runtime.client = nil
			runtime.connectedAt = nil
			runtime.lastError = disconnectErr

			if disconnectErr != nil {
				runtime.status = VGatewayStatusError
				runtime.errorCount++
				runtime.mu.Unlock()
				return fmt.Errorf(
					"disconnect vGateway before delete: %w",
					disconnectErr,
				)
			}
			runtime.mu.Unlock()
		}

		runtime.mu.Lock()
		runtime.client = nil
		runtime.connectedAt = nil
		runtime.lastError = nil
		runtime.status = VGatewayStatusDisconnected
		runtime.mu.Unlock()
	}

	if err := s.gateways.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrVGatewayNotFound) {
			return ErrVGatewayNotFound
		}
		return fmt.Errorf("delete vGateway: %w", err)
	}

	if runtime != nil {
		s.mu.Lock()
		if s.runtimes[id] == runtime {
			delete(s.runtimes, id)
		}
		s.mu.Unlock()
	}

	return nil
}

func normalizeVGatewayConfig(
	driver protocol.GatewayDriver,
	raw VGatewayConfig,
) (VGatewayConfig, error) {
	config, err := driver.NormalizeConfig(raw)
	if err == nil {
		return config, nil
	}
	if errors.Is(err, protocol.ErrInvalidGatewayConfig) {
		return nil, fmt.Errorf(
			"%w: %w",
			ErrInvalidVGatewayConfig,
			err,
		)
	}
	return nil, fmt.Errorf("normalize vGateway config: %w", err)
}

func validateVGatewayName(name string) error {
	if name == "" {
		return fmt.Errorf(
			"%w: name is required",
			ErrInvalidVGatewayName,
		)
	}
	if utf8.RuneCountInString(name) > 100 {
		return fmt.Errorf(
			"%w: name must not exceed 100 characters",
			ErrInvalidVGatewayName,
		)
	}
	return nil
}

func normalizeVGatewayDescription(description *string) *string {
	if description == nil {
		return nil
	}
	normalized := strings.TrimSpace(*description)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func valueOrDefault[T any](value *T, defaultValue T) T {
	if value == nil {
		return defaultValue
	}
	return *value
}

func cloneVGatewayConfig(config VGatewayConfig) VGatewayConfig {
	return append(VGatewayConfig(nil), config...)
}

func equalVGatewayConfig(left, right VGatewayConfig) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil ||
		json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
