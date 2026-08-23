package publisherhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

func TestPublisherHandlerListsTypesAndFiltersUnifiedSources(t *testing.T) {
	instanceID := uuid.New()
	reference := publisher.PluginOutputSource(instanceID, "energy.today_kwh")
	periodEnd := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	periodStart := periodEnd.Add(-time.Hour)
	coverage := 82.5
	service := &handlerService{types: []publisher.DefinitionDescriptor{{Type: publisher.TypeMQTT, ConfigVersion: 3}}}
	sources := &handlerSources{catalog: []publisher.SourceCatalogEntry{{
		Descriptor: publisher.SourceDescriptor{Reference: reference, Name: "Energy today", OwnerName: "Energy Management", SchemaVersion: 1, DataType: publisher.SourceDataTypeFloat64, Unit: "kWh", PeriodKind: publisher.SourcePeriodWindowed, Enabled: true},
		Current:    publisher.SourceCurrent{Quality: publisher.SourceQualityPartial, Sequence: 12, ObservedAt: &periodEnd, PeriodStart: &periodStart, PeriodEnd: &periodEnd, CoveragePercent: &coverage},
	}}}
	app := publisherTestApp(service, sources, publisher.NewJSONPayloadEngine())

	typesResponse, err := app.Test(httptest.NewRequest("GET", "/api/publisher-types", nil))
	if err != nil {
		t.Fatalf("types request: %v", err)
	}
	if typesResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("types status = %d", typesResponse.StatusCode)
	}
	var typesBody struct {
		Data []publisher.DefinitionDescriptor `json:"data"`
	}
	decodeResponse(t, typesResponse, &typesBody)
	if len(typesBody.Data) != 1 || typesBody.Data[0].Type != publisher.TypeMQTT || typesBody.Data[0].ConfigVersion != 3 {
		t.Fatalf("types = %+v", typesBody.Data)
	}

	response, err := app.Test(httptest.NewRequest("GET", "/api/publisher-sources?kind=plugin_output&enabled=true&search=energy", nil))
	if err != nil {
		t.Fatalf("sources request: %v", err)
	}
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("sources status = %d", response.StatusCode)
	}
	if sources.lastCatalog.Kind == nil || *sources.lastCatalog.Kind != publisher.SourceKindPluginOutput || sources.lastCatalog.Enabled == nil || !*sources.lastCatalog.Enabled || sources.lastCatalog.Search != "energy" {
		t.Fatalf("catalog input = %+v", sources.lastCatalog)
	}
	var body struct {
		Data []publisher.SourceCatalogEntry `json:"data"`
	}
	decodeResponse(t, response, &body)
	if len(body.Data) != 1 || body.Data[0].Descriptor.Reference != reference || body.Data[0].Descriptor.PeriodKind != publisher.SourcePeriodWindowed || body.Data[0].Current.Sequence != 12 || body.Data[0].Current.CoveragePercent == nil || *body.Data[0].Current.CoveragePercent != 82.5 || !body.Data[0].Current.PeriodStart.Equal(periodStart) || !body.Data[0].Current.PeriodEnd.Equal(periodEnd) {
		t.Fatalf("catalog body = %+v", body.Data)
	}
}

func TestPublisherHandlerValidatesPayloadWithResolvedSources(t *testing.T) {
	tagID := uuid.New()
	selection := publisher.SourceSelection{Alias: "power", Reference: publisher.TagSource(tagID)}
	sources := &handlerSources{resolved: []publisher.ResolvedSource{{
		Alias:      "power",
		Descriptor: publisher.SourceDescriptor{Reference: selection.Reference, Name: "Power", SchemaVersion: 1, DataType: publisher.SourceDataTypeFloat64, Unit: "kW", PeriodKind: publisher.SourcePeriodInstantaneous, Enabled: true},
	}}}
	app := publisherTestApp(&handlerService{}, sources, publisher.NewJSONPayloadEngine())
	requestBody := `{"payload_template":"{\"timestamp\":{{published_unix_ms}},\"power\":{{value \"power\"}}}","sources":[{"alias":"power","reference":{"kind":"tag","tag_id":"` + tagID.String() + `"}}]}`
	response, err := app.Test(httptest.NewRequest("POST", "/api/publisher-payloads/validate", strings.NewReader(requestBody)))
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	if response.StatusCode != fiber.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("validate status = %d body=%s", response.StatusCode, payload)
	}
	if len(sources.lastResolve) != 1 || sources.lastResolve[0] != selection {
		t.Fatalf("resolve selections = %+v", sources.lastResolve)
	}
	var body struct {
		ReferencedAliases []string        `json:"referenced_aliases"`
		HelperCalls       int             `json:"helper_calls"`
		Good              json.RawMessage `json:"good"`
		Unavailable       json.RawMessage `json:"unavailable"`
		Windowed          json.RawMessage `json:"windowed"`
	}
	decodeResponse(t, response, &body)
	if len(body.ReferencedAliases) != 1 || body.ReferencedAliases[0] != "power" || body.HelperCalls != 2 {
		t.Fatalf("validation metadata = %+v", body)
	}
	for name, fixture := range map[string]json.RawMessage{"good": body.Good, "unavailable": body.Unavailable, "windowed": body.Windowed} {
		if !json.Valid(fixture) {
			t.Errorf("%s fixture invalid: %s", name, fixture)
		}
	}

	invalid := `{"payload_template":"{\"power\":{{unknown \"power\"}}}","sources":[{"alias":"power","reference":{"kind":"tag","tag_id":"` + tagID.String() + `"}}]}`
	response, _ = app.Test(httptest.NewRequest("POST", "/api/publisher-payloads/validate", strings.NewReader(invalid)))
	assertAPIError(t, response, fiber.StatusBadRequest, "PUB008", "Data Publisher JSON payload template is invalid")
}

func TestPublisherHandlerCRUDUsesStrictDTOsAndSafeProjections(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	tagID := uuid.New()
	publisherID := uuid.New()
	credentialID := uuid.New()
	description := "Telemetry"
	config := publisher.Config(`{"trigger":{"mode":"interval","interval_ms":60000},"mqtt":{"broker_url":"mqtts://broker.example.com:8883","auth":{"username":{"name":"mqtt.username"},"password":{"name":"mqtt.password"}},"tls":{},"publish":{"topic":"site/telemetry","qos":1,"retain":false,"payload_template":"{\"power\":{{value \"power\"}}}"},"diagnostics":[],"keep_alive_ms":30000,"connect_timeout_ms":10000,"publish_timeout_ms":10000,"reconnect_min_ms":1000,"reconnect_max_ms":60000,"queue_capacity":256,"diagnostic_history_depth":100}}`)
	selection := publisher.SourceSelection{Alias: "power", Reference: publisher.TagSource(tagID)}
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeMQTT, Name: "MQTT Telemetry", Description: &description, CredentialID: &credentialID, Config: config, ConfigVersion: 3, Sources: []publisher.SourceSelection{selection}, SourceCount: 1, CreatedAt: now, UpdatedAt: now}
	service := &handlerService{entity: entity, list: &publisher.ListResult{Data: []publisher.Publisher{*entity}, Page: 1, PerPage: 20, Total: 1, TotalPages: 1}}
	app := publisherTestApp(service, &handlerSources{}, publisher.NewJSONPayloadEngine())

	createBody := `{"type":"mqtt","name":" MQTT Telemetry ","description":"Telemetry","enabled":false,"credential_id":"` + credentialID.String() + `","config":` + string(config) + `,"sources":[{"alias":"power","reference":{"kind":"tag","tag_id":"` + tagID.String() + `"}}]}`
	response, err := app.Test(httptest.NewRequest("POST", "/api/data-publishers", strings.NewReader(createBody)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if response.StatusCode != fiber.StatusCreated {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("create status = %d body=%s", response.StatusCode, payload)
	}
	if service.create.Type != publisher.TypeMQTT || service.create.Name != " MQTT Telemetry " || service.create.CredentialID == nil || *service.create.CredentialID != credentialID || len(service.create.Sources) != 1 || service.create.Sources[0] != selection || string(service.create.Config) != string(config) {
		t.Fatalf("create input = %+v", service.create)
	}
	var detail map[string]any
	decodeResponse(t, response, &detail)
	if detail["id"] != publisherID.String() || detail["config_version"] != float64(3) || detail["credential_id"] != credentialID.String() {
		t.Fatalf("detail = %+v", detail)
	}
	serialized, _ := json.Marshal(detail)
	if !bytesContainAll(serialized, []byte(`"mqtt.username"`), []byte(`"mqtt.password"`)) || bytesContainAll(serialized, []byte(`test-password`)) {
		t.Fatalf("unsafe detail projection = %s", serialized)
	}

	response, _ = app.Test(httptest.NewRequest("GET", "/api/data-publishers?type=mqtt&enabled=false&search=telemetry&page=1&per_page=20", nil))
	if response.StatusCode != fiber.StatusOK || service.listInput.Type == nil || *service.listInput.Type != publisher.TypeMQTT || service.listInput.Enabled == nil || *service.listInput.Enabled || service.listInput.Search != "telemetry" {
		t.Fatalf("list status/input = %d %+v", response.StatusCode, service.listInput)
	}
	var listBody map[string]any
	decodeResponse(t, response, &listBody)
	listJSON, _ := json.Marshal(listBody)
	if bytesContainAll(listJSON, []byte(`"config"`)) || bytesContainAll(listJSON, []byte(`"sources"`)) {
		t.Fatalf("list leaked detail fields: %s", listJSON)
	}

	response, _ = app.Test(httptest.NewRequest("PUT", "/api/data-publishers/"+publisherID.String(), strings.NewReader(`{"description":null,"name":"Renamed"}`)))
	if response.StatusCode != fiber.StatusOK || service.updateID != publisherID || service.update.Name == nil || *service.update.Name != "Renamed" || !service.update.Description.Set || service.update.Description.Value != nil {
		t.Fatalf("update status/input = %d %+v", response.StatusCode, service.update)
	}
	response, _ = app.Test(httptest.NewRequest("PUT", "/api/data-publishers/"+publisherID.String(), strings.NewReader(`{"credential_id":null}`)))
	if response.StatusCode != fiber.StatusOK || !service.update.CredentialID.Set || service.update.CredentialID.Value != nil {
		t.Fatalf("clear credential status/input = %d %+v", response.StatusCode, service.update.CredentialID)
	}

	response, _ = app.Test(httptest.NewRequest("DELETE", "/api/data-publishers/"+publisherID.String(), nil))
	if response.StatusCode != fiber.StatusNoContent || service.deleteID != publisherID {
		t.Fatalf("delete status/id = %d %s", response.StatusCode, service.deleteID)
	}

	response, _ = app.Test(httptest.NewRequest("POST", "/api/data-publishers", strings.NewReader(`{"type":"mqtt","name":"Strict","unknown":true}`)))
	assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR", "Invalid request body")
}

func TestPublisherHandlerMapsStableSanitizedErrors(t *testing.T) {
	tests := []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{publisher.ErrPublisherNotFound, fiber.StatusNotFound, "PUB001", "Data Publisher not found"},
		{publisher.ErrPublisherNameExists, fiber.StatusConflict, "PUB002", "Data Publisher name already exists"},
		{publisher.ErrUnsupportedPublisherType, fiber.StatusBadRequest, "PUB003", "Data Publisher type is unavailable"},
		{publisher.ErrSourceNotFound, fiber.StatusBadRequest, "PUB004", "Selected Publisher source was not found"},
		{publisher.ErrIncompatibleSource, fiber.StatusBadRequest, "PUB005", "Selected source is incompatible with this Publisher"},
		{publisher.ErrConfigVersionMismatch, fiber.StatusConflict, "PUB006", "Data Publisher configuration must be updated"},
		{publisher.ErrMQTTConnectionDNSFailed, fiber.StatusBadGateway, "PUB021", "MQTT broker hostname could not be resolved"},
		{publisher.ErrMQTTConnectionTCPFailed, fiber.StatusBadGateway, "PUB022", "MQTT broker TCP connection failed"},
		{publisher.ErrMQTTConnectionTLSFailed, fiber.StatusBadGateway, "PUB023", "MQTT TLS verification or handshake failed"},
		{publisher.ErrMQTTConnectionAuthFailed, fiber.StatusBadGateway, "PUB024", "MQTT authentication or Client ID was rejected"},
		{publisher.ErrMQTTConnectionTimedOut, fiber.StatusGatewayTimeout, "PUB025", "MQTT broker connection timed out"},
		{errors.New("database details password=secret"), fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error"},
	}
	for _, test := range tests {
		service := &handlerService{err: test.err}
		app := publisherTestApp(service, &handlerSources{}, publisher.NewJSONPayloadEngine())
		response, _ := app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+uuid.NewString(), nil))
		assertAPIError(t, response, test.status, test.code, test.message)
	}
}

func TestPublisherHandlerLifecycleStatusAndGenericDiagnostics(t *testing.T) {
	publisherID := uuid.New()
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeMQTT, Name: "MQTT", Enabled: false, ConfigVersion: 3}
	service := &handlerService{entity: entity}
	manager := &handlerManager{status: publisher.RuntimeStatus{PublisherID: publisherID, Type: publisher.TypeMQTT, State: publisher.RuntimeStateRunning, Connected: true, DeliveryCount: 7, Sources: []publisher.SourceRuntimeStatus{}}, diagnostics: []publisher.MQTTDiagnosticEvent{{Sequence: 3, Label: "ack", Topic: "site/ack", QoS: 1, Format: "json", Payload: `{"status":"success"}`, ReceivedAt: time.Now().UTC()}}}
	app := publisherTestAppWithOptions(service, &handlerSources{}, publisher.NewJSONPayloadEngine(), WithRuntimeManager(manager))

	response, _ := app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/enable", nil))
	if response.StatusCode != fiber.StatusOK || service.enabledID != publisherID || !service.enabled || manager.reconcileCount != 1 {
		t.Fatalf("enable = %d %s %v reconcile=%d", response.StatusCode, service.enabledID, service.enabled, manager.reconcileCount)
	}
	response.Body.Close()

	entity.Enabled = true
	response, _ = app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/restart", nil))
	if response.StatusCode != fiber.StatusOK || manager.restartID != publisherID {
		t.Fatalf("restart = %d %s", response.StatusCode, manager.restartID)
	}
	response.Body.Close()

	response, _ = app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String()+"/status", nil))
	var statusBody struct {
		Runtime publisher.RuntimeStatus `json:"runtime"`
	}
	decodeResponse(t, response, &statusBody)
	if statusBody.Runtime.State != publisher.RuntimeStateRunning || !statusBody.Runtime.Connected || statusBody.Runtime.DeliveryCount != 7 {
		t.Fatalf("status = %+v", statusBody.Runtime)
	}

	response, _ = app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String()+"/diagnostics", nil))
	var diagnosticsBody struct {
		Data []publisher.MQTTDiagnosticEvent `json:"data"`
	}
	decodeResponse(t, response, &diagnosticsBody)
	if len(diagnosticsBody.Data) != 1 || diagnosticsBody.Data[0].Label != "ack" || diagnosticsBody.Data[0].Payload != `{"status":"success"}` {
		t.Fatalf("diagnostics = %+v", diagnosticsBody.Data)
	}

	manager.diagnostics = nil
	response, _ = app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String()+"/diagnostics", nil))
	diagnosticsBody.Data = nil
	decodeResponse(t, response, &diagnosticsBody)
	if diagnosticsBody.Data == nil || len(diagnosticsBody.Data) != 0 {
		t.Fatalf("empty diagnostics = %#v, want non-nil empty array", diagnosticsBody.Data)
	}
}

func TestPublisherHandlerOverlaysPersistedMetadataOnEmptyStoppedRuntime(t *testing.T) {
	publisherID := uuid.New()
	updatedAt := time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeHTTPServer, Enabled: false, ConfigVersion: 4, UpdatedAt: updatedAt}
	manager := &handlerManager{status: publisher.RuntimeStatus{State: publisher.RuntimeStateStopped}}
	app := publisherTestAppWithOptions(&handlerService{entity: entity}, &handlerSources{}, publisher.NewJSONPayloadEngine(), WithRuntimeManager(manager))

	response, _ := app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String()+"/status", nil))
	var body struct {
		Runtime publisher.RuntimeStatus `json:"runtime"`
	}
	decodeResponse(t, response, &body)
	if body.Runtime.PublisherID != publisherID || body.Runtime.Type != publisher.TypeHTTPServer || body.Runtime.ConfigVersion != 4 || body.Runtime.State != publisher.RuntimeStateStopped || !body.Runtime.LastTransitionAt.Equal(updatedAt) {
		t.Fatalf("stopped runtime projection = %#v", body.Runtime)
	}
}

func TestPublisherHandlerProjectsHTTPMetadataAndProbesRunningListener(t *testing.T) {
	publisherID := uuid.New()
	config := publisher.Config(`{"trigger":{"mode":"interval","interval_ms":60000},"http":{"bind_address":"0.0.0.0","port":8088,"path":"/snapshot","access":{"mode":"anonymous","anonymous_acknowledged":true},"quality_policy":"payload","read_timeout_ms":5000,"write_timeout_ms":5000,"idle_timeout_ms":30000,"max_header_bytes":16384,"max_connections":64},"response":{"payload_template":"{\"publisher_id\":{{publisher_id}}}"}}`)
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeHTTPServer, Name: "HTTP", Enabled: true, Config: config, ConfigVersion: 4}
	service := &handlerService{entity: entity}
	manager := &handlerManager{status: publisher.RuntimeStatus{PublisherID: publisherID, Type: publisher.TypeHTTPServer, State: publisher.RuntimeStateRunning, ConfigVersion: 4, Sources: []publisher.SourceRuntimeStatus{}}}
	probedAt := time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
	prober := &handlerHTTPServerProber{result: publisher.HTTPServerProbeResult{Reachable: true, ProbedAt: probedAt, LatencyMS: 1.25}}
	app := publisherTestAppWithOptions(service, &handlerSources{}, publisher.NewJSONPayloadEngine(), WithRuntimeManager(manager), WithHTTPServerListenerProber(prober))

	response, _ := app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String(), nil))
	var detail struct {
		Endpoint *publisher.HTTPServerEndpointMetadata `json:"endpoint"`
	}
	decodeResponse(t, response, &detail)
	if detail.Endpoint == nil || detail.Endpoint.BindAddress != "0.0.0.0" || detail.Endpoint.Port != 8088 || detail.Endpoint.Path != "/snapshot" || detail.Endpoint.AccessMode != publisher.HTTPAccessAnonymous {
		t.Fatalf("detail endpoint = %#v", detail.Endpoint)
	}

	response, _ = app.Test(httptest.NewRequest("GET", "/api/data-publishers/"+publisherID.String()+"/status", nil))
	var status struct {
		Endpoint *publisher.HTTPServerEndpointMetadata `json:"endpoint"`
	}
	decodeResponse(t, response, &status)
	if status.Endpoint == nil || status.Endpoint.QualityPolicy != publisher.HTTPQualityPayload {
		t.Fatalf("status endpoint = %#v", status.Endpoint)
	}

	response, _ = app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/probe-listener", nil))
	var result publisher.HTTPServerProbeResult
	decodeResponse(t, response, &result)
	if !result.Reachable || result.LatencyMS != 1.25 || prober.entity.ID != publisherID {
		t.Fatalf("probe result = %#v entity=%s", result, prober.entity.ID)
	}

	entity.Enabled = false
	response, _ = app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/probe-listener", nil))
	assertAPIError(t, response, fiber.StatusConflict, "PUB026", "Enable the HTTP Server Publisher before probing its listener")
	entity.Enabled = true
	manager.status.State = publisher.RuntimeStateError
	response, _ = app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/probe-listener", nil))
	assertAPIError(t, response, fiber.StatusConflict, "PUB027", "HTTP Server Publisher listener is not running")
}

func TestPublisherHandlerSanitizesHTTPListenerProbeFailures(t *testing.T) {
	publisherID := uuid.New()
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeHTTPServer, Enabled: true, ConfigVersion: 4}
	manager := &handlerManager{status: publisher.RuntimeStatus{State: publisher.RuntimeStateRunning}}
	for _, test := range []struct {
		err     error
		status  int
		code    string
		message string
	}{
		{publisher.ErrHTTPServerProbeFailed, fiber.StatusBadGateway, "PUB028", "HTTP Server listener probe failed"},
		{publisher.ErrHTTPServerProbeTimedOut, fiber.StatusGatewayTimeout, "PUB029", "HTTP Server listener probe timed out"},
	} {
		prober := &handlerHTTPServerProber{err: test.err}
		app := publisherTestAppWithOptions(&handlerService{entity: entity}, &handlerSources{}, publisher.NewJSONPayloadEngine(), WithRuntimeManager(manager), WithHTTPServerListenerProber(prober))
		response, _ := app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/probe-listener", nil))
		assertAPIError(t, response, test.status, test.code, test.message)
	}
}

func TestPublisherHandlerConnectionUsesStoredCredentialProfileOnly(t *testing.T) {
	publisherID := uuid.New()
	selection := publisher.SourceSelection{Alias: "power", Reference: publisher.TagSource(uuid.New())}
	entity := &publisher.Publisher{ID: publisherID, Type: publisher.TypeMQTT, Name: "MQTT", Enabled: false, ConfigVersion: 3, Sources: []publisher.SourceSelection{selection}}
	service := &handlerService{entity: entity}
	sources := &handlerSources{resolved: []publisher.ResolvedSource{{Alias: "power", Descriptor: publisher.SourceDescriptor{Reference: selection.Reference, Name: "Power", SchemaVersion: 1, DataType: publisher.SourceDataTypeFloat64, PeriodKind: publisher.SourcePeriodInstantaneous, Enabled: true}}}}
	tester := &handlerConnectionTester{result: publisher.MQTTConnectionTestResult{Connected: true, ConnectedAt: time.Now().UTC(), LatencyMS: 24}}
	app := publisherTestAppWithOptions(service, sources, publisher.NewJSONPayloadEngine(), WithMQTTConnectionTester(tester))

	response, _ := app.Test(httptest.NewRequest("POST", "/api/data-publishers/"+publisherID.String()+"/test-connection", nil))
	var result publisher.MQTTConnectionTestResult
	decodeResponse(t, response, &result)
	if !result.Connected || result.LatencyMS != 24 || len(tester.overrides) != 0 || len(sources.lastResolve) != 1 {
		t.Fatalf("connection test = %+v overrides=%+v", result, tester.overrides)
	}

	response, _ = app.Test(httptest.NewRequest("PUT", "/api/data-publishers/"+publisherID.String()+"/secrets/mqtt.password", strings.NewReader(`{"value_base64":"c2VjcmV0"}`)))
	if response.StatusCode != fiber.StatusNotFound {
		t.Fatalf("legacy Publisher secret route status = %d, want 404", response.StatusCode)
	}
}

type handlerService struct {
	types     []publisher.DefinitionDescriptor
	entity    *publisher.Publisher
	list      *publisher.ListResult
	err       error
	create    publisher.CreateInput
	listInput publisher.ListInput
	updateID  uuid.UUID
	update    publisher.UpdateInput
	deleteID  uuid.UUID
	enabledID uuid.UUID
	enabled   bool
}

func (service *handlerService) Types() []publisher.DefinitionDescriptor {
	return append([]publisher.DefinitionDescriptor(nil), service.types...)
}

func (service *handlerService) Create(_ context.Context, input publisher.CreateInput) (*publisher.Publisher, error) {
	service.create = input
	return service.entity, service.err
}

func (service *handlerService) Get(context.Context, uuid.UUID) (*publisher.Publisher, error) {
	return service.entity, service.err
}

func (service *handlerService) List(_ context.Context, input publisher.ListInput) (*publisher.ListResult, error) {
	service.listInput = input
	if service.err != nil {
		return nil, service.err
	}
	if service.list == nil {
		return &publisher.ListResult{Data: []publisher.Publisher{}, Page: input.Page, PerPage: input.PerPage}, nil
	}
	return service.list, nil
}

func (service *handlerService) Update(_ context.Context, id uuid.UUID, input publisher.UpdateInput) (*publisher.Publisher, error) {
	service.updateID, service.update = id, input
	return service.entity, service.err
}

func (service *handlerService) SetEnabled(_ context.Context, id uuid.UUID, enabled bool) (*publisher.Publisher, error) {
	service.enabledID, service.enabled = id, enabled
	return service.entity, service.err
}

func (service *handlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deleteID = id
	return service.err
}

type handlerSources struct {
	catalog     []publisher.SourceCatalogEntry
	resolved    []publisher.ResolvedSource
	err         error
	lastCatalog publisher.SourceCatalogInput
	lastResolve []publisher.SourceSelection
}

type handlerManager struct {
	status         publisher.RuntimeStatus
	diagnostics    []publisher.MQTTDiagnosticEvent
	reconcileCount int
	restartID      uuid.UUID
	err            error
}

func (manager *handlerManager) Reconcile(context.Context) error {
	manager.reconcileCount++
	return manager.err
}

func (manager *handlerManager) Restart(_ context.Context, id uuid.UUID) error {
	manager.restartID = id
	return manager.err
}

func (manager *handlerManager) Status(uuid.UUID) publisher.RuntimeStatus { return manager.status }
func (manager *handlerManager) Diagnostics(uuid.UUID) []publisher.MQTTDiagnosticEvent {
	return append([]publisher.MQTTDiagnosticEvent(nil), manager.diagnostics...)
}

type handlerConnectionTester struct {
	result    publisher.MQTTConnectionTestResult
	overrides []publisher.MQTTSecretOverride
	err       error
}

type handlerHTTPServerProber struct {
	result publisher.HTTPServerProbeResult
	entity publisher.Publisher
	err    error
}

func (prober *handlerHTTPServerProber) Probe(_ context.Context, entity publisher.Publisher) (publisher.HTTPServerProbeResult, error) {
	prober.entity = entity
	return prober.result, prober.err
}

func (tester *handlerConnectionTester) Test(_ context.Context, _ publisher.Publisher, _ []publisher.ResolvedSource, overrides []publisher.MQTTSecretOverride) (publisher.MQTTConnectionTestResult, error) {
	tester.overrides = make([]publisher.MQTTSecretOverride, len(overrides))
	for index, override := range overrides {
		tester.overrides[index] = override
		tester.overrides[index].Material = override.Material.Clone()
	}
	return tester.result, tester.err
}

func (sources *handlerSources) Catalog(_ context.Context, input publisher.SourceCatalogInput) ([]publisher.SourceCatalogEntry, error) {
	sources.lastCatalog = input
	return append([]publisher.SourceCatalogEntry(nil), sources.catalog...), sources.err
}

func (sources *handlerSources) Resolve(_ context.Context, selections []publisher.SourceSelection) ([]publisher.ResolvedSource, error) {
	sources.lastResolve = append([]publisher.SourceSelection(nil), selections...)
	return append([]publisher.ResolvedSource(nil), sources.resolved...), sources.err
}

func publisherTestApp(service Service, sources SourceCatalog, payload PayloadEngine) *fiber.App {
	return publisherTestAppWithOptions(service, sources, payload)
}

func publisherTestAppWithOptions(service Service, sources SourceCatalog, payload PayloadEngine, options ...HandlerOption) *fiber.App {
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service, sources, payload, options...))
	return app
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func assertAPIError(t *testing.T, response *http.Response, status int, code, message string) {
	t.Helper()
	if response.StatusCode != status {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d want %d body=%s", response.StatusCode, status, payload)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code || body.Error.Message != message {
		t.Fatalf("error = %+v", body.Error)
	}
}

func bytesContainAll(value []byte, needles ...[]byte) bool {
	for _, needle := range needles {
		if !strings.Contains(string(value), string(needle)) {
			return false
		}
	}
	return true
}
