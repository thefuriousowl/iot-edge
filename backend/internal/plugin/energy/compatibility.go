package energy

import (
	"errors"
	"fmt"

	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const (
	ConfigVersionV1       uint = 1
	OutputSchemaVersionV1 uint = 1
)

var (
	ErrUnsupportedConfigVersion = errors.New("unsupported Energy config version")
	ErrUnsupportedOutputVersion = errors.New("unsupported Energy output schema version")
)

// DecodeCompatibleConfig is the single compatibility boundary for persisted
// Energy configurations. Keeping v1 explicit allows a future implementation to
// add a v2 adapter without silently reinterpreting existing JSON.
func DecodeCompatibleConfig(version uint, raw plugin.Config) (Config, error) {
	switch version {
	case ConfigVersionV1:
		return DecodeConfig(raw)
	default:
		return Config{}, fmt.Errorf("%w: %d", ErrUnsupportedConfigVersion, version)
	}
}

// CompatibleOutputDescriptors returns the immutable public catalog for a
// persisted output schema. Callers receive a defensive copy so compatibility
// metadata cannot be changed process-wide.
func CompatibleOutputDescriptors(version uint) ([]plugin.OutputDescriptor, error) {
	if version != OutputSchemaVersionV1 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedOutputVersion, version)
	}
	descriptors := outputDescriptors()
	return append([]plugin.OutputDescriptor(nil), descriptors...), nil
}
