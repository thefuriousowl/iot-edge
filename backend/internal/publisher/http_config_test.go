package publisher

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHTTPDefinitionNormalizesSecureDefaultsAndExplicitAnonymousAccess(t *testing.T) {
	t.Parallel()

	registry, _ := NewDefaultDefinitionRegistry()
	definition, _ := registry.Find(TypeHTTPServer)
	if definition.Descriptor().ConfigVersion != 4 {
		t.Fatalf("HTTP config version = %d", definition.Descriptor().ConfigVersion)
	}
	normalized, err := definition.NormalizeConfig(context.Background(), nil)
	if err != nil || string(normalized) != defaultHTTPConfigJSON {
		t.Fatalf("NormalizeConfig(default) = %s, %v", normalized, err)
	}
	config, err := ParseHTTPPublisherConfig(normalized)
	if err != nil || config.HTTP.BindAddress != "127.0.0.1" || config.HTTP.Port != 8088 || config.HTTP.Path != "/snapshot" || config.HTTP.Access.Mode != HTTPAccessAPIKey || config.HTTP.Access.APIKeyHeader != "X-Api-Key" || config.HTTP.QualityPolicy != HTTPQualityPayload || config.Response.PayloadTemplate == "" {
		t.Fatalf("ParseHTTPPublisherConfig(default) = %#v, %v", config, err)
	}

	anonymous := Config(`{"trigger":{"mode":"on_change","source_alias":"power"},"http":{"port":9080,"path":"/plant/energy","access":{"mode":"anonymous","anonymous_acknowledged":true},"quality_policy":"strict"}}`)
	normalized, err = definition.NormalizeConfig(context.Background(), anonymous)
	if err != nil {
		t.Fatalf("NormalizeConfig(anonymous) error = %v", err)
	}
	config, _ = ParseHTTPPublisherConfig(normalized)
	if config.Trigger.Mode != TriggerModeOnChange || config.Trigger.SourceAlias != "power" || config.Trigger.CoalesceMS != 100 || config.HTTP.Port != 9080 || config.HTTP.Path != "/plant/energy" || config.HTTP.Access.Mode != HTTPAccessAnonymous || !config.HTTP.Access.AnonymousAcknowledged || config.HTTP.Access.APIKeyHeader != "" || config.HTTP.QualityPolicy != HTTPQualityStrict || config.HTTP.BindAddress != "127.0.0.1" {
		t.Fatalf("anonymous config = %#v", config)
	}
}

func TestHTTPDefinitionValidatesResponseTemplateAgainstResolvedSources(t *testing.T) {
	definition := publisherDefinition{publisherType: TypeHTTPServer}
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{Alias: "power", Descriptor: SourceDescriptor{
		Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous,
	}}}
	config := defaultHTTPPublisherConfig()
	config.Response.PayloadTemplate = `{"power":{{value "power"}},"published_at":{{published_at}}}`
	normalized, err := definition.NormalizeConfig(context.Background(), mustJSON(t, config))
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if err := definition.ValidateSourceConfig(context.Background(), normalized, sources); err != nil {
		t.Fatalf("ValidateSourceConfig() error = %v", err)
	}
	config.Response.PayloadTemplate = `{"power":{{value "missing"}}}`
	normalized, _ = definition.NormalizeConfig(context.Background(), mustJSON(t, config))
	if err := definition.ValidateSourceConfig(context.Background(), normalized, sources); !errors.Is(err, ErrJSONPayloadUnknownAlias) {
		t.Fatalf("unknown alias error = %v", err)
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
		Config(`{"http":{"access":{"mode":"anonymous","anonymous_acknowledged":true,"api_key_header":"X-Key"}}}`),
		Config(`{"http":{"access":{"mode":"api_key","api_key_header":"Authorization"}}}`),
		Config(`{"http":{"access":{"mode":"api_key","anonymous_acknowledged":true}}}`),
		Config(`{"http":{"access":{"mode":"basic","api_key_header":"X-Key"}}}`),
		Config(`{"http":{"access":{"mode":"bearer","anonymous_acknowledged":true}}}`),
		Config(`{"http":{"quality_policy":"future"}}`),
		Config(`{"http":{"read_timeout_ms":99}}`),
		Config(`{"http":{"write_timeout_ms":120001}}`),
		Config(`{"http":{"idle_timeout_ms":300001}}`),
		Config(`{"http":{"max_header_bytes":1023}}`),
		Config(`{"http":{"max_connections":10001}}`),
		Config(`{"http":{"future":true}}`),
		Config(`{"http":null}`),
		Config(`{"trigger":null}`),
		Config(`{"response":null}`),
		Config(`{"response":{"payload_template":" "}}`),
	} {
		if _, err := definition.NormalizeConfig(context.Background(), config); !errors.Is(err, ErrInvalidPublisherConfig) {
			t.Errorf("NormalizeConfig(%s) error = %v", config, err)
		}
	}
}
