package assethttp

import (
	"bufio"
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
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

type Service interface {
	Create(context.Context, asset.CreateInput) (*asset.Asset, error)
	Get(context.Context, uuid.UUID) (*asset.Asset, error)
	List(context.Context, asset.ListInput) (*asset.ListResult, error)
	Children(context.Context, *uuid.UUID) ([]asset.Asset, error)
	Subtree(context.Context, uuid.UUID) (*asset.TreeNode, error)
	Ancestors(context.Context, uuid.UUID) ([]asset.Asset, error)
	Update(context.Context, uuid.UUID, asset.UpdateInput) (*asset.Asset, error)
	Move(context.Context, uuid.UUID, asset.MoveInput) (*asset.Asset, error)
	Delete(context.Context, uuid.UUID) error
	Bindings(context.Context, uuid.UUID) ([]asset.MeasurementBinding, error)
	ReplaceBindings(context.Context, uuid.UUID, []asset.MeasurementBinding) ([]asset.MeasurementBinding, error)
}

type MeasurementService interface {
	Snapshot(context.Context, uuid.UUID) (*asset.MeasurementSnapshot, error)
	Subscribe(context.Context, uuid.UUID) (asset.MeasurementSubscription, error)
}

type ConnectivityService interface {
	Asset(context.Context, uuid.UUID) (*asset.AssetConnectivity, error)
	Tag(context.Context, uuid.UUID) (*asset.TagAssetConnectivity, error)
}

type Handler struct {
	service      Service
	measurements MeasurementService
	connectivity ConnectivityService
}

type HandlerOption func(*Handler)

func WithMeasurements(measurements MeasurementService) HandlerOption {
	return func(handler *Handler) { handler.measurements = measurements }
}

func WithConnectivity(connectivity ConnectivityService) HandlerOption {
	return func(handler *Handler) { handler.connectivity = connectivity }
}

type createRequest struct {
	ParentID    *uuid.UUID      `json:"parent_id"`
	Name        string          `json:"name"`
	Kind        asset.Kind      `json:"kind"`
	Description *string         `json:"description"`
	Enabled     *bool           `json:"enabled"`
	Timezone    *string         `json:"timezone"`
	Position    int             `json:"position"`
	Metadata    json.RawMessage `json:"metadata"`
}

type updateRequest struct {
	Name        *string         `json:"name"`
	Kind        *asset.Kind     `json:"kind"`
	Description json.RawMessage `json:"description"`
	Enabled     *bool           `json:"enabled"`
	Timezone    json.RawMessage `json:"timezone"`
	Position    *int            `json:"position"`
	Metadata    json.RawMessage `json:"metadata"`
}

type moveRequest struct {
	ParentID *uuid.UUID `json:"parent_id"`
	Position int        `json:"position"`
}

type bindingsRequest struct {
	Bindings []asset.MeasurementBinding `json:"bindings"`
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

func (handler *Handler) Create(c *fiber.Ctx) error {
	if err := rejectQuery(c, nil); err != nil {
		return validation(c, "Invalid query parameters")
	}
	var request createRequest
	if err := decodeRequest(c, &request); err != nil {
		return validation(c, "Invalid request body")
	}
	value, err := handler.service.Create(c.UserContext(), asset.CreateInput{ParentID: request.ParentID, Name: request.Name, Kind: request.Kind, Description: request.Description, Enabled: request.Enabled, Timezone: request.Timezone, Position: request.Position, Metadata: request.Metadata})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(value)
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	value, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
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
	return c.JSON(fiber.Map{"data": result.Data, "pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages}})
}

func (handler *Handler) Roots(c *fiber.Ctx) error {
	if err := rejectQuery(c, nil); err != nil {
		return validation(c, "Invalid query parameters")
	}
	values, err := handler.service.Children(c.UserContext(), nil)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": values})
}

func (handler *Handler) Children(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	values, err := handler.service.Children(c.UserContext(), &id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": values})
}

func (handler *Handler) Subtree(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	value, err := handler.service.Subtree(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func (handler *Handler) Ancestors(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	values, err := handler.service.Ancestors(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": values})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	var request updateRequest
	if err := decodeRequest(c, &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := asset.UpdateInput{Name: request.Name, Kind: request.Kind, Enabled: request.Enabled, Position: request.Position}
	if request.Description != nil {
		value, parseErr := optionalString(request.Description)
		if parseErr != nil {
			return validation(c, "Invalid description")
		}
		input.Description = &value
	}
	if request.Timezone != nil {
		value, parseErr := optionalString(request.Timezone)
		if parseErr != nil {
			return validation(c, "Invalid timezone")
		}
		input.Timezone = &value
	}
	if request.Metadata != nil {
		if isJSONNull(request.Metadata) {
			return validation(c, "Invalid metadata")
		}
		input.Metadata = request.Metadata
	}
	value, err := handler.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func (handler *Handler) Move(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	var request moveRequest
	if err := decodeRequest(c, &request); err != nil {
		return validation(c, "Invalid request body")
	}
	value, err := handler.service.Move(c.UserContext(), id, asset.MoveInput{ParentID: request.ParentID, Position: request.Position})
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) Bindings(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	values, err := handler.service.Bindings(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": values})
}

func (handler *Handler) ReplaceBindings(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	var request bindingsRequest
	if err := decodeRequest(c, &request); err != nil || request.Bindings == nil {
		return validation(c, "Invalid request body")
	}
	values, err := handler.service.ReplaceBindings(c.UserContext(), id, request.Bindings)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(fiber.Map{"data": values})
}

func (handler *Handler) Measurements(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	if handler.measurements == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "ASSET_LIVE_UNAVAILABLE", "Asset measurement monitoring is unavailable")
	}
	value, err := handler.measurements.Snapshot(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func (handler *Handler) StreamMeasurements(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	if handler.measurements == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "ASSET_LIVE_UNAVAILABLE", "Asset measurement monitoring is unavailable")
	}
	subscription, err := handler.measurements.Subscribe(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set(fiber.HeaderConnection, "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(func(writer *bufio.Writer) {
		defer subscription.Close()
		if _, err := writer.WriteString("retry: 3000\n: connected\n\n"); err != nil {
			return
		}
		if err := writer.Flush(); err != nil {
			return
		}
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case reading, open := <-subscription.Events():
				if !open {
					return
				}
				payload, marshalErr := json.Marshal(reading)
				if marshalErr != nil {
					continue
				}
				if _, writeErr := writer.WriteString("event: asset_measurement\ndata: " + string(payload) + "\n\n"); writeErr != nil {
					return
				}
			case <-heartbeat.C:
				if _, writeErr := writer.WriteString(": keep-alive\n\n"); writeErr != nil {
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

func (handler *Handler) AssetConnectivity(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Asset ID or query parameters")
	}
	if handler.connectivity == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "ASSET_CONNECTIVITY_UNAVAILABLE", "Asset connectivity is unavailable")
	}
	value, err := handler.connectivity.Asset(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func (handler *Handler) TagAssets(c *fiber.Ctx) error {
	id, err := parseNoQueryID(c)
	if err != nil {
		return validation(c, "Invalid Tag ID or query parameters")
	}
	if handler.connectivity == nil {
		return apiError(c, fiber.StatusServiceUnavailable, "ASSET_CONNECTIVITY_UNAVAILABLE", "Asset connectivity is unavailable")
	}
	value, err := handler.connectivity.Tag(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(value)
}

func parseListInput(c *fiber.Ctx) (asset.ListInput, error) {
	var input asset.ListInput
	if err := rejectQuery(c, map[string]bool{"search": true, "kind": true, "enabled": true, "page": true, "per_page": true}); err != nil {
		return input, err
	}
	input.Search = c.Query("search")
	if raw := c.Query("kind"); raw != "" {
		kind := asset.Kind(raw)
		input.Kind = &kind
	}
	if raw := c.Query("enabled"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return input, err
		}
		input.Enabled = &value
	}
	if raw := c.Query("page"); raw != "" {
		value, err := positiveInteger(raw)
		if err != nil {
			return input, err
		}
		input.Page = value
	}
	if raw := c.Query("per_page"); raw != "" {
		value, err := positiveInteger(raw)
		if err != nil {
			return input, err
		}
		input.PerPage = value
	}
	return input, nil
}

func rejectQuery(c *fiber.Ctx, allowed map[string]bool) error {
	var invalid bool
	seen := map[string]bool{}
	c.Context().QueryArgs().VisitAll(func(key, _ []byte) {
		value := string(key)
		if !allowed[value] || seen[value] {
			invalid = true
		}
		seen[value] = true
	})
	if invalid {
		return errors.New("unknown query parameter")
	}
	return nil
}

func parseNoQueryID(c *fiber.Ctx) (uuid.UUID, error) {
	if err := rejectQuery(c, nil); err != nil {
		return uuid.Nil, err
	}
	return parseID(c.Params("id"))
}

func positiveInteger(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("positive integer required")
	}
	return value, nil
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

func decodeRequest(c *fiber.Ctx, target any) error {
	contentType := strings.TrimSpace(strings.Split(c.Get(fiber.HeaderContentType), ";")[0])
	if !strings.EqualFold(contentType, fiber.MIMEApplicationJSON) {
		return errors.New("application/json content type required")
	}
	return decode(c.Body(), target)
}

func optionalString(raw json.RawMessage) (*string, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func isJSONNull(raw json.RawMessage) bool   { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func parseID(raw string) (uuid.UUID, error) { return uuid.Parse(raw) }

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, asset.ErrAssetNotFound):
		return apiError(c, fiber.StatusNotFound, "ASSET004", "Asset not found")
	case errors.Is(err, asset.ErrBindingSourceMissing):
		return apiError(c, fiber.StatusNotFound, "ASSET_SOURCE004", "Measurement source not found")
	case errors.Is(err, asset.ErrAssetNameExists):
		return apiError(c, fiber.StatusConflict, "ASSET001", "Asset name already exists under parent")
	case errors.Is(err, asset.ErrAssetHasDependents):
		return apiError(c, fiber.StatusConflict, "ASSET_DEPENDENTS", "Asset has dependent hierarchy or measurements")
	case errors.Is(err, asset.ErrBindingSourceExists):
		return apiError(c, fiber.StatusConflict, "ASSET_SOURCE_EXISTS", "Measurement source already has an owner")
	case errors.Is(err, asset.ErrInvalidAssetInput), errors.Is(err, asset.ErrInvalidAssetList), errors.Is(err, asset.ErrInvalidMoveInput),
		errors.Is(err, asset.ErrInvalidAsset), errors.Is(err, asset.ErrInvalidAssetKind), errors.Is(err, asset.ErrInvalidAssetPlacement),
		errors.Is(err, asset.ErrHierarchyCycle), errors.Is(err, asset.ErrHierarchyDepth), errors.Is(err, asset.ErrInvalidBinding),
		errors.Is(err, asset.ErrInvalidSemantic), errors.Is(err, asset.ErrUnknownUnit), errors.Is(err, asset.ErrIncompatibleUnit),
		errors.Is(err, asset.ErrAmbiguousReference), errors.Is(err, asset.ErrInvalidPrecision), errors.Is(err, asset.ErrInvalidAggregation),
		errors.Is(err, asset.ErrDuplicateMeasurement), errors.Is(err, asset.ErrConflictingMeters), errors.Is(err, asset.ErrVirtualMeterCycle),
		errors.Is(err, asset.ErrIncompatibleSource):
		return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", "Invalid Asset request")
	case errors.Is(err, asset.ErrMeasurementUnavailable):
		return apiError(c, fiber.StatusConflict, "ASSET_LIVE_EMPTY", "Asset has no measurement bindings")
	case errors.Is(err, asset.ErrConnectivitySourceNotFound):
		return apiError(c, fiber.StatusNotFound, "ASSET_CONNECTIVITY_SOURCE004", "Connectivity source not found")
	case errors.Is(err, asset.ErrConnectivityUnavailable):
		return apiError(c, fiber.StatusConflict, "ASSET_CONNECTIVITY_INCOMPLETE", "Connectivity chain is incomplete")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}
