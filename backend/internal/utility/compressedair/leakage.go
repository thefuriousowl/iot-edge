package compressedair

import (
	"math"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

type BaselineBasis string

const (
	BaselineIdle         BaselineBasis = "idle"
	BaselineNoProduction BaselineBasis = "no_production"
)

// LeakageInput represents an explicitly selected non-production observation.
// It estimates the cost of observed baseline demand; it does not diagnose a leak.
type LeakageInput struct {
	Basis          BaselineBasis
	FlowNm3PerHour []analytics.Sample
	PowerKW        []analytics.Sample
	Window         analytics.Window
	MaxGap         time.Duration
	RatePerKWh     float64
	Currency       string
}

type LeakageEstimate struct {
	From                        time.Time
	To                          time.Time
	Basis                       BaselineBasis
	EstimatedFlowNm3PerHour     float64
	EstimatedVolumeNm3          float64
	EstimatedEnergyKWh          float64
	EstimatedCost               float64
	CoveragePercent             float64
	ConfidencePercent           float64
	Covered                     time.Duration
	Skipped                     time.Duration
	Currency                    string
	AutomaticLeakClaimSupported bool
}

func EstimateLeakage(input LeakageInput) (LeakageEstimate, error) {
	if input.Basis != BaselineIdle && input.Basis != BaselineNoProduction || input.Window.From.IsZero() || !input.Window.From.Before(input.Window.To) || input.MaxGap <= 0 || input.Currency == "" || input.RatePerKWh < 0 || !finite(input.RatePerKWh) {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	flow, power, err := synchronizedSamples(input.FlowNm3PerHour, input.PowerKW)
	if err != nil || !nonnegative(flow) || !nonnegative(power) {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	flowMetric, err := analytics.Integrate(flow, input.Window, input.MaxGap)
	if err != nil {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	powerMetric, err := analytics.Integrate(power, input.Window, input.MaxGap)
	if err != nil {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	covered := flowMetric.Covered
	if powerMetric.Covered < covered {
		covered = powerMetric.Covered
	}
	duration := input.Window.To.Sub(input.Window.From)
	if covered <= 0 || flowMetric.Covered != powerMetric.Covered {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	coverage := float64(covered) / float64(duration) * 100
	// Confidence is deliberately bounded by observable coverage. It is not a
	// probability that a leak exists and never enables an automatic diagnosis.
	confidence := math.Min(coverage, 100)
	result := LeakageEstimate{
		From: input.Window.From.UTC(), To: input.Window.To.UTC(), Basis: input.Basis, EstimatedFlowNm3PerHour: flowMetric.Integral / covered.Hours(),
		EstimatedVolumeNm3: flowMetric.Integral, EstimatedEnergyKWh: powerMetric.Integral,
		EstimatedCost: powerMetric.Integral * input.RatePerKWh, CoveragePercent: coverage,
		ConfidencePercent: confidence, Covered: covered, Skipped: duration - covered,
		Currency: input.Currency, AutomaticLeakClaimSupported: false,
	}
	if !finite(result.EstimatedFlowNm3PerHour) || !finite(result.EstimatedVolumeNm3) || !finite(result.EstimatedEnergyKWh) || !finite(result.EstimatedCost) {
		return LeakageEstimate{}, ErrInvalidInputs
	}
	return result, nil
}
