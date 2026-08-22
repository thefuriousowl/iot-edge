package tag

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const LinearTransformType = "linear"

var (
	ErrInvalidTransformConfig = errors.New("invalid tag transform config")
	ErrUnsupportedTransform   = errors.New("unsupported tag transform")
)

type linearTransformConfigInput struct {
	Gain   *float64 `json:"gain"`
	Offset *float64 `json:"offset"`
}

type linearTransformConfig struct {
	Gain   float64 `json:"gain"`
	Offset float64 `json:"offset"`
}

func normalizeLinearTransform(raw Config) (Config, error) {
	var input linearTransformConfigInput
	if err := decodeStrictJSON(raw, &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransformConfig, err)
	}
	if input.Gain == nil {
		return nil, fmt.Errorf("%w: gain is required", ErrInvalidTransformConfig)
	}
	config := linearTransformConfig{Gain: *input.Gain}
	if input.Offset != nil {
		config.Offset = *input.Offset
	}
	if !finite(config.Gain) || !finite(config.Offset) {
		return nil, fmt.Errorf("%w: gain and offset must be finite", ErrInvalidTransformConfig)
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransformConfig, err)
	}
	return canonical, nil
}

func applyLinearTransform(value any, raw Config) (any, error) {
	number, ok := expressionNumber(value)
	if !ok || !finite(number) {
		return nil, fmt.Errorf("%w: linear transform requires a numeric value", ErrInvalidTransformConfig)
	}
	canonical, err := normalizeLinearTransform(raw)
	if err != nil {
		return nil, err
	}
	var config linearTransformConfig
	if err := json.Unmarshal(canonical, &config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransformConfig, err)
	}
	result := number*config.Gain + config.Offset
	if !finite(result) {
		return nil, fmt.Errorf("%w: transformed value must be finite", ErrInvalidTransformConfig)
	}
	return result, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
