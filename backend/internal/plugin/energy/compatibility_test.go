package energy

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestV1ConfigCompatibilityPreservesLegacyFlatAndTagTariffs(t *testing.T) {
	t.Parallel()
	loggerID, powerID, tariffID := uuid.New(), uuid.New(), uuid.New()
	tests := []Config{
		{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4}},
		{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitW}}, Timezone: "Asia/Bangkok", MaxGapSeconds: 120, Tariff: FlatTariff{Mode: TariffModeTag, Currency: "USD", TagID: tariffID}},
	}
	for _, want := range tests {
		got, err := DecodeCompatibleConfig(ConfigVersionV1, encodeEnergyConfig(t, want))
		if err != nil {
			t.Fatalf("DecodeCompatibleConfig(v1) error = %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("decoded config = %#v; want %#v", got, want)
		}
	}
}

func TestCompatibilityFailsClosedForUnknownVersions(t *testing.T) {
	t.Parallel()
	if _, err := DecodeCompatibleConfig(0, plugin.Config(`{}`)); !errors.Is(err, ErrUnsupportedConfigVersion) {
		t.Fatalf("config version error = %v", err)
	}
	if _, err := DecodeCompatibleConfig(2, plugin.Config(`{}`)); !errors.Is(err, ErrUnsupportedConfigVersion) {
		t.Fatalf("future config version error = %v", err)
	}
	if _, err := CompatibleOutputDescriptors(0); !errors.Is(err, ErrUnsupportedOutputVersion) {
		t.Fatalf("output version error = %v", err)
	}
	if _, err := CompatibleOutputDescriptors(2); !errors.Is(err, ErrUnsupportedOutputVersion) {
		t.Fatalf("future output version error = %v", err)
	}
}

func TestV1OutputCompatibilityPreservesEveryPublicKeyAndSchema(t *testing.T) {
	t.Parallel()
	descriptors, err := CompatibleOutputDescriptors(OutputSchemaVersionV1)
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []plugin.OutputKey{
		OutputElectricalDemandKW, OutputThermalOutputKW, OutputInstantaneousCOP, OutputTariffRate,
		OutputTodayElectricalEnergyKWh, OutputTodayThermalEnergyKWh, OutputTodayCOP, OutputTodayEstimatedCost,
		OutputTodayCoveredCost, OutputTodayElectricalCoverage, OutputTodayThermalCoverage,
		OutputMonthElectricalEnergyKWh, OutputMonthThermalEnergyKWh, OutputMonthCOP, OutputMonthEstimatedCost,
		OutputMonthCoveredCost, OutputMonthElectricalCoverage, OutputMonthThermalCoverage,
	}
	if len(descriptors) != len(wantKeys) {
		t.Fatalf("descriptor count = %d; want %d", len(descriptors), len(wantKeys))
	}
	for index, descriptor := range descriptors {
		if descriptor.Key != wantKeys[index] || descriptor.SchemaVersion != OutputSchemaVersionV1 || descriptor.DataType != plugin.OutputDataTypeFloat64 {
			t.Errorf("descriptor[%d] = %#v", index, descriptor)
		}
	}
	descriptors[0].Key = "mutated"
	again, _ := CompatibleOutputDescriptors(OutputSchemaVersionV1)
	if again[0].Key != OutputElectricalDemandKW {
		t.Fatal("compatibility catalog was not defensively copied")
	}
}
