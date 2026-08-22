package dataloggerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

type Service interface {
	Create(context.Context, datalogger.CreateInput) (*datalogger.Logger, error)
	Get(context.Context, uuid.UUID) (*datalogger.Logger, error)
	List(context.Context, datalogger.ListInput) (*datalogger.ListResult, error)
	ListHistory(context.Context, uuid.UUID, datalogger.RawValueListInput) (*datalogger.RawValueListResult, error)
	QueryHistory(context.Context, uuid.UUID, datalogger.QueryInput) (*datalogger.QueryResult, error)
	Update(context.Context, uuid.UUID, datalogger.UpdateInput) (*datalogger.Logger, error)
	Delete(context.Context, uuid.UUID) error
}

type Handler struct{ service Service }

type createRequest struct {
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	Enabled      *bool           `json:"enabled"`
	Timezone     string          `json:"timezone"`
	Mode         datalogger.Mode `json:"mode"`
	StartAt      time.Time       `json:"start_at"`
	EndAt        *time.Time      `json:"end_at"`
	MaxSizeBytes *int64          `json:"max_size_bytes"`
	Config       json.RawMessage `json:"config"`
	TagIDs       []uuid.UUID     `json:"tag_ids"`
}

type updateRequest struct {
	Name         *string          `json:"name"`
	Description  json.RawMessage  `json:"description"`
	Enabled      *bool            `json:"enabled"`
	Timezone     *string          `json:"timezone"`
	Mode         *datalogger.Mode `json:"mode"`
	StartAt      *time.Time       `json:"start_at"`
	EndAt        json.RawMessage  `json:"end_at"`
	MaxSizeBytes json.RawMessage  `json:"max_size_bytes"`
	Config       json.RawMessage  `json:"config"`
	TagIDs       *[]uuid.UUID     `json:"tag_ids"`
}

func NewHandler(service Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Create(c *fiber.Ctx) error {
	var request createRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	result, err := handler.service.Create(c.UserContext(), datalogger.CreateInput{
		Name: request.Name, Description: request.Description, Enabled: request.Enabled, Timezone: request.Timezone, Mode: request.Mode, StartAt: request.StartAt, EndAt: request.EndAt, MaxSizeBytes: request.MaxSizeBytes, Config: request.Config, TagIDs: request.TagIDs,
	})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(result)
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Logger ID")
	}
	result, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (handler *Handler) List(c *fiber.Ctx) error {
	input, err := parseListInput(c)
	if err != nil {
		return validation(c, "Invalid query parameters")
	}
	result, err := handler.service.List(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{
		"data":       result.Data,
		"pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) History(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Logger ID")
	}
	input, err := parseHistoryInput(c)
	if err != nil {
		return validation(c, "Invalid history query parameters")
	}
	result, err := handler.service.ListHistory(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{
		"data":          result.Data,
		"last_batch_at": result.LastBatchAt,
		"pagination":    fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) Query(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Logger ID")
	}
	input, err := parseQueryInput(c)
	if err != nil {
		return validation(c, "Invalid Data Logger query parameters")
	}
	result, err := handler.service.QueryHistory(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{
		"data":       result.Data,
		"mode":       result.Mode,
		"bucket":     result.Bucket,
		"aggregate":  result.Aggregate,
		"pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Logger ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := datalogger.UpdateInput{Name: request.Name, Enabled: request.Enabled, Timezone: request.Timezone, Mode: request.Mode, StartAt: request.StartAt, Config: request.Config, TagIDs: request.TagIDs}
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
	if request.EndAt != nil {
		input.EndAt.Set = true
		if !isJSONNull(request.EndAt) {
			var endAt time.Time
			if err := json.Unmarshal(request.EndAt, &endAt); err != nil {
				return validation(c, "Invalid end_at")
			}
			input.EndAt.Value = &endAt
		}
	}
	if request.MaxSizeBytes != nil {
		input.MaxSizeBytes.Set = true
		if !isJSONNull(request.MaxSizeBytes) {
			var maxSizeBytes int64
			if err := json.Unmarshal(request.MaxSizeBytes, &maxSizeBytes); err != nil {
				return validation(c, "Invalid max_size_bytes")
			}
			input.MaxSizeBytes.Value = &maxSizeBytes
		}
	}
	result, err := handler.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Logger ID")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func parseListInput(c *fiber.Ctx) (datalogger.ListInput, error) {
	input := datalogger.ListInput{Search: c.Query("search")}
	if raw := c.Query("mode"); raw != "" {
		mode := datalogger.Mode(raw)
		input.Mode = &mode
	}
	if raw := c.Query("enabled"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return datalogger.ListInput{}, err
		}
		input.Enabled = &enabled
	}
	var err error
	input.Page, err = positiveQuery(c, "page", 1)
	if err != nil {
		return datalogger.ListInput{}, err
	}
	input.PerPage, err = positiveQuery(c, "per_page", 20)
	return input, err
}

func parseHistoryInput(c *fiber.Ctx) (datalogger.RawValueListInput, error) {
	var input datalogger.RawValueListInput
	if raw := c.Query("tag_id"); raw != "" {
		tagID, err := parseID(raw)
		if err != nil {
			return input, err
		}
		input.TagID = &tagID
	}
	for name, target := range map[string]**time.Time{"from": &input.From, "to": &input.To} {
		if raw := c.Query(name); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return input, err
			}
			utc := value.UTC()
			*target = &utc
		}
	}
	var err error
	input.Page, err = positiveQuery(c, "page", 1)
	if err != nil {
		return input, err
	}
	input.PerPage, err = positiveQuery(c, "per_page", 100)
	return input, err
}

func parseQueryInput(c *fiber.Ctx) (datalogger.QueryInput, error) {
	input := datalogger.QueryInput{Mode: datalogger.QueryModeRaw}
	if raw := c.Query("mode"); raw != "" {
		input.Mode = datalogger.QueryMode(raw)
	}
	if raw := c.Query("tag_ids"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			tagID, err := parseID(strings.TrimSpace(part))
			if err != nil {
				return input, err
			}
			input.TagIDs = append(input.TagIDs, tagID)
		}
	}
	for name, target := range map[string]*time.Time{"from": &input.From, "to": &input.To} {
		raw := c.Query(name)
		if raw == "" {
			return input, errors.New("query range is required")
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return input, err
		}
		*target = value.UTC()
	}
	if input.Mode == datalogger.QueryModeAggregate {
		input.Bucket = datalogger.QueryBucket(c.Query("bucket", string(datalogger.QueryBucket1Hour)))
		input.Aggregate = datalogger.AggregateFunction(c.Query("aggregate", string(datalogger.AggregateAvg)))
	}
	var err error
	input.Page, err = positiveQuery(c, "page", 1)
	if err != nil {
		return input, err
	}
	input.PerPage, err = positiveQuery(c, "per_page", 100)
	return input, err
}

func positiveQuery(c *fiber.Ctx, name string, fallback int) (int, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("query value must be positive")
	}
	return value, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}

func decode(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON document")
	}
	return nil
}

func isJSONNull(value json.RawMessage) bool {
	return strings.EqualFold(string(bytes.TrimSpace(value)), "null")
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, datalogger.ErrLoggerNotFound):
		return apiError(c, fiber.StatusNotFound, "DLG001", "Data Logger not found")
	case errors.Is(err, datalogger.ErrLoggerNameExists):
		return apiError(c, fiber.StatusConflict, "DLG002", "Data Logger name already exists")
	case errors.Is(err, datalogger.ErrLoggerTagNotFound):
		return apiError(c, fiber.StatusBadRequest, "DLG003", "Selected Tag not found")
	case errors.Is(err, datalogger.ErrStorageLimitTooSmall):
		return apiError(c, fiber.StatusBadRequest, "DLG004", "Data Logger storage limit cannot hold one complete synchronized batch")
	case errors.Is(err, datalogger.ErrInvalidInput), errors.Is(err, datalogger.ErrInvalidLogger), errors.Is(err, datalogger.ErrInvalidLoggerTag), errors.Is(err, datalogger.ErrInvalidRawBatch), errors.Is(err, datalogger.ErrInvalidQuery), errors.Is(err, datalogger.ErrRawTagNotSelected):
		return validation(c, "Invalid Data Logger configuration")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}
