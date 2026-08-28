package compressedair

import (
	"math"
	"testing"
	"time"

	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

func TestEstimateLeakageRequiresExplicitBaselineAndNeverClaimsDiagnosis(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := LeakageInput{Basis: BaselineNoProduction, FlowNm3PerHour: values(base, 100, 100, 100), PowerKW: values(base, 20, 20, 20), Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour, RatePerKWh: 4, Currency: "THB"}
	result, err := EstimateLeakage(input)
	if err != nil || result.EstimatedFlowNm3PerHour != 100 || result.EstimatedVolumeNm3 != 200 || result.EstimatedEnergyKWh != 40 || result.EstimatedCost != 160 || result.CoveragePercent != 100 || result.ConfidencePercent != 100 || result.AutomaticLeakClaimSupported {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	input.Basis = ""
	if _, err := EstimateLeakage(input); err == nil {
		t.Fatal("implicit baseline must fail closed")
	}
}

func TestEstimateLeakageReportsPartialCoverageConfidence(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow := values(base, 100, 100, 100)
	power := values(base, 20, 20, 20)
	flow[1].Quality, power[1].Quality = analytics.QualityBad, analytics.QualityBad
	input := LeakageInput{Basis: BaselineIdle, FlowNm3PerHour: flow, PowerKW: power, Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour, RatePerKWh: 4, Currency: "THB"}
	if _, err := EstimateLeakage(input); err == nil {
		t.Fatal("zero-coverage baseline must fail closed")
	}
	flow, power = values(base, 100, 100, 100, 100), values(base, 20, 20, 20, 20)
	flow[2].Quality, power[2].Quality = analytics.QualityBad, analytics.QualityBad
	input.FlowNm3PerHour, input.PowerKW = flow, power
	input.Window.To = base.Add(3 * time.Hour)
	result, err := EstimateLeakage(input)
	if err != nil || math.Abs(result.CoveragePercent-100.0/3.0) > 1e-12 || math.Abs(result.ConfidencePercent-100.0/3.0) > 1e-12 || result.Skipped != 2*time.Hour {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestEstimateLeakageRejectsCoverageMismatchAndPhysicalValues(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := LeakageInput{Basis: BaselineIdle, FlowNm3PerHour: values(base, 100, 100, 100), PowerKW: values(base, 20, 20, 20), Window: analytics.Window{From: base, To: base.Add(2 * time.Hour)}, MaxGap: 2 * time.Hour, Currency: "THB"}
	input.FlowNm3PerHour[1].Quality = analytics.QualityBad
	if _, err := EstimateLeakage(input); err == nil {
		t.Fatal("mismatched baseline coverage must fail closed")
	}
	input.FlowNm3PerHour = values(base, -1, -1, -1)
	if _, err := EstimateLeakage(input); err == nil {
		t.Fatal("negative baseline flow must fail closed")
	}
}
