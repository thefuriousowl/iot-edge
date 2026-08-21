package vgatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"syscall"
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
	Connect(context.Context, uuid.UUID) error
	Disconnect(context.Context, uuid.UUID) error
	TestConnection(context.Context, uuid.UUID, json.RawMessage) (*vgateway.VGatewayConnectionTestResult, error)
	Status(context.Context, uuid.UUID) (*vgateway.VGatewayStatusResult, error)
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

func (h *Handler) Connect(c *fiber.Ctx) error {
	id, err := parseVGatewayID(c)
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	if err := h.service.Connect(c.UserContext(), id); err != nil {
		return handleServiceError(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "Connected successfully",
		"status":  vgateway.VGatewayStatusConnected,
	})
}

func (h *Handler) Disconnect(c *fiber.Ctx) error {
	id, err := parseVGatewayID(c)
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	if err := h.service.Disconnect(c.UserContext(), id); err != nil {
		return handleServiceError(c, err)
	}

	status, err := h.service.Status(c.UserContext(), id)
	if err != nil {
		return handleServiceError(c, err)
	}
	if status == nil {
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "Disconnected successfully",
		"status":  status.Status,
	})
}

func (h *Handler) TestConnection(c *fiber.Ctx) error {
	id, err := parseVGatewayID(c)
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	var options json.RawMessage
	if len(bytes.TrimSpace(c.Body())) > 0 {
		if err := decodeRequest(c.Body(), &options); err != nil {
			return validationError(c, "Invalid request body")
		}
	}

	result, err := h.service.TestConnection(c.UserContext(), id, options)
	if err != nil {
		return handleServiceError(c, err)
	}
	if result == nil {
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}

	if !result.Success {
		failure, message := connectionFailureMessage(result.Error)
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": false,
			"error":   failure,
			"message": message,
		})
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"success":    true,
		"latency_ms": float64(result.Latency) / float64(time.Millisecond),
		"message":    "Connection successful",
	})
}

func (h *Handler) Status(c *fiber.Ctx) error {
	id, err := parseVGatewayID(c)
	if err != nil {
		return validationError(c, "Invalid vGateway ID")
	}

	status, err := h.service.Status(c.UserContext(), id)
	if err != nil {
		return handleServiceError(c, err)
	}
	if status == nil {
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}

	return c.Status(fiber.StatusOK).JSON(status)
}

func parseVGatewayID(c *fiber.Ctx) (uuid.UUID, error) {
	return uuid.Parse(c.Params("id"))
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
		errors.Is(err, vgateway.ErrInvalidVGatewayTestInput),
		errors.Is(err, vgateway.ErrUnsupportedVGatewayType):
		return apiError(c, fiber.StatusBadRequest, "VGW010", "Invalid vGateway configuration")
	case errors.Is(err, vgateway.ErrVGatewayDisabled):
		return apiError(c, fiber.StatusConflict, "VGW014", "Enable the vGateway before connecting")
	case errors.Is(err, vgateway.ErrVGatewayNameExists):
		return apiError(c, fiber.StatusConflict, "VGW011", "A vGateway with this name already exists")
	case errors.Is(err, vgateway.ErrVGatewayNotFound):
		return apiError(c, fiber.StatusNotFound, "NOT_FOUND", "vGateway not found")
	case errors.Is(err, syscall.ECONNREFUSED):
		return apiError(c, fiber.StatusInternalServerError, "VGW001", "Unable to connect. Please check host and port")
	case isTimeoutError(err):
		return apiError(c, fiber.StatusInternalServerError, "VGW002", "Connection timed out. Device may be unreachable")
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return apiError(c, fiber.StatusInternalServerError, "VGW003", "Host unreachable. Please check network settings")
	case isDNSError(err):
		return apiError(c, fiber.StatusInternalServerError, "VGW004", "Unable to resolve hostname")
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return apiError(c, fiber.StatusInternalServerError, "VGW005", "Connection was closed by the remote host")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func connectionFailureMessage(err error) (string, string) {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "Connection refused", "Unable to connect. Please check host and port."
	case isTimeoutError(err):
		return "Connection timeout", "Connection timed out. Device may be unreachable."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "Host unreachable", "Host unreachable. Please check network settings."
	case isDNSError(err):
		return "DNS resolution failed", "Unable to resolve hostname."
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return "Connection closed", "The connection was closed by the remote host."
	default:
		return "Connection failed", "Unable to connect to the vGateway."
	}
}

func isTimeoutError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func isDNSError(err error) bool {
	var dnsError *net.DNSError
	return errors.As(err, &dnsError)
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
