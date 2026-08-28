package compressedair

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/utility"
)

func TestResolveCompressedAirInputsWithExplicitReferences(t *testing.T) {
	t.Parallel()
	config := compressedConfig()
	inputs, err := Resolve(config)
	if err != nil || len(inputs.CompressorPower) != 1 || len(inputs.Flow) != 1 || len(inputs.Pressure) != 1 || len(inputs.Temperature) != 1 || inputs.OperatingState == nil || inputs.ProductionState == nil {
		t.Fatalf("inputs=%#v err=%v", inputs, err)
	}
	if inputs.Flow[0].Semantic.Reference.VolumeBasis != asset.VolumeBasisNormalized || *inputs.Flow[0].Semantic.Reference.TemperatureKelvin != 293.15 || *inputs.Flow[0].Semantic.Reference.PressurePascal != 101325 {
		t.Fatalf("flow reference=%#v", inputs.Flow[0].Semantic.Reference)
	}
	*inputs.Flow[0].Semantic.Reference.TemperatureKelvin = 1
	again, err := Resolve(config)
	if err != nil || *again.Flow[0].Semantic.Reference.TemperatureKelvin != 293.15 {
		t.Fatal("resolved inputs alias config reference pointers")
	}
}

func TestResolveAcceptsActualAccumulatedVolume(t *testing.T) {
	t.Parallel()
	config := compressedConfig()
	config.Mappings[1] = mapping("actual_volume", utility.SlotCompressedAirVolume, config.Mappings[1].OwnerAssetID, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityVolume, Unit: asset.UnitCubicMetre, Reference: asset.ReferenceCondition{VolumeBasis: asset.VolumeBasisActual}})
	inputs, err := Resolve(config)
	if err != nil || len(inputs.Flow) != 0 || len(inputs.Volume) != 1 || inputs.Volume[0].Semantic.Reference.VolumeBasis != asset.VolumeBasisActual {
		t.Fatalf("inputs=%#v err=%v", inputs, err)
	}
}

func TestResolveCompressedAirInputsFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*utility.Config)
	}{
		{"missing power", func(value *utility.Config) { value.Mappings = value.Mappings[1:] }},
		{"missing consumption", func(value *utility.Config) { value.Mappings = append(value.Mappings[:1], value.Mappings[2:]...) }},
		{"wrong flow resource", func(value *utility.Config) { value.Mappings[1].Semantic.Resource = asset.ResourceWater }},
		{"missing normalized temperature", func(value *utility.Config) { value.Mappings[1].Semantic.Reference.TemperatureKelvin = nil }},
		{"missing pressure basis", func(value *utility.Config) { value.Mappings[2].Semantic.Reference.PressureBasis = "" }},
		{"duplicate operating state", func(value *utility.Config) {
			duplicate := value.Mappings[4]
			duplicate.Key = "operating_2"
			duplicate.Source = asset.TagSource(uuid.New())
			value.Mappings = append(value.Mappings, duplicate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := compressedConfig()
			test.mutate(&value)
			if _, err := Resolve(value); !errors.Is(err, ErrInvalidInputs) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func compressedConfig() utility.Config {
	boundary, owner := uuid.New(), uuid.New()
	temperature, pressure := 293.15, 101325.0
	mappings := []utility.Mapping{
		mapping("compressor_power", utility.SlotCompressorPower, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt}),
		mapping("normalized_flow", utility.SlotCompressedAirFlow, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityFlowRate, Unit: asset.UnitNormalCubicMetrePerHour, Reference: asset.ReferenceCondition{VolumeBasis: asset.VolumeBasisNormalized, TemperatureKelvin: &temperature, PressurePascal: &pressure}}),
		mapping("header_pressure", utility.SlotCompressedAirPressure, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityPressure, Unit: asset.UnitBar, Reference: asset.ReferenceCondition{PressureBasis: asset.PressureBasisGauge}}),
		mapping("air_temperature", utility.SlotCompressedAirTemperature, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityTemperature, Unit: asset.UnitCelsius}),
		mapping("operating", utility.SlotOperatingState, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityState, Unit: asset.UnitBoolean}),
		mapping("production", utility.SlotProductionState, owner, asset.Semantic{Resource: asset.ResourceCompressedAir, Quantity: asset.QuantityState, Unit: asset.UnitBoolean}),
	}
	return utility.Config{AssetID: boundary, LoggerID: uuid.New(), Timezone: "UTC", MaxGapSeconds: 60, Mappings: mappings, Tariff: utility.Tariff{Mode: utility.TariffNone}}
}

func mapping(key string, slot utility.SemanticSlot, owner uuid.UUID, semantic asset.Semantic) utility.Mapping {
	return utility.Mapping{Key: key, Slot: slot, OwnerAssetID: owner, Source: asset.TagSource(uuid.New()), Semantic: semantic}
}
