package publisher

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrPublisherDefinitionRequired = errors.New("Data Publisher definition is required")
	ErrPublisherDefinitionExists   = errors.New("Data Publisher type is already registered")
)

type DefinitionDescriptor struct {
	Type          Type `json:"type"`
	ConfigVersion uint `json:"config_version"`
}

type Definition interface {
	Descriptor() DefinitionDescriptor
	NormalizeConfig(context.Context, Config) (Config, error)
	Supports(SourceDescriptor) bool
}

type SourceConfigDefinition interface {
	ValidateSourceConfig(context.Context, Config, []ResolvedSource) error
}

type DefinitionRegistry struct {
	mu          sync.RWMutex
	definitions map[Type]Definition
}

func NewDefinitionRegistry(definitions ...Definition) (*DefinitionRegistry, error) {
	registry := &DefinitionRegistry{definitions: make(map[Type]Definition, len(definitions))}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func NewDefaultDefinitionRegistry() (*DefinitionRegistry, error) {
	return NewDefinitionRegistry(
		publisherDefinition{publisherType: TypeHTTPServer},
		publisherDefinition{publisherType: TypeMQTT},
		publisherDefinition{publisherType: TypeModbusTCPServer},
	)
}

func (registry *DefinitionRegistry) Register(definition Definition) error {
	if registry == nil || isNilSourceDependency(definition) {
		return ErrPublisherDefinitionRequired
	}
	descriptor := definition.Descriptor()
	if !validPublisherType(descriptor.Type) || descriptor.ConfigVersion == 0 {
		return ErrPublisherDefinitionRequired
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.definitions == nil {
		registry.definitions = make(map[Type]Definition)
	}
	if _, exists := registry.definitions[descriptor.Type]; exists {
		return fmt.Errorf("%w: %s", ErrPublisherDefinitionExists, descriptor.Type)
	}
	registry.definitions[descriptor.Type] = definition
	return nil
}

func (registry *DefinitionRegistry) List() []DefinitionDescriptor {
	if registry == nil {
		return []DefinitionDescriptor{}
	}
	registry.mu.RLock()
	descriptors := make([]DefinitionDescriptor, 0, len(registry.definitions))
	for _, definition := range registry.definitions {
		descriptors = append(descriptors, definition.Descriptor())
	}
	registry.mu.RUnlock()
	sort.Slice(descriptors, func(first, second int) bool { return descriptors[first].Type < descriptors[second].Type })
	return descriptors
}

func (registry *DefinitionRegistry) Find(publisherType Type) (Definition, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedPublisherType, publisherType)
	}
	registry.mu.RLock()
	definition, exists := registry.definitions[publisherType]
	registry.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedPublisherType, publisherType)
	}
	return definition, nil
}

type publisherDefinition struct {
	publisherType Type
}

func (definition publisherDefinition) Descriptor() DefinitionDescriptor {
	version := uint(2)
	if definition.publisherType == TypeHTTPServer || definition.publisherType == TypeMQTT {
		version = 3
	}
	return DefinitionDescriptor{Type: definition.publisherType, ConfigVersion: version}
}

func (definition publisherDefinition) NormalizeConfig(ctx context.Context, config Config) (Config, error) {
	if ctx == nil {
		return nil, ErrInvalidPublisherConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if definition.publisherType == TypeHTTPServer {
		return normalizeHTTPPublisherConfig(config)
	}
	if definition.publisherType == TypeMQTT {
		return normalizeMQTTPublisherConfig(config)
	}
	return normalizePublisherConfig(config)
}

func (definition publisherDefinition) ValidateSourceConfig(ctx context.Context, config Config, sources []ResolvedSource) error {
	if definition.publisherType != TypeMQTT {
		return nil
	}
	if ctx == nil {
		return ErrInvalidMQTTConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parsed, err := ParseMQTTPublisherConfig(config)
	if err != nil {
		return err
	}
	_, err = NewJSONPayloadEngine().Compile(parsed.MQTT.Publish.PayloadTemplate, sources)
	return err
}

func (definition publisherDefinition) Supports(descriptor SourceDescriptor) bool {
	if !validSourceDataType(descriptor.DataType) {
		return false
	}
	return definition.publisherType != TypeModbusTCPServer || descriptor.DataType != SourceDataTypeString
}

func validPublisherType(publisherType Type) bool {
	switch publisherType {
	case TypeHTTPServer, TypeMQTT, TypeModbusTCPServer:
		return true
	default:
		return false
	}
}
