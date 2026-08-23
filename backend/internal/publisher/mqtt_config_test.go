package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMQTTConfigNormalizesSecureDefaultsAndEntechProfile(t *testing.T) {
	normalized, err := normalizeMQTTPublisherConfig(nil)
	if err != nil || string(normalized) != defaultMQTTConfigJSON {
		t.Fatalf("default MQTT config = %s, %v", normalized, err)
	}
	profile := Config(`{
		"trigger":{"mode":"interval","interval_ms":1000},
		"mqtt":{
			"broker_url":"mqtts://mqtt.example.com:8883",
			"client_id":"gw-client",
			"auth":{"username":{"name":"mqtt.username"},"password":{"name":"mqtt.password"}},
			"tls":{"server_name":"mqtt.example.com"},
			"publish":{"topic":"tenant/telemetry","qos":1,"retain":false,"payload_template":"{\"telemetry\":[{\"timestamp\":{{published_unix_ms}},\"power\":{{value \\\"power\\\"}}}]}"},
			"diagnostics":[{"label":"ack","topic_filter":"tenant/ack","qos":1},{"label":"error","topic_filter":"tenant/error","qos":1}],
			"keep_alive_ms":30000,"connect_timeout_ms":10000,"publish_timeout_ms":10000,
			"reconnect_min_ms":1000,"reconnect_max_ms":60000,"queue_capacity":256,"diagnostic_history_depth":100
		}
	}`)
	parsed, err := ParseMQTTPublisherConfig(profile)
	if err != nil {
		t.Fatalf("ParseMQTTPublisherConfig() error = %v", err)
	}
	if parsed.MQTT.BrokerURL != "mqtts://mqtt.example.com:8883" || parsed.MQTT.Publish.Topic != "tenant/telemetry" || len(parsed.MQTT.Diagnostics) != 2 || parsed.MQTT.Auth.Password.Name != "mqtt.password" {
		t.Fatalf("parsed MQTT config = %#v", parsed)
	}
}

func TestMQTTConfigRejectsUnsafeAndInvalidCombinations(t *testing.T) {
	base := defaultMQTTPublisherConfig()
	tests := []struct {
		name   string
		mutate func(*MQTTPublisherConfig)
	}{
		{name: "userinfo", mutate: func(config *MQTTPublisherConfig) { config.MQTT.BrokerURL = "mqtts://user:pass@broker:8883" }},
		{name: "unsupported scheme", mutate: func(config *MQTTPublisherConfig) { config.MQTT.BrokerURL = "ws://broker:8080" }},
		{name: "missing port", mutate: func(config *MQTTPublisherConfig) { config.MQTT.BrokerURL = "mqtts://broker" }},
		{name: "plaintext unacknowledged", mutate: func(config *MQTTPublisherConfig) { config.MQTT.BrokerURL = "mqtt://broker:1883" }},
		{name: "TLS acknowledged plaintext", mutate: func(config *MQTTPublisherConfig) { config.MQTT.PlaintextAcknowledged = true }},
		{name: "password without username", mutate: func(config *MQTTPublisherConfig) { config.MQTT.Auth.Password = &SecretReference{Name: "mqtt.password"} }},
		{name: "publish wildcard", mutate: func(config *MQTTPublisherConfig) { config.MQTT.Publish.Topic = "tenant/#" }},
		{name: "QoS2", mutate: func(config *MQTTPublisherConfig) { config.MQTT.Publish.QoS = 2 }},
		{name: "bad filter", mutate: func(config *MQTTPublisherConfig) {
			config.MQTT.Diagnostics = []MQTTDiagnosticSubscription{{Label: "ack", TopicFilter: "tenant/#/ack", QoS: 1}}
		}},
		{name: "duplicate diagnostic label", mutate: func(config *MQTTPublisherConfig) {
			config.MQTT.Diagnostics = []MQTTDiagnosticSubscription{{Label: "ack", TopicFilter: "a", QoS: 1}, {Label: "ack", TopicFilter: "b", QoS: 1}}
		}},
		{name: "empty template", mutate: func(config *MQTTPublisherConfig) { config.MQTT.Publish.PayloadTemplate = " " }},
		{name: "queue zero", mutate: func(config *MQTTPublisherConfig) { config.MQTT.QueueCapacity = 0 }},
		{name: "reconnect inverted", mutate: func(config *MQTTPublisherConfig) {
			config.MQTT.ReconnectMinMS = 5000
			config.MQTT.ReconnectMaxMS = 1000
		}},
		{name: "client control", mutate: func(config *MQTTPublisherConfig) { config.MQTT.ClientID = "bad\nclient" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.MQTT.Diagnostics = append([]MQTTDiagnosticSubscription(nil), base.MQTT.Diagnostics...)
			test.mutate(&candidate)
			encoded := mustJSON(t, candidate)
			if _, err := normalizeMQTTPublisherConfig(encoded); !errors.Is(err, ErrInvalidMQTTConfig) {
				t.Fatalf("normalize error = %v", err)
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"mqtt":null}`, `{"mqtt":{"auth":null}}`, `{"future":true}`, `{}` + `{}`} {
		if raw == `{}` {
			continue
		}
		if _, err := normalizeMQTTPublisherConfig(Config(raw)); !errors.Is(err, ErrInvalidMQTTConfig) {
			t.Errorf("normalize(%s) error = %v", raw, err)
		}
	}
	if _, err := normalizeMQTTPublisherConfig(Config(strings.Repeat(" ", MaxJSONPayloadTemplateBytes+100))); !errors.Is(err, ErrInvalidMQTTConfig) {
		t.Errorf("whitespace config error = %v", err)
	}
}

func TestMQTTTopicFiltersAreGenericAndStandardsShaped(t *testing.T) {
	for _, filter := range []string{"tenant/ack", "tenant/+/error", "tenant/#", "/ack"} {
		if !validMQTTTopicFilter(filter) {
			t.Errorf("valid filter %q rejected", filter)
		}
	}
	for _, filter := range []string{"", "tenant/a+", "tenant/#/error", "tenant/ack\x00"} {
		if validMQTTTopicFilter(filter) {
			t.Errorf("invalid filter %q accepted", filter)
		}
	}
	if !mqttTopicMatches("tenant/+/ack", "tenant/device/ack") || !mqttTopicMatches("tenant/#", "tenant/device/error") || mqttTopicMatches("tenant/+/ack", "tenant/ack") {
		t.Fatal("MQTT topic matching is incorrect")
	}
}

func TestMQTTDefinitionValidatesPayloadAgainstResolvedSources(t *testing.T) {
	definition := publisherDefinition{publisherType: TypeMQTT}
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "power", Descriptor: SourceDescriptor{Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous}}}
	config := defaultMQTTPublisherConfig()
	config.MQTT.Publish.PayloadTemplate = `{"power":{{value "power"}},"timestamp":{{published_unix_ms}}}`
	normalized, err := definition.NormalizeConfig(context.Background(), mustJSON(t, config))
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if err := definition.ValidateSourceConfig(context.Background(), normalized, sources); err != nil {
		t.Fatalf("ValidateSourceConfig() error = %v", err)
	}
	config.MQTT.Publish.PayloadTemplate = `{"power":{{value "missing"}}}`
	normalized, _ = definition.NormalizeConfig(context.Background(), mustJSON(t, config))
	if err := definition.ValidateSourceConfig(context.Background(), normalized, sources); !errors.Is(err, ErrJSONPayloadUnknownAlias) {
		t.Fatalf("unknown alias error = %v", err)
	}
}

func mustJSON(t *testing.T, value any) Config {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return encoded
}
