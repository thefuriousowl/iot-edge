package outputschema

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/utility"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

func TestSchemaV1RequiresCompleteUtilityProvenance(t *testing.T) {
	t.Parallel()
	config, mapping := validConfig()
	descriptors, err := Descriptors(config)
	if err != nil || len(descriptors) != 1 || descriptors[0].Key != "electrical_power" || descriptors[0].SchemaVersion != VersionV1 || !descriptors[0].DynamicUnit || descriptors[0].PeriodKind != plugin.OutputPeriodWindowed {
		t.Fatalf("descriptors=%#v err=%v", descriptors, err)
	}
	if _, err := CompatibleDescriptors(VersionV1+1, config); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("future schema version error=%v", err)
	}
	descriptors[0].Name = "mutated"
	again, _ := Descriptors(config)
	if again[0].Name != "electrical_power" {
		t.Fatal("descriptors alias caller state")
	}
	end := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	metric := analytics.Metric{From: end.Add(-time.Hour), To: end, Integral: 42.5, Covered: time.Hour, CoveragePercent: 100}
	semantic := asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityEnergy, Unit: asset.UnitKilowattHour, Precision: 3}
	value, err := Value(config, mapping, semantic, 42.5, metric, end)
	if err != nil || value.Quality != plugin.OutputQualityGood || value.Value != 42.5 || value.CoveragePercent == nil || *value.CoveragePercent != 100 || value.Attributes[AttributeAssetID] != mapping.OwnerAssetID.String() || value.Attributes[AttributeResource] != string(asset.ResourceElectricity) || value.Attributes[AttributeQuantity] != string(asset.QuantityEnergy) {
		t.Fatalf("value=%#v err=%v", value, err)
	}
	if err := ValidateValue(config, mapping, semantic, value); err != nil {
		t.Fatal(err)
	}
	value.Attributes[AttributeQuantity] = "temperature"
	if err := ValidateValue(config, mapping, semantic, value); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("tampered provenance error=%v", err)
	}
}

func TestSchemaV1MapsCoverageToQualityAndIssues(t *testing.T) {
	t.Parallel()
	config, mapping := validConfig()
	semantic := asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityEnergy, Unit: asset.UnitKilowattHour, Precision: 3}
	end := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	partial, err := Value(config, mapping, semantic, 20, analytics.Metric{From: end.Add(-time.Hour), To: end, Integral: 20, Covered: 30 * time.Minute, Skipped: 30 * time.Minute, CoveragePercent: 50}, end)
	if err != nil || partial.Quality != plugin.OutputQualityPartial || len(partial.Issues) != 1 || partial.Issues[0].Code != "partial_coverage" || partial.Issues[0].PeriodStart == nil {
		t.Fatalf("partial=%#v err=%v", partial, err)
	}
	bad, err := Value(config, mapping, semantic, 0, analytics.Metric{From: end.Add(-time.Hour), To: end, Skipped: time.Hour, CoveragePercent: 0}, end)
	if err != nil || bad.Quality != plugin.OutputQualityBad || bad.Value != nil || bad.Error == "" || len(bad.Issues) != 1 || bad.Issues[0].Code != "no_coverage" {
		t.Fatalf("bad=%#v err=%v", bad, err)
	}
}

func TestSchemaV1RejectsUnknownMappingAndIncompletePeriod(t *testing.T) {
	t.Parallel()
	config, mapping := validConfig()
	mapping.Key = "unknown"
	semantic := asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityEnergy, Unit: asset.UnitKilowattHour}
	if _, err := Value(config, mapping, semantic, 0, analytics.Metric{}, time.Now()); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("unknown mapping error=%v", err)
	}
}

func validConfig() (utility.Config, utility.Mapping) {
	boundary, owner := uuid.New(), uuid.New()
	mapping := utility.Mapping{Key: "electrical_power", Slot: utility.SlotElectricalPower, OwnerAssetID: owner, Source: asset.TagSource(uuid.New()), Semantic: asset.Semantic{Resource: asset.ResourceElectricity, Quantity: asset.QuantityPower, Unit: asset.UnitKilowatt, Precision: 3}}
	config := utility.Config{AssetID: boundary, LoggerID: uuid.New(), Timezone: "UTC", MaxGapSeconds: 60, Mappings: []utility.Mapping{mapping}, Tariff: utility.Tariff{Mode: utility.TariffNone}}
	return config, mapping
}
