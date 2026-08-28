package utility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

type LegacyEnergyMigrationInput struct {
	AssetID       uuid.UUID
	TagOwners     map[uuid.UUID]uuid.UUID
	Configuration json.RawMessage
}

type legacyEnergyPowerTag struct {
	TagID uuid.UUID  `json:"tag_id"`
	Unit  asset.Unit `json:"unit"`
}

type legacyEnergyTariff struct {
	Mode       string     `json:"mode,omitempty"`
	Currency   asset.Unit `json:"currency"`
	RatePerKWh float64    `json:"rate_per_kwh,omitempty"`
	TagID      uuid.UUID  `json:"tag_id,omitempty"`
}

type legacyEnergyConfigV1 struct {
	LoggerID            uuid.UUID              `json:"logger_id"`
	ElectricalPowerTags []legacyEnergyPowerTag `json:"electrical_power_tags"`
	ThermalPowerTags    []legacyEnergyPowerTag `json:"thermal_power_tags,omitempty"`
	Timezone            string                 `json:"timezone"`
	MaxGapSeconds       int64                  `json:"max_gap_seconds"`
	Tariff              legacyEnergyTariff     `json:"tariff"`
}

func MigrateLegacyEnergyConfigV1(input LegacyEnergyMigrationInput) (Config, error) {
	if input.AssetID == uuid.Nil || len(input.TagOwners) == 0 {
		return Config{}, ErrInvalidConfiguration
	}
	var legacy legacyEnergyConfigV1
	decoder := json.NewDecoder(bytes.NewReader(input.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, ErrInvalidConfiguration
	}
	config := Config{AssetID: input.AssetID, LoggerID: legacy.LoggerID, Timezone: legacy.Timezone, MaxGapSeconds: legacy.MaxGapSeconds, Tariff: Tariff{Mode: TariffFixed, Currency: legacy.Tariff.Currency, EnergyUnit: asset.UnitKilowattHour, RatePerEnergy: legacy.Tariff.RatePerKWh}}
	appendTags := func(prefix string, slot SemanticSlot, resource asset.Resource, values []legacyEnergyPowerTag) error {
		for index, value := range values {
			ownerID, exists := input.TagOwners[value.TagID]
			if !exists || ownerID == uuid.Nil {
				return fmt.Errorf("%w: owner for Tag %s", ErrSourceUnavailable, value.TagID)
			}
			config.Mappings = append(config.Mappings, Mapping{Key: fmt.Sprintf("%s_%d", prefix, index+1), Slot: slot, OwnerAssetID: ownerID, Source: asset.TagSource(value.TagID), Semantic: asset.Semantic{Resource: resource, Quantity: asset.QuantityPower, Unit: value.Unit, Precision: 3}})
		}
		return nil
	}
	if err := appendTags("electrical_power", SlotElectricalPower, asset.ResourceElectricity, legacy.ElectricalPowerTags); err != nil {
		return Config{}, err
	}
	if err := appendTags("thermal_power", SlotThermalPower, asset.ResourceThermal, legacy.ThermalPowerTags); err != nil {
		return Config{}, err
	}
	mode := legacy.Tariff.Mode
	if mode == "" {
		mode = "flat"
	}
	if mode == "tag" {
		ownerID, exists := input.TagOwners[legacy.Tariff.TagID]
		if !exists || ownerID == uuid.Nil {
			return Config{}, fmt.Errorf("%w: tariff Tag owner", ErrSourceUnavailable)
		}
		config.Tariff = Tariff{Mode: TariffSource, Currency: legacy.Tariff.Currency, EnergyUnit: asset.UnitKilowattHour, OwnerAssetID: ownerID, Source: asset.TagSource(legacy.Tariff.TagID)}
	} else if mode != "flat" {
		return Config{}, ErrInvalidConfiguration
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}
