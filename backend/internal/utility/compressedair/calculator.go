package compressedair

import (
	"math"
	"sort"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

type ConsumptionMode string

const (
	ConsumptionFlow    ConsumptionMode = "flow"
	ConsumptionCounter ConsumptionMode = "counter"
)

type CalculationInput struct {
	PowerKW         []analytics.Sample
	Mode            ConsumptionMode
	FlowNm3PerHour  []analytics.Sample
	CounterNm3      []analytics.Sample
	PressureBar     []analytics.Sample
	OperatingState  []analytics.Sample
	ProductionState []analytics.Sample
	Currency        string
	RatePerKWh      float64
	Window          analytics.Window
	MaxGap          time.Duration
}

type Ratio struct {
	Value float64
	Valid bool
	Error string
}

type PressureMetrics struct {
	AverageBar      float64
	StdDevBar       float64
	DropBar         float64
	CoveragePercent float64
	Valid           bool
	Error           string
}

type CalculationResult struct {
	From            time.Time
	To              time.Time
	EnergyKWh       float64
	VolumeNm3       float64
	SEC             Ratio
	Cost            float64
	CostPerNm3      Ratio
	RuntimeRatio    Ratio
	LoadRatio       Ratio
	Pressure        PressureMetrics
	Covered         time.Duration
	Skipped         time.Duration
	CoveragePercent float64
	Segments        int
	SkippedSegments int
	Currency        string
}

type CalculationComparison struct {
	Current        CalculationResult
	Previous       CalculationResult
	EnergyDeltaKWh float64
	VolumeDeltaNm3 float64
	SECDelta       float64
	CostDelta      float64
}

func Calculate(input CalculationInput) (CalculationResult, error) {
	if input.Window.From.IsZero() || !input.Window.From.Before(input.Window.To) || input.MaxGap <= 0 || input.Currency == "" || input.RatePerKWh < 0 || !finite(input.RatePerKWh) {
		return CalculationResult{}, ErrInvalidInputs
	}
	consumption, err := consumptionSamples(input)
	if err != nil {
		return CalculationResult{}, err
	}
	power, consumption, err := synchronizedSamples(input.PowerKW, consumption)
	if err != nil {
		return CalculationResult{}, err
	}
	if !nonnegative(power) || !nonnegative(consumption) {
		return CalculationResult{}, ErrInvalidInputs
	}
	powerMetric, err := analytics.Integrate(power, input.Window, input.MaxGap)
	if err != nil {
		return CalculationResult{}, ErrInvalidInputs
	}
	var volumeMetric analytics.Metric
	if input.Mode == ConsumptionFlow {
		volumeMetric, err = analytics.Integrate(consumption, input.Window, input.MaxGap)
	} else {
		volumeMetric, err = counterDelta(consumption, input.Window, input.MaxGap)
	}
	if err != nil {
		return CalculationResult{}, ErrInvalidInputs
	}
	result := CalculationResult{From: powerMetric.From, To: powerMetric.To, EnergyKWh: powerMetric.Integral, VolumeNm3: volumeMetric.Integral, Cost: powerMetric.Integral * input.RatePerKWh, Covered: powerMetric.Covered, Skipped: powerMetric.Skipped, CoveragePercent: powerMetric.CoveragePercent, Segments: powerMetric.Segments, SkippedSegments: powerMetric.SkippedSegments, Currency: input.Currency}
	if volumeMetric.Covered < result.Covered {
		result.Covered = volumeMetric.Covered
		result.Skipped = result.To.Sub(result.From) - result.Covered
		result.CoveragePercent = float64(result.Covered) / float64(result.To.Sub(result.From)) * 100
	}
	if volumeMetric.SkippedSegments > result.SkippedSegments {
		result.SkippedSegments = volumeMetric.SkippedSegments
	}
	if powerMetric.Covered != volumeMetric.Covered {
		result.SEC.Error, result.CostPerNm3.Error = "power and volume coverage differ", "power and volume coverage differ"
	} else if result.VolumeNm3 <= 0 {
		result.SEC.Error, result.CostPerNm3.Error = "normalized volume must be positive", "normalized volume must be positive"
	} else {
		result.SEC = Ratio{Value: result.EnergyKWh / result.VolumeNm3, Valid: true}
		result.CostPerNm3 = Ratio{Value: result.Cost / result.VolumeNm3, Valid: true}
	}
	result.RuntimeRatio = stateRatio(input.OperatingState, input.Window, input.MaxGap)
	production := stateRatio(input.ProductionState, input.Window, input.MaxGap)
	if production.Valid && result.RuntimeRatio.Valid && result.RuntimeRatio.Value > 0 && production.Value <= result.RuntimeRatio.Value {
		result.LoadRatio = Ratio{Value: production.Value / result.RuntimeRatio.Value, Valid: true}
	} else if len(input.ProductionState) > 0 {
		result.LoadRatio.Error = "production and operating state coverage is incomplete"
	}
	result.Pressure = pressureMetrics(input.PressureBar, input.Window, input.MaxGap)
	if !finite(result.EnergyKWh) || !finite(result.VolumeNm3) || !finite(result.Cost) || result.SEC.Valid && !finite(result.SEC.Value) || result.CostPerNm3.Valid && !finite(result.CostPerNm3.Value) {
		return CalculationResult{}, ErrInvalidInputs
	}
	return result, nil
}

func CompareCalculation(input CalculationInput) (CalculationComparison, error) {
	duration := input.Window.To.Sub(input.Window.From)
	current, err := Calculate(input)
	if err != nil {
		return CalculationComparison{}, err
	}
	input.Window = analytics.Window{From: input.Window.From.Add(-duration), To: input.Window.From}
	previous, err := Calculate(input)
	if err != nil {
		return CalculationComparison{}, err
	}
	result := CalculationComparison{Current: current, Previous: previous, EnergyDeltaKWh: current.EnergyKWh - previous.EnergyKWh, VolumeDeltaNm3: current.VolumeNm3 - previous.VolumeNm3, CostDelta: current.Cost - previous.Cost}
	if current.SEC.Valid && previous.SEC.Valid {
		result.SECDelta = current.SEC.Value - previous.SEC.Value
	}
	return result, nil
}

func consumptionSamples(input CalculationInput) ([]analytics.Sample, error) {
	switch input.Mode {
	case ConsumptionFlow:
		if len(input.FlowNm3PerHour) == 0 || len(input.CounterNm3) != 0 {
			return nil, ErrInvalidInputs
		}
		return input.FlowNm3PerHour, nil
	case ConsumptionCounter:
		if len(input.CounterNm3) == 0 || len(input.FlowNm3PerHour) != 0 {
			return nil, ErrInvalidInputs
		}
		return input.CounterNm3, nil
	default:
		return nil, ErrInvalidInputs
	}
}

func synchronizedSamples(left, right []analytics.Sample) ([]analytics.Sample, []analytics.Sample, error) {
	left, right = sortedSamples(left), sortedSamples(right)
	if len(left) < 2 || len(left) != len(right) {
		return nil, nil, ErrInvalidInputs
	}
	for i := range left {
		if !left[i].At.Equal(right[i].At) {
			return nil, nil, ErrInvalidInputs
		}
		if left[i].Quality != analytics.QualityGood || right[i].Quality != analytics.QualityGood {
			left[i].Quality, right[i].Quality = analytics.QualityBad, analytics.QualityBad
		}
	}
	return left, right, nil
}

func counterDelta(samples []analytics.Sample, window analytics.Window, maxGap time.Duration) (analytics.Metric, error) {
	samples = sortedSamples(samples)
	result := analytics.Metric{From: window.From.UTC(), To: window.To.UTC(), Skipped: window.To.Sub(window.From)}
	for i := 1; i < len(samples); i++ {
		left, right := samples[i-1], samples[i]
		from, to := laterTime(window.From.UTC(), left.At.UTC()), earlierTime(window.To.UTC(), right.At.UTC())
		if !from.Before(to) {
			continue
		}
		result.Segments++
		span := right.At.Sub(left.At)
		if span <= 0 {
			return analytics.Metric{}, ErrInvalidInputs
		}
		if span > maxGap || left.Quality != analytics.QualityGood || right.Quality != analytics.QualityGood || right.Value < left.Value {
			result.SkippedSegments++
			continue
		}
		start := interpolateValue(left, right, from)
		end := interpolateValue(left, right, to)
		result.Integral += end - start
		result.Covered += to.Sub(from)
	}
	result.Skipped = result.To.Sub(result.From) - result.Covered
	result.CoveragePercent = float64(result.Covered) / float64(result.To.Sub(result.From)) * 100
	return result, nil
}

func stateRatio(samples []analytics.Sample, window analytics.Window, maxGap time.Duration) Ratio {
	if len(samples) == 0 {
		return Ratio{}
	}
	for _, sample := range samples {
		if sample.Value != 0 && sample.Value != 1 {
			return Ratio{Error: "state values must be zero or one"}
		}
	}
	metric, err := analytics.Integrate(samples, window, maxGap)
	if err != nil || metric.Covered != window.To.Sub(window.From) {
		return Ratio{Error: "state coverage is incomplete"}
	}
	value := metric.Integral / metric.Covered.Hours()
	if value < 0 || value > 1 || !finite(value) {
		return Ratio{Error: "state values must be within zero and one"}
	}
	return Ratio{Value: value, Valid: true}
}

func pressureMetrics(samples []analytics.Sample, window analytics.Window, maxGap time.Duration) PressureMetrics {
	if len(samples) == 0 {
		return PressureMetrics{}
	}
	metric, err := analytics.Integrate(samples, window, maxGap)
	if err != nil || metric.Covered == 0 {
		return PressureMetrics{Error: "pressure has no coverage"}
	}
	average := metric.Integral / metric.Covered.Hours()
	values := clippedValues(samples, window, maxGap)
	if len(values) == 0 {
		return PressureMetrics{Error: "pressure has no covered values"}
	}
	minimum, maximum, sumSquares := values[0], values[0], 0.0
	for _, value := range values {
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
		difference := value - average
		sumSquares += difference * difference
	}
	return PressureMetrics{AverageBar: average, StdDevBar: math.Sqrt(sumSquares / float64(len(values))), DropBar: maximum - minimum, CoveragePercent: metric.CoveragePercent, Valid: true}
}

func clippedValues(samples []analytics.Sample, window analytics.Window, maxGap time.Duration) []float64 {
	samples = sortedSamples(samples)
	result := []float64{}
	for i := 1; i < len(samples); i++ {
		left, right := samples[i-1], samples[i]
		if right.At.Sub(left.At) > maxGap || left.Quality != analytics.QualityGood || right.Quality != analytics.QualityGood {
			continue
		}
		from, to := laterTime(window.From.UTC(), left.At.UTC()), earlierTime(window.To.UTC(), right.At.UTC())
		if from.Before(to) {
			result = append(result, interpolateValue(left, right, from), interpolateValue(left, right, to))
		}
	}
	return result
}

func sortedSamples(values []analytics.Sample) []analytics.Sample {
	result := append([]analytics.Sample(nil), values...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result
}
func interpolateValue(left, right analytics.Sample, at time.Time) float64 {
	ratio := float64(at.Sub(left.At)) / float64(right.At.Sub(left.At))
	return left.Value + (right.Value-left.Value)*ratio
}
func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func earlierTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func nonnegative(samples []analytics.Sample) bool {
	for _, sample := range samples {
		if sample.Quality == analytics.QualityGood && (sample.Value < 0 || !finite(sample.Value)) {
			return false
		}
	}
	return true
}
