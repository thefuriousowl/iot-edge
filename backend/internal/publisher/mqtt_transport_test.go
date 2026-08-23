package publisher

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMQTTTransportPublishesCustomPayloadAndMonitorsGenericDiagnostics(t *testing.T) {
	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{
		"mqtt.username": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("test-user")}},
		"mqtt.password": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte("test-password")}},
	}}
	client := newFakeMQTTWireClient(true)
	var settings mqttWireSettings
	factory, err := NewMQTTTransportFactory(resolver, NewJSONPayloadEngine(),
		WithMQTTClock(func() time.Time { return time.Date(2026, time.August, 23, 8, 35, 25, 573000000, time.UTC) }),
		WithMQTTWireFactory(func(candidate mqttWireSettings) (mqttWireClient, error) {
			settings = candidate
			client.settings = candidate
			return client, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewMQTTTransportFactory() error = %v", err)
	}
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "temperature", Descriptor: SourceDescriptor{Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "°C", PeriodKind: SourcePeriodInstantaneous}}}
	entity := mqttTransportPublisher(t, publisherID, `{"entech_connect_telemetry":[{"device":{"id":"device-id","slug":"meter"},"timestamp":{{published_unix_ms}},"data":{"temperature":{{value "temperature"}}},"status":{"state":"ONLINE"}}]}`, 8, 2)
	created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
	if err != nil {
		t.Fatalf("NewResolvedTransport() error = %v", err)
	}
	transport := created.(*mqttTransport)
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	waitMQTTCondition(t, func() bool { return transport.Metrics().Connected })
	if settings.BrokerURI != "ssl://mqtt.example.com:8883" || settings.ClientID != "test-client" || settings.Username == "" || settings.Password == "" || settings.TLS == nil || settings.TLS.ServerName != "mqtt.example.com" || settings.TLS.MinVersion != tls.VersionTLS12 {
		t.Fatalf("wire settings = %#v", settings)
	}
	if len(client.subscriptions()) != 2 {
		t.Fatalf("subscriptions = %#v", client.subscriptions())
	}
	observedAt := time.Date(2026, time.August, 23, 8, 35, 24, 0, time.UTC)
	snapshot := SourceSnapshot{CapturedAt: observedAt, Samples: []SourceSample{{Alias: "temperature", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "°C", Value: 27.5, Quality: SourceQualityGood, ObservedAt: &observedAt}}}
	if err := transport.Publish(context.Background(), snapshot); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	published := receiveFakeMQTTPublish(t, client.published)
	if published.topic != "tenant/telemetry" || published.qos != 1 || published.retained {
		t.Fatalf("publish envelope = %#v", published)
	}
	wantPayload := `{"entech_connect_telemetry":[{"device":{"id":"device-id","slug":"meter"},"timestamp":1787474125573,"data":{"temperature":27.5},"status":{"state":"ONLINE"}}]}`
	if string(published.payload) != wantPayload {
		t.Fatalf("published payload = %s\nwant %s", published.payload, wantPayload)
	}
	waitMQTTCondition(t, func() bool { return transport.Metrics().DeliveryCount == 1 })

	client.emit(mqttWireMessage{Topic: "tenant/ack", QoS: 1, Payload: []byte(`{"status":"success","processed":1,"timestamp":"2026-08-23T08:35:25.573Z"}`)})
	client.emit(mqttWireMessage{Topic: "tenant/error", QoS: 1, Retained: true, Payload: []byte("arbitrary non-JSON error")})
	waitMQTTCondition(t, func() bool { return transport.Metrics().DiagnosticCount == 2 })
	events := transport.Diagnostics()
	if len(events) != 2 || events[0].Label != "ack" || events[0].Format != "json" || events[1].Label != "error" || events[1].Format != "text" || !events[1].Retained {
		t.Fatalf("diagnostic events = %#v", events)
	}
	events[0].Payload = "mutated"
	if transport.Diagnostics()[0].Payload == "mutated" {
		t.Fatal("Diagnostics() returned aliased history")
	}
}

func TestMQTTTransportBoundsOfflineQueueAndDiagnosticHistory(t *testing.T) {
	client := newFakeMQTTWireClient(false)
	factory, _ := NewMQTTTransportFactory(&secretTestResolver{publisherID: uuid.Nil, materials: map[string]secretTestResolved{}}, NewJSONPayloadEngine(),
		WithMQTTWireFactory(func(settings mqttWireSettings) (mqttWireClient, error) {
			client.settings = settings
			return client, nil
		}),
	)
	publisherID := uuid.New()
	factory.secrets = &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{}}
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "value", Descriptor: SourceDescriptor{Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous}}}
	entity := mqttTransportPublisher(t, publisherID, `{"value":{{value "value"}}}`, 2, 2)
	created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
	if err != nil {
		t.Fatalf("NewResolvedTransport() error = %v", err)
	}
	transport := created.(*mqttTransport)
	for index := range 3 {
		at := time.Now().UTC()
		err := transport.Publish(context.Background(), SourceSnapshot{CapturedAt: at, Samples: []SourceSample{{Alias: "value", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Value: float64(index), Quality: SourceQualityGood}}})
		if err != nil {
			t.Fatalf("Publish(%d) error = %v", index, err)
		}
	}
	metrics := transport.Metrics()
	if metrics.TransportQueueDepth != 2 || metrics.TransportDropCount != 1 || metrics.Connected {
		t.Fatalf("offline metrics = %#v", metrics)
	}
	transport.onDiagnostic(mqttWireMessage{Topic: "tenant/ack", Payload: []byte{0xff, 0x01}})
	transport.onDiagnostic(mqttWireMessage{Topic: "tenant/ack", Payload: []byte(`{"a":1}`)})
	transport.onDiagnostic(mqttWireMessage{Topic: "tenant/error", Payload: []byte(`{"b":2}`)})
	events := transport.Diagnostics()
	if len(events) != 2 || events[0].Payload != `{"a":1}` || events[1].Payload != `{"b":2}` || transport.Metrics().DiagnosticDropCount != 1 {
		t.Fatalf("bounded diagnostics = %#v, metrics %#v", events, transport.Metrics())
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := transport.Publish(context.Background(), SourceSnapshot{}); !errors.Is(err, ErrMQTTTransportClosed) {
		t.Fatalf("Publish(after close) error = %v", err)
	}
}

func TestMQTTTransportFactoryValidatesDependenciesAndResolvedSources(t *testing.T) {
	var nilResolver *secretTestResolver
	if _, err := NewMQTTTransportFactory(nilResolver, NewJSONPayloadEngine()); !errors.Is(err, ErrSecretResolverRequired) {
		t.Errorf("nil resolver error = %v", err)
	}
	resolver := &secretTestResolver{publisherID: uuid.New(), materials: map[string]secretTestResolved{}}
	if _, err := NewMQTTTransportFactory(resolver, nil); !errors.Is(err, ErrMQTTTransportRequired) {
		t.Errorf("nil engine error = %v", err)
	}
	factory, _ := NewMQTTTransportFactory(resolver, NewJSONPayloadEngine())
	if _, err := factory.NewTransport(context.Background(), Publisher{}); !errors.Is(err, ErrMQTTTransportRequired) {
		t.Errorf("non-resolved transport error = %v", err)
	}
	if claims, err := factory.ListenerClaims(Publisher{}); err != nil || len(claims) != 0 {
		t.Errorf("ListenerClaims() = %#v, %v", claims, err)
	}
}

func mqttTransportPublisher(t *testing.T, publisherID uuid.UUID, payloadTemplate string, queueCapacity, historyDepth int) Publisher {
	t.Helper()
	config := defaultMQTTPublisherConfig()
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 100}
	config.MQTT.BrokerURL = "mqtts://mqtt.example.com:8883"
	config.MQTT.ClientID = "test-client"
	config.MQTT.Auth = MQTTAuthConfig{Username: &SecretReference{Name: "mqtt.username"}, Password: &SecretReference{Name: "mqtt.password"}}
	config.MQTT.TLS.ServerName = "mqtt.example.com"
	config.MQTT.Publish = MQTTPublishConfig{Topic: "tenant/telemetry", QoS: 1, PayloadTemplate: payloadTemplate}
	config.MQTT.Diagnostics = []MQTTDiagnosticSubscription{{Label: "ack", TopicFilter: "tenant/ack", QoS: 1}, {Label: "error", TopicFilter: "tenant/error", QoS: 1}}
	config.MQTT.QueueCapacity = queueCapacity
	config.MQTT.DiagnosticHistoryDepth = historyDepth
	if queueCapacity == 2 && historyDepth == 2 && payloadTemplate == `{"value":{{value "value"}}}` {
		config.MQTT.Auth = MQTTAuthConfig{}
	}
	normalized, err := normalizeMQTTPublisherConfig(mustJSON(t, config))
	if err != nil {
		t.Fatalf("normalizing MQTT config: %v", err)
	}
	return Publisher{ID: publisherID, Type: TypeMQTT, Name: "MQTT Test", Enabled: true, Config: normalized, ConfigVersion: 3}
}

type fakeMQTTPublish struct {
	topic    string
	qos      byte
	retained bool
	payload  []byte
}

type fakeMQTTWireClient struct {
	mu             sync.Mutex
	settings       mqttWireSettings
	connectSuccess bool
	connected      bool
	filters        map[string]byte
	handler        func(mqttWireMessage)
	published      chan fakeMQTTPublish
}

func newFakeMQTTWireClient(connectSuccess bool) *fakeMQTTWireClient {
	return &fakeMQTTWireClient{connectSuccess: connectSuccess, published: make(chan fakeMQTTPublish, 16)}
}

func (client *fakeMQTTWireClient) Connect() mqttWireToken {
	token := newFakeMQTTToken()
	client.mu.Lock()
	success := client.connectSuccess
	if success {
		client.connected = true
	}
	settings := client.settings
	client.mu.Unlock()
	if success {
		settings.OnConnected()
		token.complete(nil)
	}
	return token
}

func (client *fakeMQTTWireClient) Publish(topic string, qos byte, retained bool, payload []byte) mqttWireToken {
	client.published <- fakeMQTTPublish{topic: topic, qos: qos, retained: retained, payload: append([]byte(nil), payload...)}
	token := newFakeMQTTToken()
	token.complete(nil)
	return token
}

func (client *fakeMQTTWireClient) Subscribe(filters map[string]byte, handler func(mqttWireMessage)) mqttWireToken {
	client.mu.Lock()
	client.filters = make(map[string]byte, len(filters))
	for topic, qos := range filters {
		client.filters[topic] = qos
	}
	client.handler = handler
	client.mu.Unlock()
	token := newFakeMQTTToken()
	token.complete(nil)
	return token
}

func (client *fakeMQTTWireClient) Disconnect(uint) {
	client.mu.Lock()
	client.connected = false
	client.mu.Unlock()
}

func (client *fakeMQTTWireClient) IsConnectionOpen() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.connected
}

func (client *fakeMQTTWireClient) subscriptions() map[string]byte {
	client.mu.Lock()
	defer client.mu.Unlock()
	copy := make(map[string]byte, len(client.filters))
	for topic, qos := range client.filters {
		copy[topic] = qos
	}
	return copy
}

func (client *fakeMQTTWireClient) emit(message mqttWireMessage) {
	client.mu.Lock()
	handler := client.handler
	client.mu.Unlock()
	if handler != nil {
		handler(message)
	}
}

type fakeMQTTToken struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newFakeMQTTToken() *fakeMQTTToken             { return &fakeMQTTToken{done: make(chan struct{})} }
func (token *fakeMQTTToken) Done() <-chan struct{} { return token.done }
func (token *fakeMQTTToken) Error() error {
	token.mu.Lock()
	defer token.mu.Unlock()
	return token.err
}
func (token *fakeMQTTToken) complete(err error) {
	token.mu.Lock()
	token.err = err
	token.mu.Unlock()
	close(token.done)
}

func receiveFakeMQTTPublish(t *testing.T, published <-chan fakeMQTTPublish) fakeMQTTPublish {
	t.Helper()
	select {
	case message := <-published:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MQTT publish")
		return fakeMQTTPublish{}
	}
}

func waitMQTTCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for MQTT condition")
}
