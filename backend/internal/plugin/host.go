package plugin

import "fmt"

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
	source  Host
	allowed map[Capability]struct{}
}

func newScopedHost(source Host, manifest Manifest) (*scopedHost, error) {
	allowed := make(map[Capability]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		value, exists := source.ResolveCapability(capability)
		if !exists || isNil(value) {
			return nil, fmt.Errorf("%w: %s requires %s", ErrCapabilityUnavailable, manifest.Type, capability)
		}
		allowed[capability] = struct{}{}
	}
	return &scopedHost{source: source, allowed: allowed}, nil
}

func (host *scopedHost) ResolveCapability(capability Capability) (any, bool) {
	if host == nil {
		return nil, false
	}
	if _, allowed := host.allowed[capability]; !allowed {
		return nil, false
	}
	return host.source.ResolveCapability(capability)
}
