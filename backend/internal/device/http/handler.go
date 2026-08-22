package devicehttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

type Service interface {
	CreateDevice(context.Context, uuid.UUID, device.CreateDeviceInput) (*device.Device, error)
	GetDevice(context.Context, uuid.UUID) (*device.DeviceView, error)
	ListDevices(context.Context, uuid.UUID) ([]device.DeviceView, error)
	ListDeviceInventory(context.Context, device.DeviceInventoryInput) (*device.DeviceInventoryResult, error)
	UpdateDevice(context.Context, uuid.UUID, device.UpdateDeviceInput) (*device.Device, error)
	DeleteDevice(context.Context, uuid.UUID) error
	CreateDatasource(context.Context, uuid.UUID, device.CreateDatasourceInput) (*device.Datasource, error)
	GetDatasource(context.Context, uuid.UUID) (*device.DatasourceView, error)
	ListDatasources(context.Context, uuid.UUID) ([]device.DatasourceView, error)
	UpdateDatasource(context.Context, uuid.UUID, device.UpdateDatasourceInput) (*device.DatasourceView, error)
	DeleteDatasource(context.Context, uuid.UUID) error
	PreviewDatasource(context.Context, uuid.UUID, device.PreviewDatasourceInput) (*device.DatasourceSample, error)
	PreviewSavedDatasource(context.Context, uuid.UUID) (*device.DatasourceSample, error)
	LatestSample(context.Context, uuid.UUID) (*device.DatasourceSample, error)
	Subscribe(context.Context, uuid.UUID) (<-chan device.DatasourceSample, func(), error)
}

type Handler struct{ service Service }

type createDeviceRequest struct {
	Name        string            `json:"name"`
	Type        device.DeviceType `json:"type"`
	Description *string           `json:"description"`
	Enabled     *bool             `json:"enabled"`
	Config      json.RawMessage   `json:"config"`
}

type updateRequest struct {
	Name        *string         `json:"name"`
	Description json.RawMessage `json:"description"`
	Enabled     *bool           `json:"enabled"`
	Config      json.RawMessage `json:"config"`
}

type createDatasourceRequest struct {
	Name        string                `json:"name"`
	Type        device.DatasourceType `json:"type"`
	Description *string               `json:"description"`
	Enabled     *bool                 `json:"enabled"`
	Config      json.RawMessage       `json:"config"`
}

type previewRequest struct {
	Type   device.DatasourceType `json:"type"`
	Config json.RawMessage       `json:"config"`
}

func NewHandler(service Service) *Handler { return &Handler{service: service} }

func (h *Handler) ListDeviceInventory(c *fiber.Ctx) error {
	input, err := parseDeviceInventoryInput(c)
	if err != nil {
		return validation(c, "Invalid query parameters")
	}
	result, err := h.service.ListDeviceInventory(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{
		"data": result.Data,
		"pagination": fiber.Map{
			"page":        result.Page,
			"per_page":    result.PerPage,
			"total":       result.Total,
			"total_pages": result.TotalPages,
		},
	})
}

func (h *Handler) ListDevices(c *fiber.Ctx) error {
	id, err := parseID(c.Params("vgateway_id"))
	if err != nil {
		return validation(c, "Invalid vGateway ID")
	}
	result, err := h.service.ListDevices(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": result})
}

func (h *Handler) CreateDevice(c *fiber.Ctx) error {
	id, err := parseID(c.Params("vgateway_id"))
	if err != nil {
		return validation(c, "Invalid vGateway ID")
	}
	var request createDeviceRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := h.service.CreateDevice(c.UserContext(), id, device.CreateDeviceInput{Name: request.Name, Type: request.Type, Description: request.Description, Enabled: request.Enabled, Config: request.Config})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(result)
}

func (h *Handler) GetDevice(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	result, err := h.service.GetDevice(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) UpdateDevice(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := device.UpdateDeviceInput{Name: request.Name, Enabled: request.Enabled}
	if request.Description != nil {
		input.Description.Set = true
		if !bytes.Equal(bytes.TrimSpace(request.Description), []byte("null")) {
			var description string
			if err := json.Unmarshal(request.Description, &description); err != nil {
				return validation(c, "Invalid description")
			}
			input.Description.Value = &description
		}
	}
	if request.Config != nil {
		input.Config = &request.Config
	}
	result, err := h.service.UpdateDevice(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) DeleteDevice(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	if err := h.service.DeleteDevice(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) ListDatasources(c *fiber.Ctx) error {
	id, err := parseID(c.Params("device_id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	result, err := h.service.ListDatasources(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": result})
}

func (h *Handler) CreateDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("device_id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	var request createDatasourceRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := h.service.CreateDatasource(c.UserContext(), id, device.CreateDatasourceInput{Name: request.Name, Type: request.Type, Description: request.Description, Enabled: request.Enabled, Config: request.Config})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(result)
}

func (h *Handler) GetDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	result, err := h.service.GetDatasource(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) UpdateDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := device.UpdateDatasourceInput{Name: request.Name, Enabled: request.Enabled}
	if request.Description != nil {
		input.Description.Set = true
		if !bytes.Equal(bytes.TrimSpace(request.Description), []byte("null")) {
			var description string
			if err := json.Unmarshal(request.Description, &description); err != nil {
				return validation(c, "Invalid description")
			}
			input.Description.Value = &description
		}
	}
	if request.Config != nil {
		input.Config = &request.Config
	}
	result, err := h.service.UpdateDatasource(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) DeleteDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	if err := h.service.DeleteDatasource(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) PreviewDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("device_id"))
	if err != nil {
		return validation(c, "Invalid device ID")
	}
	var request previewRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := h.service.PreviewDatasource(c.UserContext(), id, device.PreviewDatasourceInput{Type: request.Type, Config: request.Config})
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) PreviewSavedDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	result, err := h.service.PreviewSavedDatasource(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) RawDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	result, err := h.service.LatestSample(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) StreamDatasource(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid datasource ID")
	}
	stream, unsubscribe, err := h.service.Subscribe(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set(fiber.HeaderConnection, "keep-alive")
	c.Context().SetBodyStreamWriter(func(writer *bufio.Writer) {
		defer unsubscribe()
		if _, err := writer.WriteString(": connected\n\n"); err != nil {
			return
		}
		if err := writer.Flush(); err != nil {
			return
		}
		for sample := range stream {
			payload, err := json.Marshal(sample)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(writer, "event: sample\ndata: %s\n\n", payload); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		}
	})
	return nil
}

func decode(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON documents")
		}
		return err
	}
	return nil
}

func parseID(value string) (uuid.UUID, error) { return uuid.Parse(value) }

func parseDeviceInventoryInput(c *fiber.Ctx) (device.DeviceInventoryInput, error) {
	var input device.DeviceInventoryInput
	if value := c.Query("vgateway_id"); value != "" {
		gatewayID, err := uuid.Parse(value)
		if err != nil {
			return input, err
		}
		input.VGatewayID = &gatewayID
	}
	if value := c.Query("type"); value != "" {
		deviceType := device.DeviceType(value)
		input.Type = &deviceType
	}
	if value := c.Query("enabled"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return input, err
		}
		input.Enabled = &enabled
	}
	input.Search = c.Query("search")
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

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}
func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

func requestAPIError(c *fiber.Ctx, status int, code, fallback string, err error) error {
	message, details, ok := protocol.PublicRequestError(err)
	if !ok {
		return apiError(c, status, code, fallback)
	}
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message, "details": details}})
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, device.ErrDeviceNotFound):
		return apiError(c, fiber.StatusNotFound, "DEV004", "Device not found")
	case errors.Is(err, device.ErrDatasourceNotFound):
		return apiError(c, fiber.StatusNotFound, "DS008", "Datasource not found")
	case errors.Is(err, device.ErrGatewayNotFound):
		return apiError(c, fiber.StatusNotFound, "VGW004", "vGateway not found")
	case errors.Is(err, device.ErrDeviceNameExists):
		return apiError(c, fiber.StatusConflict, "DEV001", "Device name already exists in this gateway")
	case errors.Is(err, device.ErrDatasourceNameExists):
		return apiError(c, fiber.StatusConflict, "DS001", "Datasource name already exists in this device")
	case errors.Is(err, device.ErrInvalidDevice), errors.Is(err, device.ErrInvalidDatasource), errors.Is(err, device.ErrUnsupportedDeviceType), errors.Is(err, device.ErrUnsupportedDatasourceType), errors.Is(err, device.ErrProtocolMismatch):
		return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, device.ErrMonitoringDisabled):
		return apiError(c, fiber.StatusConflict, "DS009", "Enable the gateway, device, and datasource before monitoring")
	case errors.Is(err, device.ErrDatasourceSampleUnavailable):
		return apiError(c, fiber.StatusNotFound, "DS010", "No datasource sample is available yet")
	case errors.Is(err, device.ErrDatasourceReadFailed):
		return requestAPIError(c, fiber.StatusBadGateway, "DS005", "Datasource read failed", err)
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}
