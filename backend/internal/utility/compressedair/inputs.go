package compressedair

import (
	"errors"
	"sort"

	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/utility"
)

var ErrInvalidInputs = errors.New("invalid compressed-air utility inputs")

type Inputs struct {
	CompressorPower []utility.Mapping
	Flow            []utility.Mapping
	Volume          []utility.Mapping
	Pressure        []utility.Mapping
	Temperature     []utility.Mapping
	OperatingState  *utility.Mapping
	ProductionState *utility.Mapping
}

func Resolve(config utility.Config) (Inputs, error) {
	if err := config.Validate(); err != nil {
		return Inputs{}, ErrInvalidInputs
	}
	var result Inputs
	for _, mapping := range config.Mappings {
		if mapping.Semantic.Resource != asset.ResourceCompressedAir {
			continue
		}
		mapping = cloneMapping(mapping)
		switch mapping.Slot {
		case utility.SlotCompressorPower:
			result.CompressorPower = append(result.CompressorPower, mapping)
		case utility.SlotCompressedAirFlow:
			result.Flow = append(result.Flow, mapping)
		case utility.SlotCompressedAirVolume:
			result.Volume = append(result.Volume, mapping)
		case utility.SlotCompressedAirPressure:
			result.Pressure = append(result.Pressure, mapping)
		case utility.SlotCompressedAirTemperature:
			result.Temperature = append(result.Temperature, mapping)
		case utility.SlotOperatingState:
			if result.OperatingState != nil {
				return Inputs{}, ErrInvalidInputs
			}
			value := mapping
			result.OperatingState = &value
		case utility.SlotProductionState:
			if result.ProductionState != nil {
				return Inputs{}, ErrInvalidInputs
			}
			value := mapping
			result.ProductionState = &value
		}
	}
	if len(result.CompressorPower) == 0 || len(result.Flow)+len(result.Volume) == 0 {
		return Inputs{}, ErrInvalidInputs
	}
	if !validReferences(result.Flow, result.Volume, result.Pressure) {
		return Inputs{}, ErrInvalidInputs
	}
	sortInputs(&result)
	return result, nil
}

func cloneMapping(mapping utility.Mapping) utility.Mapping {
	if mapping.Semantic.Reference.TemperatureKelvin != nil {
		value := *mapping.Semantic.Reference.TemperatureKelvin
		mapping.Semantic.Reference.TemperatureKelvin = &value
	}
	if mapping.Semantic.Reference.PressurePascal != nil {
		value := *mapping.Semantic.Reference.PressurePascal
		mapping.Semantic.Reference.PressurePascal = &value
	}
	return mapping
}

func validReferences(flow, volume, pressure []utility.Mapping) bool {
	for _, mapping := range append(append([]utility.Mapping(nil), flow...), volume...) {
		reference := mapping.Semantic.Reference
		switch mapping.Semantic.Unit {
		case asset.UnitNormalCubicMetre, asset.UnitNormalCubicMetrePerSecond, asset.UnitNormalCubicMetrePerHour:
			if reference.VolumeBasis != asset.VolumeBasisNormalized || reference.TemperatureKelvin == nil || reference.PressurePascal == nil {
				return false
			}
		case asset.UnitCubicMetre, asset.UnitCubicMetrePerSecond, asset.UnitCubicMetrePerHour:
			if reference.VolumeBasis != asset.VolumeBasisActual || reference.TemperatureKelvin != nil || reference.PressurePascal != nil {
				return false
			}
		default:
			return false
		}
	}
	for _, mapping := range pressure {
		if mapping.Semantic.Reference.PressureBasis != asset.PressureBasisGauge && mapping.Semantic.Reference.PressureBasis != asset.PressureBasisAbsolute {
			return false
		}
	}
	return true
}

func sortInputs(inputs *Inputs) {
	groups := [][]utility.Mapping{inputs.CompressorPower, inputs.Flow, inputs.Volume, inputs.Pressure, inputs.Temperature}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool { return group[i].Key < group[j].Key })
	}
}
