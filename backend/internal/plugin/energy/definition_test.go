package energy

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestDefinitionManifestAndDependencies(t *testing.T) {
	t.Parallel()
	var nilReader *energyLoggerReader
	if _, err := NewDefinition(nil); !errors.Is(err, ErrLoggerReaderRequired) {
		t.Errorf("NewDefinition(nil) error = %v", err)
	}
	if _, err := NewDefinition(nilReader); !errors.Is(err, ErrLoggerReaderRequired) {
		t.Errorf("NewDefinition(typed nil) error = %v", err)
	}
	var nilFactory *energyRuntimeFactory
	if _, err := NewDefinition(&energyLoggerReader{}, WithRuntimeFactory(nilFactory)); !errors.Is(err, ErrRuntimeFactoryRequired) {
		t.Errorf("NewDefinition(typed nil factory) error = %v", err)
	}
	definition, err := NewDefinition(&energyLoggerReader{})
	if err != nil {
		t.Fatalf("NewDefinition() error = %v", err)
	}
	manifest := definition.Manifest()
	if manifest.Type != PluginType || manifest.Name != "Energy Management" || manifest.Version != "0.1.1" || manifest.ConfigVersion != 1 || !manifest.MultipleInstances || len(manifest.Capabilities) != 3 || manifest.Capabilities[0] != plugin.CapabilityLoggerCommittedBatches || manifest.Capabilities[1] != plugin.CapabilityLoggerHistoryBatches || manifest.Capabilities[2] != plugin.CapabilityPluginOutputsPublish || len(manifest.Outputs) != 18 {
		t.Errorf("Manifest() = %#v", manifest)
	}
}

func TestDefinitionValidatesLoggerMembershipAndClassifiesRepositoryFailures(t *testing.T) {
	t.Parallel()
	loggerID, powerID := uuid.New(), uuid.New()
	config := encodeEnergyConfig(t, Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4}})
	reader := &energyLoggerReader{logger: &datalogger.Logger{ID: loggerID, Tags: []datalogger.TagReference{{ID: powerID, DataType: "float64"}}}}
	definition, _ := NewDefinition(reader)
	ctx := context.WithValue(context.Background(), energyContextKey{}, "validation")
	if err := definition.ValidateConfig(ctx, config); err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	if reader.id != loggerID || reader.ctx.Value(energyContextKey{}) != "validation" {
		t.Errorf("Find() input = %s, %#v", reader.id, reader.ctx)
	}
	if err := definition.ValidateConfig(nil, config); !errors.Is(err, plugin.ErrConfigValidationUnavailable) {
		t.Errorf("ValidateConfig(nil context) error = %v", err)
	}

	reader.err = datalogger.ErrLoggerNotFound
	if err := definition.ValidateConfig(context.Background(), config); !errors.Is(err, ErrLoggerUnavailable) || errors.Is(err, plugin.ErrConfigValidationUnavailable) {
		t.Errorf("ValidateConfig(missing Logger) error = %v", err)
	}
	databaseError := errors.New("database unavailable")
	reader.err = databaseError
	if err := definition.ValidateConfig(context.Background(), config); !errors.Is(err, plugin.ErrConfigValidationUnavailable) || !errors.Is(err, databaseError) {
		t.Errorf("ValidateConfig(repository error) = %v", err)
	}

	registry, err := plugin.NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.ValidateConfig(context.Background(), PluginType, config); !errors.Is(err, plugin.ErrConfigValidationUnavailable) || errors.Is(err, plugin.ErrInvalidConfig) {
		t.Errorf("Registry.ValidateConfig(repository error) = %v", err)
	}
	reader.err = datalogger.ErrLoggerNotFound
	if err := registry.ValidateConfig(context.Background(), PluginType, config); !errors.Is(err, plugin.ErrInvalidConfig) || !errors.Is(err, ErrLoggerUnavailable) {
		t.Errorf("Registry.ValidateConfig(missing Logger) = %v", err)
	}
}

func TestDefinitionBuildsThroughDefaultAndInjectedFactories(t *testing.T) {
	t.Parallel()
	loggerID, powerID, instanceID := uuid.New(), uuid.New(), uuid.New()
	raw := encodeEnergyConfig(t, Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitMW}}, Timezone: "UTC", MaxGapSeconds: 120, Tariff: FlatTariff{Currency: "USD", RatePerKWh: 0.15}})
	reader := &energyLoggerReader{logger: &datalogger.Logger{ID: loggerID, Tags: []datalogger.TagReference{{ID: powerID, DataType: "float64"}}}}
	definition, _ := NewDefinition(reader)
	if _, err := definition.NewRuntime(plugin.RuntimeSpec{InstanceID: instanceID, Config: raw}, energyHost{}); !errors.Is(err, ErrCommittedBatchFeedUnavailable) {
		t.Errorf("NewRuntime(default missing capability) error = %v", err)
	}
	feed := newEnergyRuntimeFeed(datalogger.RawBatch{})
	capabilityHost := newEnergyRuntimeHost(feed, &energyRuntimeOutputSink{})
	if runtime, err := definition.NewRuntime(plugin.RuntimeSpec{InstanceID: instanceID, Config: raw}, capabilityHost); err != nil || runtime == nil {
		t.Errorf("NewRuntime(default) = %T, %v", runtime, err)
	}

	factory := &energyRuntimeFactory{runtime: energyRuntime{}}
	definition, err := NewDefinition(reader, WithRuntimeFactory(factory))
	if err != nil {
		t.Fatalf("NewDefinition(factory) error = %v", err)
	}
	spec := plugin.RuntimeSpec{InstanceID: instanceID, Config: raw}
	host := energyHost{}
	runtime, err := definition.NewRuntime(spec, host)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if _, ok := runtime.(energyRuntime); !ok || factory.spec.InstanceID != instanceID || factory.config.LoggerID != loggerID || factory.config.ElectricalPowerTags[0].Unit != PowerUnitMW {
		t.Errorf("factory input/runtime = %#v, %#v, %T", factory.spec, factory.config, runtime)
	}
	if _, err := definition.NewRuntime(plugin.RuntimeSpec{InstanceID: instanceID, Config: plugin.Config(`{}`)}, host); !errors.Is(err, ErrInvalidConfiguration) || factory.calls != 1 {
		t.Errorf("NewRuntime(invalid) error/calls = %v/%d", err, factory.calls)
	}
}

type energyContextKey struct{}

type energyLoggerReader struct {
	ctx    context.Context
	id     uuid.UUID
	logger *datalogger.Logger
	err    error
}

func (reader *energyLoggerReader) Find(ctx context.Context, id uuid.UUID) (*datalogger.Logger, error) {
	reader.ctx, reader.id = ctx, id
	return reader.logger, reader.err
}

type energyRuntimeFactory struct {
	spec    plugin.RuntimeSpec
	config  Config
	runtime plugin.Runtime
	err     error
	calls   int
}

func (factory *energyRuntimeFactory) NewRuntime(spec plugin.RuntimeSpec, _ plugin.Host, config Config) (plugin.Runtime, error) {
	factory.spec, factory.config = spec, config
	factory.calls++
	return factory.runtime, factory.err
}

type energyRuntime struct{}

func (energyRuntime) Run(context.Context) error { return nil }

type energyHost struct{}

func (energyHost) ResolveCapability(plugin.Capability) (any, bool) { return nil, false }

var _ LoggerReader = (*energyLoggerReader)(nil)
var _ RuntimeFactory = (*energyRuntimeFactory)(nil)
var _ plugin.Runtime = energyRuntime{}
var _ plugin.Host = energyHost{}
