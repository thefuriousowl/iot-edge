package utility

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/utility/analytics"
)

type queryHistory struct {
	values  map[string][]analytics.Sample
	windows []analytics.Window
}

func (history *queryHistory) Read(_ context.Context, _ uuid.UUID, mapping Mapping, window analytics.Window) ([]analytics.Sample, error) {
	history.windows = append(history.windows, window)
	values, ok := history.values[mapping.Key]
	if !ok {
		return nil, ErrHistoryUnavailable
	}
	return append([]analytics.Sample(nil), values...), nil
}

func TestQueryServiceProvidesReusableResponsesFromPersistedHistory(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	config := validConfig()
	config.Timezone = "UTC"
	config.MaxGapSeconds = 7200
	config.Mappings = config.Mappings[:1]
	history := &queryHistory{values: map[string][]analytics.Sample{"electrical_power_1": {
		{At: base.Add(-2 * time.Hour), Value: 10, Quality: analytics.QualityGood},
		{At: base.Add(-time.Hour), Value: 10, Quality: analytics.QualityGood},
		{At: base, Value: 20, Quality: analytics.QualityGood},
		{At: base.Add(time.Hour), Value: 20, Quality: analytics.QualityGood},
	}}}
	service, err := NewQueryService(history)
	if err != nil {
		t.Fatal(err)
	}
	input := QueryInput{Config: config, From: base.Add(-time.Hour), To: base.Add(time.Hour), Bucket: analytics.BucketHour, MappingKeys: []string{"electrical_power_1"}, Compare: true, PointLimit: 100}
	overview, err := service.Overview(context.Background(), input)
	if err != nil || len(overview.Metrics) != 1 || overview.Metrics[0].Value != 35 || overview.Metrics[0].Unit != "kWh" || overview.Metrics[0].Comparison == nil || overview.Metrics[0].Comparison.PreviousValue != 10 {
		t.Fatalf("overview=%#v err=%v", overview, err)
	}
	if len(history.windows) == 0 || !history.windows[0].From.Equal(base.Add(-3*time.Hour)) {
		t.Fatalf("comparison history window=%#v", history.windows)
	}
	series, err := service.Series(context.Background(), input)
	if err != nil || len(series.Series) != 1 || len(series.Series[0].Points) != 2 {
		t.Fatalf("series=%#v err=%v", series, err)
	}
	breakdown, err := service.Breakdown(context.Background(), input)
	if err != nil || len(breakdown.Items) != 1 {
		t.Fatalf("breakdown=%#v err=%v", breakdown, err)
	}
	ranking, err := service.Ranking(context.Background(), input)
	if err != nil || ranking.Items[0].Key != "electrical_power_1" {
		t.Fatalf("ranking=%#v err=%v", ranking, err)
	}
	heatmap, err := service.Heatmap(context.Background(), input)
	if err != nil || len(heatmap.Maps) != 1 || len(heatmap.Maps[0].Cells) != 2 {
		t.Fatalf("heatmap=%#v err=%v", heatmap, err)
	}
	exported, err := service.ExportCSV(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(exported))).ReadAll()
	if err != nil || len(records) != 3 || records[0][0] != "mapping_key" {
		t.Fatalf("CSV=%q records=%#v err=%v", exported, records, err)
	}
}

func TestPeriodValueNormalizesRateUnits(t *testing.T) {
	t.Parallel()
	metric := analytics.Metric{Integral: 2, Covered: time.Hour}
	mapping := validConfig().Mappings[0]
	if got := periodUnit(mapping); got != "kWh" {
		t.Fatalf("power unit=%q", got)
	}
	mapping.Semantic.Quantity = "flow_rate"
	mapping.Semantic.Unit = "m3/s"
	if got := periodValue(mapping, metric); got != 7200 || periodUnit(mapping) != "m3" {
		t.Fatalf("flow value/unit=%v/%s", got, periodUnit(mapping))
	}
}

func TestQueryServiceFailsClosedForInvalidOrUnavailableHistory(t *testing.T) {
	t.Parallel()
	if _, err := NewQueryService((*queryHistory)(nil)); !errors.Is(err, ErrHistoryRequired) {
		t.Fatalf("typed nil error=%v", err)
	}
	config := validConfig()
	config.Mappings = config.Mappings[:1]
	service, _ := NewQueryService(&queryHistory{values: map[string][]analytics.Sample{}})
	base := time.Now().UTC().Truncate(time.Hour)
	input := QueryInput{Config: config, From: base, To: base.Add(time.Hour), PointLimit: 100}
	if _, err := service.Overview(context.Background(), input); !errors.Is(err, ErrHistoryUnavailable) {
		t.Fatalf("unavailable error=%v", err)
	}
	input.MappingKeys = []string{"missing"}
	if _, err := service.Overview(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("missing key error=%v", err)
	}
	input.MappingKeys = nil
	input.To = input.From.Add(MaxQueryRange + time.Second)
	if _, err := service.Overview(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("range error=%v", err)
	}
}

func TestBoundSeriesIsDeterministicAndPreservesEndpoints(t *testing.T) {
	t.Parallel()
	points := make([]SeriesPoint, 100)
	for i := range points {
		points[i] = SeriesPoint{Value: float64(i)}
	}
	bounded := boundSeries(points, 10)
	if len(bounded) != 10 || bounded[0].Value != 0 || bounded[9].Value != 99 {
		t.Fatalf("bounded=%#v", bounded)
	}
}
