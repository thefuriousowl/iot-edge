package pluginhttp

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
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

type Service interface {
	Types() []plugin.Manifest
	Create(context.Context, plugin.CreateInput) (*plugin.Instance, error)
	Get(context.Context, uuid.UUID) (*plugin.Instance, error)
	List(context.Context, plugin.ListInput) (*plugin.ListResult, error)
	Update(context.Context, uuid.UUID, plugin.UpdateInput) (*plugin.Instance, error)
	SetEnabled(context.Context, uuid.UUID, bool) (*plugin.Instance, error)
	Delete(context.Context, uuid.UUID) error
}

type RuntimeManager interface {
	Reconcile(context.Context) error
	Restart(context.Context, uuid.UUID) error
	Status(uuid.UUID) plugin.RuntimeStatus
}

type Handler struct {
	service Service
	manager RuntimeManager
}

type createRequest struct {
	Type    plugin.Type     `json:"type"`
	Name    string          `json:"name"`
	Enabled *bool           `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

type updateRequest struct {
	Name    *string         `json:"name"`
	Enabled *bool           `json:"enabled"`
	Config  json.RawMessage `json:"config"`
}

type runtimeErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type runtimeResponse struct {
	State            plugin.RuntimeState   `json:"state"`
	StartedAt        *time.Time            `json:"started_at"`
	LastTransitionAt *time.Time            `json:"last_transition_at"`
	Error            *runtimeErrorResponse `json:"error"`
}

type instanceListResponse struct {
	ID            uuid.UUID       `json:"id"`
	Type          plugin.Type     `json:"type"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	ConfigVersion uint            `json:"config_version"`
	Runtime       runtimeResponse `json:"runtime"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type instanceDetailResponse struct {
	instanceListResponse
	Config plugin.Config `json:"config"`
}

type statusResponse struct {
	ID      uuid.UUID       `json:"id"`
	Type    plugin.Type     `json:"type"`
	Enabled bool            `json:"enabled"`
	Runtime runtimeResponse `json:"runtime"`
}

func NewHandler(service Service, manager RuntimeManager) *Handler {
	return &Handler{service: service, manager: manager}
}

func (handler *Handler) Types(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"data": handler.service.Types()})
}

func (handler *Handler) Create(c *fiber.Ctx) error {
	var request createRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	instance, err := handler.service.Create(c.UserContext(), plugin.CreateInput{Type: request.Type, Name: request.Name, Enabled: request.Enabled, Config: request.Config})
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.manager.Reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.Status(fiber.StatusCreated).JSON(handler.detail(instance))
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	instance, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.detail(instance))
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
	data := make([]instanceListResponse, 0, len(result.Data))
	for index := range result.Data {
		data = append(data, handler.list(&result.Data[index]))
	}
	return c.JSON(fiber.Map{
		"data":       data,
		"pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	instance, err := handler.service.Update(c.UserContext(), id, plugin.UpdateInput{Name: request.Name, Enabled: request.Enabled, Config: request.Config})
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.manager.Reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.JSON(handler.detail(instance))
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	if err := handler.manager.Reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) Enable(c *fiber.Ctx) error {
	return handler.setEnabled(c, true)
}

func (handler *Handler) Disable(c *fiber.Ctx) error {
	return handler.setEnabled(c, false)
}

func (handler *Handler) Restart(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	if err := decodeEmpty(c.Body()); err != nil {
		return validation(c, "Invalid request body")
	}
	instance, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	if !instance.Enabled {
		return apiError(c, fiber.StatusConflict, "PLG007", "Enable the Plugin instance before restarting it")
	}
	if err := handler.manager.Restart(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.status(instance))
}

func (handler *Handler) Status(c *fiber.Ctx) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	instance, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.status(instance))
}

func (handler *Handler) setEnabled(c *fiber.Ctx, enabled bool) error {
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Plugin instance ID")
	}
	if err := decodeEmpty(c.Body()); err != nil {
		return validation(c, "Invalid request body")
	}
	instance, err := handler.service.SetEnabled(c.UserContext(), id, enabled)
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.manager.Reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.JSON(handler.detail(instance))
}

func (handler *Handler) list(instance *plugin.Instance) instanceListResponse {
	return instanceListResponse{
		ID: instance.ID, Type: instance.Type, Name: instance.Name, Enabled: instance.Enabled, ConfigVersion: instance.ConfigVersion,
		Runtime: projectRuntime(handler.manager.Status(instance.ID)), CreatedAt: instance.CreatedAt, UpdatedAt: instance.UpdatedAt,
	}
}

func (handler *Handler) detail(instance *plugin.Instance) instanceDetailResponse {
	return instanceDetailResponse{instanceListResponse: handler.list(instance), Config: append(plugin.Config(nil), instance.Config...)}
}

func (handler *Handler) status(instance *plugin.Instance) statusResponse {
	return statusResponse{ID: instance.ID, Type: instance.Type, Enabled: instance.Enabled, Runtime: projectRuntime(handler.manager.Status(instance.ID))}
}

func projectRuntime(status plugin.RuntimeStatus) runtimeResponse {
	state := status.State
	if state == "" {
		state = plugin.RuntimeStateStopped
	}
	response := runtimeResponse{State: state}
	if status.StartedAt != nil {
		startedAt := status.StartedAt.UTC()
		response.StartedAt = &startedAt
	}
	if !status.LastTransitionAt.IsZero() {
		transition := status.LastTransitionAt.UTC()
		response.LastTransitionAt = &transition
	}
	response.Error = sanitizeRuntimeError(status.LastError)
	return response
}

func sanitizeRuntimeError(err error) *runtimeErrorResponse {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, plugin.ErrConfigVersionMismatch):
		return &runtimeErrorResponse{Code: "PLUGIN_CONFIG_VERSION", Message: "Plugin configuration must be updated"}
	case errors.Is(err, plugin.ErrCapabilityUnavailable):
		return &runtimeErrorResponse{Code: "PLUGIN_CAPABILITY_UNAVAILABLE", Message: "A required platform capability is unavailable"}
	case errors.Is(err, plugin.ErrNotRegistered):
		return &runtimeErrorResponse{Code: "PLUGIN_TYPE_UNAVAILABLE", Message: "Plugin implementation is unavailable"}
	case errors.Is(err, plugin.ErrInvalidConfig):
		return &runtimeErrorResponse{Code: "PLUGIN_CONFIG_INVALID", Message: "Plugin configuration is invalid"}
	case errors.Is(err, plugin.ErrConfigValidationUnavailable):
		return &runtimeErrorResponse{Code: "PLUGIN_VALIDATION_UNAVAILABLE", Message: "Plugin configuration validation is temporarily unavailable"}
	case errors.Is(err, plugin.ErrRuntimeUnavailable):
		return &runtimeErrorResponse{Code: "PLUGIN_RUNTIME_UNAVAILABLE", Message: "Plugin runtime implementation is unavailable"}
	case errors.Is(err, plugin.ErrRuntimePanicked), errors.Is(err, plugin.ErrRuntimeExited):
		return &runtimeErrorResponse{Code: "PLUGIN_RUNTIME_STOPPED", Message: "Plugin runtime stopped unexpectedly"}
	default:
		return &runtimeErrorResponse{Code: "PLUGIN_RUNTIME_FAILED", Message: "Plugin runtime failed"}
	}
}

func parseListInput(c *fiber.Ctx) (plugin.ListInput, error) {
	input := plugin.ListInput{Search: c.Query("search")}
	if raw := c.Query("type"); raw != "" {
		pluginType := plugin.Type(raw)
		input.Type = &pluginType
	}
	if raw := c.Query("enabled"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return plugin.ListInput{}, err
		}
		input.Enabled = &enabled
	}
	var err error
	input.Page, err = positiveQuery(c, "page", 1)
	if err == nil {
		input.PerPage, err = positiveQuery(c, "per_page", 20)
	}
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

func decodeEmpty(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return decode(body, &struct{}{})
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, plugin.ErrInstanceNotFound):
		return apiError(c, fiber.StatusNotFound, "PLG001", "Plugin instance not found")
	case errors.Is(err, plugin.ErrInstanceNameExists):
		return apiError(c, fiber.StatusConflict, "PLG002", "Plugin instance name already exists")
	case errors.Is(err, plugin.ErrNotRegistered):
		return apiError(c, fiber.StatusBadRequest, "PLG003", "Plugin type is unavailable")
	case errors.Is(err, plugin.ErrMultipleInstancesNotAllowed):
		return apiError(c, fiber.StatusConflict, "PLG004", "Plugin type allows only one instance")
	case errors.Is(err, plugin.ErrConfigVersionMismatch):
		return apiError(c, fiber.StatusConflict, "PLG005", "Plugin configuration must be updated before enabling")
	case errors.Is(err, plugin.ErrManagerNotStarted):
		return apiError(c, fiber.StatusServiceUnavailable, "PLG006", "Plugin runtime manager is unavailable")
	case errors.Is(err, plugin.ErrConfigValidationUnavailable):
		return apiError(c, fiber.StatusServiceUnavailable, "PLG009", "Plugin configuration validation is temporarily unavailable")
	case errors.Is(err, plugin.ErrInvalidInput), errors.Is(err, plugin.ErrInvalidConfig), errors.Is(err, plugin.ErrInvalidInstance):
		return validation(c, "Invalid Plugin configuration")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func reconciliationPending(c *fiber.Ctx) error {
	return apiError(c, fiber.StatusServiceUnavailable, "PLG008", "Plugin configuration was saved, but runtime reconciliation is pending")
}

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}
