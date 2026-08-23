package energy

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

const (
	defaultHistoryPerPage = 100
	maxHistoryPerPage     = 500
	exportHistoryPerPage  = 500
	maxHistoryRange       = 366 * 24 * time.Hour
	maxRawHistoryReadSpan = 31 * 24 * time.Hour
)

var (
	ErrInstanceReaderRequired = errors.New("Energy Plugin instance reader is required")
	ErrHistoryReaderRequired  = errors.New("Energy history reader is required")
	ErrEnergyInstanceRequired = errors.New("Plugin instance is not Energy Management")
	ErrInvalidHistoryQuery    = errors.New("invalid Energy history query")
)

type InstanceReader interface {
	Find(context.Context, uuid.UUID) (*plugin.Instance, error)
}

type HistoryReader interface {
	LatestBatch(context.Context, uuid.UUID) (*datalogger.RawBatch, error)
	ListBatches(context.Context, datalogger.RawBatchListInput) ([]datalogger.RawBatch, error)
}

type ServiceOption func(*Service) error

func WithServiceClock(clock func() time.Time) ServiceOption {
	return func(service *Service) error {
		if clock == nil {
			return ErrInvalidHistoryQuery
		}
		service.now = clock
		return nil
	}
}

func WithLiveHub(hub *LiveHub) ServiceOption {
	return func(service *Service) error {
		if hub == nil {
			return ErrLiveHubRequired
		}
		service.live = hub
		return nil
	}
}

type Service struct {
	instances InstanceReader
	history   HistoryReader
	now       func() time.Time
	live      *LiveHub
}

type HistoryInput struct {
	From    time.Time
	To      time.Time
	Bucket  datalogger.QueryBucket
	Page    int
	PerPage int
}

type EnergySummary struct {
	KilowattHours   float64        `json:"kilowatt_hours"`
	CoveredSeconds  float64        `json:"covered_seconds"`
	SkippedSeconds  float64        `json:"skipped_seconds"`
	CoveragePercent float64        `json:"coverage_percent"`
	Segments        int            `json:"segments"`
	SkippedSegments int            `json:"skipped_segments"`
	Issues          []SegmentIssue `json:"issues"`
}

type PeriodSummary struct {
	From       time.Time     `json:"from"`
	To         time.Time     `json:"to"`
	Electrical EnergySummary `json:"electrical"`
	Thermal    EnergySummary `json:"thermal"`
	Cost       RatioMetric   `json:"cost"`
	COP        RatioMetric   `json:"cop"`
}

type HistoryRow struct {
	PeriodSummary
}

type HistoryResult struct {
	InstanceID  uuid.UUID              `json:"instance_id"`
	LoggerID    uuid.UUID              `json:"logger_id"`
	Timezone    string                 `json:"timezone"`
	Bucket      datalogger.QueryBucket `json:"bucket"`
	Currency    string                 `json:"currency"`
	TariffMode  TariffMode             `json:"tariff_mode"`
	TariffTagID uuid.UUID              `json:"tariff_tag_id,omitempty"`
	RatePerKWh  float64                `json:"rate_per_kwh"`
	Data        []HistoryRow           `json:"data"`
	Page        int                    `json:"page"`
	PerPage     int                    `json:"per_page"`
	Total       int                    `json:"total"`
	TotalPages  int                    `json:"total_pages"`
}

type OverviewResult struct {
	InstanceID  uuid.UUID     `json:"instance_id"`
	LoggerID    uuid.UUID     `json:"logger_id"`
	Timezone    string        `json:"timezone"`
	Currency    string        `json:"currency"`
	TariffMode  TariffMode    `json:"tariff_mode"`
	TariffTagID uuid.UUID     `json:"tariff_tag_id,omitempty"`
	RatePerKWh  float64       `json:"rate_per_kwh"`
	AsOf        time.Time     `json:"as_of"`
	Latest      *BatchMetrics `json:"latest"`
	Today       PeriodSummary `json:"today"`
	Month       PeriodSummary `json:"month"`
}

type bucketWindow struct {
	From time.Time
	To   time.Time
}

func NewService(instances InstanceReader, history HistoryReader, options ...ServiceOption) (*Service, error) {
	if isNil(instances) {
		return nil, ErrInstanceReaderRequired
	}
	if isNil(history) {
		return nil, ErrHistoryReaderRequired
	}
	service := &Service{instances: instances, history: history, now: time.Now}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *Service) Overview(ctx context.Context, instanceID uuid.UUID) (*OverviewResult, error) {
	if ctx == nil || instanceID == uuid.Nil {
		return nil, ErrInvalidHistoryQuery
	}
	config, calculator, err := service.loadCalculator(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	if now.IsZero() {
		return nil, ErrInvalidHistoryQuery
	}
	location, _ := time.LoadLocation(config.Timezone)
	localNow := now.In(location)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location)
	batches, err := service.history.ListBatches(ctx, datalogger.RawBatchListInput{
		LoggerID: config.LoggerID, TagIDs: powerTagIDs(config), From: monthStart.UTC(), To: now, IncludeNeighbors: true,
	})
	if err != nil {
		return nil, err
	}
	metrics, err := evaluateBatches(calculator, batches)
	if err != nil {
		return nil, err
	}
	today, err := calculatePeriod(calculator, metrics, todayStart.UTC(), now)
	if err != nil {
		return nil, err
	}
	month, err := calculatePeriod(calculator, metrics, monthStart.UTC(), now)
	if err != nil {
		return nil, err
	}
	result := &OverviewResult{
		InstanceID: instanceID, LoggerID: config.LoggerID, Timezone: config.Timezone,
		Currency: config.Tariff.Currency, TariffMode: config.Tariff.effectiveMode(), TariffTagID: config.Tariff.TagID, RatePerKWh: config.Tariff.RatePerKWh, AsOf: now,
		Today: summarizePeriod(today), Month: summarizePeriod(month),
	}
	latest, latestErr := service.history.LatestBatch(ctx, config.LoggerID)
	if latestErr == nil {
		latestMetrics, evaluateErr := calculator.Evaluate(*latest)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		result.Latest = &latestMetrics
	} else if !errors.Is(latestErr, datalogger.ErrRawBatchNotFound) {
		return nil, latestErr
	}
	return result, nil
}

func (service *Service) History(ctx context.Context, instanceID uuid.UUID, input HistoryInput) (*HistoryResult, error) {
	if ctx == nil || instanceID == uuid.Nil {
		return nil, ErrInvalidHistoryQuery
	}
	input, err := normalizeHistoryInput(input)
	if err != nil {
		return nil, err
	}
	config, calculator, err := service.loadCalculator(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	location, _ := time.LoadLocation(config.Timezone)
	windows, total := pagedBucketWindows(input.From, input.To, input.Bucket, location, input.Page, input.PerPage)
	result := &HistoryResult{
		InstanceID: instanceID, LoggerID: config.LoggerID, Timezone: config.Timezone, Bucket: input.Bucket,
		Currency: config.Tariff.Currency, TariffMode: config.Tariff.effectiveMode(), TariffTagID: config.Tariff.TagID, RatePerKWh: config.Tariff.RatePerKWh,
		Data: []HistoryRow{}, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages(total, input.PerPage),
	}
	if len(windows) == 0 {
		return result, nil
	}
	for _, group := range groupHistoryWindows(windows, maxRawHistoryReadSpan) {
		batches, readErr := service.history.ListBatches(ctx, datalogger.RawBatchListInput{
			LoggerID: config.LoggerID, TagIDs: powerTagIDs(config),
			From: group[len(group)-1].From, To: group[0].To, IncludeNeighbors: true,
		})
		if readErr != nil {
			return nil, readErr
		}
		metrics, evaluateErr := evaluateBatches(calculator, batches)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		for _, window := range group {
			period, calculateErr := calculatePeriod(calculator, metrics, window.From, window.To)
			if calculateErr != nil {
				return nil, calculateErr
			}
			result.Data = append(result.Data, HistoryRow{PeriodSummary: summarizePeriod(period)})
		}
	}
	return result, nil
}

func (service *Service) ValidateHistory(ctx context.Context, instanceID uuid.UUID, input HistoryInput) error {
	if ctx == nil || instanceID == uuid.Nil {
		return ErrInvalidHistoryQuery
	}
	if _, err := normalizeHistoryInput(input); err != nil {
		return err
	}
	_, _, err := service.loadCalculator(ctx, instanceID)
	return err
}

func (service *Service) SubscribeLive(ctx context.Context, instanceID uuid.UUID, cursor string) (*LiveSubscription, error) {
	if ctx == nil || instanceID == uuid.Nil {
		return nil, ErrInvalidLiveCursor
	}
	if service.live == nil {
		return nil, ErrLiveHubRequired
	}
	if _, _, _, err := parseLiveCursor(cursor); err != nil {
		return nil, err
	}
	config, calculator, err := service.loadCalculator(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	latest, err := service.history.LatestBatch(ctx, config.LoggerID)
	if err == nil {
		metrics, evaluateErr := calculator.Evaluate(*latest)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		service.live.Publish(instanceID, metrics)
	} else if !errors.Is(err, datalogger.ErrRawBatchNotFound) {
		return nil, err
	}
	return service.live.Subscribe(instanceID, cursor)
}

func (service *Service) ExportCSV(ctx context.Context, instanceID uuid.UUID, input HistoryInput, writer io.Writer) error {
	if writer == nil {
		return ErrInvalidHistoryQuery
	}
	input.Page = 1
	input.PerPage = exportHistoryPerPage
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{
		"from", "to", "electrical_kwh", "thermal_kwh", "cost", "currency", "cop",
		"electrical_coverage_percent", "thermal_coverage_percent", "electrical_skipped_seconds", "thermal_skipped_seconds", "issues",
	}); err != nil {
		return err
	}
	for {
		result, err := service.History(ctx, instanceID, input)
		if err != nil {
			return err
		}
		location, _ := time.LoadLocation(result.Timezone)
		for _, row := range result.Data {
			record := []string{
				row.From.In(location).Format(time.RFC3339), row.To.In(location).Format(time.RFC3339),
				formatFloat(row.Electrical.KilowattHours), formatFloat(row.Thermal.KilowattHours), optionalRatio(row.Cost), result.Currency, optionalRatio(row.COP),
				formatFloat(row.Electrical.CoveragePercent), formatFloat(row.Thermal.CoveragePercent),
				formatFloat(row.Electrical.SkippedSeconds), formatFloat(row.Thermal.SkippedSeconds), formatIssues(row.Electrical.Issues, row.Thermal.Issues),
			}
			if err := csvWriter.Write(record); err != nil {
				return err
			}
		}
		if input.Page >= result.TotalPages || len(result.Data) == 0 {
			break
		}
		input.Page++
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func (service *Service) loadCalculator(ctx context.Context, instanceID uuid.UUID) (Config, *Calculator, error) {
	instance, err := service.instances.Find(ctx, instanceID)
	if err != nil {
		return Config{}, nil, err
	}
	if instance.Type != PluginType {
		return Config{}, nil, ErrEnergyInstanceRequired
	}
	config, err := DecodeConfig(instance.Config)
	if err != nil {
		return Config{}, nil, err
	}
	calculator, err := NewCalculator(config)
	return config, calculator, err
}

func normalizeHistoryInput(input HistoryInput) (HistoryInput, error) {
	input.From, input.To = input.From.UTC(), input.To.UTC()
	if input.From.IsZero() || input.To.IsZero() || !input.From.Before(input.To) || input.To.Sub(input.From) > maxHistoryRange || !validEnergyBucket(input.Bucket) {
		return HistoryInput{}, ErrInvalidHistoryQuery
	}
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = defaultHistoryPerPage
	}
	if input.PerPage > maxHistoryPerPage {
		return HistoryInput{}, ErrInvalidHistoryQuery
	}
	return input, nil
}

func validEnergyBucket(bucket datalogger.QueryBucket) bool {
	switch bucket {
	case datalogger.QueryBucket1Minute, datalogger.QueryBucket5Minutes, datalogger.QueryBucket15Minutes,
		datalogger.QueryBucket1Hour, datalogger.QueryBucket6Hours, datalogger.QueryBucket1Day, datalogger.QueryBucket1Week:
		return true
	default:
		return false
	}
}

func pagedBucketWindows(from, to time.Time, bucket datalogger.QueryBucket, location *time.Location, page, perPage int) ([]bucketWindow, int) {
	if bucket != datalogger.QueryBucket1Day && bucket != datalogger.QueryBucket1Week {
		return pagedFixedBucketWindows(from, to, time.Duration(bucket.Seconds())*time.Second, location, page, perPage)
	}
	cursor := calendarCeiling(to, bucket, location)
	offset := (page - 1) * perPage
	windows := make([]bucketWindow, 0, perPage)
	total := 0
	for cursor.After(from) {
		start := previousCalendarBoundary(cursor, bucket)
		window := bucketWindow{From: laterTime(from, start.UTC()), To: earlierTime(to, cursor.UTC())}
		if window.From.Before(window.To) {
			if total >= offset && len(windows) < perPage {
				windows = append(windows, window)
			}
			total++
		}
		cursor = start
	}
	return windows, total
}

func pagedFixedBucketWindows(from, to time.Time, duration time.Duration, location *time.Location, page, perPage int) ([]bucketWindow, int) {
	localFrom := from.In(location)
	anchor := time.Date(localFrom.Year(), time.January, 1, 0, 0, 0, 0, location).UTC()
	toDelta := to.Sub(anchor)
	ceilingSteps := int64(toDelta / duration)
	if anchor.Add(time.Duration(ceilingSteps) * duration).Before(to) {
		ceilingSteps++
	}
	cursor := anchor.Add(time.Duration(ceilingSteps) * duration)
	total := int(math.Ceil(float64(cursor.Sub(from)) / float64(duration)))
	if total < 0 {
		total = 0
	}
	offset := (page - 1) * perPage
	if offset >= total {
		return []bucketWindow{}, total
	}
	count := min(perPage, total-offset)
	windows := make([]bucketWindow, 0, count)
	for index := 0; index < count; index++ {
		end := cursor.Add(-time.Duration(offset+index) * duration)
		start := end.Add(-duration)
		windows = append(windows, bucketWindow{From: laterTime(from, start), To: earlierTime(to, end)})
	}
	return windows, total
}

func calendarCeiling(at time.Time, bucket datalogger.QueryBucket, location *time.Location) time.Time {
	local := at.In(location)
	floor := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	if bucket == datalogger.QueryBucket1Week {
		daysSinceMonday := (int(floor.Weekday()) + 6) % 7
		floor = floor.AddDate(0, 0, -daysSinceMonday)
	}
	if floor.Equal(local) {
		return floor
	}
	if bucket == datalogger.QueryBucket1Week {
		return floor.AddDate(0, 0, 7)
	}
	return floor.AddDate(0, 0, 1)
}

func previousCalendarBoundary(at time.Time, bucket datalogger.QueryBucket) time.Time {
	if bucket == datalogger.QueryBucket1Week {
		return at.AddDate(0, 0, -7)
	}
	return at.AddDate(0, 0, -1)
}

func evaluateBatches(calculator *Calculator, batches []datalogger.RawBatch) ([]BatchMetrics, error) {
	metrics := make([]BatchMetrics, 0, len(batches))
	for _, batch := range batches {
		value, err := calculator.Evaluate(batch)
		if err != nil {
			return nil, err
		}
		if len(metrics) > 0 && !metrics[len(metrics)-1].BatchAt.Before(value.BatchAt) {
			return nil, ErrUnorderedMetrics
		}
		metrics = append(metrics, value)
	}
	return metrics, nil
}

func calculatePeriod(calculator *Calculator, metrics []BatchMetrics, from, to time.Time) (PeriodMetrics, error) {
	if !from.Before(to) {
		return PeriodMetrics{From: from, To: to}, nil
	}
	return calculator.Integrate(metricsForPeriod(metrics, from, to), from, to)
}

func metricsForPeriod(metrics []BatchMetrics, from, to time.Time) []BatchMetrics {
	if len(metrics) == 0 {
		return nil
	}
	from, to = from.UTC(), to.UTC()
	start := sort.Search(len(metrics), func(index int) bool {
		return !metrics[index].BatchAt.Before(from)
	})
	if start == len(metrics) || (start > 0 && !metrics[start].BatchAt.Equal(from)) {
		start--
	}
	end := sort.Search(len(metrics), func(index int) bool {
		return !metrics[index].BatchAt.Before(to)
	})
	if end < len(metrics) {
		end++
	}
	if end < start {
		end = start
	}
	return metrics[start:end]
}

func groupHistoryWindows(windows []bucketWindow, maxSpan time.Duration) [][]bucketWindow {
	if len(windows) == 0 || maxSpan <= 0 {
		return nil
	}
	groups := make([][]bucketWindow, 0, (windows[0].To.Sub(windows[len(windows)-1].From)/maxSpan)+1)
	for start := 0; start < len(windows); {
		end := start + 1
		for end < len(windows) && windows[start].To.Sub(windows[end].From) <= maxSpan {
			end++
		}
		groups = append(groups, windows[start:end])
		start = end
	}
	return groups
}

func summarizePeriod(period PeriodMetrics) PeriodSummary {
	duration := period.To.Sub(period.From)
	return PeriodSummary{
		From: period.From, To: period.To,
		Electrical: summarizeEnergy(period.Electrical, duration), Thermal: summarizeEnergy(period.Thermal, duration),
		Cost: period.Cost, COP: period.COP,
	}
}

func summarizeEnergy(metric EnergyMetric, duration time.Duration) EnergySummary {
	coverage := 0.0
	if duration > 0 {
		coverage = float64(metric.Covered) / float64(duration) * 100
	}
	return EnergySummary{
		KilowattHours: metric.KilowattHours, CoveredSeconds: metric.Covered.Seconds(), SkippedSeconds: metric.Skipped.Seconds(),
		CoveragePercent: coverage, Segments: metric.Segments, SkippedSegments: metric.SkippedSegments,
		Issues: append([]SegmentIssue(nil), metric.Issues...),
	}
}

func powerTagIDs(config Config) []uuid.UUID {
	mappings := appendPowerTags(config)
	ids := make([]uuid.UUID, 0, len(mappings))
	for _, mapping := range mappings {
		ids = append(ids, mapping.TagID)
	}
	if config.Tariff.effectiveMode() == TariffModeTag {
		ids = append(ids, config.Tariff.TagID)
	}
	return ids
}

func totalPages(total, perPage int) int {
	if total == 0 {
		return 0
	}
	return (total + perPage - 1) / perPage
}

func optionalRatio(metric RatioMetric) string {
	if !metric.Valid {
		return ""
	}
	return formatFloat(metric.Value)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatIssues(groups ...[]SegmentIssue) string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, issues := range groups {
		for _, issue := range issues {
			if len(issue.Errors) == 0 {
				value := string(issue.Code)
				if _, exists := seen[value]; !exists {
					seen[value] = struct{}{}
					values = append(values, value)
				}
				continue
			}
			for _, metricError := range issue.Errors {
				value := fmt.Sprintf("%s:%s:%s", metricError.Code, metricError.TagID, metricError.Message)
				if _, exists := seen[value]; !exists {
					seen[value] = struct{}{}
					values = append(values, value)
				}
			}
		}
	}
	return strings.Join(values, " | ")
}

var _ InstanceReader = (plugin.Repository)(nil)
var _ HistoryReader = (datalogger.HistoryRepository)(nil)
