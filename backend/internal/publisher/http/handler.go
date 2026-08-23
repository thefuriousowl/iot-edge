package publisherhttp

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
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

type Service interface {
	Types() []publisher.DefinitionDescriptor
	Create(context.Context, publisher.CreateInput) (*publisher.Publisher, error)
	Get(context.Context, uuid.UUID) (*publisher.Publisher, error)
	List(context.Context, publisher.ListInput) (*publisher.ListResult, error)
	Update(context.Context, uuid.UUID, publisher.UpdateInput) (*publisher.Publisher, error)
	SetEnabled(context.Context, uuid.UUID, bool) (*publisher.Publisher, error)
	Delete(context.Context, uuid.UUID) error
}

type SourceCatalog interface {
	Catalog(context.Context, publisher.SourceCatalogInput) ([]publisher.SourceCatalogEntry, error)
	Resolve(context.Context, []publisher.SourceSelection) ([]publisher.ResolvedSource, error)
}

type PayloadEngine interface {
	Validate(string, []publisher.ResolvedSource) (publisher.JSONPayloadValidation, error)
}

type RuntimeManager interface {
	Reconcile(context.Context) error
	Restart(context.Context, uuid.UUID) error
	Status(uuid.UUID) publisher.RuntimeStatus
	Diagnostics(uuid.UUID) []publisher.MQTTDiagnosticEvent
}

type MQTTConnectionTester interface {
	Test(context.Context, publisher.Publisher, []publisher.ResolvedSource, []publisher.MQTTSecretOverride) (publisher.MQTTConnectionTestResult, error)
}

type HandlerOption func(*Handler)

func WithRuntimeManager(manager RuntimeManager) HandlerOption {
	return func(handler *Handler) { handler.manager = manager }
}

func WithMQTTConnectionTester(tester MQTTConnectionTester) HandlerOption {
	return func(handler *Handler) { handler.tester = tester }
}

type Handler struct {
	service Service
	sources SourceCatalog
	payload PayloadEngine
	manager RuntimeManager
	tester  MQTTConnectionTester
}

type createRequest struct {
	Type         publisher.Type              `json:"type"`
	Name         string                      `json:"name"`
	Description  *string                     `json:"description"`
	Enabled      *bool                       `json:"enabled"`
	Config       json.RawMessage             `json:"config"`
	Sources      []publisher.SourceSelection `json:"sources"`
	CredentialID *uuid.UUID                  `json:"credential_id"`
}

type updateRequest struct {
	Name         *string                      `json:"name"`
	Description  json.RawMessage              `json:"description"`
	Enabled      *bool                        `json:"enabled"`
	Config       json.RawMessage              `json:"config"`
	Sources      *[]publisher.SourceSelection `json:"sources"`
	CredentialID json.RawMessage              `json:"credential_id"`
}

type validatePayloadRequest struct {
	PayloadTemplate string                      `json:"payload_template"`
	Sources         []publisher.SourceSelection `json:"sources"`
}

type listResponse struct {
	ID            uuid.UUID               `json:"id"`
	Type          publisher.Type          `json:"type"`
	Name          string                  `json:"name"`
	Description   *string                 `json:"description,omitempty"`
	Enabled       bool                    `json:"enabled"`
	ConfigVersion uint                    `json:"config_version"`
	SourceCount   int                     `json:"source_count"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Runtime       publisher.RuntimeStatus `json:"runtime"`
	CredentialID  *uuid.UUID              `json:"credential_id,omitempty"`
}

type detailResponse struct {
	listResponse
	Config  json.RawMessage             `json:"config"`
	Sources []publisher.SourceSelection `json:"sources"`
}

func NewHandler(service Service, sources SourceCatalog, payload PayloadEngine, options ...HandlerOption) *Handler {
	handler := &Handler{service: service, sources: sources, payload: payload}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

func (handler *Handler) Types(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	return c.JSON(fiber.Map{"data": handler.service.Types()})
}

func (handler *Handler) Sources(c *fiber.Ctx) error {
	if handler == nil || handler.sources == nil {
		return unavailable(c)
	}
	input, err := parseSourceCatalogInput(c)
	if err != nil {
		return validation(c, "Invalid Publisher source query")
	}
	entries, err := handler.sources.Catalog(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	if entries == nil {
		entries = []publisher.SourceCatalogEntry{}
	}
	return c.JSON(fiber.Map{"data": entries})
}

func (handler *Handler) ValidatePayload(c *fiber.Ctx) error {
	if handler == nil || handler.sources == nil || handler.payload == nil {
		return unavailable(c)
	}
	var request validatePayloadRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	resolved, err := handler.sources.Resolve(c.UserContext(), request.Sources)
	if err != nil {
		return handleError(c, err)
	}
	result, err := handler.payload.Validate(request.PayloadTemplate, resolved)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (handler *Handler) Create(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	var request createRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	if request.Enabled != nil && *request.Enabled && handler.manager == nil {
		return runtimeUnavailable(c)
	}
	result, err := handler.service.Create(c.UserContext(), publisher.CreateInput{
		Type: request.Type, Name: request.Name, Description: request.Description, Enabled: request.Enabled,
		Config: request.Config, Sources: request.Sources, CredentialID: request.CredentialID,
	})
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.Status(fiber.StatusCreated).JSON(handler.projectDetail(result))
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	result, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.projectDetail(result))
}

func (handler *Handler) List(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	input, err := parseListInput(c)
	if err != nil {
		return validation(c, "Invalid Data Publisher query")
	}
	result, err := handler.service.List(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	data := make([]listResponse, len(result.Data))
	for index := range result.Data {
		data[index] = handler.projectList(&result.Data[index])
	}
	return c.JSON(fiber.Map{
		"data":       data,
		"pagination": fiber.Map{"page": result.Page, "per_page": result.PerPage, "total": result.Total, "total_pages": result.TotalPages},
	})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	if request.Enabled != nil && *request.Enabled && handler.manager == nil {
		return runtimeUnavailable(c)
	}
	input := publisher.UpdateInput{Name: request.Name, Enabled: request.Enabled, Sources: request.Sources}
	if request.Config != nil {
		config := publisher.Config(append(json.RawMessage(nil), request.Config...))
		input.Config = &config
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
	if request.CredentialID != nil {
		input.CredentialID.Set = true
		if !isJSONNull(request.CredentialID) {
			var credentialID uuid.UUID
			if err := json.Unmarshal(request.CredentialID, &credentialID); err != nil || credentialID == uuid.Nil {
				return validation(c, "Invalid Credential Profile ID")
			}
			input.CredentialID.Value = &credentialID
		}
	}
	result, err := handler.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.JSON(handler.projectDetail(result))
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	if err := handler.reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) Enable(c *fiber.Ctx) error { return handler.setEnabled(c, true) }

func (handler *Handler) Disable(c *fiber.Ctx) error { return handler.setEnabled(c, false) }

func (handler *Handler) Restart(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil || handler.manager == nil {
		return runtimeUnavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	if err := decodeEmpty(c.Body()); err != nil {
		return validation(c, "Invalid request body")
	}
	entity, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	if !entity.Enabled {
		return apiError(c, fiber.StatusConflict, "PUB012", "Enable the Data Publisher before restarting it")
	}
	if err := handler.manager.Restart(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.status(entity))
}

func (handler *Handler) Status(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil || handler.manager == nil {
		return runtimeUnavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	entity, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(handler.status(entity))
}

func (handler *Handler) Diagnostics(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil || handler.manager == nil {
		return runtimeUnavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	entity, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	if entity.Type != publisher.TypeMQTT {
		return apiError(c, fiber.StatusBadRequest, "PUB003", "Data Publisher type is unavailable")
	}
	events := handler.manager.Diagnostics(id)
	if events == nil {
		events = []publisher.MQTTDiagnosticEvent{}
	}
	return c.JSON(fiber.Map{"data": events})
}

func (handler *Handler) TestConnection(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil || handler.sources == nil || handler.tester == nil {
		return runtimeUnavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	if err := decodeEmpty(c.Body()); err != nil {
		return validation(c, "Invalid request body")
	}
	entity, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	if entity.Type != publisher.TypeMQTT {
		return apiError(c, fiber.StatusBadRequest, "PUB003", "Data Publisher type is unavailable")
	}
	resolved, err := handler.sources.Resolve(c.UserContext(), entity.Sources)
	if err != nil {
		return handleError(c, err)
	}
	result, err := handler.tester.Test(c.UserContext(), *entity, resolved, nil)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(result)
}

func (handler *Handler) setEnabled(c *fiber.Ctx, enabled bool) error {
	if handler == nil || handler.service == nil || handler.manager == nil {
		return runtimeUnavailable(c)
	}
	id, err := parseID(c.Params("id"))
	if err != nil {
		return validation(c, "Invalid Data Publisher ID")
	}
	if err := decodeEmpty(c.Body()); err != nil {
		return validation(c, "Invalid request body")
	}
	entity, err := handler.service.SetEnabled(c.UserContext(), id, enabled)
	if err != nil {
		return handleError(c, err)
	}
	if err := handler.manager.Reconcile(c.UserContext()); err != nil {
		return reconciliationPending(c)
	}
	return c.JSON(handler.projectDetail(entity))
}

func (handler *Handler) reconcile(ctx context.Context) error {
	if handler == nil || handler.manager == nil {
		return nil
	}
	return handler.manager.Reconcile(ctx)
}

func (handler *Handler) projectList(entity *publisher.Publisher) listResponse {
	if entity == nil {
		return listResponse{}
	}
	runtime := handler.runtimeStatus(entity)
	runtime.Sources = nil
	return listResponse{
		ID: entity.ID, Type: entity.Type, Name: entity.Name, Description: cloneString(entity.Description), Enabled: entity.Enabled,
		ConfigVersion: entity.ConfigVersion, SourceCount: entity.SourceCount,
		CredentialID: cloneUUID(entity.CredentialID),
		CreatedAt:    entity.CreatedAt.UTC(), UpdatedAt: entity.UpdatedAt.UTC(),
		Runtime: runtime,
	}
}

func (handler *Handler) projectDetail(entity *publisher.Publisher) detailResponse {
	response := detailResponse{listResponse: handler.projectList(entity), Config: json.RawMessage{}, Sources: []publisher.SourceSelection{}}
	if entity == nil {
		return response
	}
	response.Config = append(json.RawMessage(nil), entity.Config...)
	response.Sources = append([]publisher.SourceSelection(nil), entity.Sources...)
	response.Runtime = handler.runtimeStatus(entity)
	return response
}

func (handler *Handler) runtimeStatus(entity *publisher.Publisher) publisher.RuntimeStatus {
	if entity == nil {
		return publisher.RuntimeStatus{State: publisher.RuntimeStateStopped, Sources: []publisher.SourceRuntimeStatus{}}
	}
	if handler == nil || handler.manager == nil {
		return publisher.RuntimeStatus{PublisherID: entity.ID, Type: entity.Type, State: publisher.RuntimeStateStopped, ConfigVersion: entity.ConfigVersion, Sources: []publisher.SourceRuntimeStatus{}}
	}
	status := handler.manager.Status(entity.ID)
	if status.State == "" {
		status.State = publisher.RuntimeStateStopped
	}
	if status.Sources == nil {
		status.Sources = []publisher.SourceRuntimeStatus{}
	}
	return status
}

func (handler *Handler) status(entity *publisher.Publisher) fiber.Map {
	return fiber.Map{"id": entity.ID, "type": entity.Type, "enabled": entity.Enabled, "runtime": handler.runtimeStatus(entity)}
}

func parseSourceCatalogInput(c *fiber.Ctx) (publisher.SourceCatalogInput, error) {
	input := publisher.SourceCatalogInput{Search: c.Query("search")}
	if raw := c.Query("kind"); raw != "" {
		kind := publisher.SourceKind(raw)
		if kind != publisher.SourceKindTag && kind != publisher.SourceKindPluginOutput {
			return input, errors.New("invalid source kind")
		}
		input.Kind = &kind
	}
	if raw := c.Query("enabled"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return input, err
		}
		input.Enabled = &enabled
	}
	return input, nil
}

func parseListInput(c *fiber.Ctx) (publisher.ListInput, error) {
	input := publisher.ListInput{Search: c.Query("search")}
	if raw := c.Query("type"); raw != "" {
		publisherType := publisher.Type(raw)
		input.Type = &publisherType
	}
	if raw := c.Query("enabled"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return input, err
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

func isJSONNull(value json.RawMessage) bool {
	return strings.EqualFold(string(bytes.TrimSpace(value)), "null")
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, publisher.ErrPublisherNotFound):
		return apiError(c, fiber.StatusNotFound, "PUB001", "Data Publisher not found")
	case errors.Is(err, publisher.ErrPublisherNameExists):
		return apiError(c, fiber.StatusConflict, "PUB002", "Data Publisher name already exists")
	case errors.Is(err, publisher.ErrUnsupportedPublisherType):
		return apiError(c, fiber.StatusBadRequest, "PUB003", "Data Publisher type is unavailable")
	case errors.Is(err, publisher.ErrSourceNotFound):
		return apiError(c, fiber.StatusBadRequest, "PUB004", "Selected Publisher source was not found")
	case errors.Is(err, publisher.ErrIncompatibleSource):
		return apiError(c, fiber.StatusBadRequest, "PUB005", "Selected source is incompatible with this Publisher")
	case errors.Is(err, publisher.ErrCredentialNotFound):
		return apiError(c, fiber.StatusBadRequest, "PUB019", "Selected Credential Profile was not found")
	case errors.Is(err, publisher.ErrCredentialIncompatible):
		return apiError(c, fiber.StatusBadRequest, "PUB020", "Selected Credential Profile is incompatible with this Publisher")
	case errors.Is(err, publisher.ErrConfigVersionMismatch):
		return apiError(c, fiber.StatusConflict, "PUB006", "Data Publisher configuration must be updated")
	case errors.Is(err, publisher.ErrSourceCatalogTooLarge):
		return apiError(c, fiber.StatusRequestEntityTooLarge, "PUB007", "Publisher source catalog is too large")
	case errors.Is(err, publisher.ErrInvalidJSONPayloadTemplate), errors.Is(err, publisher.ErrJSONPayloadTooComplex), errors.Is(err, publisher.ErrJSONPayloadUnknownAlias), errors.Is(err, publisher.ErrJSONPayloadRender), errors.Is(err, publisher.ErrJSONPayloadInvalid), errors.Is(err, publisher.ErrJSONPayloadTooLarge):
		return apiError(c, fiber.StatusBadRequest, "PUB008", "MQTT JSON payload template is invalid")
	case errors.Is(err, publisher.ErrSecretNotFound):
		return apiError(c, fiber.StatusNotFound, "PUB014", "Data Publisher secret was not found")
	case errors.Is(err, publisher.ErrSecretKindMismatch):
		return apiError(c, fiber.StatusConflict, "PUB015", "Data Publisher secret kind cannot be changed")
	case errors.Is(err, publisher.ErrInvalidSecretReference), errors.Is(err, publisher.ErrInvalidSecretMaterial):
		return apiError(c, fiber.StatusBadRequest, "PUB016", "Data Publisher secret material is invalid")
	case errors.Is(err, publisher.ErrPublisherMustBeDisabled):
		return apiError(c, fiber.StatusConflict, "PUB017", "Disable the Data Publisher before testing its connection")
	case errors.Is(err, publisher.ErrMQTTConnectionTestFailed):
		return apiError(c, fiber.StatusBadGateway, "PUB018", "MQTT connection test failed")
	case errors.Is(err, publisher.ErrMQTTConnectionDNSFailed):
		return apiError(c, fiber.StatusBadGateway, "PUB021", "MQTT broker hostname could not be resolved")
	case errors.Is(err, publisher.ErrMQTTConnectionTCPFailed):
		return apiError(c, fiber.StatusBadGateway, "PUB022", "MQTT broker TCP connection failed")
	case errors.Is(err, publisher.ErrMQTTConnectionTLSFailed):
		return apiError(c, fiber.StatusBadGateway, "PUB023", "MQTT TLS verification or handshake failed")
	case errors.Is(err, publisher.ErrMQTTConnectionAuthFailed):
		return apiError(c, fiber.StatusBadGateway, "PUB024", "MQTT authentication or Client ID was rejected")
	case errors.Is(err, publisher.ErrMQTTConnectionTimedOut):
		return apiError(c, fiber.StatusGatewayTimeout, "PUB025", "MQTT broker connection timed out")
	case errors.Is(err, publisher.ErrManagerNotStarted), errors.Is(err, publisher.ErrTransportUnavailable):
		return runtimeUnavailable(c)
	case errors.Is(err, publisher.ErrInvalidInput), errors.Is(err, publisher.ErrInvalidPublisher), errors.Is(err, publisher.ErrInvalidPublisherConfig), errors.Is(err, publisher.ErrInvalidSourceReference), errors.Is(err, publisher.ErrInvalidSourceSelection), errors.Is(err, publisher.ErrDuplicateSourceAlias), errors.Is(err, publisher.ErrDuplicateSource), errors.Is(err, publisher.ErrInvalidCatalogInput):
		return validation(c, "Invalid Data Publisher configuration")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apiError(c, fiber.StatusServiceUnavailable, "PUB009", "Data Publisher operation was interrupted")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func runtimeUnavailable(c *fiber.Ctx) error {
	return apiError(c, fiber.StatusServiceUnavailable, "PUB010", "Data Publisher runtime is unavailable")
}

func reconciliationPending(c *fiber.Ctx) error {
	return apiError(c, fiber.StatusServiceUnavailable, "PUB011", "Data Publisher was saved but runtime reconciliation is pending")
}

func unavailable(c *fiber.Ctx) error {
	return apiError(c, fiber.StatusServiceUnavailable, "PUB010", "Data Publisher API is unavailable")
}

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}
