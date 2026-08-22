package tag

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestLinearTransformNormalizesAndAppliesFiniteValues(t *testing.T) {
	t.Parallel()
	canonical, err := normalizeLinearTransform(json.RawMessage(`{"gain":0.0015259021896696422,"offset":-10}`))
	if err != nil {
		t.Fatalf("normalizeLinearTransform() error = %v", err)
	}
	if string(canonical) != `{"gain":0.0015259021896696422,"offset":-10}` {
		t.Errorf("canonical = %s", canonical)
	}
	value, err := applyLinearTransform(uint16(32768), canonical)
	if err != nil {
		t.Fatalf("applyLinearTransform() error = %v", err)
	}
	if difference := math.Abs(value.(float64) - 40.000762951094835); difference > 1e-12 {
		t.Errorf("value = %.15f", value)
	}
}

func TestLinearTransformRejectsInvalidContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  any
		config string
	}{
		{name: "missing gain", value: 1, config: `{}`},
		{name: "unknown field", value: 1, config: `{"gain":1,"scale":2}`},
		{name: "non finite gain", value: 1, config: `{"gain":1e999}`},
		{name: "non numeric source", value: true, config: `{"gain":1}`},
		{name: "non finite result", value: math.MaxFloat64, config: `{"gain":2}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := applyLinearTransform(test.value, json.RawMessage(test.config)); !errors.Is(err, ErrInvalidTransformConfig) {
				t.Fatalf("applyLinearTransform() error = %v", err)
			}
		})
	}
}
