package asset

import (
	"errors"
	"fmt"
	"math"
)

type Resource string
type Quantity string
type Dimension string
type Unit string
type VolumeBasis string
type PressureBasis string

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

	DimensionPower       Dimension = "power"
	DimensionEnergy      Dimension = "energy"
	DimensionFlowRate    Dimension = "volume_per_time"
	DimensionVolume      Dimension = "volume"
	DimensionPressure    Dimension = "pressure"
	DimensionTemperature Dimension = "temperature"
	DimensionRatio       Dimension = "ratio"
	DimensionState       Dimension = "state"
	DimensionCurrency    Dimension = "currency"

	UnitWatt                    Unit = "W"
	UnitKilowatt                Unit = "kW"
	UnitMegawatt                Unit = "MW"
	UnitWattHour                Unit = "Wh"
	UnitKilowattHour            Unit = "kWh"
	UnitMegawattHour            Unit = "MWh"
	UnitCubicMetre              Unit = "m3"
	UnitNormalCubicMetre        Unit = "Nm3"
	UnitCubicMetrePerSecond     Unit = "m3/s"
	UnitCubicMetrePerHour       Unit = "m3/h"
	UnitNormalCubicMetrePerHour Unit = "Nm3/h"
	UnitPascal                  Unit = "Pa"
	UnitKilopascal              Unit = "kPa"
	UnitBar                     Unit = "bar"
	UnitKelvin                  Unit = "K"
	UnitCelsius                 Unit = "degC"
	UnitFahrenheit              Unit = "degF"
	UnitOne                     Unit = "1"
	UnitPercent                 Unit = "%"
	UnitBoolean                 Unit = "bool"

	VolumeBasisActual     VolumeBasis   = "actual"
	VolumeBasisNormalized VolumeBasis   = "normalized"
	PressureBasisAbsolute PressureBasis = "absolute"
	PressureBasisGauge    PressureBasis = "gauge"
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

type unitDefinition struct {
	dimension Dimension
	canonical Unit
	scale     float64
	offset    float64
	volume    VolumeBasis
}

var unitDefinitions = map[Unit]unitDefinition{
	UnitWatt: {DimensionPower, UnitWatt, 1, 0, ""}, UnitKilowatt: {DimensionPower, UnitWatt, 1e3, 0, ""}, UnitMegawatt: {DimensionPower, UnitWatt, 1e6, 0, ""},
	UnitWattHour: {DimensionEnergy, UnitWattHour, 1, 0, ""}, UnitKilowattHour: {DimensionEnergy, UnitWattHour, 1e3, 0, ""}, UnitMegawattHour: {DimensionEnergy, UnitWattHour, 1e6, 0, ""},
	UnitCubicMetre: {DimensionVolume, UnitCubicMetre, 1, 0, VolumeBasisActual}, UnitNormalCubicMetre: {DimensionVolume, UnitCubicMetre, 1, 0, VolumeBasisNormalized},
	UnitCubicMetrePerSecond: {DimensionFlowRate, UnitCubicMetrePerSecond, 1, 0, VolumeBasisActual}, UnitCubicMetrePerHour: {DimensionFlowRate, UnitCubicMetrePerSecond, 1.0 / 3600, 0, VolumeBasisActual}, UnitNormalCubicMetrePerHour: {DimensionFlowRate, UnitCubicMetrePerSecond, 1.0 / 3600, 0, VolumeBasisNormalized},
	UnitPascal: {DimensionPressure, UnitPascal, 1, 0, ""}, UnitKilopascal: {DimensionPressure, UnitPascal, 1e3, 0, ""}, UnitBar: {DimensionPressure, UnitPascal, 1e5, 0, ""},
	UnitKelvin: {DimensionTemperature, UnitKelvin, 1, 0, ""}, UnitCelsius: {DimensionTemperature, UnitKelvin, 1, 273.15, ""}, UnitFahrenheit: {DimensionTemperature, UnitKelvin, 5.0 / 9, 255.3722222222222, ""},
	UnitOne: {DimensionRatio, UnitOne, 1, 0, ""}, UnitPercent: {DimensionRatio, UnitOne, 0.01, 0, ""}, UnitBoolean: {DimensionState, UnitBoolean, 1, 0, ""},
}

var quantityDimensions = map[Quantity]Dimension{
	QuantityPower: DimensionPower, QuantityEnergy: DimensionEnergy, QuantityFlowRate: DimensionFlowRate,
	QuantityVolume: DimensionVolume, QuantityPressure: DimensionPressure, QuantityTemperature: DimensionTemperature,
	QuantityRatio: DimensionRatio, QuantityState: DimensionState, QuantityCost: DimensionCurrency,
}

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
	definition, exists := unitDefinitions[semantic.Unit]
	if !exists {
		return ErrUnknownUnit
	}
	if definition.dimension != dimension {
		return ErrIncompatibleUnit
	}
	return validateReference(definition, semantic.Reference)
}

func (semantic Semantic) CanonicalUnit() (Unit, error) {
	if err := semantic.Validate(); err != nil {
		return "", err
	}
	return unitDefinitions[semantic.Unit].canonical, nil
}

func (semantic Semantic) CanonicalValue(value float64) (float64, error) {
	if err := semantic.Validate(); err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrInvalidSemantic
	}
	definition := unitDefinitions[semantic.Unit]
	return round(value*definition.scale+definition.offset, semantic.Precision), nil
}

func Convert(value float64, from, to Semantic) (float64, error) {
	if err := from.Validate(); err != nil {
		return 0, err
	}
	if err := to.Validate(); err != nil {
		return 0, err
	}
	fromDefinition, toDefinition := unitDefinitions[from.Unit], unitDefinitions[to.Unit]
	if from.Resource != to.Resource || from.Quantity != to.Quantity || fromDefinition.dimension != toDefinition.dimension {
		return 0, ErrIncompatibleUnit
	}
	if !equalReference(from.Reference, to.Reference) || fromDefinition.volume != toDefinition.volume {
		return 0, ErrAmbiguousReference
	}
	canonical := value*fromDefinition.scale + fromDefinition.offset
	return round((canonical-toDefinition.offset)/toDefinition.scale, to.Precision), nil
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

func validateReference(definition unitDefinition, reference ReferenceCondition) error {
	if definition.volume == VolumeBasisNormalized {
		if reference.VolumeBasis != VolumeBasisNormalized || reference.TemperatureKelvin == nil || reference.PressurePascal == nil || *reference.TemperatureKelvin <= 0 || *reference.PressurePascal <= 0 {
			return ErrAmbiguousReference
		}
	} else if definition.volume == VolumeBasisActual && reference.VolumeBasis != VolumeBasisActual {
		return ErrAmbiguousReference
	}
	if definition.dimension == DimensionPressure {
		if reference.PressureBasis != PressureBasisAbsolute && reference.PressureBasis != PressureBasisGauge {
			return ErrAmbiguousReference
		}
	} else if reference.PressureBasis != "" {
		return ErrInvalidSemantic
	}
	return nil
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
