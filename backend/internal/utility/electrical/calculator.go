package electrical

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

var ErrInvalidInput = errors.New("invalid electrical utility calculation input")

type TariffMode string

const (
	TariffFixed  TariffMode = "fixed"
	TariffSource TariffMode = "source"
)

type Tariff struct {
	Mode      TariffMode
	Currency  string
	FixedRate float64
	Samples   []analytics.Sample
}

type Input struct {
	PowerKW []analytics.Sample
	Window  analytics.Window
	MaxGap  time.Duration
	Tariff  Tariff
}

type Result struct {
	From            time.Time
	To              time.Time
	EnergyKWh       float64
	DemandKW        float64
	Cost            float64
	Currency        string
	Covered         time.Duration
	Skipped         time.Duration
	CoveragePercent float64
	Segments        int
	SkippedSegments int
}

type Comparison struct {
	Current             Result
	Previous            Result
	EnergyDeltaKWh      float64
	EnergyPercentChange *float64
	CostDelta           float64
	CostPercentChange   *float64
}

func Calculate(input Input) (Result, error) {
	if input.Window.From.IsZero() || !input.Window.From.Before(input.Window.To) || input.MaxGap <= 0 || input.Tariff.Currency == "" || (input.Tariff.Mode != TariffFixed && input.Tariff.Mode != TariffSource) || input.Tariff.FixedRate < 0 || math.IsNaN(input.Tariff.FixedRate) || math.IsInf(input.Tariff.FixedRate, 0) {
		return Result{}, ErrInvalidInput
	}
	metric, err := analytics.Integrate(input.PowerKW, input.Window, input.MaxGap)
	if err != nil {
		return Result{}, ErrInvalidInput
	}
	result := Result{From: metric.From, To: metric.To, EnergyKWh: metric.Integral, Currency: input.Tariff.Currency, Covered: metric.Covered, Skipped: metric.Skipped, CoveragePercent: metric.CoveragePercent, Segments: metric.Segments, SkippedSegments: metric.SkippedSegments}
	result.DemandKW = peak(input.PowerKW, input.Window, input.MaxGap)
	switch input.Tariff.Mode {
	case TariffFixed:
		result.Cost = result.EnergyKWh * input.Tariff.FixedRate
	case TariffSource:
		cost, covered, err := sourceCost(input.PowerKW, input.Tariff.Samples, input.Window, input.MaxGap)
		if err != nil || covered != metric.Covered {
			return Result{}, ErrInvalidInput
		}
		result.Cost = cost
	}
	if !finite(result.EnergyKWh) || !finite(result.DemandKW) || !finite(result.Cost) {
		return Result{}, ErrInvalidInput
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
	result := Comparison{Current: current, Previous: previous, EnergyDeltaKWh: current.EnergyKWh - previous.EnergyKWh, CostDelta: current.Cost - previous.Cost}
	if previous.EnergyKWh != 0 {
		value := result.EnergyDeltaKWh / previous.EnergyKWh * 100
		result.EnergyPercentChange = &value
	}
	if previous.Cost != 0 {
		value := result.CostDelta / previous.Cost * 100
		result.CostPercentChange = &value
	}
	return result, nil
}

func sourceCost(power, tariff []analytics.Sample, window analytics.Window, maxGap time.Duration) (float64, time.Duration, error) {
	power = sorted(power)
	tariff = sorted(tariff)
	if len(power) != len(tariff) {
		return 0, 0, ErrInvalidInput
	}
	var cost float64
	var covered time.Duration
	for i := 1; i < len(power); i++ {
		if !power[i].At.Equal(tariff[i].At) || !power[i-1].At.Equal(tariff[i-1].At) || power[i].At.Sub(power[i-1].At) > maxGap || tariff[i].At.Sub(tariff[i-1].At) > maxGap || power[i-1].Quality != analytics.QualityGood || power[i].Quality != analytics.QualityGood || tariff[i-1].Quality != analytics.QualityGood || tariff[i-1].Value < 0 || !finite(tariff[i-1].Value) {
			continue
		}
		from, to := later(window.From.UTC(), power[i-1].At.UTC()), earlier(window.To.UTC(), power[i].At.UTC())
		if !from.Before(to) {
			continue
		}
		span := power[i].At.Sub(power[i-1].At)
		startRatio := float64(from.Sub(power[i-1].At)) / float64(span)
		endRatio := float64(to.Sub(power[i-1].At)) / float64(span)
		start := power[i-1].Value + (power[i].Value-power[i-1].Value)*startRatio
		end := power[i-1].Value + (power[i].Value-power[i-1].Value)*endRatio
		energy := (start + end) / 2 * to.Sub(from).Hours()
		cost += energy * tariff[i-1].Value
		covered += to.Sub(from)
	}
	if !finite(cost) {
		return 0, 0, ErrInvalidInput
	}
	return cost, covered, nil
}

func peak(samples []analytics.Sample, window analytics.Window, maxGap time.Duration) float64 {
	samples = sorted(samples)
	maximum := 0.0
	for i := 1; i < len(samples); i++ {
		left, right := samples[i-1], samples[i]
		if left.Quality != analytics.QualityGood || right.Quality != analytics.QualityGood || right.At.Sub(left.At) > maxGap {
			continue
		}
		from, to := later(window.From.UTC(), left.At.UTC()), earlier(window.To.UTC(), right.At.UTC())
		if !from.Before(to) {
			continue
		}
		span := right.At.Sub(left.At)
		start := left.Value + (right.Value-left.Value)*float64(from.Sub(left.At))/float64(span)
		end := left.Value + (right.Value-left.Value)*float64(to.Sub(left.At))/float64(span)
		if start > maximum {
			maximum = start
		}
		if end > maximum {
			maximum = end
		}
	}
	return maximum
}

func sorted(values []analytics.Sample) []analytics.Sample {
	result := append([]analytics.Sample(nil), values...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.Before(result[j].At) })
	return result
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
