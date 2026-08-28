package analytics

import (
	"errors"
	"math"
	"sort"
	"time"
)

type Bucket string
type Quality string

const (
	BucketHour  Bucket  = "hour"
	BucketDay   Bucket  = "day"
	BucketWeek  Bucket  = "week"
	BucketMonth Bucket  = "month"
	QualityGood Quality = "good"
	QualityBad  Quality = "bad"
)

var ErrInvalidInput = errors.New("invalid utility analytics input")

type Sample struct {
	At      time.Time
	Value   float64
	Quality Quality
}
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
type Metric struct {
	From            time.Time
	To              time.Time
	Integral        float64
	Covered         time.Duration
	Skipped         time.Duration
	CoveragePercent float64
	Segments        int
	SkippedSegments int
}
type Point struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}
type Comparison struct {
	Current       Metric
	Previous      Metric
	Delta         float64
	PercentChange *float64
}

func Integrate(samples []Sample, window Window, maxGap time.Duration) (Metric, error) {
	if window.From.IsZero() || !window.From.Before(window.To) || maxGap <= 0 {
		return Metric{}, ErrInvalidInput
	}
	ordered := append([]Sample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	for i := range ordered {
		ordered[i].At = ordered[i].At.UTC()
		if ordered[i].At.IsZero() || math.IsNaN(ordered[i].Value) || math.IsInf(ordered[i].Value, 0) || i > 0 && !ordered[i-1].At.Before(ordered[i].At) {
			return Metric{}, ErrInvalidInput
		}
	}
	from, to := window.From.UTC(), window.To.UTC()
	result := Metric{From: from, To: to, Skipped: to.Sub(from)}
	for i := 1; i < len(ordered); i++ {
		left, right := ordered[i-1], ordered[i]
		start, end := later(from, left.At), earlier(to, right.At)
		if !start.Before(end) {
			continue
		}
		result.Segments++
		span := right.At.Sub(left.At)
		if span > maxGap || left.Quality != QualityGood || right.Quality != QualityGood {
			result.SkippedSegments++
			continue
		}
		startValue := interpolate(left, right, start)
		endValue := interpolate(left, right, end)
		duration := end.Sub(start)
		result.Integral += (startValue + endValue) / 2 * duration.Hours()
		result.Covered += duration
	}
	result.Skipped = to.Sub(from) - result.Covered
	result.CoveragePercent = float64(result.Covered) / float64(to.Sub(from)) * 100
	return result, nil
}

func Compare(samples []Sample, current Window, maxGap time.Duration) (Comparison, error) {
	duration := current.To.Sub(current.From)
	previous := Window{From: current.From.Add(-duration), To: current.From}
	cur, err := Integrate(samples, current, maxGap)
	if err != nil {
		return Comparison{}, err
	}
	prev, err := Integrate(samples, previous, maxGap)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{Current: cur, Previous: prev, Delta: cur.Integral - prev.Integral}
	if prev.Integral != 0 {
		value := result.Delta / prev.Integral * 100
		result.PercentChange = &value
	}
	return result, nil
}

func Windows(window Window, bucket Bucket, timezone string) ([]Window, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil || window.From.IsZero() || !window.From.Before(window.To) {
		return nil, ErrInvalidInput
	}
	local := window.From.In(location)
	var cursor time.Time
	switch bucket {
	case BucketHour:
		cursor = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
	case BucketDay:
		cursor = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	case BucketWeek:
		days := (int(local.Weekday()) + 6) % 7
		day := local.AddDate(0, 0, -days)
		cursor = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
	case BucketMonth:
		cursor = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
	default:
		return nil, ErrInvalidInput
	}
	result := []Window{}
	for cursor.Before(window.To.In(location)) {
		var next time.Time
		switch bucket {
		case BucketHour:
			next = cursor.Add(time.Hour)
		case BucketDay:
			next = cursor.AddDate(0, 0, 1)
		case BucketWeek:
			next = cursor.AddDate(0, 0, 7)
		case BucketMonth:
			next = cursor.AddDate(0, 1, 0)
		}
		start, end := later(window.From.UTC(), cursor.UTC()), earlier(window.To.UTC(), next.UTC())
		if start.Before(end) {
			result = append(result, Window{From: start, To: end})
		}
		cursor = next
	}
	return result, nil
}

func BucketIntegrals(samples []Sample, window Window, bucket Bucket, timezone string, maxGap time.Duration) ([]Metric, error) {
	windows, err := Windows(window, bucket, timezone)
	if err != nil {
		return nil, err
	}
	result := make([]Metric, len(windows))
	for i, value := range windows {
		result[i], err = Integrate(samples, value, maxGap)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func Downsample(points []Point, limit int) ([]Point, error) {
	if limit < 2 {
		return nil, ErrInvalidInput
	}
	if len(points) <= limit {
		return append([]Point(nil), points...), nil
	}
	result := make([]Point, 0, limit)
	result = append(result, points[0])
	interior := limit - 2
	for i := 1; i <= interior; i++ {
		index := 1 + (i*(len(points)-2))/(interior+1)
		result = append(result, points[index])
	}
	result = append(result, points[len(points)-1])
	return result, nil
}

func interpolate(left, right Sample, at time.Time) float64 {
	ratio := float64(at.Sub(left.At)) / float64(right.At.Sub(left.At))
	return left.Value + (right.Value-left.Value)*ratio
}
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
