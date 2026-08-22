package reporthttp

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
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/report"
)

type Service interface {
	Create(context.Context, report.SaveInput) (*report.Report, error)
	Get(context.Context, uuid.UUID) (*report.Report, error)
	List(context.Context, report.ListInput) (*report.ListResult, error)
	Update(context.Context, uuid.UUID, report.SaveInput) (*report.Report, error)
	Delete(context.Context, uuid.UUID) error
	Query(context.Context, uuid.UUID, report.QueryInput) (*report.QueryResult, error)
	ExportCSV(context.Context, uuid.UUID, report.QueryInput, io.Writer) error
}

type Handler struct{ service Service }

type columnRequest struct {
	TagID     uuid.UUID                    `json:"tag_id"`
	Name      string                       `json:"name"`
	Aggregate datalogger.AggregateFunction `json:"aggregate"`
}

type saveRequest struct {
	Name        string                 `json:"name"`
	Description *string                `json:"description"`
	LoggerID    uuid.UUID              `json:"logger_id"`
	Timezone    string                 `json:"timezone"`
	Mode        datalogger.QueryMode   `json:"mode"`
	Bucket      datalogger.QueryBucket `json:"bucket"`
	Columns     []columnRequest        `json:"columns"`
}

func NewHandler(service Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Create(c *fiber.Ctx) error {
	input, err := decodeSave(c.Body())
	if err != nil {
		return validation(c, "Invalid request body")
	}
	entity, err := handler.service.Create(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(entity)
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Report ID")
	}
	entity, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(entity)
}

func (handler *Handler) List(c *fiber.Ctx) error {
	input := report.ListInput{Search: c.Query("search")}
	if raw := c.Query("logger_id"); raw != "" {
		loggerID, err := parseID(raw)
		if err != nil {
			return validation(c, "Invalid query parameters")
		}
		input.LoggerID = &loggerID
	}
	if raw := c.Query("mode"); raw != "" {
		mode := datalogger.QueryMode(raw)
		input.Mode = &mode
	}
	var err error
	input.Page, err = positiveQuery(c, "page", 1)
	if err == nil {
		input.PerPage, err = positiveQuery(c, "per_page", 20)
	}
	if err != nil {
		return validation(c, "Invalid query parameters")
	}
	result, err := handler.service.List(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": result.Data, "pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages}})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Report ID")
	}
	input, err := decodeSave(c.Body())
	if err != nil {
		return validation(c, "Invalid request body")
	}
	entity, err := handler.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(entity)
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Report ID")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) Query(c *fiber.Ctx) error {
	id, input, err := parseQuery(c)
	if err != nil {
		return validation(c, "Invalid Report query parameters")
	}
	result, err := handler.service.Query(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"report": result.Report, "data": result.Data, "pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages}})
}

func (handler *Handler) ExportCSV(c *fiber.Ctx) error {
	id, input, err := parseQuery(c)
	if err != nil {
		return validation(c, "Invalid Report export parameters")
	}
	ctx := c.UserContext()
	if _, err := handler.service.Get(ctx, id); err != nil {
		return handleError(c, err)
	}
	reader, writer := io.Pipe()
	go func() {
		writer.CloseWithError(handler.service.ExportCSV(ctx, id, input, writer))
	}()
	c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="report-`+id.String()+`.csv"`)
	return c.SendStream(reader)
}

func decodeSave(body []byte) (report.SaveInput, error) {
	var request saveRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return report.SaveInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return report.SaveInput{}, errors.New("request must contain one JSON document")
	}
	input := report.SaveInput{Name: request.Name, Description: request.Description, LoggerID: request.LoggerID, Timezone: request.Timezone, Mode: request.Mode, Bucket: request.Bucket}
	for _, column := range request.Columns {
		input.Columns = append(input.Columns, report.ColumnInput{TagID: column.TagID, Name: column.Name, Aggregate: column.Aggregate})
	}
	return input, nil
}

func parseQuery(c *fiber.Ctx) (uuid.UUID, report.QueryInput, error) {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return uuid.Nil, report.QueryInput{}, err
	}
	var input report.QueryInput
	for name, target := range map[string]*time.Time{"from": &input.From, "to": &input.To} {
		raw := c.Query(name)
		if raw == "" {
			return uuid.Nil, report.QueryInput{}, errors.New("query range is required")
		}
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return uuid.Nil, report.QueryInput{}, parseErr
		}
		*target = value.UTC()
	}
	input.Page, err = positiveQuery(c, "page", 1)
	if err == nil {
		input.PerPage, err = positiveQuery(c, "per_page", 100)
	}
	return id, input, err
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

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, report.ErrNotFound):
		return apiError(c, fiber.StatusNotFound, "RPT001", "Report not found")
	case errors.Is(err, report.ErrNameExists):
		return apiError(c, fiber.StatusConflict, "RPT002", "Report name already exists")
	case errors.Is(err, report.ErrTagNotSelected):
		return apiError(c, fiber.StatusBadRequest, "RPT003", "Report Tag is not selected by its Data Logger")
	case errors.Is(err, report.ErrColumnNameExists):
		return apiError(c, fiber.StatusBadRequest, "RPT004", "Report column names must be unique")
	case errors.Is(err, report.ErrInvalidInput), errors.Is(err, report.ErrInvalidReport), errors.Is(err, datalogger.ErrInvalidQuery):
		return validation(c, "Invalid Report configuration")
	case errors.Is(err, datalogger.ErrLoggerNotFound):
		return apiError(c, fiber.StatusBadRequest, "RPT005", "Data Logger not found")
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
