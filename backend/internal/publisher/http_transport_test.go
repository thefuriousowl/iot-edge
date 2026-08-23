package publisher

import (
	"bytes"
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

func TestHTTPTransportServesImmutableAnonymousSnapshotWithDeterministicPolicies(t *testing.T) {
	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{}}
	factory, err := NewHTTPTransportFactory(resolver, WithHTTPListenFunc(loopbackEphemeralHTTPListener))
	if err != nil {
		t.Fatalf("NewHTTPTransportFactory() error = %v", err)
	}
	entity := httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	created, err := factory.NewTransport(context.Background(), entity)
	if err != nil {
		t.Fatalf("NewTransport() error = %v", err)
	}
	transport := created.(*httpSnapshotTransport)
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	endpoint := "http://" + transport.Address() + "/snapshot"

	response := httpRequest(t, http.MethodGet, endpoint, nil, "")
	assertHTTPResponse(t, response, http.StatusServiceUnavailable, "no-store", true)
	var unavailable httpErrorResponse
	decodeHTTPBody(t, response, &unavailable)
	if unavailable.Error.Code != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("unavailable response = %#v", unavailable)
	}

	observedAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	snapshot := SourceSnapshot{CapturedAt: observedAt.Add(time.Second), Samples: []SourceSample{{
		Alias: "power", Reference: TagSource(uuid.New()), Available: true, Sequence: 7, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "kW", Value: 42.5, Quality: SourceQualityGood, ObservedAt: &observedAt,
	}}}
	if err := transport.Publish(context.Background(), snapshot); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	snapshot.Samples[0].Alias = "mutated"
	response = httpRequest(t, http.MethodGet, endpoint, nil, "")
	assertHTTPResponse(t, response, http.StatusOK, "no-store", true)
	var payload httpSnapshotResponse
	decodeHTTPBody(t, response, &payload)
	if payload.PublisherID != publisherID || payload.Quality != "good" || len(payload.Samples) != 1 || payload.Samples[0].Alias != "power" || payload.Samples[0].Value != 42.5 {
		t.Fatalf("snapshot payload = %#v", payload)
	}
	badSnapshot := SourceSnapshot{CapturedAt: observedAt.Add(2 * time.Second), Samples: []SourceSample{{
		Alias: "power", Reference: snapshot.Samples[0].Reference, Available: false, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Quality: SourceQualityUnavailable,
	}}}
	if err := transport.Publish(context.Background(), badSnapshot); err != nil {
		t.Fatalf("Publish(unavailable) error = %v", err)
	}
	response = httpRequest(t, http.MethodGet, endpoint, nil, "")
	assertHTTPResponse(t, response, http.StatusOK, "no-store", true)
	decodeHTTPBody(t, response, &payload)
	if payload.Quality != "unavailable" {
		t.Fatalf("payload quality response = %#v", payload)
	}

	response = httpRequest(t, http.MethodPost, endpoint, nil, "")
	assertHTTPResponse(t, response, http.StatusMethodNotAllowed, "no-store", true)
	_ = response.Body.Close()
	response = httpRequest(t, http.MethodGet, "http://"+transport.Address()+"/other", nil, "")
	assertHTTPResponse(t, response, http.StatusNotFound, "no-store", true)
	_ = response.Body.Close()
	response = httpRequest(t, http.MethodGet, endpoint, strings.NewReader("body"), "")
	assertHTTPResponse(t, response, http.StatusRequestEntityTooLarge, "no-store", true)
	_ = response.Body.Close()

	metrics := transport.Metrics()
	if metrics.ExternalRequestCount != 6 || metrics.RejectedRequestCount != 4 || metrics.LastExternalRequestAt == nil {
		t.Fatalf("HTTP metrics = %#v", metrics)
	}
}

func TestPublisherManagerPushesSnapshotsIntoHTTPWithoutRequestAcquisition(t *testing.T) {
	repository := newSourceTestPublisherRepository()
	sources := newManagerSourceFeed()
	reference := TagSource(uuid.New())
	sources.add(reference, SourceDescriptor{Reference: reference, Name: "Power", SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kW", PeriodKind: SourcePeriodInstantaneous, Enabled: true})
	capturedAt := time.Now().UTC()
	sources.setSnapshot(SourceSnapshot{CapturedAt: capturedAt, Samples: []SourceSample{{
		Alias: "power", Reference: reference, Available: true, Sequence: 1, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "kW", Value: 12.5, Quality: SourceQualityGood, ObservedAt: &capturedAt,
	}}})
	entity := httpTransportPublisher(t, uuid.New(), HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	entity.Sources = []SourceSelection{{Alias: "power", Reference: reference}}
	entity.SourceCount = 1
	config, _ := ParseHTTPPublisherConfig(entity.Config)
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 100}
	encoded, _ := json.Marshal(config)
	entity.Config, _ = normalizeHTTPPublisherConfig(encoded)
	if err := repository.Create(context.Background(), &entity); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	listeners := make(chan net.Listener, 1)
	resolver := &secretTestResolver{publisherID: entity.ID, materials: map[string]secretTestResolved{}}
	factory, _ := NewHTTPTransportFactory(resolver, WithHTTPListenFunc(func(_, _ string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			listeners <- listener
		}
		return listener, err
	}))
	definitions, _ := NewDefaultDefinitionRegistry()
	transports := NewTransportRegistry()
	if err := transports.Register(TypeHTTPServer, factory); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(repository, sources, definitions, transports, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	listener := <-listeners
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	snapshotCalls := sources.snapshotCalls()
	response := httpRequest(t, http.MethodGet, "http://"+listener.Addr().String()+"/snapshot", nil, "")
	assertHTTPResponse(t, response, http.StatusOK, "no-store", true)
	var payload httpSnapshotResponse
	decodeHTTPBody(t, response, &payload)
	if len(payload.Samples) != 1 || payload.Samples[0].Alias != "power" || payload.Samples[0].Value != 12.5 {
		t.Fatalf("Manager HTTP snapshot = %#v", payload)
	}
	if sources.snapshotCalls() != snapshotCalls {
		t.Fatalf("HTTP GET initiated source snapshot: before %d, after %d", snapshotCalls, sources.snapshotCalls())
	}
	waitPublisherStatus(t, manager, entity.ID, func(status RuntimeStatus) bool { return status.ExternalRequestCount == 1 })
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestHTTPTransportRequiresAPIKeyAndStrictQualityReturns503(t *testing.T) {
	publisherID := uuid.New()
	apiKey := bytes.Repeat([]byte{0x4d}, 32)
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{
		"http.api_key": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: apiKey}},
	}}
	factory, _ := NewHTTPTransportFactory(resolver, WithHTTPListenFunc(loopbackEphemeralHTTPListener))
	entity := httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAPIKey, APIKey: &SecretReference{Name: "http.api_key"}}, HTTPQualityStrict)
	created, err := factory.NewTransport(context.Background(), entity)
	if err != nil {
		t.Fatalf("NewTransport() error = %v", err)
	}
	transport := created.(*httpSnapshotTransport)
	endpoint := "http://" + transport.Address() + "/snapshot"
	response := httpRequest(t, http.MethodGet, endpoint, nil, "")
	assertHTTPResponse(t, response, http.StatusUnauthorized, "no-store", false)
	_ = response.Body.Close()
	response = httpRequest(t, http.MethodGet, endpoint, nil, "wrong")
	assertHTTPResponse(t, response, http.StatusUnauthorized, "no-store", false)
	_ = response.Body.Close()

	capturedAt := time.Now().UTC()
	if err := transport.Publish(context.Background(), SourceSnapshot{CapturedAt: capturedAt, Samples: []SourceSample{{
		Alias: "cost", Reference: PluginOutputSource(uuid.New(), "today.cost"), Available: true, SchemaVersion: 1,
		DataType: SourceDataTypeFloat64, Unit: "THB", Value: 10.0, Quality: SourceQualityPartial,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	response = httpRequest(t, http.MethodGet, endpoint, nil, string(apiKey))
	assertHTTPResponse(t, response, http.StatusServiceUnavailable, "no-store", false)
	var payload httpSnapshotResponse
	decodeHTTPBody(t, response, &payload)
	if payload.Quality != "degraded" || len(payload.Samples) != 1 {
		t.Fatalf("strict payload = %#v", payload)
	}
	if resolver.calls != 1 {
		t.Fatalf("secret resolver calls = %d", resolver.calls)
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(transport.apiKey) != 0 {
		t.Fatal("Close() retained API key bytes")
	}
}

func TestHTTPTransportFactoryClaimsListenerAndReportsUnexpectedServeFailure(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{}}
	if _, err := NewHTTPTransportFactory(nil); !errors.Is(err, ErrSecretResolverRequired) {
		t.Errorf("NewHTTPTransportFactory(nil) error = %v", err)
	}
	if _, err := NewHTTPTransportFactory(resolver, WithHTTPListenFunc(nil)); !errors.Is(err, ErrHTTPTransportRequired) {
		t.Errorf("nil listener option error = %v", err)
	}
	factory, _ := NewHTTPTransportFactory(resolver, WithHTTPListenFunc(func(string, string) (net.Listener, error) {
		return failingHTTPListener{}, nil
	}))
	entity := httpTransportPublisher(t, publisherID, HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}, HTTPQualityPayload)
	claims, err := factory.ListenerClaims(entity)
	if err != nil || len(claims) != 1 || claims[0].Network != "tcp" || claims[0].Address != "127.0.0.1:8088" {
		t.Fatalf("ListenerClaims() = %#v, %v", claims, err)
	}
	created, err := factory.NewTransport(context.Background(), entity)
	if err != nil {
		t.Fatalf("NewTransport() error = %v", err)
	}
	transport := created.(*httpSnapshotTransport)
	select {
	case failure := <-transport.Failures():
		if !errors.Is(failure, ErrHTTPTransportExited) || strings.Contains(failure.Error(), "api_key") {
			t.Fatalf("transport failure = %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for serve failure")
	}
	_ = transport.Close(context.Background())
}

func httpTransportPublisher(t *testing.T, publisherID uuid.UUID, access HTTPAccessConfig, policy HTTPQualityPolicy) Publisher {
	t.Helper()
	config := defaultHTTPPublisherConfig()
	config.HTTP.Access = access
	config.HTTP.QualityPolicy = policy
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshalling HTTP config: %v", err)
	}
	normalized, err := normalizeHTTPPublisherConfig(Config(encoded))
	if err != nil {
		t.Fatalf("normalizing HTTP config: %v", err)
	}
	return Publisher{ID: publisherID, Type: TypeHTTPServer, Name: "HTTP Test", Enabled: true, Config: normalized, ConfigVersion: 3}
}

func loopbackEphemeralHTTPListener(_, _ string) (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

func httpRequest(t *testing.T, method, endpoint string, body io.Reader, apiKey string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	if apiKey != "" {
		request.Header.Set("X-API-Key", apiKey)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	return response
}

func assertHTTPResponse(t *testing.T, response *http.Response, status int, cacheControl string, anonymous bool) {
	t.Helper()
	if response.StatusCode != status || response.Header.Get("Cache-Control") != cacheControl {
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
