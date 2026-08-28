package asset

import (
	"errors"
	"math"
	"testing"
)

func TestSemanticCanonicalConversions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		from, to    Semantic
		value, want float64
	}{
		{"power", Semantic{ResourceElectricity, QuantityPower, UnitKilowatt, 3, ReferenceCondition{}}, Semantic{ResourceElectricity, QuantityPower, UnitWatt, 0, ReferenceCondition{}}, 1.234, 1234},
		{"energy", Semantic{ResourceThermal, QuantityEnergy, UnitMegawattHour, 3, ReferenceCondition{}}, Semantic{ResourceThermal, QuantityEnergy, UnitKilowattHour, 1, ReferenceCondition{}}, 1.25, 1250},
		{"temperature", Semantic{ResourceCompressedAir, QuantityTemperature, UnitFahrenheit, 2, ReferenceCondition{}}, Semantic{ResourceCompressedAir, QuantityTemperature, UnitCelsius, 2, ReferenceCondition{}}, 68, 20},
		{"flow", Semantic{ResourceWater, QuantityFlowRate, UnitCubicMetrePerHour, 6, ReferenceCondition{VolumeBasis: VolumeBasisActual}}, Semantic{ResourceWater, QuantityFlowRate, UnitCubicMetrePerSecond, 6, ReferenceCondition{VolumeBasis: VolumeBasisActual}}, 3.6, .001},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Convert(test.value, test.from, test.to)
			if err != nil || math.Abs(got-test.want) > 1e-9 {
				t.Fatalf("Convert() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestSemanticFailsClosedForDimensionsAndReferences(t *testing.T) {
	t.Parallel()
	normal := Semantic{ResourceCompressedAir, QuantityVolume, UnitNormalCubicMetre, 3, ReferenceCondition{VolumeBasis: VolumeBasisNormalized}}
	if err := normal.Validate(); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("normalized volume error = %v", err)
	}
	temperature, pressure := 293.15, 101325.0
	normal.Reference.TemperatureKelvin, normal.Reference.PressurePascal = &temperature, &pressure
	if err := normal.Validate(); err != nil {
		t.Fatalf("normalized volume error = %v", err)
	}
	actual := Semantic{ResourceCompressedAir, QuantityVolume, UnitCubicMetre, 3, ReferenceCondition{VolumeBasis: VolumeBasisActual}}
	if _, err := Convert(1, normal, actual); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("normalized to actual error = %v", err)
	}
	ambiguousPressure := Semantic{ResourceCompressedAir, QuantityPressure, UnitBar, 2, ReferenceCondition{}}
	if err := ambiguousPressure.Validate(); !errors.Is(err, ErrAmbiguousReference) {
		t.Fatalf("pressure error = %v", err)
	}
	incompatible := Semantic{ResourceElectricity, QuantityEnergy, UnitKilowatt, 2, ReferenceCondition{}}
	if err := incompatible.Validate(); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("dimension error = %v", err)
	}
}

func TestSemanticPrecisionAndFiniteValues(t *testing.T) {
	t.Parallel()
	semantic := Semantic{ResourceElectricity, QuantityPower, UnitKilowatt, 2, ReferenceCondition{}}
	got, err := semantic.CanonicalValue(1.23456)
	if err != nil || got != 1234.56 {
		t.Fatalf("CanonicalValue() = %v, %v", got, err)
	}
	semantic.Precision = 13
	if err := semantic.Validate(); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("precision error = %v", err)
	}
	semantic.Precision = 2
	if _, err := semantic.CanonicalValue(math.Inf(1)); !errors.Is(err, ErrInvalidSemantic) {
		t.Fatalf("infinite error = %v", err)
	}
}
