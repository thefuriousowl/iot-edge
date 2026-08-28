package thermal

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

var ErrInvalidInput = errors.New("invalid thermal utility calculation input")

type Mode string
type Direction string

const (
	ModeDirect  Mode = "direct_power"
	ModeDerived Mode = "flow_delta_t"

	DirectionSupplyHotter Direction = "supply_hotter"
	DirectionReturnHotter Direction = "return_hotter"
)

type Input struct {
	Mode              Mode
	DirectPowerKW     []analytics.Sample
	Flow              []analytics.Sample
	SupplyTemperature []analytics.Sample
	ReturnTemperature []analytics.Sample
	MediumFactor      float64
	Direction         Direction
	ElectricalPowerKW []analytics.Sample
	Window            analytics.Window
	MaxGap            time.Duration
}

type Result struct {
	From                time.Time
	To                  time.Time
	ThermalEnergyKWh    float64
	ElectricalEnergyKWh float64
	COP                 float64
	COPValid            bool
	COPError            string
	Covered             time.Duration
	Skipped             time.Duration
	CoveragePercent     float64
	Segments            int
	SkippedSegments     int
}

type Comparison struct {
	Current                    Result
	Previous                   Result
	ThermalEnergyDeltaKWh      float64
	ThermalEnergyPercentChange *float64
	COPDelta                   float64
}

func Calculate(input Input) (Result, error) {
	if input.Window.From.IsZero() || !input.Window.From.Before(input.Window.To) || input.MaxGap <= 0 {
		return Result{}, ErrInvalidInput
	}
	thermalPower, err := thermalSamples(input)
	if err != nil {
		return Result{}, err
	}
	thermalPower, electrical, err := synchronized(thermalPower, input.ElectricalPowerKW)
	if err != nil {
		return Result{}, err
	}
	thermalMetric, err := analytics.Integrate(thermalPower, input.Window, input.MaxGap)
	if err != nil {
		return Result{}, ErrInvalidInput
	}
	electricalMetric, err := analytics.Integrate(electrical, input.Window, input.MaxGap)
	if err != nil {
		return Result{}, ErrInvalidInput
	}
	result := Result{From: thermalMetric.From, To: thermalMetric.To, ThermalEnergyKWh: thermalMetric.Integral, ElectricalEnergyKWh: electricalMetric.Integral, Covered: thermalMetric.Covered, Skipped: thermalMetric.Skipped, CoveragePercent: thermalMetric.CoveragePercent, Segments: thermalMetric.Segments, SkippedSegments: thermalMetric.SkippedSegments}
	switch {
	case thermalMetric.Covered == 0:
		result.COPError = "thermal and electrical energy have no shared coverage"
	case electricalMetric.Integral <= 0:
		result.COPError = "electrical energy denominator must be positive"
	default:
		result.COP = thermalMetric.Integral / electricalMetric.Integral
		if !finite(result.COP) {
			result.COP = 0
			result.COPError = "COP is not finite"
		} else {
			result.COPValid = true
		}
	}
	return result, nil
}

func Compare(input Input) (Comparison, error) {
	duration := input.Window.To.Sub(input.Window.From)
	current, err := Calculate(input)
	if err != nil {
		return Comparison{}, err
	}
	input.Window = analytics.Window{From: input.Window.From.Add(-duration), To: input.Window.From}
	previous, err := Calculate(input)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{Current: current, Previous: previous, ThermalEnergyDeltaKWh: current.ThermalEnergyKWh - previous.ThermalEnergyKWh}
	if previous.ThermalEnergyKWh != 0 {
		value := result.ThermalEnergyDeltaKWh / previous.ThermalEnergyKWh * 100
		result.ThermalEnergyPercentChange = &value
	}
	if current.COPValid && previous.COPValid {
		result.COPDelta = current.COP - previous.COP
	}
	return result, nil
}

func thermalSamples(input Input) ([]analytics.Sample, error) {
	switch input.Mode {
	case ModeDirect:
		if len(input.DirectPowerKW) == 0 || len(input.Flow) != 0 || len(input.SupplyTemperature) != 0 || len(input.ReturnTemperature) != 0 || input.MediumFactor != 0 || input.Direction != "" {
			return nil, ErrInvalidInput
		}
		return sorted(input.DirectPowerKW), nil
	case ModeDerived:
		if len(input.DirectPowerKW) != 0 || len(input.Flow) == 0 || len(input.Flow) != len(input.SupplyTemperature) || len(input.Flow) != len(input.ReturnTemperature) || input.MediumFactor <= 0 || !finite(input.MediumFactor) || (input.Direction != DirectionSupplyHotter && input.Direction != DirectionReturnHotter) {
			return nil, ErrInvalidInput
		}
		flow, supply, ret := sorted(input.Flow), sorted(input.SupplyTemperature), sorted(input.ReturnTemperature)
		result := make([]analytics.Sample, len(flow))
		for i := range flow {
			if !flow[i].At.Equal(supply[i].At) || !flow[i].At.Equal(ret[i].At) {
				return nil, ErrInvalidInput
			}
			quality := analytics.QualityGood
			delta := supply[i].Value - ret[i].Value
			if input.Direction == DirectionReturnHotter {
				delta = -delta
			}
			if flow[i].Quality != analytics.QualityGood || supply[i].Quality != analytics.QualityGood || ret[i].Quality != analytics.QualityGood || flow[i].Value < 0 || delta < 0 {
				quality = analytics.QualityBad
			}
			value := flow[i].Value * input.MediumFactor * delta
			if !finite(value) {
				quality, value = analytics.QualityBad, 0
			}
			result[i] = analytics.Sample{At: flow[i].At, Value: value, Quality: quality}
		}
		return result, nil
	default:
		return nil, ErrInvalidInput
	}
}

func synchronized(thermal, electrical []analytics.Sample) ([]analytics.Sample, []analytics.Sample, error) {
	thermal, electrical = sorted(thermal), sorted(electrical)
	if len(thermal) == 0 || len(thermal) != len(electrical) {
		return nil, nil, ErrInvalidInput
	}
	for i := range thermal {
		if !thermal[i].At.Equal(electrical[i].At) {
			return nil, nil, ErrInvalidInput
		}
		if thermal[i].Quality != analytics.QualityGood || electrical[i].Quality != analytics.QualityGood {
			thermal[i].Quality, electrical[i].Quality = analytics.QualityBad, analytics.QualityBad
		}
	}
	return thermal, electrical, nil
}

func sorted(values []analytics.Sample) []analytics.Sample {
	result := append([]analytics.Sample(nil), values...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
