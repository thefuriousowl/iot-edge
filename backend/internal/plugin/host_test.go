package plugin

import (
	"errors"
	"testing"
)

func TestCapabilityHostSnapshotsValuesAndScopedHostEnforcesManifest(t *testing.T) {
	t.Parallel()

	values := map[Capability]any{"logger.history": "history", "secrets": "secret"}
	host, err := NewCapabilityHost(values)
	if err != nil {
		t.Fatalf("NewCapabilityHost() error = %v", err)
	}
	delete(values, "logger.history")
	values["secrets"] = "changed"
	if value, exists := host.ResolveCapability("logger.history"); !exists || value != "history" {
		t.Fatalf("ResolveCapability(logger.history) = %#v, %t", value, exists)
	}
	if value, exists := host.ResolveCapability("secrets"); !exists || value != "secret" {
		t.Fatalf("ResolveCapability(secrets) = %#v, %t", value, exists)
	}

	scoped, err := newScopedHost(host, Manifest{Type: "energy_management", Capabilities: []Capability{"logger.history"}})
	if err != nil {
		t.Fatalf("newScopedHost() error = %v", err)
	}
	if value, exists := scoped.ResolveCapability("logger.history"); !exists || value != "history" {
		t.Fatalf("scoped history = %#v, %t", value, exists)
	}
	if value, exists := scoped.ResolveCapability("secrets"); exists || value != nil {
		t.Fatalf("scoped undeclared secret = %#v, %t", value, exists)
	}
	if _, err := newScopedHost(host, Manifest{Type: "energy_management", Capabilities: []Capability{"logger.batches"}}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("newScopedHost(missing) error = %v", err)
	}
}

func TestCapabilityHostRejectsInvalidCapabilities(t *testing.T) {
	t.Parallel()

	var typedNil *int
	tests := []map[Capability]any{
		{"Bad Capability": "value"},
		{"logger.history": nil},
		{"logger.history": typedNil},
	}
	for _, capabilities := range tests {
		if _, err := NewCapabilityHost(capabilities); !errors.Is(err, ErrInvalidCapabilityHost) {
			t.Errorf("NewCapabilityHost(%#v) error = %v", capabilities, err)
		}
	}
	if value, exists := (*CapabilityHost)(nil).ResolveCapability("logger.history"); exists || value != nil {
		t.Errorf("nil host resolution = %#v, %t", value, exists)
	}
	if _, err := newScopedHost(nilValueHost{}, Manifest{Type: "energy_management", Capabilities: []Capability{"logger.history"}}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Errorf("newScopedHost(typed nil value) error = %v", err)
	}
}

type nilValueHost struct{}

func (nilValueHost) ResolveCapability(Capability) (any, bool) {
	var value *int
	return value, true
}
