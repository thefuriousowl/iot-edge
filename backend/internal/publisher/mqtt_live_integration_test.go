package publisher

import (
	"bytes"
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
	secretRepository := newSecretMemoryRepository(publisherID)
	secretCipher, err := NewAESGCMSecretCipher("live-test-master-v1", bytes.Repeat([]byte{0x7a}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMSecretCipher() error = %v", err)
	}
	secretRotations, err := NewSecretRotationBroker(8)
	if err != nil {
		t.Fatalf("NewSecretRotationBroker() error = %v", err)
	}
	secretService, err := NewSecretService(secretRepository, secretCipher, secretRotations)
	if err != nil {
		t.Fatalf("NewSecretService() error = %v", err)
	}
	secretVault, err := NewSecretVault(secretRepository, secretCipher)
	if err != nil {
		t.Fatalf("NewSecretVault() error = %v", err)
	}
	for reference, value := range map[string]string{"mqtt.username": username, "mqtt.password": password} {
		material := SecretMaterial{Opaque: []byte(value)}
		_, putErr := secretService.Put(context.Background(), publisherID, PutSecretInput{Reference: SecretReference{Name: reference}, Kind: SecretKindOpaque, Material: material})
		material.Destroy()
		if putErr != nil {
			t.Fatalf("storing encrypted live MQTT secret %s: %v", reference, putErr)
		}
	}
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
	entity := Publisher{ID: publisherID, Type: TypeMQTT, Name: "Live TLS Test", Enabled: false, Config: normalized, ConfigVersion: 3}
	payloadEngine := NewJSONPayloadEngine()
	connectionTester, err := NewMQTTConnectionTester(secretVault, payloadEngine)
	if err != nil {
		t.Fatalf("NewMQTTConnectionTester() error = %v", err)
	}
	connectionResult, err := connectionTester.Test(context.Background(), entity, sources, nil)
	if err != nil || !connectionResult.Connected || connectionResult.LatencyMS < 0 {
		t.Fatalf("MQTTConnectionTester.Test() = %+v, %v", connectionResult, err)
	}
	t.Logf("encrypted-vault connection test succeeded: connected=%t latency_ms=%d", connectionResult.Connected, connectionResult.LatencyMS)
	entity.Enabled = true
	factory, err := NewMQTTTransportFactory(secretVault, payloadEngine)
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
	metrics := transport.Metrics()
	lastEvent := events[len(events)-1]
	t.Logf("live MQTT checkpoint succeeded: deliveries=%d diagnostics=%d diagnostic_label=%s format=%s", metrics.DeliveryCount, metrics.DiagnosticCount, lastEvent.Label, lastEvent.Format)
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
