package energy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const (
	PowerUnitW     PowerUnit  = "W"
	PowerUnitKW    PowerUnit  = "kW"
	PowerUnitMW    PowerUnit  = "MW"
	TariffModeFlat TariffMode = "flat"
	TariffModeTag  TariffMode = "tag"

	MaxPowerTags  = 100
	MaxGapSeconds = int64(^uint64(0)>>1) / int64(time.Second)
)

var (
	ErrInvalidConfiguration = errors.New("invalid Energy configuration")
	ErrLoggerUnavailable    = errors.New("Energy Data Logger is unavailable")
	ErrPowerTagNotSelected  = errors.New("Energy power Tag is not selected by Data Logger")
	ErrPowerTagNotNumeric   = errors.New("Energy power Tag must be numeric")
	ErrTariffTagNotSelected = errors.New("Energy tariff Tag is not selected by Data Logger")
	ErrTariffTagNotNumeric  = errors.New("Energy tariff Tag must be numeric")

	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

type PowerUnit string
type TariffMode string

type PowerTag struct {
	TagID uuid.UUID `json:"tag_id"`
	Unit  PowerUnit `json:"unit"`
}

type FlatTariff struct {
	Mode       TariffMode `json:"mode,omitempty"`
	Currency   string     `json:"currency"`
	RatePerKWh float64    `json:"rate_per_kwh,omitempty"`
	TagID      uuid.UUID  `json:"tag_id,omitempty"`
}

type Config struct {
	LoggerID            uuid.UUID  `json:"logger_id"`
	ElectricalPowerTags []PowerTag `json:"electrical_power_tags"`
	ThermalPowerTags    []PowerTag `json:"thermal_power_tags,omitempty"`
	Timezone            string     `json:"timezone"`
	MaxGapSeconds       int64      `json:"max_gap_seconds"`
	Tariff              FlatTariff `json:"tariff"`
}

func DecodeConfig(raw plugin.Config) (Config, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return Config{}, ErrInvalidConfiguration
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: multiple JSON documents", ErrInvalidConfiguration)
	}
	if err := validateConfigShape(config); err != nil {
		return Config{}, err
	}
	config.ElectricalPowerTags = append([]PowerTag(nil), config.ElectricalPowerTags...)
	config.ThermalPowerTags = append([]PowerTag(nil), config.ThermalPowerTags...)
	return config, nil
}

func (unit PowerUnit) ToKilowatts(value float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrInvalidConfiguration
	}
	var normalized float64
	switch unit {
	case PowerUnitW:
		normalized = value / 1000
	case PowerUnitKW:
		normalized = value
	case PowerUnitMW:
		normalized = value * 1000
	default:
		return 0, ErrInvalidConfiguration
	}
	if math.IsNaN(normalized) || math.IsInf(normalized, 0) {
		return 0, ErrInvalidConfiguration
	}
	return normalized, nil
}

func ValidateLoggerMembership(config Config, logger *datalogger.Logger) error {
	if logger == nil || logger.ID != config.LoggerID {
		return ErrLoggerUnavailable
	}
	selected := make(map[uuid.UUID]datalogger.TagReference, len(logger.Tags))
	for _, reference := range logger.Tags {
		selected[reference.ID] = reference
	}
	for _, mapping := range appendPowerTags(config) {
		reference, exists := selected[mapping.TagID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrPowerTagNotSelected, mapping.TagID)
		}
		if !numericDataType(reference.DataType) {
			return fmt.Errorf("%w: %s", ErrPowerTagNotNumeric, mapping.TagID)
		}
	}
	if config.Tariff.effectiveMode() == TariffModeTag {
		reference, exists := selected[config.Tariff.TagID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrTariffTagNotSelected, config.Tariff.TagID)
		}
		if !numericDataType(reference.DataType) {
			return fmt.Errorf("%w: %s", ErrTariffTagNotNumeric, config.Tariff.TagID)
		}
	}
	return nil
}

func validateConfigShape(config Config) error {
	if config.LoggerID == uuid.Nil || len(config.ElectricalPowerTags) == 0 || len(config.ElectricalPowerTags)+len(config.ThermalPowerTags) > MaxPowerTags {
		return ErrInvalidConfiguration
	}
	if config.Timezone == "" || config.Timezone == "Local" || config.Timezone != strings.TrimSpace(config.Timezone) || len(config.Timezone) > 100 {
		return ErrInvalidConfiguration
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil || location.String() != config.Timezone {
		return ErrInvalidConfiguration
	}
	if config.MaxGapSeconds < 1 || config.MaxGapSeconds > MaxGapSeconds {
		return ErrInvalidConfiguration
	}
	if !currencyPattern.MatchString(config.Tariff.Currency) {
		return ErrInvalidConfiguration
	}
	switch config.Tariff.effectiveMode() {
	case TariffModeFlat:
		if config.Tariff.TagID != uuid.Nil || config.Tariff.RatePerKWh < 0 || math.IsNaN(config.Tariff.RatePerKWh) || math.IsInf(config.Tariff.RatePerKWh, 0) {
			return ErrInvalidConfiguration
		}
	case TariffModeTag:
		if config.Tariff.TagID == uuid.Nil || config.Tariff.RatePerKWh != 0 {
			return ErrInvalidConfiguration
		}
	default:
		return ErrInvalidConfiguration
	}
	seen := make(map[uuid.UUID]struct{}, len(config.ElectricalPowerTags)+len(config.ThermalPowerTags))
	for _, mapping := range appendPowerTags(config) {
		if mapping.TagID == uuid.Nil {
			return ErrInvalidConfiguration
		}
		if _, exists := seen[mapping.TagID]; exists {
			return ErrInvalidConfiguration
		}
		seen[mapping.TagID] = struct{}{}
		if _, err := mapping.Unit.ToKilowatts(1); err != nil {
			return err
		}
	}
	if config.Tariff.effectiveMode() == TariffModeTag {
		if _, exists := seen[config.Tariff.TagID]; exists {
			return ErrInvalidConfiguration
		}
	}
	return nil
}

func (tariff FlatTariff) effectiveMode() TariffMode {
	if tariff.Mode == "" {
		return TariffModeFlat
	}
	return tariff.Mode
}

func appendPowerTags(config Config) []PowerTag {
	mappings := make([]PowerTag, 0, len(config.ElectricalPowerTags)+len(config.ThermalPowerTags))
	mappings = append(mappings, config.ElectricalPowerTags...)
	mappings = append(mappings, config.ThermalPowerTags...)
	return mappings
}

func numericDataType(dataType string) bool {
	switch dataType {
	case "int16", "uint16", "int32", "uint32", "float32", "float64":
		return true
	default:
		return false
	}
}
