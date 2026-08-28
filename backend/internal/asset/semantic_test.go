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

func TestCanonicalRegistryCoversUtilityQuantities(t *testing.T) {
	t.Parallel()
	quantities := QuantityRegistry()
	want := map[Quantity]Dimension{
		QuantityPower: DimensionPower, QuantityEnergy: DimensionEnergy, QuantityFlowRate: DimensionFlowRate,
		QuantityVolume: DimensionVolume, QuantityPressure: DimensionPressure, QuantityTemperature: DimensionTemperature,
		QuantityRatio: DimensionRatio, QuantityState: DimensionState, QuantityCost: DimensionCurrency,
		QuantityEmissions: DimensionEmissions, QuantityCustom: DimensionCustom,
	}
	if len(quantities) != len(want) {
		t.Fatalf("quantity registry length = %d, want %d", len(quantities), len(want))
	}
	for _, definition := range quantities {
		if want[definition.Quantity] != definition.Dimension {
			t.Fatalf("quantity definition = %#v", definition)
		}
		if definition.Quantity == QuantityCost && definition.UnitPolicy != UnitPolicyISOCurrency || definition.Quantity == QuantityCustom && definition.UnitPolicy != UnitPolicyNamespacedCustom {
			t.Fatalf("dynamic unit policy = %#v", definition)
		}
		if (definition.Quantity == QuantityFlowRate || definition.Quantity == QuantityVolume) && definition.CanonicalUnit != "" {
			t.Fatalf("reference-dependent quantity has global canonical unit = %#v", definition)
		}
		delete(want, definition.Quantity)
	}
	if len(want) != 0 {
		t.Fatalf("missing quantities = %v", want)
	}

	emissionUnits, err := UnitRegistry(QuantityEmissions)
	if err != nil || len(emissionUnits) != 3 {
		t.Fatalf("emission units = %#v, %v", emissionUnits, err)
	}
	if _, err := LookupUnit(QuantityPower, UnitKilogramCO2Equivalent); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("cross-dimension registry lookup error = %v", err)
	}
	if _, err := UnitRegistry("unknown"); !errors.Is(err, ErrInvalidSemantic) {
		t.Fatalf("unknown quantity registry error = %v", err)
	}
}

func TestCanonicalRegistryConvertsEmissionsAndFailsClosedForDynamicUnits(t *testing.T) {
	t.Parallel()
	emissionsFrom := Semantic{Resource: ResourceElectricity, Quantity: QuantityEmissions, Unit: UnitTonneCO2Equivalent, Precision: 3}
	emissionsTo := Semantic{Resource: ResourceElectricity, Quantity: QuantityEmissions, Unit: UnitKilogramCO2Equivalent, Precision: 1}
	converted, err := Convert(1.25, emissionsFrom, emissionsTo)
	if err != nil || converted != 1250 {
		t.Fatalf("emissions conversion = %v, %v", converted, err)
	}

	thb := Semantic{Resource: ResourceElectricity, Quantity: QuantityCost, Unit: "THB", Precision: 2}
	if err := thb.Validate(); err != nil {
		t.Fatalf("THB semantic error = %v", err)
	}
	if converted, err := Convert(12.345, thb, thb); err != nil || converted != 12.35 {
		t.Fatalf("same-currency conversion = %v, %v", converted, err)
	}
	usd := thb
	usd.Unit = "USD"
	if _, err := Convert(1, thb, usd); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("cross-currency conversion error = %v", err)
	}
	invalidCurrency := thb
	invalidCurrency.Unit = "thb"
	if err := invalidCurrency.Validate(); !errors.Is(err, ErrUnknownUnit) {
		t.Fatalf("invalid currency error = %v", err)
	}

	custom := Semantic{Resource: ResourceCustom, Quantity: QuantityCustom, Unit: "custom:production_tonne", Precision: 3}
	if err := custom.Validate(); err != nil {
		t.Fatalf("custom semantic error = %v", err)
	}
	otherCustom := custom
	otherCustom.Unit = "custom:batch"
	if _, err := Convert(1, custom, otherCustom); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("cross-custom conversion error = %v", err)
	}
	invalidCustom := custom
	invalidCustom.Unit = "tonne"
	if err := invalidCustom.Validate(); !errors.Is(err, ErrUnknownUnit) {
		t.Fatalf("unqualified custom unit error = %v", err)
	}
}

func TestCanonicalRegistryUtilityResourceMatrix(t *testing.T) {
	t.Parallel()
	normalTemperature, normalPressure := 293.15, 101325.0
	normalReference := ReferenceCondition{VolumeBasis: VolumeBasisNormalized, TemperatureKelvin: &normalTemperature, PressurePascal: &normalPressure}
	tests := []struct {
		name        string
		from, to    Semantic
		value, want float64
	}{
		{"electricity", Semantic{Resource: ResourceElectricity, Quantity: QuantityPower, Unit: UnitWatt, Precision: 3}, Semantic{Resource: ResourceElectricity, Quantity: QuantityPower, Unit: UnitKilowatt, Precision: 3}, 1250, 1.25},
		{"thermal", Semantic{Resource: ResourceThermal, Quantity: QuantityEnergy, Unit: UnitKilowattHour, Precision: 3}, Semantic{Resource: ResourceThermal, Quantity: QuantityEnergy, Unit: UnitMegawattHour, Precision: 6}, 1250, 1.25},
		{"compressed air", Semantic{Resource: ResourceCompressedAir, Quantity: QuantityFlowRate, Unit: UnitNormalCubicMetrePerHour, Precision: 6, Reference: normalReference}, Semantic{Resource: ResourceCompressedAir, Quantity: QuantityFlowRate, Unit: UnitNormalCubicMetrePerSecond, Precision: 6, Reference: normalReference}, 3600, 1},
		{"water", Semantic{Resource: ResourceWater, Quantity: QuantityVolume, Unit: UnitCubicMetre, Precision: 3, Reference: ReferenceCondition{VolumeBasis: VolumeBasisActual}}, Semantic{Resource: ResourceWater, Quantity: QuantityVolume, Unit: UnitCubicMetre, Precision: 3, Reference: ReferenceCondition{VolumeBasis: VolumeBasisActual}}, 12.5, 12.5},
		{"state", Semantic{Resource: ResourceCustom, Quantity: QuantityState, Unit: UnitBoolean}, Semantic{Resource: ResourceCustom, Quantity: QuantityState, Unit: UnitBoolean}, 1, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Convert(test.value, test.from, test.to)
			if err != nil || math.Abs(got-test.want) > 1e-9 {
				t.Fatalf("Convert() = %v, %v; want %v", got, err, test.want)
			}
		})
	}

	crossResource := tests[0].to
	crossResource.Resource = ResourceThermal
	if _, err := Convert(1, tests[0].from, crossResource); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("cross-resource conversion error = %v", err)
	}
	canonical, err := tests[2].from.CanonicalUnit()
	if err != nil || canonical != UnitNormalCubicMetrePerSecond {
		t.Fatalf("normalized canonical unit = %q, %v", canonical, err)
	}
}
