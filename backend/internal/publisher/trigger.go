package publisher

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	defaultPublisherInterval = time.Minute
	defaultPublisherCoalesce = 100 * time.Millisecond
	minPublisherInterval     = 100 * time.Millisecond
	maxPublisherInterval     = 24 * time.Hour
	minPublisherCoalesce     = time.Millisecond
	maxPublisherCoalesce     = time.Minute
)

type TriggerMode string

const (
	TriggerModeInterval TriggerMode = "interval"
	TriggerModeOnChange TriggerMode = "on_change"
)

type TriggerConfig struct {
	Mode        TriggerMode `json:"mode"`
	SourceAlias string      `json:"source_alias,omitempty"`
	IntervalMS  uint64      `json:"interval_ms,omitempty"`
	CoalesceMS  uint64      `json:"coalesce_ms,omitempty"`
}

type publisherConfig struct {
	Trigger TriggerConfig `json:"trigger"`
}

func normalizePublisherConfig(config Config) (Config, error) {
	decoded := publisherConfig{Trigger: defaultIntervalTrigger()}
	if len(config) != 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(config, &object); err != nil || object == nil {
			return nil, ErrInvalidPublisherConfig
		}
		decoder := json.NewDecoder(bytes.NewReader(config))
		decoder.DisallowUnknownFields()
		var input struct {
			Trigger *TriggerConfig `json:"trigger"`
		}
		if err := decoder.Decode(&input); err != nil {
			return nil, ErrInvalidPublisherConfig
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrInvalidPublisherConfig
		}
		if rawTrigger, exists := object["trigger"]; exists && bytes.Equal(bytes.TrimSpace(rawTrigger), []byte("null")) {
			return nil, ErrInvalidPublisherConfig
		}
		if input.Trigger != nil {
			decoded.Trigger = *input.Trigger
		}
	}

	trigger, err := normalizeTrigger(decoded.Trigger)
	if err != nil {
		return nil, err
	}
	decoded.Trigger = trigger
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, ErrInvalidPublisherConfig
	}
	return Config(normalized), nil
}

func ParseTriggerConfig(config Config) (TriggerConfig, error) {
	if len(config) == 0 {
		return defaultIntervalTrigger(), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil || object == nil {
		return TriggerConfig{}, ErrInvalidPublisherConfig
	}
	raw, exists := object["trigger"]
	if !exists {
		return defaultIntervalTrigger(), nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return TriggerConfig{}, ErrInvalidPublisherConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var trigger TriggerConfig
	if err := decoder.Decode(&trigger); err != nil {
		return TriggerConfig{}, ErrInvalidPublisherConfig
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return TriggerConfig{}, ErrInvalidPublisherConfig
	}
	return normalizeTrigger(trigger)
}

func (config TriggerConfig) Interval() time.Duration {
	return time.Duration(config.IntervalMS) * time.Millisecond
}

func (config TriggerConfig) Coalesce() time.Duration {
	return time.Duration(config.CoalesceMS) * time.Millisecond
}

func normalizeTrigger(trigger TriggerConfig) (TriggerConfig, error) {
	if trigger.Mode == "" {
		return defaultIntervalTrigger(), nil
	}
	switch trigger.Mode {
	case TriggerModeInterval:
		if trigger.SourceAlias != "" || trigger.CoalesceMS != 0 {
			return TriggerConfig{}, ErrInvalidPublisherConfig
		}
		if trigger.IntervalMS == 0 {
			trigger.IntervalMS = uint64(defaultPublisherInterval / time.Millisecond)
		}
		if trigger.Interval() < minPublisherInterval || trigger.Interval() > maxPublisherInterval {
			return TriggerConfig{}, ErrInvalidPublisherConfig
		}
	case TriggerModeOnChange:
		if !sourceAliasPattern.MatchString(trigger.SourceAlias) || trigger.IntervalMS != 0 {
			return TriggerConfig{}, ErrInvalidPublisherConfig
		}
		if trigger.CoalesceMS == 0 {
			trigger.CoalesceMS = uint64(defaultPublisherCoalesce / time.Millisecond)
		}
		if trigger.Coalesce() < minPublisherCoalesce || trigger.Coalesce() > maxPublisherCoalesce {
			return TriggerConfig{}, ErrInvalidPublisherConfig
		}
	default:
		return TriggerConfig{}, ErrInvalidPublisherConfig
	}
	return trigger, nil
}

func defaultIntervalTrigger() TriggerConfig {
	return TriggerConfig{Mode: TriggerModeInterval, IntervalMS: uint64(defaultPublisherInterval / time.Millisecond)}
}
