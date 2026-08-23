package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPTransportRendersImmutableAnonymousSnapshot(t *testing.T) {
	publisherID := uuid.New()
	publishedAt := time.Date(2026, time.August, 23, 9, 30, 0, 0, time.UTC)
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{}}
	factory, err := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(loopbackEphemeralHTTPListener), WithHTTPClock(func() time.Time { return publishedAt }))
	if err != nil {
		t.Fatalf("NewHTTPTransportFactory() error = %v", err)
	}
	entity, sources := httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
	if err != nil {
		t.Fatalf("NewResolvedTransport() error = %v", err)
	}
	transport := created.(*httpSnapshotTransport)
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	endpoint := "http://" + transport.Address() + "/snapshot"

	response := httpRequest(t, http.MethodGet, endpoint, nil, nil)
	assertHTTPResponse(t, response, http.StatusServiceUnavailable, true)
	var unavailable httpErrorResponse
	decodeHTTPBody(t, response, &unavailable)
	if unavailable.Error.Code != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("unavailable response = %#v", unavailable)
	}

	observedAt := publishedAt.Add(-time.Second)
	reference := sources[0].Descriptor.Reference
	snapshot := SourceSnapshot{CapturedAt: observedAt, Samples: []SourceSample{{
		Alias: "power", Reference: reference, Available: true, Sequence: 7, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "kW", Value: 42.5, Quality: SourceQualityGood, ObservedAt: &observedAt,
	}}}
	if err := transport.Publish(context.Background(), snapshot); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	snapshot.Samples[0].Value = 999.0
	response = httpRequest(t, http.MethodGet, endpoint, nil, nil)
	assertHTTPResponse(t, response, http.StatusOK, true)
	var payload struct {
		PublisherID uuid.UUID `json:"publisher_id"`
		PublishedAt time.Time `json:"published_at"`
		Power       float64   `json:"power"`
		Quality     string    `json:"quality"`
	}
	decodeHTTPBody(t, response, &payload)
	if payload.PublisherID != publisherID || !payload.PublishedAt.Equal(publishedAt) || payload.Power != 42.5 || payload.Quality != "good" {
		t.Fatalf("rendered payload = %#v", payload)
	}

	response = httpRequest(t, http.MethodPost, endpoint, nil, nil)
	assertHTTPResponse(t, response, http.StatusMethodNotAllowed, true)
	_ = response.Body.Close()
	response = httpRequest(t, http.MethodGet, "http://"+transport.Address()+"/other", nil, nil)
	assertHTTPResponse(t, response, http.StatusNotFound, true)
	_ = response.Body.Close()
	response = httpRequest(t, http.MethodGet, endpoint, strings.NewReader("body"), nil)
	assertHTTPResponse(t, response, http.StatusRequestEntityTooLarge, true)
	_ = response.Body.Close()

	metrics := transport.Metrics()
	if metrics.ExternalRequestCount != 5 || metrics.RejectedRequestCount != 4 || metrics.LastExternalRequestAt == nil {
		t.Fatalf("HTTP metrics = %#v", metrics)
	}
}

func TestPublisherManagerPushesSnapshotsIntoHTTPWithoutRequestAcquisition(t *testing.T) {
	repository := newSourceTestPublisherRepository()
	sourceFeed := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sourceFeed.add(reference, SourceDescriptor{Reference: reference, Name: "Power", SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	capturedAt := time.Now().UTC()
	sourceFeed.setSnapshot(SourceSnapshot{CapturedAt: capturedAt, Samples: []SourceSample{{
		Alias: "power", Reference: reference, Available: true, Sequence: 1, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "kW", Value: 12.5, Quality: SourceQualityGood, ObservedAt: &capturedAt,
	}}})
	entity, _ := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	entity.Sources = []SourceSelection{{Alias: "power", Reference: reference}}
	entity.SourceCount = 1
	config, _ := ParseHTTPPublisherConfig(entity.Config)
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 60_000}
	encoded, _ := json.Marshal(config)
	entity.Config, _ = normalizeHTTPPublisherConfig(encoded)
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	type listenerResult struct {
		listener net.Listener
		err      error
	}
	listeners := make(chan listenerResult, 1)
	resolver := &secretTestResolver{publisherID: entity.ID, materials: map[string]secretTestResolved{}}
	factory, _ := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(func(_, _ string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		listeners <- listenerResult{listener: listener, err: err}
		return listener, err
	}))
	definitions, _ := NewDefaultDefinitionRegistry()
	transports := NewTransportRegistry()
	if err := transports.Register(TypeHTTPServer, factory); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(repository, sourceFeed, definitions, transports, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background()) })
	createdListener := <-listeners
	if createdListener.err != nil {
		t.Fatalf("creating HTTP listener: %v", createdListener.err)
	}
	listener := createdListener.listener
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	snapshotCalls := sourceFeed.snapshotCalls()
	response := httpRequest(t, http.MethodGet, "http://"+listener.Addr().String()+"/snapshot", nil, nil)
	assertHTTPResponse(t, response, http.StatusOK, true)
	var payload struct {
		Power float64 `json:"power"`
	}
	decodeHTTPBody(t, response, &payload)
	if payload.Power != 12.5 {
		t.Fatalf("Manager HTTP payload = %#v", payload)
	}
	if sourceFeed.snapshotCalls() != snapshotCalls {
		t.Fatalf("HTTP GET initiated source snapshot: before %d, after %d", snapshotCalls, sourceFeed.snapshotCalls())
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.ExternalRequestCount == 1 })

	mutateManagerPublisher(repository, entity.ID, func(stored *Publisher) { stored.Enabled = false })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(disable) error = %v", err)
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.State == RuntimeStateStopped })

	recapturedAt := capturedAt.Add(time.Second)
	sourceFeed.setSnapshot(SourceSnapshot{CapturedAt: recapturedAt, Samples: []SourceSample{{
		Alias: "power", Reference: reference, Available: true, Sequence: 2, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "kW", Value: 18.75, Quality: SourceQualityGood, ObservedAt: &recapturedAt,
	}}})
	mutateManagerPublisher(repository, entity.ID, func(stored *Publisher) { stored.Enabled = true })
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile(re-enable) error = %v", err)
	}
	restartedListener := <-listeners
	if restartedListener.err != nil {
		t.Fatalf("recreating HTTP listener: %v", restartedListener.err)
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	response = httpRequest(t, http.MethodGet, "http://"+restartedListener.listener.Addr().String()+"/snapshot", nil, nil)
	assertHTTPResponse(t, response, http.StatusOK, true)
	decodeHTTPBody(t, response, &payload)
	if payload.Power != 18.75 {
		t.Fatalf("re-enabled Manager HTTP payload = %#v", payload)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestHTTPTransportAuthenticatesCredentialProfileSlots(t *testing.T) {
	tests := []struct {
		name      string
		access    HTTPAccessConfig
		materials map[string]secretTestResolved
		headers   map[string]string
	}{
		{
			name: "API key", access: HTTPAccessConfig{Mode: HTTPAccessAPIKey, APIKeyHeader: "X-Plant-Key"},
			materials: map[string]secretTestResolved{"http.api_key": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("plant-key")}}},
			headers:   map[string]string{"X-Plant-Key": "plant-key"},
		},
		{
			name: "Basic", access: HTTPAccessConfig{Mode: HTTPAccessBasic},
			materials: map[string]secretTestResolved{
				"http.username": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("operator")}},
				"http.password": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("correct horse")}},
			},
			headers: map[string]string{"Authorization": "Basic b3BlcmF0b3I6Y29ycmVjdCBob3JzZQ=="},
		},
		{
			name: "Bearer", access: HTTPAccessConfig{Mode: HTTPAccessBearer},
			materials: map[string]secretTestResolved{"http.bearer_token": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("signed-token")}}},
			headers:   map[string]string{"Authorization": "Bearer signed-token"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisherID := uuid.New()
			resolver := &secretTestResolver{publisherID: publisherID, materials: test.materials}
			factory, _ := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(loopbackEphemeralHTTPListener))
			entity, sources := httpTransportPublisher(t, publisherID, test.access, HTTPQualityStrict)
			created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
			if err != nil {
				t.Fatalf("NewResolvedTransport() error = %v", err)
			}
			transport := created.(*httpSnapshotTransport)
			endpoint := "http://" + transport.Address() + "/snapshot"
			response := httpRequest(t, http.MethodGet, endpoint, nil, nil)
			assertHTTPResponse(t, response, http.StatusUnauthorized, false)
			_ = response.Body.Close()

			capturedAt := time.Now().UTC()
			if err := transport.Publish(context.Background(), SourceSnapshot{CapturedAt: capturedAt, Samples: []SourceSample{{
				Alias: "power", Reference: sources[0].Descriptor.Reference, Available: true, SchemaVersion: 1,
				DataType: SourceDataTypeFloat64, Unit: "kW", Value: 10.0, Quality: SourceQualityPartial,
			}}}); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			response = httpRequest(t, http.MethodGet, endpoint, nil, test.headers)
			assertHTTPResponse(t, response, http.StatusServiceUnavailable, false)
			var payload struct {
				Power   float64 `json:"power"`
				Quality string  `json:"quality"`
			}
			decodeHTTPBody(t, response, &payload)
			if payload.Power != 10 || payload.Quality != "partial" {
				t.Fatalf("strict payload = %#v", payload)
			}
			if err := transport.Close(context.Background()); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			if transport.access.apiKeyHash != [32]byte{} || transport.access.usernameHash != [32]byte{} || transport.access.passwordHash != [32]byte{} || transport.access.bearerHash != [32]byte{} {
				t.Fatal("Close() retained credential verifier hashes")
			}
		})
	}
}

func TestHTTPTransportFactoryValidatesDependenciesTemplateAndFailures(t *testing.T) {
	t.Parallel()
	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{}}
	if _, err := NewHTTPTransportFactory(nil, NewJSONPayloadEngine()); !errors.Is(err, ErrSecretResolverRequired) {
		t.Errorf("nil resolver error = %v", err)
	}
	if _, err := NewHTTPTransportFactory(resolver, nil); !errors.Is(err, ErrHTTPTransportRequired) {
		t.Errorf("nil payload engine error = %v", err)
	}
	if _, err := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(nil)); !errors.Is(err, ErrHTTPTransportRequired) {
		t.Errorf("nil listener option error = %v", err)
	}
	entity, sources := httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	config, _ := ParseHTTPPublisherConfig(entity.Config)
	config.Response.PayloadTemplate = `{"missing":{{value "missing"}}}`
	encoded, _ := json.Marshal(config)
	entity.Config, _ = normalizeHTTPPublisherConfig(encoded)
	factory, _ := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(loopbackEphemeralHTTPListener))
	if _, err := factory.NewResolvedTransport(context.Background(), entity, sources); !errors.Is(err, ErrJSONPayloadUnknownAlias) {
		t.Fatalf("unknown alias error = %v", err)
	}

	factory, _ = NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(func(string, string) (net.Listener, error) {
		return failingHTTPListener{}, nil
	}))
	entity, sources = httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	claims, err := factory.ListenerClaims(entity)
	if err != nil || len(claims) != 1 || claims[0].Address != "127.0.0.1:8088" {
		t.Fatalf("ListenerClaims() = %#v, %v", claims, err)
	}
	created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
	if err != nil {
		t.Fatalf("NewResolvedTransport() error = %v", err)
	}
	transport := created.(*httpSnapshotTransport)
	select {
	case failure := <-transport.Failures():
		if !errors.Is(failure, ErrHTTPTransportExited) || strings.Contains(failure.Error(), "credential") {
			t.Fatalf("transport failure = %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for serve failure")
	}
	_ = transport.Close(context.Background())
}

func httpTransportPublisher(t *testing.T, publisherID uuid.UUID, access HTTPAccessConfig, policy HTTPQualityPolicy) (Publisher, []ResolvedSource) {
	t.Helper()
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "power", Descriptor: SourceDescriptor{
		Reference: reference, Name: "Power", SchemaVersion: 1, DataType: SourceDataTypeFloat64,
		Unit: "kW", PeriodKind: SourcePeriodInstantaneous, Enabled: true,
	}}}
	config := defaultHTTPPublisherConfig()
	config.HTTP.Access = access
	config.HTTP.QualityPolicy = policy
	config.Response.PayloadTemplate = `{"publisher_id":{{publisher_id}},"published_at":{{published_at}},"power":{{value "power"}},"quality":{{quality "power"}}}`
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshalling HTTP config: %v", err)
	}
	normalized, err := normalizeHTTPPublisherConfig(Config(encoded))
	if err != nil {
		t.Fatalf("normalizing HTTP config: %v", err)
	}
	return Publisher{ID: publisherID, Type: TypeHTTPServer, Name: "HTTP Test", Enabled: true, Config: normalized, ConfigVersion: 4}, sources
}

func loopbackEphemeralHTTPListener(_, _ string) (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

func httpRequest(t *testing.T, method, endpoint string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	return response
}

func assertHTTPResponse(t *testing.T, response *http.Response, status int, anonymous bool) {
	t.Helper()
	if response.StatusCode != status || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP response = %d, cache %q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	if (response.Header.Get("X-IoT-Edge-Anonymous") == "true") != anonymous {
		t.Fatalf("anonymous response header = %q", response.Header.Get("X-IoT-Edge-Anonymous"))
	}
}

func decodeHTTPBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decoding HTTP response: %v", err)
	}
}

type failingHTTPListener struct{}

func (failingHTTPListener) Accept() (net.Conn, error) { return nil, errors.New("listener failed") }
func (failingHTTPListener) Close() error              { return nil }
func (failingHTTPListener) Addr() net.Addr            { return testHTTPAddress("127.0.0.1:0") }

type testHTTPAddress string

func (address testHTTPAddress) Network() string { return "tcp" }
func (address testHTTPAddress) String() string  { return string(address) }
