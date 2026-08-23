package energy

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const PluginType plugin.Type = "energy_management"

var (
	ErrLoggerReaderRequired   = errors.New("Energy Data Logger reader is required")
	ErrRuntimeFactoryRequired = errors.New("Energy runtime factory is required")
)

type LoggerReader interface {
	Find(context.Context, uuid.UUID) (*datalogger.Logger, error)
}

type RuntimeFactory interface {
	NewRuntime(plugin.RuntimeSpec, plugin.Host, Config) (plugin.Runtime, error)
}

type DefinitionOption func(*Definition) error

func WithRuntimeFactory(factory RuntimeFactory) DefinitionOption {
	return func(definition *Definition) error {
		if isNil(factory) {
			return ErrRuntimeFactoryRequired
		}
		definition.factory = factory
		return nil
	}
}

type Definition struct {
	loggers LoggerReader
	factory RuntimeFactory
}

func NewDefinition(loggers LoggerReader, options ...DefinitionOption) (*Definition, error) {
	if isNil(loggers) {
		return nil, ErrLoggerReaderRequired
	}
	definition := &Definition{loggers: loggers, factory: defaultRuntimeFactory{}}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(definition); err != nil {
			return nil, err
		}
	}
	return definition, nil
}

func (*Definition) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type: PluginType, Name: "Energy Management",
		Description: "Calculate electrical and thermal energy, cost, and COP from synchronized Data Logger batches",
		Version:     "0.1.0", ConfigVersion: 1, MultipleInstances: true,
		Capabilities: []plugin.Capability{
			plugin.CapabilityLoggerCommittedBatches,
			plugin.CapabilityLoggerHistoryBatches,
			plugin.CapabilityPluginOutputsPublish,
		},
		Outputs: outputDescriptors(),
	}
}

func (definition *Definition) ValidateConfig(ctx context.Context, raw plugin.Config) error {
	if ctx == nil {
		return plugin.ErrConfigValidationUnavailable
	}
	config, err := DecodeConfig(raw)
	if err != nil {
		return err
	}
	logger, err := definition.loggers.Find(ctx, config.LoggerID)
	if err != nil {
		if errors.Is(err, datalogger.ErrLoggerNotFound) {
			return fmt.Errorf("%w: %s", ErrLoggerUnavailable, config.LoggerID)
		}
		return fmt.Errorf("%w: loading Data Logger: %w", plugin.ErrConfigValidationUnavailable, err)
	}
	return ValidateLoggerMembership(config, logger)
}

func (definition *Definition) NewRuntime(spec plugin.RuntimeSpec, host plugin.Host) (plugin.Runtime, error) {
	config, err := DecodeConfig(spec.Config)
	if err != nil {
		return nil, err
	}
	return definition.factory.NewRuntime(spec, host, config)
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

var _ plugin.Definition = (*Definition)(nil)
