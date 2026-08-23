package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type registryDefinition struct {
	manifest Manifest
	validate func(Config) error
	build    func(RuntimeSpec, Host) (Runtime, error)
}

func (definition *registryDefinition) Manifest() Manifest {
	return definition.manifest
}

func (definition *registryDefinition) ValidateConfig(_ context.Context, config Config) error {
	if definition.validate != nil {
		return definition.validate(config)
	}
	return nil
}

func (definition *registryDefinition) NewRuntime(spec RuntimeSpec, host Host) (Runtime, error) {
	if definition.build != nil {
		return definition.build(spec, host)
	}
	return registryRuntime{}, nil
}

type registryRuntime struct{}

func (registryRuntime) Run(context.Context) error { return nil }

type registryRuntimePointer struct{}

func (*registryRuntimePointer) Run(context.Context) error { return nil }

type registryHost struct{}

func (registryHost) ResolveCapability(Capability) (any, bool) { return nil, false }

type registryHostPointer struct{}

func (*registryHostPointer) ResolveCapability(Capability) (any, bool) { return nil, false }

func TestRegistryRegistersAndListsImmutableManifestsDeterministically(t *testing.T) {
	t.Parallel()

	energy := validRegistryDefinition("energy_management")
	energy.manifest.Capabilities = []Capability{"logger.history", "logger.batches", CapabilityPluginOutputsPublish}
	energy.manifest.Outputs = []OutputDescriptor{validOutputDescriptor("electrical_demand_kw")}
	mqtt := validRegistryDefinition("mqtt_publisher")
	registry, err := NewRegistry(mqtt, energy)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	energy.manifest.Name = "Changed externally"
	energy.manifest.Capabilities[0] = "tag.events"
	energy.manifest.Outputs[0].Name = "Changed output"
	manifests := registry.List()
	if len(manifests) != 2 || manifests[0].Type != "energy_management" || manifests[1].Type != "mqtt_publisher" {
		t.Fatalf("List() = %#v, want deterministic type ordering", manifests)
	}
	if manifests[0].Name != "Energy Management" || manifests[0].Capabilities[0] != "logger.history" || manifests[0].Outputs[0].Name != "Metric" {
		t.Fatalf("List() manifest = %#v, registration snapshot was mutated", manifests[0])
	}

	manifests[0].Name = "Changed return value"
	manifests[0].Capabilities[0] = "secrets"
	manifests[0].Outputs[0].Name = "Changed return output"
	stored, err := registry.Manifest("energy_management")
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	if stored.Name != "Energy Management" || stored.Capabilities[0] != "logger.history" || stored.Outputs[0].Name != "Metric" {
		t.Fatalf("Manifest() = %#v, returned value aliases registry state", stored)
	}
	registered, err := registry.Definition("energy_management")
	if err != nil || registered != energy {
		t.Fatalf("Definition() = %T, %v; want original definition", registered, err)
	}
}

func TestRegistryRejectsInvalidDefinitionsAndManifests(t *testing.T) {
	t.Parallel()

	var typedNil *registryDefinition
	tooManyCapabilities := make([]Capability, maxPluginCapabilities+1)
	for index := range tooManyCapabilities {
		tooManyCapabilities[index] = Capability(fmt.Sprintf("capability.%d", index))
	}
	tests := []struct {
		name       string
		definition Definition
	}{
		{name: "nil definition"},
		{name: "typed nil definition", definition: typedNil},
		{name: "empty type", definition: definitionWithManifest(Manifest{Name: "Valid", Version: "1.0.0", ConfigVersion: 1})},
		{name: "uppercase type", definition: definitionWithManifest(Manifest{Type: "Energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1})},
		{name: "spaced type", definition: definitionWithManifest(Manifest{Type: "energy plugin", Name: "Valid", Version: "1.0.0", ConfigVersion: 1})},
		{name: "empty name", definition: definitionWithManifest(Manifest{Type: "energy", Version: "1.0.0", ConfigVersion: 1})},
		{name: "trimmed name", definition: definitionWithManifest(Manifest{Type: "energy", Name: " Energy ", Version: "1.0.0", ConfigVersion: 1})},
		{name: "long name", definition: definitionWithManifest(Manifest{Type: "energy", Name: stringOfLength(maxPluginNameLength + 1), Version: "1.0.0", ConfigVersion: 1})},
		{name: "trimmed description", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Description: " Description ", Version: "1.0.0", ConfigVersion: 1})},
		{name: "long description", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Description: stringOfLength(maxPluginDescriptionLength + 1), Version: "1.0.0", ConfigVersion: 1})},
		{name: "empty version", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", ConfigVersion: 1})},
		{name: "spaced version", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0 beta", ConfigVersion: 1})},
		{name: "zero config version", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0"})},
		{name: "invalid capability", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Capabilities: []Capability{"Logger History"}})},
		{name: "duplicate capability", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Capabilities: []Capability{"logger.history", "logger.history"}})},
		{name: "too many capabilities", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Capabilities: tooManyCapabilities})},
		{name: "invalid output", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Outputs: []OutputDescriptor{{}}})},
		{name: "duplicate output", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Outputs: []OutputDescriptor{validOutputDescriptor("power"), validOutputDescriptor("power")}})},
		{name: "outputs without capability", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Outputs: []OutputDescriptor{validOutputDescriptor("power")}})},
		{name: "capability without outputs", definition: definitionWithManifest(Manifest{Type: "energy", Name: "Valid", Version: "1.0.0", ConfigVersion: 1, Capabilities: []Capability{CapabilityPluginOutputsPublish}})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := NewRegistry(test.definition)
			if registry != nil {
				t.Fatalf("NewRegistry() registry = %#v, want nil", registry)
			}
			if test.definition == nil || isNil(test.definition) {
				if !errors.Is(err, ErrDefinitionRequired) {
					t.Fatalf("NewRegistry() error = %v, want ErrDefinitionRequired", err)
				}
			} else if !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("NewRegistry() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestRegistryRejectsDuplicateTypesWithoutReplacingTheOriginal(t *testing.T) {
	t.Parallel()

	original := validRegistryDefinition("energy_management")
	replacement := validRegistryDefinition("energy_management")
	replacement.manifest.Name = "Replacement"
	registry, err := NewRegistry(original)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Register(replacement); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("Register() error = %v, want ErrAlreadyRegistered", err)
	}
	registered, err := registry.Definition("energy_management")
	if err != nil || registered != original {
		t.Fatalf("Definition() = %T, %v; duplicate replaced original", registered, err)
	}
}

func TestRegistryValidatesConfigUsingDefensiveCopies(t *testing.T) {
	t.Parallel()

	validationError := errors.New("unsupported tariff")
	definition := validRegistryDefinition("energy_management")
	definition.validate = func(config Config) error {
		config[0] = '['
		return validationError
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	config := Config(`{"tariff":4.2}`)
	err = registry.ValidateConfig(context.Background(), "energy_management", config)
	if !errors.Is(err, ErrInvalidConfig) || !errors.Is(err, validationError) {
		t.Fatalf("ValidateConfig() error = %v, want both config errors", err)
	}
	if string(config) != `{"tariff":4.2}` {
		t.Fatalf("ValidateConfig() mutated caller config to %q", config)
	}
}

func TestRegistryCreatesRuntimeWithValidatedIndependentSpec(t *testing.T) {
	t.Parallel()

	definition := validRegistryDefinition("energy_management")
	validatedConfig := ""
	builtConfig := ""
	definition.validate = func(config Config) error {
		validatedConfig = string(config)
		config[0] = '['
		return nil
	}
	definition.build = func(spec RuntimeSpec, host Host) (Runtime, error) {
		builtConfig = string(spec.Config)
		spec.Config[0] = '['
		if _, ok := host.(registryHost); !ok {
			t.Fatalf("NewRuntime() host = %T, want registryHost", host)
		}
		return registryRuntime{}, nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	config := Config(`{"source":"logger"}`)
	runtime, err := registry.NewRuntime(context.Background(), "energy_management", RuntimeSpec{InstanceID: uuid.New(), Config: config}, registryHost{})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if runtime == nil || validatedConfig != string(config) || builtConfig != string(config) {
		t.Fatalf("NewRuntime() runtime/config = %T, %q, %q", runtime, validatedConfig, builtConfig)
	}
	if string(config) != `{"source":"logger"}` {
		t.Fatalf("NewRuntime() mutated caller config to %q", config)
	}
}

func TestRegistryRuntimeAndLookupErrors(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(validRegistryDefinition("energy_management"))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if manifests := (*Registry)(nil).List(); manifests == nil || len(manifests) != 0 {
		t.Fatalf("nil Registry List() = %#v, want non-nil empty list", manifests)
	}
	if _, err := registry.Manifest("unknown"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Manifest() error = %v, want ErrNotRegistered", err)
	}
	if _, err := registry.Definition("unknown"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Definition() error = %v, want ErrNotRegistered", err)
	}
	if err := registry.ValidateConfig(context.Background(), "unknown", nil); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("ValidateConfig() error = %v, want ErrNotRegistered", err)
	}
	if err := registry.ValidateConfig(nil, "energy_management", nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ValidateConfig(nil context) error = %v, want ErrInvalidInput", err)
	}
	if _, err := registry.NewRuntime(nil, "energy_management", RuntimeSpec{InstanceID: uuid.New()}, registryHost{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NewRuntime(nil context) error = %v, want ErrInvalidInput", err)
	}
	if _, err := registry.NewRuntime(context.Background(), "energy_management", RuntimeSpec{}, registryHost{}); !errors.Is(err, ErrInvalidRuntimeSpec) {
		t.Fatalf("NewRuntime() error = %v, want ErrInvalidRuntimeSpec", err)
	}
	if _, err := registry.NewRuntime(context.Background(), "energy_management", RuntimeSpec{InstanceID: uuid.New()}, nil); !errors.Is(err, ErrHostRequired) {
		t.Fatalf("NewRuntime() error = %v, want ErrHostRequired", err)
	}
	var typedNilHost *registryHostPointer
	if _, err := registry.NewRuntime(context.Background(), "energy_management", RuntimeSpec{InstanceID: uuid.New()}, typedNilHost); !errors.Is(err, ErrHostRequired) {
		t.Fatalf("NewRuntime() typed nil host error = %v, want ErrHostRequired", err)
	}

	nilRuntimeDefinition := validRegistryDefinition("nil_runtime")
	nilRuntimeDefinition.build = func(RuntimeSpec, Host) (Runtime, error) { return nil, nil }
	typedNilRuntimeDefinition := validRegistryDefinition("typed_nil_runtime")
	typedNilRuntimeDefinition.build = func(RuntimeSpec, Host) (Runtime, error) {
		var runtime *registryRuntimePointer
		return runtime, nil
	}
	factoryError := errors.New("failed to subscribe")
	failingDefinition := validRegistryDefinition("failing_runtime")
	failingDefinition.build = func(RuntimeSpec, Host) (Runtime, error) { return nil, factoryError }
	for _, definition := range []*registryDefinition{nilRuntimeDefinition, typedNilRuntimeDefinition, failingDefinition} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.manifest.Type, err)
		}
	}
	if _, err := registry.NewRuntime(context.Background(), "nil_runtime", RuntimeSpec{InstanceID: uuid.New()}, registryHost{}); !errors.Is(err, ErrRuntimeRequired) {
		t.Fatalf("NewRuntime(nil) error = %v, want ErrRuntimeRequired", err)
	}
	if _, err := registry.NewRuntime(context.Background(), "typed_nil_runtime", RuntimeSpec{InstanceID: uuid.New()}, registryHost{}); !errors.Is(err, ErrRuntimeRequired) {
		t.Fatalf("NewRuntime(typed nil) error = %v, want ErrRuntimeRequired", err)
	}
	if _, err := registry.NewRuntime(context.Background(), "failing_runtime", RuntimeSpec{InstanceID: uuid.New()}, registryHost{}); !errors.Is(err, factoryError) {
		t.Fatalf("NewRuntime(failure) error = %v, want factory error", err)
	}
}

func TestRegistrySupportsConcurrentStartupRegistrationAndReads(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	const definitionCount = 24
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index := 0; index < definitionCount; index++ {
		pluginType := Type(fmt.Sprintf("plugin_%02d", index))
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			if err := registry.Register(validRegistryDefinition(pluginType)); err != nil {
				t.Errorf("Register(%s) error = %v", pluginType, err)
			}
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			_ = registry.List()
		}()
	}
	close(start)
	waitGroup.Wait()
	if manifests := registry.List(); len(manifests) != definitionCount {
		t.Fatalf("List() count = %d, want %d", len(manifests), definitionCount)
	}
}

func validRegistryDefinition(pluginType Type) *registryDefinition {
	name := "Energy Management"
	if pluginType != "energy_management" {
		name = string(pluginType)
	}
	return &registryDefinition{manifest: Manifest{
		Type:              pluginType,
		Name:              name,
		Description:       "Built-in Plugin",
		Version:           "0.1.0",
		ConfigVersion:     1,
		MultipleInstances: true,
		Capabilities:      []Capability{},
	}}
}

func definitionWithManifest(manifest Manifest) *registryDefinition {
	return &registryDefinition{manifest: manifest}
}

func stringOfLength(length int) string {
	value := make([]byte, length)
	for index := range value {
		value[index] = 'x'
	}
	return string(value)
}
