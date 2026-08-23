package plugin

import (
	"fmt"

	"github.com/google/uuid"
)

type CapabilityHost struct {
	capabilities map[Capability]any
}

func NewCapabilityHost(capabilities map[Capability]any) (*CapabilityHost, error) {
	host := &CapabilityHost{capabilities: make(map[Capability]any, len(capabilities))}
	for capability, value := range capabilities {
		if !pluginCapabilityPattern.MatchString(string(capability)) || isNil(value) {
			return nil, fmt.Errorf("%w: capability %q", ErrInvalidCapabilityHost, capability)
		}
		host.capabilities[capability] = value
	}
	return host, nil
}

func (host *CapabilityHost) ResolveCapability(capability Capability) (any, bool) {
	if host == nil {
		return nil, false
	}
	value, exists := host.capabilities[capability]
	return value, exists
}

type scopedHost struct {
	capabilities map[Capability]any
}

type capabilityScoper interface {
	ScopeCapability(uuid.UUID, Manifest) (any, error)
}

func newScopedHost(source Host, instanceID uuid.UUID, manifest Manifest) (*scopedHost, error) {
	capabilities := make(map[Capability]any, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		value, exists := source.ResolveCapability(capability)
		if !exists || isNil(value) {
			return nil, fmt.Errorf("%w: %s requires %s", ErrCapabilityUnavailable, manifest.Type, capability)
		}
		if scoper, scoped := value.(capabilityScoper); scoped {
			scopedValue, err := scoper.ScopeCapability(instanceID, manifest)
			if err != nil {
				return nil, fmt.Errorf("%w: %s requires scoped %s: %w", ErrCapabilityUnavailable, manifest.Type, capability, err)
			}
			if isNil(scopedValue) {
				return nil, fmt.Errorf("%w: %s requires scoped %s", ErrCapabilityUnavailable, manifest.Type, capability)
			}
			value = scopedValue
		}
		capabilities[capability] = value
	}
	return &scopedHost{capabilities: capabilities}, nil
}

func (host *scopedHost) ResolveCapability(capability Capability) (any, bool) {
	if host == nil {
		return nil, false
	}
	value, exists := host.capabilities[capability]
	return value, exists
}
