package publisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	defaultHTTPBindAddress    = "127.0.0.1"
	defaultHTTPPort           = 8088
	defaultHTTPSnapshotPath   = "/snapshot"
	defaultHTTPTimeout        = 5 * time.Second
	defaultHTTPIdleTimeout    = 30 * time.Second
	defaultHTTPMaxHeaderBytes = 16 * 1024
	defaultHTTPMaxConnections = 64
	maxHTTPSnapshotPathLength = 128
	maxHTTPConnections        = 10000
	defaultHTTPAPIKeyHeader   = "X-API-Key"
)

type HTTPAccessMode string
type HTTPQualityPolicy string

const (
	HTTPAccessAPIKey    HTTPAccessMode = "api_key"
	HTTPAccessBasic     HTTPAccessMode = "basic"
	HTTPAccessBearer    HTTPAccessMode = "bearer"
	HTTPAccessAnonymous HTTPAccessMode = "anonymous"
)

const (
	HTTPQualityPayload HTTPQualityPolicy = "payload"
	HTTPQualityStrict  HTTPQualityPolicy = "strict"
)

type HTTPAccessConfig struct {
	Mode                  HTTPAccessMode `json:"mode"`
	APIKeyHeader          string         `json:"api_key_header,omitempty"`
	AnonymousAcknowledged bool           `json:"anonymous_acknowledged,omitempty"`
}

type HTTPServerConfig struct {
	BindAddress    string            `json:"bind_address"`
	Port           uint16            `json:"port"`
	Path           string            `json:"path"`
	Access         HTTPAccessConfig  `json:"access"`
	QualityPolicy  HTTPQualityPolicy `json:"quality_policy"`
	ReadTimeoutMS  uint64            `json:"read_timeout_ms"`
	WriteTimeoutMS uint64            `json:"write_timeout_ms"`
	IdleTimeoutMS  uint64            `json:"idle_timeout_ms"`
	MaxHeaderBytes int               `json:"max_header_bytes"`
	MaxConnections int               `json:"max_connections"`
}

type HTTPResponseConfig struct {
	PayloadTemplate string `json:"payload_template"`
}

type HTTPPublisherConfig struct {
	Trigger  TriggerConfig      `json:"trigger"`
	HTTP     HTTPServerConfig   `json:"http"`
	Response HTTPResponseConfig `json:"response"`
}

func ParseHTTPPublisherConfig(config Config) (HTTPPublisherConfig, error) {
	normalized, err := normalizeHTTPPublisherConfig(config)
	if err != nil {
		return HTTPPublisherConfig{}, err
	}
	var decoded HTTPPublisherConfig
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return HTTPPublisherConfig{}, ErrInvalidPublisherConfig
	}
	return decoded, nil
}

func normalizeHTTPPublisherConfig(config Config) (Config, error) {
	decoded := defaultHTTPPublisherConfig()
	if len(config) != 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(config, &object); err != nil || object == nil {
			return nil, ErrInvalidPublisherConfig
		}
		decoder := json.NewDecoder(bytes.NewReader(config))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, ErrInvalidPublisherConfig
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrInvalidPublisherConfig
		}
		for _, key := range []string{"trigger", "http", "response"} {
			if raw, exists := object[key]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return nil, ErrInvalidPublisherConfig
			}
		}
		trigger, err := ParseTriggerConfig(config)
		if err != nil {
			return nil, err
		}
		decoded.Trigger = trigger
		if rawHTTP, exists := object["http"]; exists {
			var httpObject map[string]json.RawMessage
			if err := json.Unmarshal(rawHTTP, &httpObject); err != nil || httpObject == nil {
				return nil, ErrInvalidPublisherConfig
			}
			if rawAccess, exists := httpObject["access"]; exists {
				if bytes.Equal(bytes.TrimSpace(rawAccess), []byte("null")) {
					return nil, ErrInvalidPublisherConfig
				}
				var access HTTPAccessConfig
				if err := json.Unmarshal(rawAccess, &access); err != nil {
					return nil, ErrInvalidPublisherConfig
				}
				decoded.HTTP.Access = access
			}
		}
	}
	trigger, err := normalizeTrigger(decoded.Trigger)
	if err != nil {
		return nil, err
	}
	decoded.Trigger = trigger
	if err := normalizeHTTPServerConfig(&decoded.HTTP); err != nil {
		return nil, err
	}
	if strings.TrimSpace(decoded.Response.PayloadTemplate) == "" || len(decoded.Response.PayloadTemplate) > MaxJSONPayloadTemplateBytes {
		return nil, ErrInvalidPublisherConfig
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, ErrInvalidPublisherConfig
	}
	return Config(normalized), nil
}

func normalizeHTTPServerConfig(config *HTTPServerConfig) error {
	if config == nil {
		return ErrInvalidPublisherConfig
	}
	config.BindAddress = strings.TrimSpace(config.BindAddress)
	bindIP := net.ParseIP(config.BindAddress)
	if bindIP == nil || (!bindIP.IsLoopback() && !bindIP.IsUnspecified()) || config.Port == 0 {
		return ErrInvalidPublisherConfig
	}
	config.Path = strings.TrimSpace(config.Path)
	if config.Path == "" || len(config.Path) > maxHTTPSnapshotPathLength || !strings.HasPrefix(config.Path, "/") || strings.ContainsAny(config.Path, "?#") || path.Clean(config.Path) != config.Path {
		return ErrInvalidPublisherConfig
	}
	switch config.Access.Mode {
	case HTTPAccessAPIKey:
		if config.Access.APIKeyHeader == "" {
			config.Access.APIKeyHeader = defaultHTTPAPIKeyHeader
		}
		config.Access.APIKeyHeader = httpCanonicalHeader(config.Access.APIKeyHeader)
		if config.Access.APIKeyHeader == "" || strings.EqualFold(config.Access.APIKeyHeader, "Authorization") || config.Access.AnonymousAcknowledged {
			return ErrInvalidPublisherConfig
		}
	case HTTPAccessBasic, HTTPAccessBearer:
		if config.Access.APIKeyHeader != "" || config.Access.AnonymousAcknowledged {
			return ErrInvalidPublisherConfig
		}
	case HTTPAccessAnonymous:
		if config.Access.APIKeyHeader != "" || !config.Access.AnonymousAcknowledged {
			return ErrInvalidPublisherConfig
		}
	default:
		return ErrInvalidPublisherConfig
	}
	if config.QualityPolicy != HTTPQualityPayload && config.QualityPolicy != HTTPQualityStrict {
		return ErrInvalidPublisherConfig
	}
	if !validHTTPTimeout(config.ReadTimeoutMS, 100, 120000) || !validHTTPTimeout(config.WriteTimeoutMS, 100, 120000) || !validHTTPTimeout(config.IdleTimeoutMS, 100, 300000) {
		return ErrInvalidPublisherConfig
	}
	if config.MaxHeaderBytes < 1024 || config.MaxHeaderBytes > 1024*1024 || config.MaxConnections < 1 || config.MaxConnections > maxHTTPConnections {
		return ErrInvalidPublisherConfig
	}
	return nil
}

func httpCanonicalHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for index := range value {
		character := value[index]
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character))) {
			return ""
		}
	}
	return http.CanonicalHeaderKey(value)
}

func validHTTPTimeout(value, minimum, maximum uint64) bool {
	return value >= minimum && value <= maximum
}

func defaultHTTPPublisherConfig() HTTPPublisherConfig {
	return HTTPPublisherConfig{
		Trigger: defaultIntervalTrigger(),
		HTTP: HTTPServerConfig{
			BindAddress: defaultHTTPBindAddress, Port: defaultHTTPPort, Path: defaultHTTPSnapshotPath,
			Access:        HTTPAccessConfig{Mode: HTTPAccessAPIKey, APIKeyHeader: defaultHTTPAPIKeyHeader},
			QualityPolicy: HTTPQualityPayload,
			ReadTimeoutMS: uint64(defaultHTTPTimeout / time.Millisecond), WriteTimeoutMS: uint64(defaultHTTPTimeout / time.Millisecond),
			IdleTimeoutMS: uint64(defaultHTTPIdleTimeout / time.Millisecond), MaxHeaderBytes: defaultHTTPMaxHeaderBytes,
			MaxConnections: defaultHTTPMaxConnections,
		},
		Response: HTTPResponseConfig{PayloadTemplate: `{"publisher_id":{{publisher_id}},"published_at":{{published_at}},"values":{}}`},
	}
}
