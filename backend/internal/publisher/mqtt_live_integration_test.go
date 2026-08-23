package publisher

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMQTTLiveTLSIntegration(t *testing.T) {
	if os.Getenv("MQTT_LIVE_TEST") != "1" {
		t.Skip("set MQTT_LIVE_TEST=1 with the documented temporary environment to run")
	}
	required := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("required live MQTT environment %s is missing", name)
		}
		return value
	}
	brokerURL := required("MQTT_LIVE_BROKER_URL")
	clientID := required("MQTT_LIVE_CLIENT_ID")
	username := required("MQTT_LIVE_USERNAME")
	password := required("MQTT_LIVE_PASSWORD")
	telemetryTopic := required("MQTT_LIVE_TELEMETRY_TOPIC")
	ackTopic := required("MQTT_LIVE_ACK_TOPIC")
	errorTopic := required("MQTT_LIVE_ERROR_TOPIC")
	deviceID := required("MQTT_LIVE_DEVICE_ID")
	deviceSlug := required("MQTT_LIVE_DEVICE_SLUG")

	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{
		"mqtt.username": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte(username)}},
		"mqtt.password": {kind: SecretKindOpaque, material: SecretMaterial{Opaque: []byte(password)}},
	}}
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "temperature", Descriptor: SourceDescriptor{Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "°C", PeriodKind: SourcePeriodInstantaneous}}}
	payloadTemplate := fmt.Sprintf(`{"entech_connect_telemetry":[{"device":{"id":%q,"slug":%q},"timestamp":{{published_unix_ms}},"data":{"temperature":{{value "temperature"}}},"status":{"state":"ONLINE"}}]}`, deviceID, deviceSlug)
	config := defaultMQTTPublisherConfig()
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 1000}
	config.MQTT.BrokerURL = brokerURL
	config.MQTT.ClientID = clientID
	config.MQTT.Auth = MQTTAuthConfig{Username: &SecretReference{Name: "mqtt.username"}, Password: &SecretReference{Name: "mqtt.password"}}
	config.MQTT.Publish = MQTTPublishConfig{Topic: telemetryTopic, QoS: 1, PayloadTemplate: payloadTemplate}
	config.MQTT.Diagnostics = []MQTTDiagnosticSubscription{{Label: "ack", TopicFilter: ackTopic, QoS: 1}, {Label: "error", TopicFilter: errorTopic, QoS: 1}}
	normalized, err := normalizeMQTTPublisherConfig(mustJSON(t, config))
	if err != nil {
		t.Fatalf("normalizing live config: %v", err)
	}
	entity := Publisher{ID: publisherID, Type: TypeMQTT, Name: "Live TLS Test", Enabled: true, Config: normalized, ConfigVersion: 3}
	factory, err := NewMQTTTransportFactory(resolver, NewJSONPayloadEngine())
	if err != nil {
		t.Fatalf("NewMQTTTransportFactory() error = %v", err)
	}
	created, err := factory.NewResolvedTransport(context.Background(), entity, sources)
	if err != nil {
		t.Fatalf("NewResolvedTransport() error = %v", err)
	}
	transport := created.(*mqttTransport)
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	waitLiveMQTT(t, 30*time.Second, func() bool { return transport.Metrics().Connected }, "TLS MQTT connection")
	at := time.Now().UTC()
	if err := transport.Publish(context.Background(), SourceSnapshot{CapturedAt: at, Samples: []SourceSample{{Alias: "temperature", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "°C", Value: 27.5, Quality: SourceQualityGood, ObservedAt: &at}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitLiveMQTT(t, 15*time.Second, func() bool { return transport.Metrics().DeliveryCount >= 1 }, "QoS delivery")
	waitLiveMQTT(t, 15*time.Second, func() bool { return transport.Metrics().DiagnosticCount >= 1 }, "ACK or error diagnostic")
	events := transport.Diagnostics()
	if len(events) == 0 || events[len(events)-1].Topic != ackTopic && events[len(events)-1].Topic != errorTopic {
		t.Fatalf("unexpected generic diagnostics: %#v", events)
	}
}

func waitLiveMQTT(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
