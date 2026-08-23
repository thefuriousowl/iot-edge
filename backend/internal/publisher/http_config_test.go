package publisher

import (
	"context"
	"errors"
	"testing"
)

func TestHTTPDefinitionNormalizesSecureDefaultsAndExplicitAnonymousAccess(t *testing.T) {
	t.Parallel()

	registry, _ := NewDefaultDefinitionRegistry()
	definition, _ := registry.Find(TypeHTTPServer)
	if definition.Descriptor().ConfigVersion != 3 {
		t.Fatalf("HTTP config version = %d", definition.Descriptor().ConfigVersion)
	}
	normalized, err := definition.NormalizeConfig(context.Background(), nil)
	if err != nil || string(normalized) != defaultHTTPConfigJSON {
		t.Fatalf("NormalizeConfig(default) = %s, %v", normalized, err)
	}
	config, err := ParseHTTPPublisherConfig(normalized)
	if err != nil || config.HTTP.BindAddress != "127.0.0.1" || config.HTTP.Port != 8088 || config.HTTP.Path != "/snapshot" || config.HTTP.Access.Mode != HTTPAccessAPIKey || config.HTTP.Access.APIKey == nil || config.HTTP.Access.APIKey.Name != "http.api_key" || config.HTTP.QualityPolicy != HTTPQualityPayload {
		t.Fatalf("ParseHTTPPublisherConfig(default) = %#v, %v", config, err)
	}

	anonymous := Config(`{"trigger":{"mode":"on_change","source_alias":"power"},"http":{"port":9080,"path":"/plant/energy","access":{"mode":"anonymous","anonymous_acknowledged":true},"quality_policy":"strict"}}`)
	normalized, err = definition.NormalizeConfig(context.Background(), anonymous)
	if err != nil {
		t.Fatalf("NormalizeConfig(anonymous) error = %v", err)
	}
	config, _ = ParseHTTPPublisherConfig(normalized)
	if config.Trigger.Mode != TriggerModeOnChange || config.Trigger.SourceAlias != "power" || config.Trigger.CoalesceMS != 100 || config.HTTP.Port != 9080 || config.HTTP.Path != "/plant/energy" || config.HTTP.Access.Mode != HTTPAccessAnonymous || !config.HTTP.Access.AnonymousAcknowledged || config.HTTP.Access.APIKey != nil || config.HTTP.QualityPolicy != HTTPQualityStrict || config.HTTP.BindAddress != "127.0.0.1" {
		t.Fatalf("anonymous config = %#v", config)
	}
}

func TestHTTPDefinitionRejectsUnsafeAndUnboundedConfig(t *testing.T) {
	t.Parallel()

	registry, _ := NewDefaultDefinitionRegistry()
	definition, _ := registry.Find(TypeHTTPServer)
	for _, config := range []Config{
		Config(`{"http":{"bind_address":"localhost"}}`),
		Config(`{"http":{"port":0}}`),
		Config(`{"http":{"path":"snapshot"}}`),
		Config(`{"http":{"path":"/a/../snapshot"}}`),
		Config(`{"http":{"path":"/snapshot?secret=yes"}}`),
		Config(`{"http":{"access":{"mode":"anonymous"}}}`),
		Config(`{"http":{"access":{"mode":"anonymous","anonymous_acknowledged":true,"api_key":{"name":"http.api_key"}}}}`),
		Config(`{"http":{"access":{"mode":"api_key"}}}`),
		Config(`{"http":{"access":{"mode":"api_key","api_key":{"name":"http.api_key"},"anonymous_acknowledged":true}}}`),
		Config(`{"http":{"quality_policy":"future"}}`),
		Config(`{"http":{"read_timeout_ms":99}}`),
		Config(`{"http":{"write_timeout_ms":120001}}`),
		Config(`{"http":{"idle_timeout_ms":300001}}`),
		Config(`{"http":{"max_header_bytes":1023}}`),
		Config(`{"http":{"max_connections":10001}}`),
		Config(`{"http":{"future":true}}`),
		Config(`{"http":null}`),
		Config(`{"trigger":null}`),
	} {
		if _, err := definition.NormalizeConfig(context.Background(), config); !errors.Is(err, ErrInvalidPublisherConfig) {
			t.Errorf("NormalizeConfig(%s) error = %v", config, err)
		}
	}
}
