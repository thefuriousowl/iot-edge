package vgatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
)

type Service interface {
	Create(context.Context, vgateway.CreateVGatewayInput) (*vgateway.VGateway, error)
	Get(context.Context, uuid.UUID) (*vgateway.VGatewayView, error)
	List(context.Context, vgateway.VGatewayListInput) (*vgateway.VGatewayListResult, error)
	Update(context.Context, uuid.UUID, vgateway.UpdateVGatewayInput) (*vgateway.VGatewayView, error)
	Delete(context.Context, uuid.UUID) error
}

type Handler struct {
	service Service
}

type createRequest struct {
	Name        string                `json:"name"`
	Type        vgateway.VGatewayType `json:"type"`
	Description *string               `json:"description"`
	Enabled     *bool                 `json:"enabled"`
	Config      json.RawMessage       `json:"config"`
}

type updateRequest struct {
	Name        *string         `json:"name"`
	Description json.RawMessage `json:"description"`
	Enabled     *bool           `json:"enabled"`
	Config      json.RawMessage `json:"config"`
}

type gatewayListItem struct {
	ID           uuid.UUID                         `json:"id"`
	Name         string                            `json:"name"`
	Type         vgateway.VGatewayType             `json:"type"`
	Description  *string                           `json:"description"`
	Enabled      bool                              `json:"enabled"`
	Status       vgateway.VGatewayConnectionStatus `json:"status"`
	DeviceCount  int                               `json:"device_count"`
	LastActivity *time.Time                        `json:"last_activity"`
	CreatedAt    time.Time                         `json:"created_at"`
	UpdatedAt    time.Time                         `json:"updated_at"`
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) List(c *fiber.Ctx) error {
	input, err := parseListInput(c)
	if err != nil {
		return validationError(c, "Invalid query parameters")
	}

	result, err := h.service.List(c.UserContext(), input)
	if err != nil {
		return handleServiceError(c, err)
	}

	items := make([]gatewayListItem, 0, len(result.Data))
	for _, gateway := range result.Data {
		items = append(items, gatewayListItem{
			ID:          gateway.ID,
			Name:        gateway.Name,
			Type:        gateway.Type,
			Description: gateway.Description,
			Enabled:     gateway.Enabled,
			Status:      gateway.Status,
			CreatedAt:   gateway.CreatedAt,
			UpdatedAt:   gateway.UpdatedAt,
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"data": items,
		"pagination": fiber.Map{
			"page":        result.Page,
			"per_page":    result.PerPage,
			"total":       result.Total,
			"total_pages": result.TotalPages,
		},
	})
}

func (h *Handler) Create(c *fiber.Ctx) error {
	var request createRequest
	if err := decodeRequest(c.Body(), &request); err != nil {
		return validationError(c, "Invalid request body")
	}

	gateway, err := h.service.Create(c.UserContext(), vgateway.CreateVGatewayInput{
		Name:        request.Name,
		Type:        request.Type,
		Description: request.Description,
		Enabled:     request.Enabled,
		Config:      request.Config,
	})
	if err != nil {
		return handleServiceError(c, err)
	}

	status := vgateway.VGatewayStatusDisconnected
	if !gateway.Enabled {
		status = vgateway.VGatewayStatusStopped
	}

	return c.Status(fiber.StatusCreated).JSON(vgateway.VGatewayView{
		VGateway: *gateway,
		Status:   status,
	})
}

func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	gateway, err := h.service.Get(c.UserContext(), id)
	if err != nil {
		return handleServiceError(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(gateway)
}

func (h *Handler) Update(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	var request updateRequest
	if err := decodeRequest(c.Body(), &request); err != nil {
		return validationError(c, "Invalid request body")
	}

	input := vgateway.UpdateVGatewayInput{
		Name:    request.Name,
		Enabled: request.Enabled,
	}
	if request.Description != nil {
		input.Description.Set = true
		if err := json.Unmarshal(request.Description, &input.Description.Value); err != nil {
			return validationError(c, "Invalid description")
		}
	}
	if request.Config != nil {
		input.Config = &request.Config
	}

	gateway, err := h.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleServiceError(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(gateway)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return handleServiceError(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func parseListInput(c *fiber.Ctx) (vgateway.VGatewayListInput, error) {
	var input vgateway.VGatewayListInput

	if value := c.Query("type"); value != "" {
		gatewayType := vgateway.VGatewayType(value)
		input.Type = &gatewayType
	}
	if value := c.Query("enabled"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return input, err
		}
		input.Enabled = &enabled
	}
	if value := c.Query("page"); value != "" {
		page, err := parsePositiveInteger(value)
		if err != nil {
			return input, err
		}
		input.Page = page
	}
	if value := c.Query("per_page"); value != "" {
		perPage, err := parsePositiveInteger(value)
		if err != nil {
			return input, err
		}
		input.PerPage = perPage
	}

	return input, nil
}

func parsePositiveInteger(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, errors.New("value must be a positive integer")
	}
	return number, nil
}

func decodeRequest(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON document")
	}
	return nil
}

func handleServiceError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, vgateway.ErrInvalidVGatewayName),
		errors.Is(err, vgateway.ErrInvalidVGatewayConfig),
		errors.Is(err, vgateway.ErrUnsupportedVGatewayType):
		return apiError(c, fiber.StatusBadRequest, "VGW010", "Invalid vGateway configuration")
	case errors.Is(err, vgateway.ErrVGatewayNameExists):
		return apiError(c, fiber.StatusConflict, "VGW011", "A vGateway with this name already exists")
	case errors.Is(err, vgateway.ErrVGatewayNotFound):
		return apiError(c, fiber.StatusNotFound, "NOT_FOUND", "vGateway not found")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func validationError(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code string, message string) error {
	return c.Status(status).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    code,
			"message": message,
		},
	})
}
