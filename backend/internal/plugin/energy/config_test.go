package energy

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

func TestDecodeConfigAndPowerUnitNormalization(t *testing.T) {
	t.Parallel()
	loggerID, electricalID, thermalID := uuid.New(), uuid.New(), uuid.New()
	raw := encodeEnergyConfig(t, Config{
		LoggerID:            loggerID,
		ElectricalPowerTags: []PowerTag{{TagID: electricalID, Unit: PowerUnitW}},
		ThermalPowerTags:    []PowerTag{{TagID: thermalID, Unit: PowerUnitMW}},
		Timezone:            "Asia/Bangkok", MaxGapSeconds: 90,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4.25},
	})
	config, err := DecodeConfig(raw)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if config.LoggerID != loggerID || config.Timezone != "Asia/Bangkok" || config.MaxGapSeconds != 90 || config.Tariff.Currency != "THB" || config.Tariff.RatePerKWh != 4.25 || len(config.ElectricalPowerTags) != 1 || config.ElectricalPowerTags[0].TagID != electricalID || len(config.ThermalPowerTags) != 1 || config.ThermalPowerTags[0].TagID != thermalID {
		t.Errorf("decoded config = %#v", config)
	}
	tests := []struct {
		unit  PowerUnit
		value float64
		want  float64
	}{
		{unit: PowerUnitW, value: 1250, want: 1.25},
		{unit: PowerUnitKW, value: -2.5, want: -2.5},
		{unit: PowerUnitMW, value: 1.5, want: 1500},
	}
	for _, test := range tests {
		if got, err := test.unit.ToKilowatts(test.value); err != nil || got != test.want {
			t.Errorf("%s.ToKilowatts(%v) = %v, %v; want %v", test.unit, test.value, got, err, test.want)
		}
	}
	for _, test := range []struct {
		unit  PowerUnit
		value float64
	}{{unit: "kw", value: 1}, {unit: PowerUnitKW, value: math.NaN()}, {unit: PowerUnitMW, value: math.MaxFloat64}} {
		if _, err := test.unit.ToKilowatts(test.value); !errors.Is(err, ErrInvalidConfiguration) {
			t.Errorf("%s.ToKilowatts(%v) error = %v", test.unit, test.value, err)
		}
	}
}

func TestDecodeConfigSupportsTagTariffAndLegacyFlatTariff(t *testing.T) {
	t.Parallel()
	loggerID, powerID, tariffID := uuid.New(), uuid.New(), uuid.New()
	legacy, err := DecodeConfig(encodeEnergyConfig(t, Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4}}))
	if err != nil || legacy.Tariff.effectiveMode() != TariffModeFlat {
		t.Fatalf("legacy flat tariff = %#v, %v", legacy.Tariff, err)
	}
	dynamic, err := DecodeConfig(encodeEnergyConfig(t, Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID}}))
	if err != nil {
		t.Fatalf("DecodeConfig(tag tariff) error = %v", err)
	}
	if dynamic.Tariff.effectiveMode() != TariffModeTag || dynamic.Tariff.TagID != tariffID || dynamic.Tariff.RatePerKWh != 0 {
		t.Errorf("dynamic tariff = %#v", dynamic.Tariff)
	}
}

func TestDecodeConfigRejectsInvalidShapes(t *testing.T) {
	t.Parallel()
	loggerID, tagID := uuid.New(), uuid.New()
	valid := Config{
		LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: tagID, Unit: PowerUnitKW}},
		Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4},
	}
	tests := []struct {
		name string
		raw  plugin.Config
	}{
		{name: "empty", raw: nil},
		{name: "null", raw: plugin.Config(`null`)},
		{name: "array", raw: plugin.Config(`[]`)},
		{name: "malformed", raw: plugin.Config(`{"logger_id":`)},
		{name: "multiple documents", raw: append(encodeEnergyConfig(t, valid), []byte(` {}`)...)},
		{name: "unknown field", raw: withEnergyField(t, valid, "unknown", true)},
		{name: "nil logger", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.LoggerID = uuid.Nil }))},
		{name: "missing electrical", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.ElectricalPowerTags = nil }))},
		{name: "nil tag", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.ElectricalPowerTags[0].TagID = uuid.Nil }))},
		{name: "invalid unit", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.ElectricalPowerTags[0].Unit = "kw" }))},
		{name: "duplicate electrical", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) {
			config.ElectricalPowerTags = append(config.ElectricalPowerTags, config.ElectricalPowerTags[0])
		}))},
		{name: "duplicate across roles", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.ThermalPowerTags = []PowerTag{config.ElectricalPowerTags[0]} }))},
		{name: "blank timezone", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Timezone = "" }))},
		{name: "untrimmed timezone", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Timezone = " UTC " }))},
		{name: "machine local timezone", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Timezone = "Local" }))},
		{name: "unknown timezone", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Timezone = "Mars/Olympus" }))},
		{name: "zero gap", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.MaxGapSeconds = 0 }))},
		{name: "oversized gap", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.MaxGapSeconds = MaxGapSeconds + 1 }))},
		{name: "lowercase currency", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.Currency = "thb" }))},
		{name: "invalid currency", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.Currency = "US" }))},
		{name: "negative tariff", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.RatePerKWh = -1 }))},
		{name: "unknown tariff mode", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.Mode = "tou" }))},
		{name: "flat tariff with Tag", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.TagID = uuid.New() }))},
		{name: "tag tariff without Tag", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.Mode = TariffModeTag; config.Tariff.RatePerKWh = 0 }))},
		{name: "tag tariff with fixed rate", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) { config.Tariff.Mode = TariffModeTag; config.Tariff.TagID = uuid.New() }))},
		{name: "tag tariff duplicates power", raw: encodeEnergyConfig(t, mutateConfig(valid, func(config *Config) {
			config.Tariff.Mode = TariffModeTag
			config.Tariff.RatePerKWh = 0
			config.Tariff.TagID = config.ElectricalPowerTags[0].TagID
		}))},
	}
	tooMany := mutateConfig(valid, func(config *Config) {
		config.ElectricalPowerTags = make([]PowerTag, MaxPowerTags+1)
		for index := range config.ElectricalPowerTags {
			config.ElectricalPowerTags[index] = PowerTag{TagID: uuid.New(), Unit: PowerUnitKW}
		}
	})
	tests = append(tests, struct {
		name string
		raw  plugin.Config
	}{name: "too many Tags", raw: encodeEnergyConfig(t, tooMany)})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeConfig(test.raw); !errors.Is(err, ErrInvalidConfiguration) {
				t.Errorf("DecodeConfig() error = %v", err)
			}
		})
	}
}

func TestValidateLoggerMembershipRequiresSelectedNumericTags(t *testing.T) {
	t.Parallel()
	loggerID := uuid.New()
	numericTypes := []string{"int16", "uint16", "int32", "uint32", "float32", "float64"}
	logger := &datalogger.Logger{ID: loggerID, Tags: make([]datalogger.TagReference, 0, len(numericTypes)+1)}
	mappings := make([]PowerTag, 0, len(numericTypes))
	for index, dataType := range numericTypes {
		tagID := uuid.New()
		logger.Tags = append(logger.Tags, datalogger.TagReference{ID: tagID, DataType: dataType, Enabled: index%2 == 0})
		mappings = append(mappings, PowerTag{TagID: tagID, Unit: PowerUnitKW})
	}
	boolID := uuid.New()
	logger.Tags = append(logger.Tags, datalogger.TagReference{ID: boolID, DataType: "bool", Enabled: true})
	config := Config{LoggerID: loggerID, ElectricalPowerTags: mappings[:3], ThermalPowerTags: mappings[3:], Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4}}
	if err := ValidateLoggerMembership(config, logger); err != nil {
		t.Fatalf("ValidateLoggerMembership() error = %v", err)
	}
	if err := ValidateLoggerMembership(config, nil); !errors.Is(err, ErrLoggerUnavailable) {
		t.Errorf("ValidateLoggerMembership(nil) error = %v", err)
	}
	wrongLogger := *logger
	wrongLogger.ID = uuid.New()
	if err := ValidateLoggerMembership(config, &wrongLogger); !errors.Is(err, ErrLoggerUnavailable) {
		t.Errorf("ValidateLoggerMembership(wrong Logger) error = %v", err)
	}
	missing := config
	missing.ElectricalPowerTags = []PowerTag{{TagID: uuid.New(), Unit: PowerUnitKW}}
	if err := ValidateLoggerMembership(missing, logger); !errors.Is(err, ErrPowerTagNotSelected) {
		t.Errorf("ValidateLoggerMembership(missing Tag) error = %v", err)
	}
	nonNumeric := config
	nonNumeric.ElectricalPowerTags = []PowerTag{{TagID: boolID, Unit: PowerUnitKW}}
	nonNumeric.ThermalPowerTags = nil
	if err := ValidateLoggerMembership(nonNumeric, logger); !errors.Is(err, ErrPowerTagNotNumeric) {
		t.Errorf("ValidateLoggerMembership(bool Tag) error = %v", err)
	}
}

func TestValidateLoggerMembershipRequiresSelectedNumericTariffTag(t *testing.T) {
	t.Parallel()
	loggerID, powerID, tariffID, boolID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	logger := &datalogger.Logger{ID: loggerID, Tags: []datalogger.TagReference{{ID: powerID, DataType: "float64"}, {ID: tariffID, DataType: "float32"}, {ID: boolID, DataType: "bool"}}}
	config := Config{LoggerID: loggerID, ElectricalPowerTags: []PowerTag{{TagID: powerID, Unit: PowerUnitKW}}, Timezone: "UTC", MaxGapSeconds: 60, Tariff: FlatTariff{Mode: TariffModeTag, Currency: "THB", TagID: tariffID}}
	if err := ValidateLoggerMembership(config, logger); err != nil {
		t.Fatalf("ValidateLoggerMembership(tag tariff) error = %v", err)
	}
	missing := config
	missing.Tariff.TagID = uuid.New()
	if err := ValidateLoggerMembership(missing, logger); !errors.Is(err, ErrTariffTagNotSelected) {
		t.Errorf("missing tariff Tag error = %v", err)
	}
	nonNumeric := config
	nonNumeric.Tariff.TagID = boolID
	if err := ValidateLoggerMembership(nonNumeric, logger); !errors.Is(err, ErrTariffTagNotNumeric) {
		t.Errorf("non-numeric tariff Tag error = %v", err)
	}
}

func mutateConfig(source Config, mutate func(*Config)) Config {
	cloned := source
	cloned.ElectricalPowerTags = append([]PowerTag(nil), source.ElectricalPowerTags...)
	cloned.ThermalPowerTags = append([]PowerTag(nil), source.ThermalPowerTags...)
	mutate(&cloned)
	return cloned
}

func encodeEnergyConfig(t *testing.T, config Config) plugin.Config {
	t.Helper()
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encoding Energy config: %v", err)
	}
	return payload
}

func withEnergyField(t *testing.T, config Config, name string, value any) plugin.Config {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(encodeEnergyConfig(t, config), &object); err != nil {
		t.Fatalf("decoding Energy config fixture: %v", err)
	}
	object[name] = value
	payload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encoding Energy config fixture: %v", err)
	}
	return payload
}
