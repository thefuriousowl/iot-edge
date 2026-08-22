package taghttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

type Service interface {
	Create(context.Context, tag.CreateInput) (*tag.Tag, error)
	Get(context.Context, uuid.UUID) (*tag.Tag, error)
	List(context.Context, tag.ListInput) (*tag.ListResult, error)
	Update(context.Context, uuid.UUID, tag.UpdateInput) (*tag.Tag, error)
	Delete(context.Context, uuid.UUID) error
	Preview(context.Context, tag.PreviewInput) (*tag.PreviewResult, error)
	PreviewSaved(context.Context, uuid.UUID) (*tag.PreviewResult, error)
	ValidateCalculatedExpression(context.Context, uuid.UUID, string) ([]uuid.UUID, error)
}

type ValueMonitor interface {
	Latest(uuid.UUID) (tag.TagValue, bool)
	History(uuid.UUID, int) []tag.TagValue
	Subscribe(context.Context, []uuid.UUID) (<-chan tag.TagValue, func())
	SubscribeValues(context.Context, []uuid.UUID, uint64) tag.ValueSubscription
}

type Handler struct {
	service Service
	values  ValueMonitor
}

const (
	tagValueStreamHeartbeatInterval = 15 * time.Second
	tagValueStreamRetryMilliseconds = 3000
)

type HandlerOption func(*Handler)

func WithValueMonitor(values ValueMonitor) HandlerOption {
	return func(handler *Handler) { handler.values = values }
}

type createRequest struct {
	DatasourceID *uuid.UUID      `json:"datasource_id"`
	Name         string          `json:"name"`
	Type         tag.Type        `json:"type"`
	DataType     tag.DataType    `json:"data_type"`
	Description  *string         `json:"description"`
	Enabled      *bool           `json:"enabled"`
	Config       json.RawMessage `json:"config"`
}

type updateRequest struct {
	DatasourceID json.RawMessage `json:"datasource_id"`
	Name         *string         `json:"name"`
	DataType     *tag.DataType   `json:"data_type"`
	Description  json.RawMessage `json:"description"`
	Enabled      *bool           `json:"enabled"`
	Config       json.RawMessage `json:"config"`
}

type previewRequest struct {
	DatasourceID *uuid.UUID      `json:"datasource_id"`
	Type         tag.Type        `json:"type"`
	DataType     tag.DataType    `json:"data_type"`
	Config       json.RawMessage `json:"config"`
}

type validateExpressionRequest struct {
	TagID      *uuid.UUID `json:"tag_id"`
	Expression string     `json:"expression"`
}

func NewHandler(service Service, options ...HandlerOption) *Handler {
	handler := &Handler{service: service}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

func (h *Handler) List(c *fiber.Ctx) error {
	input, err := parseListInput(c)
	if err != nil {
		return validation(c, "Invalid query parameters")
	}
	result, err := h.service.List(c.UserContext(), input)
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

func (h *Handler) Values(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Tag ID")
	}
	if _, err := h.service.Get(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	if h.values == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "TAG009", "Tag value monitoring is unavailable")
	}
	limit := tag.DefaultTagValueHistoryLimit
	if raw := c.Query("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > tag.DefaultTagValueHistoryLimit {
			return validation(c, "limit must be between 1 and 10")
		}
		limit = parsed
	}
	history := h.values.History(id, limit)
	if history == nil {
		history = []tag.TagValue{}
	}
	var latest *tag.TagValue
	if value, exists := h.values.Latest(id); exists {
		latest = &value
	}
	return c.JSON(fiber.Map{"latest": latest, "history": history, "latest_retention": "persistent", "history_retention": "runtime_memory"})
}

func (h *Handler) StreamValues(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Tag ID")
	}
	if _, err := h.service.Get(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	if h.values == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "TAG009", "Tag value monitoring is unavailable")
	}
	stream, unsubscribe := h.values.Subscribe(c.UserContext(), []uuid.UUID{id})
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
		heartbeat := time.NewTicker(tagValueStreamHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case value, open := <-stream:
				if !open {
					return
				}
				payload, err := json.Marshal(value)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(writer, "event: tag_value\ndata: %s\n\n", payload); err != nil {
					return
				}
			case <-heartbeat.C:
				if _, err := writer.WriteString(": keep-alive\n\n"); err != nil {
					return
				}
			}
			if err := writer.Flush(); err != nil {
				return
			}
		}
	})
	return nil
}

func (h *Handler) StreamAllValues(c *fiber.Ctx) error {
	if h.values == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "TAG009", "Tag value monitoring is unavailable")
	}
	afterSequence, err := parseLastEventID(c.Get("Last-Event-ID"))
	if err != nil {
		return validation(c, "Last-Event-ID must be an unsigned integer")
	}
	subscription := h.values.SubscribeValues(c.UserContext(), nil, afterSequence)
	return streamTagValues(c, subscription)
}

func parseLastEventID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

func streamTagValues(c *fiber.Ctx, subscription tag.ValueSubscription) error {
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set(fiber.HeaderConnection, "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(func(writer *bufio.Writer) {
		defer subscription.Unsubscribe()
		if _, err := fmt.Fprintf(writer, "retry: %d\n: connected\n\n", tagValueStreamRetryMilliseconds); err != nil {
			return
		}
		for _, value := range subscription.Replay {
			if err := writeTagValueEvent(writer, value); err != nil {
				return
			}
		}
		if err := writer.Flush(); err != nil {
			return
		}
		heartbeat := time.NewTicker(tagValueStreamHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case value, open := <-subscription.Stream:
				if !open {
					return
				}
				if err := writeTagValueEvent(writer, value); err != nil {
					return
				}
			case <-heartbeat.C:
				if _, err := writer.WriteString(": keep-alive\n\n"); err != nil {
					return
				}
			}
			if err := writer.Flush(); err != nil {
				return
			}
		}
	})
	return nil
}

func writeTagValueEvent(writer io.Writer, value tag.TagValue) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: tag_value\ndata: %s\n\n", value.Sequence, payload)
	return err
}

func (h *Handler) Create(c *fiber.Ctx) error {
	var request createRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := h.service.Create(c.UserContext(), tag.CreateInput{
		DatasourceID: request.DatasourceID,
		Name:         request.Name,
		Type:         request.Type,
		DataType:     request.DataType,
		Description:  request.Description,
		Enabled:      request.Enabled,
		Config:       request.Config,
	})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(result)
}

func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid tag ID")
	}
	result, err := h.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) Update(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid tag ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := tag.UpdateInput{Name: request.Name, DataType: request.DataType, Enabled: request.Enabled}
	if request.DatasourceID != nil {
		input.DatasourceID.Set = true
		if !isJSONNull(request.DatasourceID) {
			var datasourceID uuid.UUID
			if err := json.Unmarshal(request.DatasourceID, &datasourceID); err != nil {
				return validation(c, "Invalid datasource ID")
			}
			input.DatasourceID.Value = &datasourceID
		}
	}
	if request.Description != nil {
		input.Description.Set = true
		if !isJSONNull(request.Description) {
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
	result, err := h.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid tag ID")
	}
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) Preview(c *fiber.Ctx) error {
	var request previewRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := h.service.Preview(c.UserContext(), tag.PreviewInput{
		DatasourceID: request.DatasourceID,
		Type:         request.Type,
		DataType:     request.DataType,
		Config:       request.Config,
	})
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) PreviewSaved(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid tag ID")
	}
	result, err := h.service.PreviewSaved(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) ValidateExpression(c *fiber.Ctx) error {
	var request validateExpressionRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	tagID := uuid.Nil
	if request.TagID != nil {
		tagID = *request.TagID
	}
	dependencies, err := h.service.ValidateCalculatedExpression(c.UserContext(), tagID, request.Expression)
	if err != nil {
		return handleError(c, err)
	}
	if dependencies == nil {
		dependencies = make([]uuid.UUID, 0)
	}
	return c.JSON(fiber.Map{"valid": true, "dependencies": dependencies})
}

func parseListInput(c *fiber.Ctx) (tag.ListInput, error) {
	var input tag.ListInput
	if value := c.Query("type"); value != "" {
		tagType := tag.Type(value)
		input.Type = &tagType
	}
	if value := c.Query("data_type"); value != "" {
		dataType := tag.DataType(value)
		input.DataType = &dataType
	}
	if value := c.Query("enabled"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return input, err
		}
		input.Enabled = &enabled
	}
	if value := c.Query("datasource_id"); value != "" {
		datasourceID, err := uuid.Parse(value)
		if err != nil {
			return input, err
		}
		input.DatasourceID = &datasourceID
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

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
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
	case errors.Is(err, tag.ErrTagNotFound):
		return apiError(c, fiber.StatusNotFound, "TAG004", "Tag not found")
	case errors.Is(err, tag.ErrDatasourceMissing):
		return apiError(c, fiber.StatusNotFound, "DS008", "Datasource not found")
	case errors.Is(err, tag.ErrTagNameExists):
		return apiError(c, fiber.StatusConflict, "TAG001", "Tag name already exists")
	case errors.Is(err, tag.ErrTagDisabled):
		return apiError(c, fiber.StatusConflict, "TAG006", "Enable the tag before previewing")
	case errors.Is(err, tag.ErrCalculatedSnapshotRequired):
		return apiError(c, fiber.StatusConflict, "TAG008", "Calculated Tags require a runtime value snapshot")
	case errors.Is(err, tag.ErrTagSourceReadFailed):
		return requestAPIError(c, fiber.StatusBadGateway, "TAG007", "Tag datasource read failed", err)
	case errors.Is(err, tag.ErrInvalidTagInput),
		errors.Is(err, tag.ErrUnsupportedTagType),
		errors.Is(err, tag.ErrUnsupportedTagDataType),
		errors.Is(err, tag.ErrUnsupportedDecoder),
		errors.Is(err, tag.ErrInvalidDecoderConfig),
		errors.Is(err, tag.ErrInvalidTransformConfig),
		errors.Is(err, tag.ErrUnsupportedTransform),
		errors.Is(err, tag.ErrCalculatedTriggerInvalid),
		errors.Is(err, tag.ErrCalculatedDependencyMissing),
		errors.Is(err, tag.ErrCircularTagDependency),
		errors.Is(err, tag.ErrTagDependencyTooDeep),
		errors.Is(err, tag.ErrDependencyMissing),
		errors.Is(err, tag.ErrInvalidTag),
		errors.Is(err, tag.ErrInvalidDependency),
		errors.Is(err, tag.ErrInvalidExpression),
		errors.Is(err, tag.ErrExpressionTooComplex),
		errors.Is(err, tag.ErrExpressionReference),
		errors.Is(err, tag.ErrExpressionEvaluation):
		return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}
