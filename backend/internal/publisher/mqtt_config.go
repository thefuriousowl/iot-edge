package publisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultMQTTKeepAlive       = 30 * time.Second
	defaultMQTTConnectTimeout  = 10 * time.Second
	defaultMQTTPublishTimeout  = 10 * time.Second
	defaultMQTTReconnectMin    = time.Second
	defaultMQTTReconnectMax    = time.Minute
	defaultMQTTQueueCapacity   = 256
	defaultMQTTDiagnosticDepth = 100
	maxMQTTTopicBytes          = 1024
	maxMQTTClientIDBytes       = 128
	maxMQTTDiagnostics         = 16
	maxMQTTQueueCapacity       = 10000
	maxMQTTDiagnosticDepth     = 1000
)

var ErrInvalidMQTTConfig = fmt.Errorf("%w: MQTT", ErrInvalidPublisherConfig)

type MQTTAuthConfig struct {
	Username *SecretReference `json:"username,omitempty"`
	Password *SecretReference `json:"password,omitempty"`
}

type MQTTPublishConfig struct {
	Topic           string `json:"topic"`
	QoS             byte   `json:"qos"`
	Retain          bool   `json:"retain"`
	PayloadTemplate string `json:"payload_template"`
}

type MQTTDiagnosticSubscription struct {
	Label       string `json:"label"`
	TopicFilter string `json:"topic_filter"`
	QoS         byte   `json:"qos"`
}

type MQTTTransportConfig struct {
	BrokerURL              string                       `json:"broker_url"`
	PlaintextAcknowledged  bool                         `json:"plaintext_acknowledged,omitempty"`
	ClientID               string                       `json:"client_id,omitempty"`
	Auth                   MQTTAuthConfig               `json:"auth"`
	TLS                    ClientTLSConfig              `json:"tls"`
	Publish                MQTTPublishConfig            `json:"publish"`
	Diagnostics            []MQTTDiagnosticSubscription `json:"diagnostics"`
	KeepAliveMS            uint64                       `json:"keep_alive_ms"`
	ConnectTimeoutMS       uint64                       `json:"connect_timeout_ms"`
	PublishTimeoutMS       uint64                       `json:"publish_timeout_ms"`
	ReconnectMinMS         uint64                       `json:"reconnect_min_ms"`
	ReconnectMaxMS         uint64                       `json:"reconnect_max_ms"`
	QueueCapacity          int                          `json:"queue_capacity"`
	DiagnosticHistoryDepth int                          `json:"diagnostic_history_depth"`
}

type MQTTPublisherConfig struct {
	Trigger TriggerConfig       `json:"trigger"`
	MQTT    MQTTTransportConfig `json:"mqtt"`
}

func ParseMQTTPublisherConfig(config Config) (MQTTPublisherConfig, error) {
	normalized, err := normalizeMQTTPublisherConfig(config)
	if err != nil {
		return MQTTPublisherConfig{}, err
	}
	var decoded MQTTPublisherConfig
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return MQTTPublisherConfig{}, ErrInvalidMQTTConfig
	}
	return decoded, nil
}

func normalizeMQTTPublisherConfig(config Config) (Config, error) {
	decoded := defaultMQTTPublisherConfig()
	if len(config) != 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(config, &object); err != nil || object == nil {
			return nil, ErrInvalidMQTTConfig
		}
		decoder := json.NewDecoder(bytes.NewReader(config))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, ErrInvalidMQTTConfig
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrInvalidMQTTConfig
		}
		for _, key := range []string{"trigger", "mqtt"} {
			if raw, exists := object[key]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return nil, ErrInvalidMQTTConfig
			}
		}
		trigger, err := ParseTriggerConfig(config)
		if err != nil {
			return nil, err
		}
		decoded.Trigger = trigger
		if rawMQTT, exists := object["mqtt"]; exists {
			var mqttObject map[string]json.RawMessage
			if err := json.Unmarshal(rawMQTT, &mqttObject); err != nil || mqttObject == nil {
				return nil, ErrInvalidMQTTConfig
			}
			for _, key := range []string{"auth", "tls", "publish", "diagnostics"} {
				if raw, exists := mqttObject[key]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return nil, ErrInvalidMQTTConfig
				}
			}
		}
	}
	trigger, err := normalizeTrigger(decoded.Trigger)
	if err != nil {
		return nil, err
	}
	decoded.Trigger = trigger
	if err := normalizeMQTTTransportConfig(&decoded.MQTT); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, ErrInvalidMQTTConfig
	}
	return Config(normalized), nil
}

func normalizeMQTTTransportConfig(config *MQTTTransportConfig) error {
	if config == nil {
		return ErrInvalidMQTTConfig
	}
	config.BrokerURL = strings.TrimSpace(config.BrokerURL)
	parsed, err := url.Parse(config.BrokerURL)
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts") || parsed.User != nil || parsed.Hostname() == "" || parsed.Port() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrInvalidMQTTConfig
	}
	if _, err := net.LookupPort("tcp", parsed.Port()); err != nil {
		return ErrInvalidMQTTConfig
	}
	if parsed.Scheme == "mqtt" {
		if !config.PlaintextAcknowledged || config.TLS != (ClientTLSConfig{}) {
			return ErrInvalidMQTTConfig
		}
	} else if config.PlaintextAcknowledged {
		return ErrInvalidMQTTConfig
	}
	config.ClientID = strings.TrimSpace(config.ClientID)
	if len(config.ClientID) > maxMQTTClientIDBytes || strings.ContainsAny(config.ClientID, "\x00\r\n") {
		return ErrInvalidMQTTConfig
	}
	if config.Auth.Username != nil && config.Auth.Username.Validate() != nil || config.Auth.Password != nil && config.Auth.Password.Validate() != nil || config.Auth.Password != nil && config.Auth.Username == nil {
		return ErrInvalidMQTTConfig
	}
	if parsed.Scheme == "mqtts" {
		if _, err := normalizeTLSServerName(config.TLS.ServerName, parsed.Hostname()); err != nil {
			return ErrInvalidMQTTConfig
		}
		if config.TLS.CustomCA != nil && config.TLS.CustomCA.Validate() != nil || config.TLS.ClientIdentity != nil && config.TLS.ClientIdentity.Validate() != nil {
			return ErrInvalidMQTTConfig
		}
	}
	config.Publish.Topic = strings.TrimSpace(config.Publish.Topic)
	if !validMQTTPublishTopic(config.Publish.Topic) || config.Publish.QoS > 1 || strings.TrimSpace(config.Publish.PayloadTemplate) == "" || len(config.Publish.PayloadTemplate) > MaxJSONPayloadTemplateBytes {
		return ErrInvalidMQTTConfig
	}
	labels := make(map[string]struct{}, len(config.Diagnostics))
	filters := make(map[string]struct{}, len(config.Diagnostics))
	if len(config.Diagnostics) > maxMQTTDiagnostics {
		return ErrInvalidMQTTConfig
	}
	for index := range config.Diagnostics {
		diagnostic := &config.Diagnostics[index]
		diagnostic.Label = strings.TrimSpace(diagnostic.Label)
		diagnostic.TopicFilter = strings.TrimSpace(diagnostic.TopicFilter)
		if !sourceAliasPattern.MatchString(diagnostic.Label) || !validMQTTTopicFilter(diagnostic.TopicFilter) || diagnostic.QoS > 1 {
			return ErrInvalidMQTTConfig
		}
		if _, exists := labels[diagnostic.Label]; exists {
			return ErrInvalidMQTTConfig
		}
		if _, exists := filters[diagnostic.TopicFilter]; exists {
			return ErrInvalidMQTTConfig
		}
		labels[diagnostic.Label] = struct{}{}
		filters[diagnostic.TopicFilter] = struct{}{}
	}
	if !validMQTTDuration(config.KeepAliveMS, 10000, 3600000) || !validMQTTDuration(config.ConnectTimeoutMS, 1000, 120000) || !validMQTTDuration(config.PublishTimeoutMS, 1000, 120000) || !validMQTTDuration(config.ReconnectMinMS, 100, 60000) || !validMQTTDuration(config.ReconnectMaxMS, config.ReconnectMinMS, 300000) {
		return ErrInvalidMQTTConfig
	}
	if config.QueueCapacity < 1 || config.QueueCapacity > maxMQTTQueueCapacity || config.DiagnosticHistoryDepth < 1 || config.DiagnosticHistoryDepth > maxMQTTDiagnosticDepth {
		return ErrInvalidMQTTConfig
	}
	return nil
}

func validMQTTDuration(value, minimum, maximum uint64) bool {
	return value >= minimum && value <= maximum
}

func validMQTTPublishTopic(topic string) bool {
	return validMQTTTopicText(topic) && !strings.ContainsAny(topic, "+#")
}

func validMQTTTopicFilter(filter string) bool {
	if !validMQTTTopicText(filter) {
		return false
	}
	levels := strings.Split(filter, "/")
	for index, level := range levels {
		if strings.Contains(level, "#") && (level != "#" || index != len(levels)-1) {
			return false
		}
		if strings.Contains(level, "+") && level != "+" {
			return false
		}
	}
	return true
}

func validMQTTTopicText(topic string) bool {
	return topic != "" && len(topic) <= maxMQTTTopicBytes && utf8.ValidString(topic) && !strings.ContainsRune(topic, '\x00')
}

func defaultMQTTPublisherConfig() MQTTPublisherConfig {
	return MQTTPublisherConfig{
		Trigger: defaultIntervalTrigger(),
		MQTT: MQTTTransportConfig{
			BrokerURL:   "mqtts://localhost:8883",
			Publish:     MQTTPublishConfig{Topic: "telemetry", QoS: 1, PayloadTemplate: `{"timestamp":{{published_unix_ms}}}`},
			Diagnostics: []MQTTDiagnosticSubscription{},
			KeepAliveMS: uint64(defaultMQTTKeepAlive / time.Millisecond), ConnectTimeoutMS: uint64(defaultMQTTConnectTimeout / time.Millisecond),
			PublishTimeoutMS: uint64(defaultMQTTPublishTimeout / time.Millisecond), ReconnectMinMS: uint64(defaultMQTTReconnectMin / time.Millisecond),
			ReconnectMaxMS: uint64(defaultMQTTReconnectMax / time.Millisecond), QueueCapacity: defaultMQTTQueueCapacity,
			DiagnosticHistoryDepth: defaultMQTTDiagnosticDepth,
		},
	}
}
