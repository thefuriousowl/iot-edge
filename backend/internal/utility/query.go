package utility

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

const (
	MaxQueryRange   = 366 * 24 * time.Hour
	MaxSeriesPoints = 5000
)

var (
	ErrHistoryRequired    = errors.New("persisted utility history is required")
	ErrHistoryUnavailable = errors.New("persisted utility history is unavailable")
	ErrInvalidQuery       = errors.New("invalid utility query")
)

// PersistedHistory is deliberately separate from live source readers. Returned
// values must already use mapping.Semantic.Unit and include boundary neighbors.
type PersistedHistory interface {
	Read(context.Context, uuid.UUID, Mapping, analytics.Window) ([]analytics.Sample, error)
}

type QueryInput struct {
	Config      Config
	From        time.Time
	To          time.Time
	Bucket      analytics.Bucket
	MappingKeys []string
	Compare     bool
	PointLimit  int
}

type QueryMetric struct {
	Key             string            `json:"key"`
	Slot            SemanticSlot      `json:"slot"`
	OwnerAssetID    uuid.UUID         `json:"owner_asset_id"`
	Unit            string            `json:"unit"`
	Value           float64           `json:"value"`
	CoveragePercent float64           `json:"coverage_percent"`
	Comparison      *MetricComparison `json:"comparison,omitempty"`
}

type MetricComparison struct {
	PreviousValue float64  `json:"previous_value"`
	Delta         float64  `json:"delta"`
	PercentChange *float64 `json:"percent_change,omitempty"`
}

type OverviewResponse struct {
	From    time.Time     `json:"from"`
	To      time.Time     `json:"to"`
	Metrics []QueryMetric `json:"metrics"`
}

type SeriesPoint struct {
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	Value           float64   `json:"value"`
	CoveragePercent float64   `json:"coverage_percent"`
}

type Series struct {
	Key    string        `json:"key"`
	Unit   string        `json:"unit"`
	Points []SeriesPoint `json:"points"`
}

type SeriesResponse struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Bucket string    `json:"bucket"`
	Series []Series  `json:"series"`
}

type BreakdownResponse struct {
	Items []QueryMetric `json:"items"`
}
type RankingResponse struct {
	Items []QueryMetric `json:"items"`
}

type HeatmapCell struct {
	Weekday         int     `json:"weekday"`
	Hour            int     `json:"hour"`
	Value           float64 `json:"value"`
	CoveragePercent float64 `json:"coverage_percent"`
}

type Heatmap struct {
	Key   string        `json:"key"`
	Unit  string        `json:"unit"`
	Cells []HeatmapCell `json:"cells"`
}

type HeatmapResponse struct {
	Maps []Heatmap `json:"maps"`
}

type QueryService struct{ history PersistedHistory }

func NewQueryService(history PersistedHistory) (*QueryService, error) {
	if isNil(history) {
		return nil, ErrHistoryRequired
	}
	return &QueryService{history: history}, nil
}

func (service *QueryService) Overview(ctx context.Context, input QueryInput) (*OverviewResponse, error) {
	mappings, window, err := service.validate(ctx, input, false)
	if err != nil {
		return nil, err
	}
	result := &OverviewResponse{From: window.From.UTC(), To: window.To.UTC(), Metrics: make([]QueryMetric, 0, len(mappings))}
	for _, mapping := range mappings {
		readWindow := window
		if input.Compare {
			readWindow.From = window.From.Add(-window.To.Sub(window.From))
		}
		samples, err := service.history.Read(ctx, input.Config.LoggerID, mapping, readWindow)
		if err != nil {
			return nil, fmt.Errorf("mapping %s: %w", mapping.Key, err)
		}
		metric, err := analytics.Integrate(samples, window, time.Duration(input.Config.MaxGapSeconds)*time.Second)
		if err != nil {
			return nil, fmt.Errorf("mapping %s: %w", mapping.Key, err)
		}
		entry := queryMetric(mapping, metric)
		if input.Compare {
			comparison, err := analytics.Compare(samples, window, time.Duration(input.Config.MaxGapSeconds)*time.Second)
			if err != nil {
				return nil, err
			}
			previous := periodValue(mapping, comparison.Previous)
			current := periodValue(mapping, comparison.Current)
			entry.Comparison = &MetricComparison{PreviousValue: previous, Delta: current - previous}
			if previous != 0 {
				value := (current - previous) / previous * 100
				entry.Comparison.PercentChange = &value
			}
		}
		result.Metrics = append(result.Metrics, entry)
	}
	return result, nil
}

func (service *QueryService) Series(ctx context.Context, input QueryInput) (*SeriesResponse, error) {
	mappings, window, err := service.validate(ctx, input, true)
	if err != nil {
		return nil, err
	}
	result := &SeriesResponse{From: window.From.UTC(), To: window.To.UTC(), Bucket: string(input.Bucket), Series: make([]Series, 0, len(mappings))}
	for _, mapping := range mappings {
		samples, err := service.history.Read(ctx, input.Config.LoggerID, mapping, window)
		if err != nil {
			return nil, fmt.Errorf("mapping %s: %w", mapping.Key, err)
		}
		metrics, err := analytics.BucketIntegrals(samples, window, input.Bucket, input.Config.Timezone, time.Duration(input.Config.MaxGapSeconds)*time.Second)
		if err != nil {
			return nil, err
		}
		points := make([]SeriesPoint, len(metrics))
		for i, metric := range metrics {
			points[i] = SeriesPoint{From: metric.From, To: metric.To, Value: periodValue(mapping, metric), CoveragePercent: metric.CoveragePercent}
		}
		points = boundSeries(points, input.PointLimit)
		result.Series = append(result.Series, Series{Key: mapping.Key, Unit: periodUnit(mapping), Points: points})
	}
	return result, nil
}

func (service *QueryService) Breakdown(ctx context.Context, input QueryInput) (*BreakdownResponse, error) {
	overview, err := service.Overview(ctx, input)
	if err != nil {
		return nil, err
	}
	return &BreakdownResponse{Items: overview.Metrics}, nil
}

func (service *QueryService) Ranking(ctx context.Context, input QueryInput) (*RankingResponse, error) {
	overview, err := service.Overview(ctx, input)
	if err != nil {
		return nil, err
	}
	items := append([]QueryMetric(nil), overview.Metrics...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Value == items[j].Value {
			return items[i].Key < items[j].Key
		}
		return items[i].Value > items[j].Value
	})
	return &RankingResponse{Items: items}, nil
}

func (service *QueryService) Heatmap(ctx context.Context, input QueryInput) (*HeatmapResponse, error) {
	input.Bucket = analytics.BucketHour
	series, err := service.Series(ctx, input)
	if err != nil {
		return nil, err
	}
	location, _ := time.LoadLocation(input.Config.Timezone)
	result := &HeatmapResponse{Maps: make([]Heatmap, len(series.Series))}
	for i, values := range series.Series {
		cells := make([]HeatmapCell, len(values.Points))
		for j, point := range values.Points {
			local := point.From.In(location)
			cells[j] = HeatmapCell{Weekday: int(local.Weekday()), Hour: local.Hour(), Value: point.Value, CoveragePercent: point.CoveragePercent}
		}
		result.Maps[i] = Heatmap{Key: values.Key, Unit: values.Unit, Cells: cells}
	}
	return result, nil
}

func (service *QueryService) ExportCSV(ctx context.Context, input QueryInput) ([]byte, error) {
	series, err := service.Series(ctx, input)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"mapping_key", "unit", "period_start", "period_end", "value", "coverage_percent"})
	for _, values := range series.Series {
		for _, point := range values.Points {
			_ = writer.Write([]string{values.Key, values.Unit, point.From.Format(time.RFC3339Nano), point.To.Format(time.RFC3339Nano), strconv.FormatFloat(point.Value, 'g', -1, 64), strconv.FormatFloat(point.CoveragePercent, 'g', -1, 64)})
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (service *QueryService) validate(ctx context.Context, input QueryInput, bucketRequired bool) ([]Mapping, analytics.Window, error) {
	if service == nil || isNil(service.history) || ctx == nil || input.Config.Validate() != nil || input.From.IsZero() || !input.From.Before(input.To) || input.To.Sub(input.From) > MaxQueryRange || input.PointLimit < 2 || input.PointLimit > MaxSeriesPoints {
		return nil, analytics.Window{}, ErrInvalidQuery
	}
	if bucketRequired && input.Bucket != analytics.BucketHour && input.Bucket != analytics.BucketDay && input.Bucket != analytics.BucketWeek && input.Bucket != analytics.BucketMonth {
		return nil, analytics.Window{}, ErrInvalidQuery
	}
	selected := make(map[string]struct{}, len(input.MappingKeys))
	for _, key := range input.MappingKeys {
		if key == "" {
			return nil, analytics.Window{}, ErrInvalidQuery
		}
		if _, duplicate := selected[key]; duplicate {
			return nil, analytics.Window{}, ErrInvalidQuery
		}
		selected[key] = struct{}{}
	}
	mappings := make([]Mapping, 0, len(input.Config.Mappings))
	for _, mapping := range input.Config.Mappings {
		if len(selected) == 0 {
			mappings = append(mappings, mapping)
			continue
		}
		if _, ok := selected[mapping.Key]; ok {
			mappings = append(mappings, mapping)
			delete(selected, mapping.Key)
		}
	}
	if len(mappings) == 0 || len(selected) != 0 {
		return nil, analytics.Window{}, ErrInvalidQuery
	}
	return mappings, analytics.Window{From: input.From.UTC(), To: input.To.UTC()}, nil
}

func queryMetric(mapping Mapping, metric analytics.Metric) QueryMetric {
	return QueryMetric{Key: mapping.Key, Slot: mapping.Slot, OwnerAssetID: mapping.OwnerAssetID, Unit: periodUnit(mapping), Value: periodValue(mapping, metric), CoveragePercent: metric.CoveragePercent}
}

func periodUnit(mapping Mapping) string {
	unit := string(mapping.Semantic.Unit)
	if mapping.Semantic.Quantity == "power" {
		switch unit {
		case "W":
			return "Wh"
		case "kW":
			return "kWh"
		case "MW":
			return "MWh"
		}
	}
	if mapping.Semantic.Quantity == "flow_rate" {
		switch unit {
		case "m3/s", "m3/h":
			return "m3"
		case "Nm3/s", "Nm3/h":
			return "Nm3"
		}
	}
	return unit
}

func periodValue(mapping Mapping, metric analytics.Metric) float64 {
	if mapping.Semantic.Quantity == "power" {
		return metric.Integral
	}
	if mapping.Semantic.Quantity == "flow_rate" {
		if mapping.Semantic.Unit == "m3/s" || mapping.Semantic.Unit == "Nm3/s" {
			return metric.Integral * 3600
		}
		return metric.Integral
	}
	if metric.Covered <= 0 {
		return 0
	}
	return metric.Integral / metric.Covered.Hours()
}

func boundSeries(points []SeriesPoint, limit int) []SeriesPoint {
	if len(points) <= limit {
		return points
	}
	result := make([]SeriesPoint, 0, limit)
	result = append(result, points[0])
	for i := 1; i < limit-1; i++ {
		index := 1 + i*(len(points)-2)/(limit-1)
		result = append(result, points[index])
	}
	return append(result, points[len(points)-1])
}
