package publisher

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDefaultPublisherDefinitionsAreVersionedAndTypeCompatible(t *testing.T) {
	t.Parallel()

	registry, err := NewDefaultDefinitionRegistry()
	if err != nil {
		t.Fatalf("NewDefaultDefinitionRegistry() error = %v", err)
	}
	descriptors := registry.List()
	wantTypes := []Type{TypeHTTPServer, TypeMQTT}
	wantVersions := map[Type]uint{TypeHTTPServer: 4, TypeMQTT: 3}
	if len(descriptors) != len(wantTypes) {
		t.Fatalf("definitions = %#v", descriptors)
	}
	for index, wantType := range wantTypes {
		if descriptors[index].Type != wantType || descriptors[index].ConfigVersion != wantVersions[wantType] {
			t.Errorf("definition %d = %#v", index, descriptors[index])
		}
	}
	for _, publisherType := range []Type{TypeHTTPServer, TypeMQTT} {
		definition, err := registry.Find(publisherType)
		if err != nil {
			t.Fatalf("Find(%s) error = %v", publisherType, err)
		}
		for _, config := range []Config{nil, Config(`{}`), Config(" { } \n")} {
			normalized, err := definition.NormalizeConfig(context.Background(), config)
			wantConfig := `{"trigger":{"mode":"interval","interval_ms":60000}}`
			if publisherType == TypeHTTPServer {
				wantConfig = defaultHTTPConfigJSON
			} else if publisherType == TypeMQTT {
				wantConfig = defaultMQTTConfigJSON
			}
			if err != nil || string(normalized) != wantConfig {
				t.Errorf("NormalizeConfig(%s, %q) = %q, %v", publisherType, config, normalized, err)
			}
		}
		for _, config := range []Config{Config(`[]`), Config(`null`), Config(`{"future":true}`), Config(`{} {}`)} {
			if _, err := definition.NormalizeConfig(context.Background(), config); !errors.Is(err, ErrInvalidPublisherConfig) {
				t.Errorf("NormalizeConfig(%s, %q) error = %v", publisherType, config, err)
			}
		}
		numeric := SourceDescriptor{Reference: TagSource(uuid.New()), DataType: SourceDataTypeFloat64}
		text := SourceDescriptor{Reference: PluginOutputSource(uuid.New(), "status"), DataType: SourceDataTypeString}
		if !definition.Supports(numeric) {
			t.Errorf("%s does not support numeric source", publisherType)
		}
		if !definition.Supports(text) {
			t.Errorf("%s string support = %t", publisherType, definition.Supports(text))
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	definition, _ := registry.Find(TypeHTTPServer)
	if _, err := definition.NormalizeConfig(cancelled, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled config error = %v", err)
	}
	if _, err := definition.NormalizeConfig(nil, nil); !errors.Is(err, ErrInvalidPublisherConfig) {
		t.Errorf("nil context error = %v", err)
	}
}

const defaultHTTPConfigJSON = `{"trigger":{"mode":"interval","interval_ms":60000},"http":{"bind_address":"127.0.0.1","port":8088,"path":"/snapshot","access":{"mode":"api_key","api_key_header":"X-Api-Key"},"quality_policy":"payload","read_timeout_ms":5000,"write_timeout_ms":5000,"idle_timeout_ms":30000,"max_header_bytes":16384,"max_connections":64},"response":{"payload_template":"{\"publisher_id\":{{publisher_id}},\"published_at\":{{published_at}},\"values\":{}}"}}`
const defaultMQTTConfigJSON = `{"trigger":{"mode":"interval","interval_ms":60000},"mqtt":{"broker_url":"mqtts://localhost:8883","auth":{},"tls":{},"publish":{"topic":"telemetry","qos":1,"retain":false,"payload_template":"{\"timestamp\":{{published_unix_ms}}}"},"diagnostics":[],"keep_alive_ms":30000,"connect_timeout_ms":10000,"publish_timeout_ms":10000,"reconnect_min_ms":1000,"reconnect_max_ms":60000,"queue_capacity":256,"diagnostic_history_depth":100}}`

func TestPublisherDefinitionNormalizesStrictTriggerConfig(t *testing.T) {
	t.Parallel()

	registry, _ := NewDefaultDefinitionRegistry()
	definition, _ := registry.Find(TypeHTTPServer)
	for _, test := range []struct {
		name   string
		config Config
		want   TriggerConfig
	}{
		{name: "interval", config: Config(`{"trigger":{"mode":"interval","interval_ms":1000}}`), want: TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 1000}},
		{name: "interval default", config: Config(`{"trigger":{"mode":"interval"}}`), want: TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 60000}},
		{name: "on change", config: Config(`{"trigger":{"mode":"on_change","source_alias":"power","coalesce_ms":250}}`), want: TriggerConfig{Mode: TriggerModeOnChange, SourceAlias: "power", CoalesceMS: 250}},
		{name: "on change default coalesce", config: Config(`{"trigger":{"mode":"on_change","source_alias":"power"}}`), want: TriggerConfig{Mode: TriggerModeOnChange, SourceAlias: "power", CoalesceMS: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := definition.NormalizeConfig(context.Background(), test.config)
			if err != nil {
				t.Fatalf("NormalizeConfig() = %s, %v", normalized, err)
			}
			trigger, err := ParseTriggerConfig(normalized)
			if err != nil || trigger != test.want {
				t.Fatalf("ParseTriggerConfig() = %#v, %v; want %#v", trigger, err, test.want)
			}
		})
	}

	for _, config := range []Config{
		Config(`{"trigger":{"mode":"future"}}`),
		Config(`{"trigger":{"mode":"interval","interval_ms":99}}`),
		Config(`{"trigger":{"mode":"interval","source_alias":"power"}}`),
		Config(`{"trigger":{"mode":"on_change"}}`),
		Config(`{"trigger":{"mode":"on_change","source_alias":"power","interval_ms":1000}}`),
		Config(`{"trigger":{"mode":"on_change","source_alias":"power","coalesce_ms":60001}}`),
		Config(`{"trigger":{"mode":"interval"},"future":true}`),
		Config(`{"trigger":{"mode":"interval","future":true}}`),
	} {
		if _, err := definition.NormalizeConfig(context.Background(), config); !errors.Is(err, ErrInvalidPublisherConfig) {
			t.Errorf("NormalizeConfig(%s) error = %v", config, err)
		}
	}
}

func TestPublisherDefinitionRegistryRejectsInvalidAndDuplicateDefinitions(t *testing.T) {
	t.Parallel()

	var nilDefinition *sourceTestDefinition
	for _, definition := range []Definition{
		nil,
		nilDefinition,
		sourceTestDefinition{descriptor: DefinitionDescriptor{}},
		sourceTestDefinition{descriptor: DefinitionDescriptor{Type: "future", ConfigVersion: 1}},
		sourceTestDefinition{descriptor: DefinitionDescriptor{Type: TypeHTTPServer}},
	} {
		registry, err := NewDefinitionRegistry(definition)
		if registry != nil || !errors.Is(err, ErrPublisherDefinitionRequired) {
			t.Errorf("NewDefinitionRegistry(%T) = %#v, %v", definition, registry, err)
		}
	}
	definition := sourceTestDefinition{descriptor: DefinitionDescriptor{Type: TypeHTTPServer, ConfigVersion: 1}}
	if _, err := NewDefinitionRegistry(definition, definition); !errors.Is(err, ErrPublisherDefinitionExists) {
		t.Errorf("duplicate definition error = %v", err)
	}
	if err := (*DefinitionRegistry)(nil).Register(definition); !errors.Is(err, ErrPublisherDefinitionRequired) {
		t.Errorf("nil registry Register() error = %v", err)
	}
	if descriptors := (*DefinitionRegistry)(nil).List(); len(descriptors) != 0 {
		t.Errorf("nil registry List() = %#v", descriptors)
	}
	if _, err := (*DefinitionRegistry)(nil).Find(TypeHTTPServer); !errors.Is(err, ErrUnsupportedPublisherType) {
		t.Errorf("nil registry Find() error = %v", err)
	}
}

type sourceTestDefinition struct {
	descriptor DefinitionDescriptor
}

func (definition sourceTestDefinition) Descriptor() DefinitionDescriptor {
	return definition.descriptor
}
func (definition sourceTestDefinition) NormalizeConfig(context.Context, Config) (Config, error) {
	return Config(`{}`), nil
}
func (definition sourceTestDefinition) Supports(SourceDescriptor) bool { return true }
