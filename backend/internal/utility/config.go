package utility

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

const (
	ConfigVersionV1 uint = 1
	MaxMappings          = 256

	SlotElectricalPower          SemanticSlot = "electrical_power"
	SlotThermalPower             SemanticSlot = "thermal_power"
	SlotFlowRate                 SemanticSlot = "flow_rate"
	SlotAccumulatedVolume        SemanticSlot = "accumulated_volume"
	SlotPressure                 SemanticSlot = "pressure"
	SlotTemperature              SemanticSlot = "temperature"
	SlotOperatingState           SemanticSlot = "operating_state"
	SlotProductionState          SemanticSlot = "production_state"
	SlotEmissions                SemanticSlot = "emissions"
	SlotCustom                   SemanticSlot = "custom"
	SlotCompressorPower          SemanticSlot = "compressor_power"
	SlotCompressedAirFlow        SemanticSlot = "compressed_air_flow"
	SlotCompressedAirVolume      SemanticSlot = "compressed_air_volume"
	SlotCompressedAirPressure    SemanticSlot = "compressed_air_pressure"
	SlotCompressedAirTemperature SemanticSlot = "compressed_air_temperature"

	TariffNone   TariffMode = "none"
	TariffFixed  TariffMode = "fixed"
	TariffSource TariffMode = "source"
)

var (
	ErrInvalidConfiguration     = errors.New("invalid utility mapping configuration")
	ErrUnsupportedConfigVersion = errors.New("unsupported utility mapping config version")
	ErrSourceUnavailable        = errors.New("utility mapping source is unavailable")
	ErrSourceOutsideAssetScope  = errors.New("utility mapping source is outside Asset scope")
	ErrIncompatibleSource       = errors.New("utility mapping source is semantically incompatible")

	mappingKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type SemanticSlot string
type TariffMode string

type Mapping struct {
	Key          string                `json:"key"`
	Slot         SemanticSlot          `json:"slot"`
	OwnerAssetID uuid.UUID             `json:"owner_asset_id"`
	Source       asset.SourceReference `json:"source"`
	Semantic     asset.Semantic        `json:"semantic"`
}

type Tariff struct {
	Mode          TariffMode            `json:"mode"`
	Currency      asset.Unit            `json:"currency,omitempty"`
	RatePerEnergy float64               `json:"rate_per_energy,omitempty"`
	EnergyUnit    asset.Unit            `json:"energy_unit,omitempty"`
	OwnerAssetID  uuid.UUID             `json:"owner_asset_id,omitempty"`
	Source        asset.SourceReference `json:"source,omitempty"`
}

type Config struct {
	AssetID       uuid.UUID `json:"asset_id"`
	LoggerID      uuid.UUID `json:"logger_id"`
	Timezone      string    `json:"timezone"`
	MaxGapSeconds int64     `json:"max_gap_seconds"`
	Mappings      []Mapping `json:"mappings"`
	Tariff        Tariff    `json:"tariff"`
}

type SourceDescriptor struct {
	Reference      asset.SourceReference
	OwnerAssetID   uuid.UUID
	OwnerAncestors []uuid.UUID
	DataType       string
	Unit           asset.Unit
	DynamicUnit    bool
}

type SourceCatalog interface {
	Describe(context.Context, uuid.UUID, asset.SourceReference) (SourceDescriptor, error)
}

func DecodeCompatibleConfig(version uint, raw json.RawMessage) (Config, error) {
	if version != ConfigVersionV1 {
		return Config{}, fmt.Errorf("%w: %d", ErrUnsupportedConfigVersion, version)
	}
	return DecodeConfigV1(raw)
}

func DecodeConfigV1(raw json.RawMessage) (Config, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Config{}, ErrInvalidConfiguration
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: multiple JSON documents", ErrInvalidConfiguration)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}

func (config Config) Validate() error {
	if config.AssetID == uuid.Nil || config.LoggerID == uuid.Nil || len(config.Mappings) == 0 || len(config.Mappings) > MaxMappings {
		return ErrInvalidConfiguration
	}
	if config.Timezone == "" || config.Timezone == "Local" || config.Timezone != strings.TrimSpace(config.Timezone) || len(config.Timezone) > 100 {
		return ErrInvalidConfiguration
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil || location.String() != config.Timezone {
		return ErrInvalidConfiguration
	}
	if config.MaxGapSeconds < 1 || config.MaxGapSeconds > int64(^uint64(0)>>1)/int64(time.Second) {
		return ErrInvalidConfiguration
	}
	keys := make(map[string]struct{}, len(config.Mappings))
	sources := make(map[string]struct{}, len(config.Mappings)+1)
	for _, mapping := range config.Mappings {
		if !mappingKeyPattern.MatchString(mapping.Key) || !validSlot(mapping.Slot) || mapping.OwnerAssetID == uuid.Nil || mapping.Source.Validate() != nil || mapping.Semantic.Validate() != nil || !slotAccepts(mapping.Slot, mapping.Semantic) {
			return ErrInvalidConfiguration
		}
		if _, exists := keys[mapping.Key]; exists {
			return ErrInvalidConfiguration
		}
		keys[mapping.Key] = struct{}{}
		if _, exists := sources[mapping.Source.Key()]; exists {
			return ErrInvalidConfiguration
		}
		sources[mapping.Source.Key()] = struct{}{}
	}
	if err := config.Tariff.validate(); err != nil {
		return err
	}
	if config.Tariff.Mode == TariffSource {
		if _, exists := sources[config.Tariff.Source.Key()]; exists {
			return ErrInvalidConfiguration
		}
	}
	return nil
}

func (tariff Tariff) validate() error {
	switch tariff.Mode {
	case TariffNone:
		if tariff.Currency != "" || tariff.RatePerEnergy != 0 || tariff.EnergyUnit != "" || tariff.OwnerAssetID != uuid.Nil || tariff.Source != (asset.SourceReference{}) {
			return ErrInvalidConfiguration
		}
		return nil
	case TariffFixed, TariffSource:
		if _, err := asset.LookupUnit(asset.QuantityCost, tariff.Currency); err != nil {
			return ErrInvalidConfiguration
		}
		if _, err := asset.LookupUnit(asset.QuantityEnergy, tariff.EnergyUnit); err != nil {
			return ErrInvalidConfiguration
		}
	default:
		return ErrInvalidConfiguration
	}
	if tariff.Mode == TariffFixed {
		if tariff.RatePerEnergy < 0 || math.IsNaN(tariff.RatePerEnergy) || math.IsInf(tariff.RatePerEnergy, 0) || tariff.OwnerAssetID != uuid.Nil || tariff.Source != (asset.SourceReference{}) {
			return ErrInvalidConfiguration
		}
		return nil
	}
	if tariff.RatePerEnergy != 0 || tariff.OwnerAssetID == uuid.Nil || tariff.Source.Validate() != nil {
		return ErrInvalidConfiguration
	}
	return nil
}

func ValidateSources(ctx context.Context, config Config, catalog SourceCatalog) error {
	if ctx == nil || isNil(catalog) {
		return ErrInvalidConfiguration
	}
	if err := config.Validate(); err != nil {
		return err
	}
	mappings := append([]Mapping(nil), config.Mappings...)
	for _, mapping := range mappings {
		descriptor, err := catalog.Describe(ctx, mapping.OwnerAssetID, mapping.Source)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrSourceUnavailable, mapping.Key)
		}
		if err := validateDescriptor(config.AssetID, mapping.OwnerAssetID, mapping.Source, mapping.Semantic, descriptor); err != nil {
			return fmt.Errorf("mapping %s: %w", mapping.Key, err)
		}
	}
	if config.Tariff.Mode == TariffSource {
		descriptor, err := catalog.Describe(ctx, config.Tariff.OwnerAssetID, config.Tariff.Source)
		if err != nil {
			return fmt.Errorf("%w: tariff", ErrSourceUnavailable)
		}
		if descriptor.Reference != config.Tariff.Source || descriptor.OwnerAssetID != config.Tariff.OwnerAssetID || !withinScope(config.AssetID, descriptor) || !numericDataType(descriptor.DataType) || descriptor.DynamicUnit || descriptor.Unit != tariffUnit(config.Tariff) {
			return fmt.Errorf("tariff: %w", ErrIncompatibleSource)
		}
	}
	return nil
}

func validateDescriptor(scopeID, ownerID uuid.UUID, reference asset.SourceReference, semantic asset.Semantic, descriptor SourceDescriptor) error {
	if descriptor.Reference != reference || descriptor.OwnerAssetID != ownerID {
		return ErrSourceUnavailable
	}
	if !withinScope(scopeID, descriptor) {
		return ErrSourceOutsideAssetScope
	}
	if semantic.Quantity == asset.QuantityState {
		if descriptor.DataType != "bool" || descriptor.Unit != asset.UnitBoolean || descriptor.DynamicUnit {
			return ErrIncompatibleSource
		}
		return nil
	}
	if !numericDataType(descriptor.DataType) || descriptor.DynamicUnit {
		return ErrIncompatibleSource
	}
	sourceSemantic := semantic
	sourceSemantic.Unit = descriptor.Unit
	if sourceSemantic.Validate() != nil {
		return ErrIncompatibleSource
	}
	if _, err := asset.Convert(1, sourceSemantic, semantic); err != nil {
		return ErrIncompatibleSource
	}
	return nil
}

func withinScope(scopeID uuid.UUID, descriptor SourceDescriptor) bool {
	if descriptor.OwnerAssetID == scopeID {
		return true
	}
	for _, ancestor := range descriptor.OwnerAncestors {
		if ancestor == scopeID {
			return true
		}
	}
	return false
}

func validSlot(slot SemanticSlot) bool {
	switch slot {
	case SlotElectricalPower, SlotThermalPower, SlotFlowRate, SlotAccumulatedVolume, SlotPressure, SlotTemperature, SlotOperatingState, SlotProductionState, SlotEmissions, SlotCustom, SlotCompressorPower, SlotCompressedAirFlow, SlotCompressedAirVolume, SlotCompressedAirPressure, SlotCompressedAirTemperature:
		return true
	default:
		return false
	}
}

func slotAccepts(slot SemanticSlot, semantic asset.Semantic) bool {
	switch slot {
	case SlotElectricalPower:
		return semantic.Resource == asset.ResourceElectricity && semantic.Quantity == asset.QuantityPower
	case SlotThermalPower:
		return semantic.Resource == asset.ResourceThermal && semantic.Quantity == asset.QuantityPower
	case SlotFlowRate:
		return semantic.Quantity == asset.QuantityFlowRate
	case SlotAccumulatedVolume:
		return semantic.Quantity == asset.QuantityVolume
	case SlotPressure:
		return semantic.Quantity == asset.QuantityPressure
	case SlotTemperature:
		return semantic.Quantity == asset.QuantityTemperature
	case SlotOperatingState, SlotProductionState:
		return semantic.Quantity == asset.QuantityState
	case SlotEmissions:
		return semantic.Quantity == asset.QuantityEmissions
	case SlotCustom:
		return semantic.Quantity == asset.QuantityCustom
	case SlotCompressorPower:
		return semantic.Resource == asset.ResourceCompressedAir && semantic.Quantity == asset.QuantityPower
	case SlotCompressedAirFlow:
		return semantic.Resource == asset.ResourceCompressedAir && semantic.Quantity == asset.QuantityFlowRate
	case SlotCompressedAirVolume:
		return semantic.Resource == asset.ResourceCompressedAir && semantic.Quantity == asset.QuantityVolume
	case SlotCompressedAirPressure:
		return semantic.Resource == asset.ResourceCompressedAir && semantic.Quantity == asset.QuantityPressure
	case SlotCompressedAirTemperature:
		return semantic.Resource == asset.ResourceCompressedAir && semantic.Quantity == asset.QuantityTemperature
	default:
		return false
	}
}

func tariffUnit(tariff Tariff) asset.Unit {
	return asset.Unit(string(tariff.Currency) + "/" + string(tariff.EnergyUnit))
}

func numericDataType(value string) bool {
	switch value {
	case "int16", "uint16", "int32", "uint32", "float32", "float64":
		return true
	default:
		return false
	}
}

func cloneConfig(config Config) Config {
	config.Mappings = append([]Mapping(nil), config.Mappings...)
	for index := range config.Mappings {
		config.Mappings[index].Semantic.Reference = cloneReference(config.Mappings[index].Semantic.Reference)
	}
	sort.SliceStable(config.Mappings, func(i, j int) bool { return config.Mappings[i].Key < config.Mappings[j].Key })
	return config
}

func cloneReference(reference asset.ReferenceCondition) asset.ReferenceCondition {
	if reference.TemperatureKelvin != nil {
		value := *reference.TemperatureKelvin
		reference.TemperatureKelvin = &value
	}
	if reference.PressurePascal != nil {
		value := *reference.PressurePascal
		reference.PressurePascal = &value
	}
	return reference
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
