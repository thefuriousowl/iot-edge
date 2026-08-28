package utility

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
)

func TestMigrateLegacyEnergyConfigV1PreservesMappingsAndFixedTariff(t *testing.T) {
	t.Parallel()
	scopeID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	electricalOwner, thermalOwner := uuid.New(), uuid.New()
	raw := legacyConfig(t, map[string]any{
		"logger_id": uuid.New(), "electrical_power_tags": []any{map[string]any{"tag_id": electricalID, "unit": "kW"}},
		"thermal_power_tags": []any{map[string]any{"tag_id": thermalID, "unit": "MW"}}, "timezone": "Asia/Bangkok", "max_gap_seconds": 90,
		"tariff": map[string]any{"currency": "THB", "rate_per_kwh": 4.25},
	})
	config, err := MigrateLegacyEnergyConfigV1(LegacyEnergyMigrationInput{AssetID: scopeID, TagOwners: map[uuid.UUID]uuid.UUID{electricalID: electricalOwner, thermalID: thermalOwner}, Configuration: raw})
	if err != nil {
		t.Fatal(err)
	}
	if config.AssetID != scopeID || config.MaxGapSeconds != 90 || len(config.Mappings) != 2 || config.Mappings[0].Slot != SlotElectricalPower || config.Mappings[0].OwnerAssetID != electricalOwner || config.Mappings[1].Slot != SlotThermalPower || config.Tariff.Mode != TariffFixed || config.Tariff.RatePerEnergy != 4.25 {
		t.Fatalf("migrated config = %#v", config)
	}
}

func TestMigrateLegacyEnergyConfigV1PreservesTagTariffWithoutGuessingOwner(t *testing.T) {
	t.Parallel()
	powerID, tariffID := uuid.New(), uuid.New()
	powerOwner, tariffOwner := uuid.New(), uuid.New()
	raw := legacyConfig(t, map[string]any{
		"logger_id": uuid.New(), "electrical_power_tags": []any{map[string]any{"tag_id": powerID, "unit": "W"}},
		"timezone": "UTC", "max_gap_seconds": 60, "tariff": map[string]any{"mode": "tag", "currency": "USD", "tag_id": tariffID},
	})
	input := LegacyEnergyMigrationInput{AssetID: uuid.New(), TagOwners: map[uuid.UUID]uuid.UUID{powerID: powerOwner, tariffID: tariffOwner}, Configuration: raw}
	config, err := MigrateLegacyEnergyConfigV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if config.Tariff.Mode != TariffSource || config.Tariff.OwnerAssetID != tariffOwner || config.Tariff.Source != asset.TagSource(tariffID) || config.Tariff.Currency != "USD" {
		t.Fatalf("migrated tariff = %#v", config.Tariff)
	}
	delete(input.TagOwners, tariffID)
	if _, err := MigrateLegacyEnergyConfigV1(input); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("missing tariff owner error = %v", err)
	}
}

func TestMigrateLegacyEnergyConfigV1FailsClosed(t *testing.T) {
	t.Parallel()
	tagID := uuid.New()
	valid := map[string]any{
		"logger_id": uuid.New(), "electrical_power_tags": []any{map[string]any{"tag_id": tagID, "unit": "kW"}},
		"timezone": "UTC", "max_gap_seconds": 60, "tariff": map[string]any{"currency": "THB", "rate_per_kwh": 4.0},
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"future field", func(value map[string]any) { value["future"] = true }},
		{"bad power unit", func(value map[string]any) {
			value["electrical_power_tags"] = []any{map[string]any{"tag_id": tagID, "unit": "kw"}}
		}},
		{"bad timezone", func(value map[string]any) { value["timezone"] = "Local" }},
		{"bad tariff mode", func(value map[string]any) { value["tariff"] = map[string]any{"mode": "future", "currency": "THB"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := cloneMap(valid)
			test.mutate(value)
			_, err := MigrateLegacyEnergyConfigV1(LegacyEnergyMigrationInput{AssetID: uuid.New(), TagOwners: map[uuid.UUID]uuid.UUID{tagID: uuid.New()}, Configuration: legacyConfig(t, value)})
			if err == nil {
				t.Fatal("migration unexpectedly succeeded")
			}
		})
	}
}

func legacyConfig(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cloneMap(value map[string]any) map[string]any {
	copy := make(map[string]any, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}
