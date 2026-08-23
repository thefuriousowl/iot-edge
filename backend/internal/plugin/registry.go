package plugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	maxPluginNameLength        = 100
	maxPluginDescriptionLength = 500
	maxPluginCapabilities      = 32
)

var (
	pluginTypePattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	pluginVersionPattern    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,31}$`)
	pluginCapabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type registration struct {
	definition Definition
	manifest   Manifest
}

type Registry struct {
	mutex         sync.RWMutex
	registrations map[Type]registration
}

func NewRegistry(definitions ...Definition) (*Registry, error) {
	registry := &Registry{registrations: make(map[Type]registration, len(definitions))}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (registry *Registry) Register(definition Definition) error {
	if registry == nil || isNil(definition) {
		return ErrDefinitionRequired
	}
	manifest := cloneManifest(definition.Manifest())
	if err := validateManifest(manifest); err != nil {
		return err
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.registrations == nil {
		registry.registrations = make(map[Type]registration)
	}
	if _, exists := registry.registrations[manifest.Type]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, manifest.Type)
	}
	registry.registrations[manifest.Type] = registration{definition: definition, manifest: manifest}
	return nil
}

func (registry *Registry) List() []Manifest {
	if registry == nil {
		return []Manifest{}
	}
	registry.mutex.RLock()
	manifests := make([]Manifest, 0, len(registry.registrations))
	for _, registered := range registry.registrations {
		manifests = append(manifests, cloneManifest(registered.manifest))
	}
	registry.mutex.RUnlock()
	sort.Slice(manifests, func(left, right int) bool {
		return manifests[left].Type < manifests[right].Type
	})
	return manifests
}

func (registry *Registry) Manifest(pluginType Type) (Manifest, error) {
	registered, err := registry.find(pluginType)
	if err != nil {
		return Manifest{}, err
	}
	return cloneManifest(registered.manifest), nil
}

func (registry *Registry) Definition(pluginType Type) (Definition, error) {
	registered, err := registry.find(pluginType)
	if err != nil {
		return nil, err
	}
	return registered.definition, nil
}

func (registry *Registry) ValidateConfig(ctx context.Context, pluginType Type, config Config) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	registered, err := registry.find(pluginType)
	if err != nil {
		return err
	}
	if err := registered.definition.ValidateConfig(ctx, cloneConfig(config)); err != nil {
		if errors.Is(err, ErrConfigValidationUnavailable) {
			return err
		}
		return fmt.Errorf("%w for %s: %w", ErrInvalidConfig, pluginType, err)
	}
	return nil
}

func (registry *Registry) NewRuntime(ctx context.Context, pluginType Type, spec RuntimeSpec, host Host) (Runtime, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if spec.InstanceID == uuid.Nil {
		return nil, ErrInvalidRuntimeSpec
	}
	if isNil(host) {
		return nil, ErrHostRequired
	}
	registered, err := registry.find(pluginType)
	if err != nil {
		return nil, err
	}
	if err := registered.definition.ValidateConfig(ctx, cloneConfig(spec.Config)); err != nil {
		if errors.Is(err, ErrConfigValidationUnavailable) {
			return nil, err
		}
		return nil, fmt.Errorf("%w for %s: %w", ErrInvalidConfig, pluginType, err)
	}
	spec.Config = cloneConfig(spec.Config)
	runtime, err := registered.definition.NewRuntime(spec, host)
	if err != nil {
		return nil, err
	}
	if isNil(runtime) {
		return nil, ErrRuntimeRequired
	}
	return runtime, nil
}

func (registry *Registry) find(pluginType Type) (registration, error) {
	if registry == nil {
		return registration{}, fmt.Errorf("%w: %s", ErrNotRegistered, pluginType)
	}
	registry.mutex.RLock()
	registered, exists := registry.registrations[pluginType]
	registry.mutex.RUnlock()
	if !exists {
		return registration{}, fmt.Errorf("%w: %s", ErrNotRegistered, pluginType)
	}
	return registered, nil
}

func validateManifest(manifest Manifest) error {
	if !pluginTypePattern.MatchString(string(manifest.Type)) {
		return fmt.Errorf("%w: type", ErrInvalidManifest)
	}
	if manifest.Name == "" || manifest.Name != strings.TrimSpace(manifest.Name) || len(manifest.Name) > maxPluginNameLength {
		return fmt.Errorf("%w: name", ErrInvalidManifest)
	}
	if manifest.Description != strings.TrimSpace(manifest.Description) || len(manifest.Description) > maxPluginDescriptionLength {
		return fmt.Errorf("%w: description", ErrInvalidManifest)
	}
	if !pluginVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("%w: version", ErrInvalidManifest)
	}
	if manifest.ConfigVersion == 0 {
		return fmt.Errorf("%w: config version", ErrInvalidManifest)
	}
	if len(manifest.Capabilities) > maxPluginCapabilities {
		return fmt.Errorf("%w: capabilities", ErrInvalidManifest)
	}
	seen := make(map[Capability]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		if !pluginCapabilityPattern.MatchString(string(capability)) {
			return fmt.Errorf("%w: capability %q", ErrInvalidManifest, capability)
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("%w: duplicate capability %q", ErrInvalidManifest, capability)
		}
		seen[capability] = struct{}{}
	}
	if err := ValidateOutputDescriptors(manifest.Outputs); err != nil {
		return fmt.Errorf("%w: outputs: %v", ErrInvalidManifest, err)
	}
	_, publishesOutputs := seen[CapabilityPluginOutputsPublish]
	if publishesOutputs != (len(manifest.Outputs) > 0) {
		return fmt.Errorf("%w: output capability and descriptors must be declared together", ErrInvalidManifest)
	}
	return nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
