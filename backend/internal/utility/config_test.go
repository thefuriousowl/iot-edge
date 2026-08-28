package utility

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

func TestConfigV1StrictDecodeAndDefensiveOrdering(t *testing.T) {
	t.Parallel()
	config := validConfig()
	config.Mappings[0], config.Mappings[1] = config.Mappings[1], config.Mappings[0]
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCompatibleConfig(ConfigVersionV1, raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Mappings[0].Key != "electrical_power_1" || decoded.Mappings[1].Key != "operating_state" {
		t.Fatalf("deterministic mappings = %#v", decoded.Mappings)
	}
	decoded.Mappings[0].Semantic.Reference.VolumeBasis = asset.VolumeBasisNormalized
	redecoded, err := DecodeConfigV1(raw)
	if err != nil || redecoded.Mappings[0].Semantic.Reference.VolumeBasis != "" {
		t.Fatalf("decoded config aliases caller: %#v, %v", redecoded, err)
	}
	if _, err := DecodeCompatibleConfig(2, raw); !errors.Is(err, ErrUnsupportedConfigVersion) {
		t.Fatalf("future version error = %v", err)
	}
	if _, err := DecodeConfigV1(append(raw, []byte(` {}`)...)); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("multiple JSON documents error = %v", err)
	}
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	object["future"] = true
	unknown, _ := json.Marshal(object)
	if _, err := DecodeConfigV1(unknown); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestConfigV1RejectsInvalidSlotsSourcesAndTariffs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing scope", func(value *Config) { value.AssetID = uuid.Nil }},
		{"local timezone", func(value *Config) { value.Timezone = "Local" }},
		{"zero max gap", func(value *Config) { value.MaxGapSeconds = 0 }},
		{"duplicate key", func(value *Config) { value.Mappings[1].Key = value.Mappings[0].Key }},
		{"duplicate source", func(value *Config) { value.Mappings[1].Source = value.Mappings[0].Source }},
		{"slot mismatch", func(value *Config) { value.Mappings[0].Slot = SlotPressure }},
		{"unknown slot", func(value *Config) { value.Mappings[0].Slot = "future" }},
		{"lowercase currency", func(value *Config) { value.Tariff.Currency = "thb" }},
		{"wrong energy unit", func(value *Config) { value.Tariff.EnergyUnit = asset.UnitKilowatt }},
		{"non finite rate", func(value *Config) { value.Tariff.RatePerEnergy = jsonNumberNaN() }},
		{"fixed tariff source", func(value *Config) { value.Tariff.Source = asset.TagSource(uuid.New()) }},
		{"source tariff missing owner", func(value *Config) {
			value.Tariff = Tariff{Mode: TariffSource, Currency: "THB", EnergyUnit: asset.UnitKilowattHour, Source: asset.TagSource(uuid.New())}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validConfig()
			test.mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateSourcesEnforcesAssetScopeAndSemanticCompatibility(t *testing.T) {
	t.Parallel()
	config := validConfig()
	scopeID := config.AssetID
	catalog := &sourceCatalog{values: map[string]SourceDescriptor{}}
	for _, mapping := range config.Mappings {
		unit, dataType := mapping.Semantic.Unit, "float64"
		if mapping.Semantic.Quantity == asset.QuantityPower {
			unit = asset.UnitWatt
		}
		if mapping.Semantic.Quantity == asset.QuantityState {
			dataType = "bool"
		}
		catalog.values[mapping.Source.Key()] = SourceDescriptor{Reference: mapping.Source, OwnerAssetID: mapping.OwnerAssetID, OwnerAncestors: []uuid.UUID{scopeID}, DataType: dataType, Unit: unit}
	}
	if err := ValidateSources(context.Background(), config, catalog); err != nil {
		t.Fatal(err)
	}
	var typedNil *sourceCatalog
	if err := ValidateSources(context.Background(), config, typedNil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("typed nil catalog error = %v", err)
	}

	outside := cloneConfig(config)
	descriptor := catalog.values[outside.Mappings[0].Source.Key()]
	descriptor.OwnerAncestors = []uuid.UUID{uuid.New()}
	catalog.values[outside.Mappings[0].Source.Key()] = descriptor
	if err := ValidateSources(context.Background(), outside, catalog); !errors.Is(err, ErrSourceOutsideAssetScope) {
		t.Fatalf("outside scope error = %v", err)
	}
	descriptor.OwnerAncestors = []uuid.UUID{scopeID}
	descriptor.DynamicUnit = true
	catalog.values[outside.Mappings[0].Source.Key()] = descriptor
	if err := ValidateSources(context.Background(), outside, catalog); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("dynamic source error = %v", err)
	}
}

func TestValidateTariffSourceUsesFixedRateUnitAndScope(t *testing.T) {
	t.Parallel()
	config := validConfig()
	tariffTag, tariffOwner := uuid.New(), config.Mappings[0].OwnerAssetID
	config.Tariff = Tariff{Mode: TariffSource, Currency: "THB", EnergyUnit: asset.UnitKilowattHour, OwnerAssetID: tariffOwner, Source: asset.TagSource(tariffTag)}
	catalog := &sourceCatalog{values: map[string]SourceDescriptor{}}
	for _, mapping := range config.Mappings {
		dataType := "float64"
		if mapping.Semantic.Quantity == asset.QuantityState {
			dataType = "bool"
		}
		catalog.values[mapping.Source.Key()] = SourceDescriptor{Reference: mapping.Source, OwnerAssetID: mapping.OwnerAssetID, OwnerAncestors: []uuid.UUID{config.AssetID}, DataType: dataType, Unit: mapping.Semantic.Unit}
	}
	catalog.values[config.Tariff.Source.Key()] = SourceDescriptor{Reference: config.Tariff.Source, OwnerAssetID: tariffOwner, OwnerAncestors: []uuid.UUID{config.AssetID}, DataType: "float64", Unit: "THB/kWh"}
	if err := ValidateSources(context.Background(), config, catalog); err != nil {
		t.Fatal(err)
	}
	descriptor := catalog.values[config.Tariff.Source.Key()]
	descriptor.Unit = "USD/kWh"
	catalog.values[config.Tariff.Source.Key()] = descriptor
	if err := ValidateSources(context.Background(), config, catalog); !errors.Is(err, ErrIncompatibleSource) {
		t.Fatalf("tariff unit error = %v", err)
	}
}

type sourceCatalog struct{ values map[string]SourceDescriptor }

func (catalog *sourceCatalog) Describe(_ context.Context, _ uuid.UUID, reference asset.SourceReference) (SourceDescriptor, error) {
	value, exists := catalog.values[reference.Key()]
	if !exists {
		return SourceDescriptor{}, ErrSourceUnavailable
	}
	value.OwnerAncestors = append([]uuid.UUID(nil), value.OwnerAncestors...)
	return value, nil
}

func validConfig() Config {
	scopeID, powerOwner, stateOwner := uuid.New(), uuid.New(), uuid.New()
	return Config{
		AssetID: scopeID, LoggerID: uuid.New(), Timezone: "Asia/Bangkok", MaxGapSeconds: 60,
		Mappings: []Mapping{
			{Key: "electrical_power_1", Slot: SlotElectricalPower, OwnerAssetID: powerOwner, Source: asset.TagSource(uuid.New()), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}},
			{Key: "operating_state", Slot: SlotOperatingState, OwnerAssetID: stateOwner, Source: asset.PluginOutputSource(uuid.New(), "running"), Semantic: asset.Semantic{Resource: asset.ResourceCustom, Quantity: asset.QuantityState, Unit: asset.UnitBoolean}},
		},
		Tariff: Tariff{Mode: TariffFixed, Currency: "THB", RatePerEnergy: 4.5, EnergyUnit: asset.UnitKilowattHour},
	}
}

func jsonNumberNaN() float64 { return math.NaN() }
