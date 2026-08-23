package plugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

type Type string
type Capability string
type Config = json.RawMessage

const (
	CapabilityLoggerCommittedBatches Capability = "logger.committed_batches"
	CapabilityLoggerHistoryBatches   Capability = "logger.history_batches"
	CapabilityPluginOutputsPublish   Capability = "plugin.outputs.publish"
)

var (
	ErrDefinitionRequired          = errors.New("plugin definition is required")
	ErrInvalidManifest             = errors.New("invalid plugin manifest")
	ErrAlreadyRegistered           = errors.New("plugin type is already registered")
	ErrNotRegistered               = errors.New("plugin type is not registered")
	ErrInvalidConfig               = errors.New("invalid plugin config")
	ErrInvalidRuntimeSpec          = errors.New("invalid plugin runtime spec")
	ErrHostRequired                = errors.New("plugin host is required")
	ErrRuntimeRequired             = errors.New("plugin runtime is required")
	ErrRuntimeUnavailable          = errors.New("plugin runtime is unavailable")
	ErrConfigValidationUnavailable = errors.New("plugin config validation is unavailable")
)

type Manifest struct {
	Type              Type               `json:"type"`
	Name              string             `json:"name"`
	Description       string             `json:"description,omitempty"`
	Version           string             `json:"version"`
	ConfigVersion     uint               `json:"config_version"`
	MultipleInstances bool               `json:"multiple_instances"`
	Capabilities      []Capability       `json:"capabilities"`
	Outputs           []OutputDescriptor `json:"outputs,omitempty"`
}

type RuntimeSpec struct {
	InstanceID uuid.UUID
	Config     Config
}

type Host interface {
	ResolveCapability(Capability) (any, bool)
}

type Runtime interface {
	Run(context.Context) error
}

type Definition interface {
	Manifest() Manifest
	ValidateConfig(context.Context, Config) error
	NewRuntime(RuntimeSpec, Host) (Runtime, error)
}

func cloneConfig(config Config) Config {
	if config == nil {
		return nil
	}
	return append(Config(nil), config...)
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Capabilities = append([]Capability(nil), manifest.Capabilities...)
	manifest.Outputs = append([]OutputDescriptor(nil), manifest.Outputs...)
	return manifest
}
