package asset

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
)

type Resource string
type Quantity string
type Dimension string
type Unit string
type VolumeBasis string
type PressureBasis string
type UnitPolicy string

const (
	ResourceElectricity   Resource = "electricity"
	ResourceThermal       Resource = "thermal"
	ResourceCompressedAir Resource = "compressed_air"
	ResourceSteam         Resource = "steam"
	ResourceGas           Resource = "gas"
	ResourceWater         Resource = "water"
	ResourceSolar         Resource = "solar"
	ResourceCustom        Resource = "custom"

	QuantityPower       Quantity = "power"
	QuantityEnergy      Quantity = "energy"
	QuantityFlowRate    Quantity = "flow_rate"
	QuantityVolume      Quantity = "volume"
	QuantityPressure    Quantity = "pressure"
	QuantityTemperature Quantity = "temperature"
	QuantityRatio       Quantity = "ratio"
	QuantityState       Quantity = "state"
	QuantityCost        Quantity = "cost"
	QuantityEmissions   Quantity = "emissions"
	QuantityCustom      Quantity = "custom"

	DimensionPower       Dimension = "power"
	DimensionEnergy      Dimension = "energy"
	DimensionFlowRate    Dimension = "volume_per_time"
	DimensionVolume      Dimension = "volume"
	DimensionPressure    Dimension = "pressure"
	DimensionTemperature Dimension = "temperature"
	DimensionRatio       Dimension = "ratio"
	DimensionState       Dimension = "state"
	DimensionCurrency    Dimension = "currency"
	DimensionEmissions   Dimension = "emissions_mass"
	DimensionCustom      Dimension = "custom"

	UnitWatt                      Unit = "W"
	UnitKilowatt                  Unit = "kW"
	UnitMegawatt                  Unit = "MW"
	UnitWattHour                  Unit = "Wh"
	UnitKilowattHour              Unit = "kWh"
	UnitMegawattHour              Unit = "MWh"
	UnitCubicMetre                Unit = "m3"
	UnitNormalCubicMetre          Unit = "Nm3"
	UnitCubicMetrePerSecond       Unit = "m3/s"
	UnitCubicMetrePerHour         Unit = "m3/h"
	UnitNormalCubicMetrePerSecond Unit = "Nm3/s"
	UnitNormalCubicMetrePerHour   Unit = "Nm3/h"
	UnitPascal                    Unit = "Pa"
	UnitKilopascal                Unit = "kPa"
	UnitBar                       Unit = "bar"
	UnitKelvin                    Unit = "K"
	UnitCelsius                   Unit = "degC"
	UnitFahrenheit                Unit = "degF"
	UnitOne                       Unit = "1"
	UnitPercent                   Unit = "%"
	UnitBoolean                   Unit = "bool"
	UnitGramCO2Equivalent         Unit = "gCO2e"
	UnitKilogramCO2Equivalent     Unit = "kgCO2e"
	UnitTonneCO2Equivalent        Unit = "tCO2e"

	VolumeBasisActual     VolumeBasis   = "actual"
	VolumeBasisNormalized VolumeBasis   = "normalized"
	PressureBasisAbsolute PressureBasis = "absolute"
	PressureBasisGauge    PressureBasis = "gauge"

	UnitPolicyFixed            UnitPolicy = "fixed"
	UnitPolicyISOCurrency      UnitPolicy = "iso_4217"
	UnitPolicyNamespacedCustom UnitPolicy = "namespaced_custom"
)

var (
	ErrInvalidSemantic    = errors.New("invalid measurement semantic")
	ErrUnknownUnit        = errors.New("unknown unit")
	ErrIncompatibleUnit   = errors.New("incompatible unit")
	ErrAmbiguousReference = errors.New("ambiguous reference condition")
	ErrInvalidPrecision   = errors.New("invalid precision")
)

type ReferenceCondition struct {
	VolumeBasis       VolumeBasis   `json:"volume_basis,omitempty"`
	PressureBasis     PressureBasis `json:"pressure_basis,omitempty"`
	TemperatureKelvin *float64      `json:"temperature_kelvin,omitempty"`
	PressurePascal    *float64      `json:"pressure_pascal,omitempty"`
}

type Semantic struct {
	Resource  Resource           `json:"resource"`
	Quantity  Quantity           `json:"quantity"`
	Unit      Unit               `json:"unit"`
	Precision uint8              `json:"precision"`
	Reference ReferenceCondition `json:"reference,omitempty"`
}

type UnitDefinition struct {
	Unit      Unit        `json:"unit"`
	Dimension Dimension   `json:"dimension"`
	Canonical Unit        `json:"canonical_unit"`
	Scale     float64     `json:"scale"`
	Offset    float64     `json:"offset"`
	Volume    VolumeBasis `json:"volume_basis,omitempty"`
	ExactOnly bool        `json:"exact_only,omitempty"`
	Dynamic   bool        `json:"dynamic,omitempty"`
}

type QuantityDefinition struct {
	Quantity      Quantity   `json:"quantity"`
	Dimension     Dimension  `json:"dimension"`
	CanonicalUnit Unit       `json:"canonical_unit,omitempty"`
	UnitPolicy    UnitPolicy `json:"unit_policy"`
}

var unitDefinitions = map[Unit]UnitDefinition{
	UnitWatt: {UnitWatt, DimensionPower, UnitWatt, 1, 0, "", false, false}, UnitKilowatt: {UnitKilowatt, DimensionPower, UnitWatt, 1e3, 0, "", false, false}, UnitMegawatt: {UnitMegawatt, DimensionPower, UnitWatt, 1e6, 0, "", false, false},
	UnitWattHour: {UnitWattHour, DimensionEnergy, UnitWattHour, 1, 0, "", false, false}, UnitKilowattHour: {UnitKilowattHour, DimensionEnergy, UnitWattHour, 1e3, 0, "", false, false}, UnitMegawattHour: {UnitMegawattHour, DimensionEnergy, UnitWattHour, 1e6, 0, "", false, false},
	UnitCubicMetre: {UnitCubicMetre, DimensionVolume, UnitCubicMetre, 1, 0, VolumeBasisActual, false, false}, UnitNormalCubicMetre: {UnitNormalCubicMetre, DimensionVolume, UnitNormalCubicMetre, 1, 0, VolumeBasisNormalized, false, false},
	UnitCubicMetrePerSecond: {UnitCubicMetrePerSecond, DimensionFlowRate, UnitCubicMetrePerSecond, 1, 0, VolumeBasisActual, false, false}, UnitCubicMetrePerHour: {UnitCubicMetrePerHour, DimensionFlowRate, UnitCubicMetrePerSecond, 1.0 / 3600, 0, VolumeBasisActual, false, false}, UnitNormalCubicMetrePerSecond: {UnitNormalCubicMetrePerSecond, DimensionFlowRate, UnitNormalCubicMetrePerSecond, 1, 0, VolumeBasisNormalized, false, false}, UnitNormalCubicMetrePerHour: {UnitNormalCubicMetrePerHour, DimensionFlowRate, UnitNormalCubicMetrePerSecond, 1.0 / 3600, 0, VolumeBasisNormalized, false, false},
	UnitPascal: {UnitPascal, DimensionPressure, UnitPascal, 1, 0, "", false, false}, UnitKilopascal: {UnitKilopascal, DimensionPressure, UnitPascal, 1e3, 0, "", false, false}, UnitBar: {UnitBar, DimensionPressure, UnitPascal, 1e5, 0, "", false, false},
	UnitKelvin: {UnitKelvin, DimensionTemperature, UnitKelvin, 1, 0, "", false, false}, UnitCelsius: {UnitCelsius, DimensionTemperature, UnitKelvin, 1, 273.15, "", false, false}, UnitFahrenheit: {UnitFahrenheit, DimensionTemperature, UnitKelvin, 5.0 / 9, 255.3722222222222, "", false, false},
	UnitOne: {UnitOne, DimensionRatio, UnitOne, 1, 0, "", false, false}, UnitPercent: {UnitPercent, DimensionRatio, UnitOne, 0.01, 0, "", false, false}, UnitBoolean: {UnitBoolean, DimensionState, UnitBoolean, 1, 0, "", true, false},
	UnitGramCO2Equivalent: {UnitGramCO2Equivalent, DimensionEmissions, UnitKilogramCO2Equivalent, .001, 0, "", false, false}, UnitKilogramCO2Equivalent: {UnitKilogramCO2Equivalent, DimensionEmissions, UnitKilogramCO2Equivalent, 1, 0, "", false, false}, UnitTonneCO2Equivalent: {UnitTonneCO2Equivalent, DimensionEmissions, UnitKilogramCO2Equivalent, 1e3, 0, "", false, false},
}

var quantityDimensions = map[Quantity]Dimension{
	QuantityPower: DimensionPower, QuantityEnergy: DimensionEnergy, QuantityFlowRate: DimensionFlowRate,
	QuantityVolume: DimensionVolume, QuantityPressure: DimensionPressure, QuantityTemperature: DimensionTemperature,
	QuantityRatio: DimensionRatio, QuantityState: DimensionState, QuantityCost: DimensionCurrency,
	QuantityEmissions: DimensionEmissions, QuantityCustom: DimensionCustom,
}

var canonicalUnitsByQuantity = map[Quantity]Unit{
	QuantityPower: UnitWatt, QuantityEnergy: UnitWattHour, QuantityPressure: UnitPascal,
	QuantityTemperature: UnitKelvin, QuantityRatio: UnitOne, QuantityState: UnitBoolean,
	QuantityEmissions: UnitKilogramCO2Equivalent,
}

var currencyUnitPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var customUnitPattern = regexp.MustCompile(`^custom:[a-z][a-z0-9_.-]{0,23}$`)

func (semantic Semantic) Validate() error {
	if !validResource(semantic.Resource) || semantic.Precision > 12 {
		if semantic.Precision > 12 {
			return ErrInvalidPrecision
		}
		return ErrInvalidSemantic
	}
	dimension, exists := quantityDimensions[semantic.Quantity]
	if !exists {
		return ErrInvalidSemantic
	}
	definition, exists := resolveUnitDefinition(semantic.Quantity, semantic.Unit)
	if !exists {
		return ErrUnknownUnit
	}
	if definition.Dimension != dimension {
		return ErrIncompatibleUnit
	}
	return validateReference(definition, semantic.Reference)
}

func (semantic Semantic) CanonicalUnit() (Unit, error) {
	if err := semantic.Validate(); err != nil {
		return "", err
	}
	definition, _ := resolveUnitDefinition(semantic.Quantity, semantic.Unit)
	return definition.Canonical, nil
}

func (semantic Semantic) CanonicalValue(value float64) (float64, error) {
	if err := semantic.Validate(); err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrInvalidSemantic
	}
	definition, _ := resolveUnitDefinition(semantic.Quantity, semantic.Unit)
	return round(value*definition.Scale+definition.Offset, semantic.Precision), nil
}

func Convert(value float64, from, to Semantic) (float64, error) {
	if err := from.Validate(); err != nil {
		return 0, err
	}
	if err := to.Validate(); err != nil {
		return 0, err
	}
	fromDefinition, _ := resolveUnitDefinition(from.Quantity, from.Unit)
	toDefinition, _ := resolveUnitDefinition(to.Quantity, to.Unit)
	if from.Resource != to.Resource || from.Quantity != to.Quantity || fromDefinition.Dimension != toDefinition.Dimension {
		return 0, ErrIncompatibleUnit
	}
	if (fromDefinition.ExactOnly || toDefinition.ExactOnly) && from.Unit != to.Unit {
		return 0, ErrIncompatibleUnit
	}
	if !equalReference(from.Reference, to.Reference) || fromDefinition.Volume != toDefinition.Volume {
		return 0, ErrAmbiguousReference
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrInvalidSemantic
	}
	canonical := value*fromDefinition.Scale + fromDefinition.Offset
	return round((canonical-toDefinition.Offset)/toDefinition.Scale, to.Precision), nil
}

func equalReference(left, right ReferenceCondition) bool {
	return left.VolumeBasis == right.VolumeBasis && left.PressureBasis == right.PressureBasis &&
		equalOptionalFloat(left.TemperatureKelvin, right.TemperatureKelvin) && equalOptionalFloat(left.PressurePascal, right.PressurePascal)
}

func equalOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func validateReference(definition UnitDefinition, reference ReferenceCondition) error {
	if definition.Volume == VolumeBasisNormalized {
		if reference.VolumeBasis != VolumeBasisNormalized || reference.TemperatureKelvin == nil || reference.PressurePascal == nil || *reference.TemperatureKelvin <= 0 || *reference.PressurePascal <= 0 {
			return ErrAmbiguousReference
		}
	} else if definition.Volume == VolumeBasisActual && reference.VolumeBasis != VolumeBasisActual {
		return ErrAmbiguousReference
	} else if definition.Volume == "" && reference.VolumeBasis != "" {
		return ErrInvalidSemantic
	}
	if definition.Dimension == DimensionPressure {
		if reference.PressureBasis != PressureBasisAbsolute && reference.PressureBasis != PressureBasisGauge {
			return ErrAmbiguousReference
		}
	} else if reference.PressureBasis != "" {
		return ErrInvalidSemantic
	}
	return nil
}

func resolveUnitDefinition(quantity Quantity, unit Unit) (UnitDefinition, bool) {
	if definition, exists := unitDefinitions[unit]; exists {
		return definition, true
	}
	if quantity == QuantityCost && currencyUnitPattern.MatchString(string(unit)) {
		return UnitDefinition{Unit: unit, Dimension: DimensionCurrency, Canonical: unit, Scale: 1, ExactOnly: true, Dynamic: true}, true
	}
	if quantity == QuantityCustom && customUnitPattern.MatchString(string(unit)) {
		return UnitDefinition{Unit: unit, Dimension: DimensionCustom, Canonical: unit, Scale: 1, ExactOnly: true, Dynamic: true}, true
	}
	return UnitDefinition{}, false
}

func QuantityRegistry() []QuantityDefinition {
	values := make([]QuantityDefinition, 0, len(quantityDimensions))
	for quantity, dimension := range quantityDimensions {
		policy := UnitPolicyFixed
		if quantity == QuantityCost {
			policy = UnitPolicyISOCurrency
		} else if quantity == QuantityCustom {
			policy = UnitPolicyNamespacedCustom
		}
		values = append(values, QuantityDefinition{Quantity: quantity, Dimension: dimension, CanonicalUnit: canonicalUnitsByQuantity[quantity], UnitPolicy: policy})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Quantity < values[j].Quantity })
	return values
}

func UnitRegistry(quantity Quantity) ([]UnitDefinition, error) {
	dimension, exists := quantityDimensions[quantity]
	if !exists {
		return nil, ErrInvalidSemantic
	}
	values := []UnitDefinition{}
	for _, definition := range unitDefinitions {
		if definition.Dimension == dimension {
			values = append(values, definition)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Unit < values[j].Unit })
	return values, nil
}

func LookupUnit(quantity Quantity, unit Unit) (UnitDefinition, error) {
	dimension, exists := quantityDimensions[quantity]
	if !exists {
		return UnitDefinition{}, ErrInvalidSemantic
	}
	definition, exists := resolveUnitDefinition(quantity, unit)
	if !exists {
		return UnitDefinition{}, ErrUnknownUnit
	}
	if definition.Dimension != dimension {
		return UnitDefinition{}, ErrIncompatibleUnit
	}
	return definition, nil
}

func validResource(resource Resource) bool {
	switch resource {
	case ResourceElectricity, ResourceThermal, ResourceCompressedAir, ResourceSteam, ResourceGas, ResourceWater, ResourceSolar, ResourceCustom:
		return true
	default:
		return false
	}
}

func round(value float64, precision uint8) float64 {
	factor := math.Pow10(int(precision))
	return math.Round(value*factor) / factor
}

func (semantic Semantic) String() string {
	return fmt.Sprintf("%s/%s[%s]", semantic.Resource, semantic.Quantity, semantic.Unit)
}
