package analytics

import (
	"math"
	"testing"
	"time"
)

func TestIntegrateClipsBoundariesAndReportsCoverage(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []Sample{{base, 0, QualityGood}, {base.Add(time.Hour), 10, QualityGood}, {base.Add(2 * time.Hour), 20, QualityGood}}
	metric, err := Integrate(samples, Window{base.Add(30 * time.Minute), base.Add(90 * time.Minute)}, 2*time.Hour)
	if err != nil || math.Abs(metric.Integral-10) > 1e-9 || metric.CoveragePercent != 100 {
		t.Fatalf("metric=%#v err=%v", metric, err)
	}
}

func TestIntegrateSkipsBadAndStaleSegments(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []Sample{{base, 10, QualityGood}, {base.Add(time.Hour), 10, QualityBad}, {base.Add(3 * time.Hour), 10, QualityGood}, {base.Add(4 * time.Hour), 10, QualityGood}}
	metric, err := Integrate(samples, Window{base, base.Add(4 * time.Hour)}, 90*time.Minute)
	if err != nil || metric.Covered != time.Hour || metric.Skipped != 3*time.Hour || metric.CoveragePercent != 25 || metric.SkippedSegments != 2 {
		t.Fatalf("metric=%#v err=%v", metric, err)
	}
}

func TestTimezoneWindowsRespectDSTAndClipRange(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("America/New_York")
	from := time.Date(2026, 3, 8, 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 1)
	windows, err := Windows(Window{from, to}, BucketDay, "America/New_York")
	if err != nil || len(windows) != 1 || windows[0].To.Sub(windows[0].From) != 23*time.Hour {
		t.Fatalf("windows=%#v err=%v", windows, err)
	}
}

func TestCompareAndBoundedDownsample(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := []Sample{{base, 10, QualityGood}, {base.Add(time.Hour), 10, QualityGood}, {base.Add(2 * time.Hour), 30, QualityGood}}
	comparison, err := Compare(samples, Window{base.Add(time.Hour), base.Add(2 * time.Hour)}, 2*time.Hour)
	if err != nil || comparison.Delta != 10 || comparison.PercentChange == nil || *comparison.PercentChange != 100 {
		t.Fatalf("comparison=%#v err=%v", comparison, err)
	}
	points := make([]Point, 100)
	for i := range points {
		points[i] = Point{base.Add(time.Duration(i) * time.Minute), float64(i)}
	}
	down, err := Downsample(points, 10)
	if err != nil || len(down) != 10 || down[0] != points[0] || down[9] != points[99] {
		t.Fatalf("downsample=%#v err=%v", down, err)
	}
}

func TestBucketIntegralsClipCalendarBoundaries(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC)
	samples := []Sample{{base, 2, QualityGood}, {base.Add(2 * time.Hour), 2, QualityGood}}
	metrics, err := BucketIntegrals(samples, Window{base.Add(30 * time.Minute), base.Add(90 * time.Minute)}, BucketMonth, "UTC", 3*time.Hour)
	if err != nil || len(metrics) != 2 || metrics[0].Integral != 1 || metrics[1].Integral != 1 {
		t.Fatalf("metrics=%#v err=%v", metrics, err)
	}
}

func TestInvalidInputsAndZeroComparison(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	duplicate := []Sample{{base, 1, QualityGood}, {base, 2, QualityGood}}
	if _, err := Integrate(duplicate, Window{base, base.Add(time.Hour)}, time.Hour); err == nil {
		t.Fatal("duplicate timestamps must fail closed")
	}
	comparison, err := Compare([]Sample{{base, 0, QualityGood}, {base.Add(2 * time.Hour), 0, QualityGood}}, Window{base.Add(time.Hour), base.Add(2 * time.Hour)}, 3*time.Hour)
	if err != nil || comparison.PercentChange != nil {
		t.Fatalf("comparison=%#v err=%v", comparison, err)
	}
	if _, err := Downsample(nil, 1); err == nil {
		t.Fatal("limit below two must fail closed")
	}
}
