package energy

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

type MetricErrorCode string

const (
	MetricErrorMissingSample MetricErrorCode = "missing_sample"
	MetricErrorSourceBad     MetricErrorCode = "source_bad"
	MetricErrorInvalidValue  MetricErrorCode = "invalid_value"
	MetricErrorStaleGap      MetricErrorCode = "stale_gap"
	MetricErrorNoCoverage    MetricErrorCode = "no_coverage"
)

var (
	ErrInvalidEnergyBatch = errors.New("invalid Energy batch")
	ErrInvalidEnergyRange = errors.New("invalid Energy calculation range")
	ErrUnorderedMetrics   = errors.New("Energy batch metrics must be strictly ordered")
)

type MetricError struct {
	Code    MetricErrorCode `json:"code"`
	TagID   uuid.UUID       `json:"tag_id,omitempty"`
	At      time.Time       `json:"at"`
	Message string          `json:"message"`
}

type DemandMetric struct {
	Kilowatts float64       `json:"kilowatts"`
	Valid     bool          `json:"valid"`
	Errors    []MetricError `json:"errors"`
}

type RatioMetric struct {
	Value float64 `json:"value"`
	Valid bool    `json:"valid"`
	Error string  `json:"error,omitempty"`
}

type TariffMetric struct {
	RatePerKWh float64       `json:"rate_per_kwh"`
	Valid      bool          `json:"valid"`
	Errors     []MetricError `json:"errors"`
}

type BatchMetrics struct {
	BatchAt    time.Time    `json:"batch_at"`
	Electrical DemandMetric `json:"electrical"`
	Thermal    DemandMetric `json:"thermal"`
	Tariff     TariffMetric `json:"tariff"`
	COP        RatioMetric  `json:"cop"`
}

type SegmentIssue struct {
	From   time.Time       `json:"from"`
	To     time.Time       `json:"to"`
	Code   MetricErrorCode `json:"code"`
	Errors []MetricError   `json:"errors"`
}

type EnergyMetric struct {
	KilowattHours   float64
	Covered         time.Duration
	Skipped         time.Duration
	Segments        int
	SkippedSegments int
	Issues          []SegmentIssue
}

type PeriodMetrics struct {
	From       time.Time
	To         time.Time
	Electrical EnergyMetric
	Thermal    EnergyMetric
	Cost       RatioMetric
	COP        RatioMetric
}

type Calculator struct {
	config Config
}

func NewCalculator(config Config) (*Calculator, error) {
	if err := validateConfigShape(config); err != nil {
		return nil, err
	}
	config.ElectricalPowerTags = append([]PowerTag(nil), config.ElectricalPowerTags...)
	config.ThermalPowerTags = append([]PowerTag(nil), config.ThermalPowerTags...)
	return &Calculator{config: config}, nil
}

func (calculator *Calculator) Evaluate(batch datalogger.RawBatch) (BatchMetrics, error) {
	if calculator == nil || batch.LoggerID != calculator.config.LoggerID || batch.BatchAt.IsZero() {
		return BatchMetrics{}, ErrInvalidEnergyBatch
	}
	samples := make(map[uuid.UUID]datalogger.RawSample, len(batch.Samples))
	for _, sample := range batch.Samples {
		if sample.TagID == uuid.Nil {
			return BatchMetrics{}, ErrInvalidEnergyBatch
		}
		if _, duplicate := samples[sample.TagID]; duplicate {
			return BatchMetrics{}, ErrInvalidEnergyBatch
		}
		samples[sample.TagID] = sample
	}
	metrics := BatchMetrics{
		BatchAt:    batch.BatchAt.UTC(),
		Electrical: evaluateDemand(batch.BatchAt, calculator.config.ElectricalPowerTags, samples),
		Thermal:    evaluateDemand(batch.BatchAt, calculator.config.ThermalPowerTags, samples),
		Tariff:     evaluateTariff(batch.BatchAt, calculator.config.Tariff, samples),
	}
	metrics.COP = instantaneousCOP(metrics.Electrical, metrics.Thermal)
	return metrics, nil
}

func (calculator *Calculator) Integrate(metrics []BatchMetrics, from, to time.Time) (PeriodMetrics, error) {
	if calculator == nil || from.IsZero() || to.IsZero() || !from.Before(to) {
		return PeriodMetrics{}, ErrInvalidEnergyRange
	}
	from, to = from.UTC(), to.UTC()
	ordered := append([]BatchMetrics(nil), metrics...)
	for index := range ordered {
		ordered[index] = cloneBatchMetrics(ordered[index])
		if ordered[index].BatchAt.IsZero() {
			return PeriodMetrics{}, ErrInvalidEnergyBatch
		}
		ordered[index].BatchAt = ordered[index].BatchAt.UTC()
		if index > 0 && !ordered[index-1].BatchAt.Before(ordered[index].BatchAt) {
			return PeriodMetrics{}, ErrUnorderedMetrics
		}
	}
	maxGap := time.Duration(calculator.config.MaxGapSeconds) * time.Second
	result := PeriodMetrics{From: from, To: to}
	result.Electrical = integrateDemand(ordered, from, to, maxGap, func(value BatchMetrics) DemandMetric { return value.Electrical })
	result.Thermal = integrateDemand(ordered, from, to, maxGap, func(value BatchMetrics) DemandMetric { return value.Thermal })
	if calculator.config.Tariff.effectiveMode() == TariffModeTag {
		result.Cost = periodTagCost(ordered, from, to, maxGap, result.Electrical)
	} else {
		result.Cost = periodCost(result.Electrical, calculator.config.Tariff.RatePerKWh)
	}
	result.COP = periodCOP(result.Electrical, result.Thermal)
	return result, nil
}

func evaluateTariff(batchAt time.Time, tariff FlatTariff, samples map[uuid.UUID]datalogger.RawSample) TariffMetric {
	if tariff.effectiveMode() == TariffModeFlat {
		return TariffMetric{RatePerKWh: tariff.RatePerKWh, Valid: true}
	}
	sample, exists := samples[tariff.TagID]
	if !exists {
		return TariffMetric{Errors: []MetricError{{Code: MetricErrorMissingSample, TagID: tariff.TagID, At: batchAt.UTC(), Message: "tariff Tag sample is missing from committed batch"}}}
	}
	if sample.Quality != datalogger.RawQualityGood {
		message := strings.TrimSpace(sample.Error)
		if message == "" {
			message = "tariff Tag source quality is bad"
		}
		return TariffMetric{Errors: []MetricError{{Code: MetricErrorSourceBad, TagID: tariff.TagID, At: sample.ObservedAt.UTC(), Message: message}}}
	}
	value, valid := numericSampleValue(sample)
	if !valid || value < 0 {
		return TariffMetric{Errors: []MetricError{{Code: MetricErrorInvalidValue, TagID: tariff.TagID, At: sample.ObservedAt.UTC(), Message: "tariff Tag value must be a finite non-negative number"}}}
	}
	return TariffMetric{RatePerKWh: value, Valid: true}
}

func evaluateDemand(batchAt time.Time, mappings []PowerTag, samples map[uuid.UUID]datalogger.RawSample) DemandMetric {
	if len(mappings) == 0 {
		return DemandMetric{}
	}
	metric := DemandMetric{Valid: true}
	for _, mapping := range mappings {
		sample, exists := samples[mapping.TagID]
		if !exists {
			metric.Valid = false
			metric.Errors = append(metric.Errors, MetricError{Code: MetricErrorMissingSample, TagID: mapping.TagID, At: batchAt.UTC(), Message: "power Tag sample is missing from committed batch"})
			continue
		}
		if sample.Quality != datalogger.RawQualityGood {
			message := strings.TrimSpace(sample.Error)
			if message == "" {
				message = "power Tag source quality is bad"
			}
			metric.Valid = false
			metric.Errors = append(metric.Errors, MetricError{Code: MetricErrorSourceBad, TagID: mapping.TagID, At: sample.ObservedAt.UTC(), Message: message})
			continue
		}
		value, valid := numericSampleValue(sample)
		if !valid {
			metric.Valid = false
			metric.Errors = append(metric.Errors, MetricError{Code: MetricErrorInvalidValue, TagID: mapping.TagID, At: sample.ObservedAt.UTC(), Message: "power Tag value is not a finite numeric value"})
			continue
		}
		kilowatts, err := mapping.Unit.ToKilowatts(value)
		if err != nil || math.IsNaN(metric.Kilowatts+kilowatts) || math.IsInf(metric.Kilowatts+kilowatts, 0) {
			metric.Valid = false
			metric.Errors = append(metric.Errors, MetricError{Code: MetricErrorInvalidValue, TagID: mapping.TagID, At: sample.ObservedAt.UTC(), Message: "power Tag value cannot be represented in kilowatts"})
			continue
		}
		metric.Kilowatts += kilowatts
	}
	if !metric.Valid {
		metric.Kilowatts = 0
	}
	return metric
}

func numericSampleValue(sample datalogger.RawSample) (float64, bool) {
	var value float64
	switch sample.DataType {
	case "int16":
		typed, valid := sample.Value.(int16)
		if !valid {
			return 0, false
		}
		value = float64(typed)
	case "uint16":
		typed, valid := sample.Value.(uint16)
		if !valid {
			return 0, false
		}
		value = float64(typed)
	case "int32":
		typed, valid := sample.Value.(int32)
		if !valid {
			return 0, false
		}
		value = float64(typed)
	case "uint32":
		typed, valid := sample.Value.(uint32)
		if !valid {
			return 0, false
		}
		value = float64(typed)
	case "float32":
		typed, valid := sample.Value.(float32)
		if !valid {
			return 0, false
		}
		value = float64(typed)
	case "float64":
		typed, valid := sample.Value.(float64)
		if !valid {
			return 0, false
		}
		value = typed
	default:
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0)
}

func instantaneousCOP(electrical, thermal DemandMetric) RatioMetric {
	if !electrical.Valid || !thermal.Valid {
		return RatioMetric{Error: "electrical and thermal demand must both be valid"}
	}
	if electrical.Kilowatts <= 0 {
		return RatioMetric{Error: "electrical demand must be positive"}
	}
	value := thermal.Kilowatts / electrical.Kilowatts
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return RatioMetric{Error: "COP is not finite"}
	}
	return RatioMetric{Value: value, Valid: true}
}

func integrateDemand(metrics []BatchMetrics, from, to time.Time, maxGap time.Duration, selectDemand func(BatchMetrics) DemandMetric) EnergyMetric {
	result := EnergyMetric{Skipped: to.Sub(from)}
	if len(metrics) < 2 {
		result.Issues = append(result.Issues, SegmentIssue{From: from, To: to, Code: MetricErrorNoCoverage})
		return result
	}
	for index := 1; index < len(metrics); index++ {
		left, right := metrics[index-1], metrics[index]
		segmentFrom := laterTime(from, left.BatchAt)
		segmentTo := earlierTime(to, right.BatchAt)
		if !segmentFrom.Before(segmentTo) {
			continue
		}
		duration := segmentTo.Sub(segmentFrom)
		leftDemand, rightDemand := selectDemand(left), selectDemand(right)
		if right.BatchAt.Sub(left.BatchAt) > maxGap {
			result.SkippedSegments++
			result.Issues = append(result.Issues, SegmentIssue{From: segmentFrom, To: segmentTo, Code: MetricErrorStaleGap})
			continue
		}
		if !leftDemand.Valid || !rightDemand.Valid {
			result.SkippedSegments++
			errors := append(cloneMetricErrors(leftDemand.Errors), rightDemand.Errors...)
			result.Issues = append(result.Issues, SegmentIssue{From: segmentFrom, To: segmentTo, Code: MetricErrorInvalidValue, Errors: errors})
			continue
		}
		fullDuration := right.BatchAt.Sub(left.BatchAt)
		startFraction := float64(segmentFrom.Sub(left.BatchAt)) / float64(fullDuration)
		endFraction := float64(segmentTo.Sub(left.BatchAt)) / float64(fullDuration)
		startPower := leftDemand.Kilowatts + (rightDemand.Kilowatts-leftDemand.Kilowatts)*startFraction
		endPower := leftDemand.Kilowatts + (rightDemand.Kilowatts-leftDemand.Kilowatts)*endFraction
		energy := (startPower + endPower) / 2 * duration.Hours()
		if math.IsNaN(energy) || math.IsInf(energy, 0) || math.IsNaN(result.KilowattHours+energy) || math.IsInf(result.KilowattHours+energy, 0) {
			result.SkippedSegments++
			result.Issues = append(result.Issues, SegmentIssue{From: segmentFrom, To: segmentTo, Code: MetricErrorInvalidValue})
			continue
		}
		result.KilowattHours += energy
		result.Covered += duration
		result.Segments++
	}
	result.Skipped -= result.Covered
	if result.Skipped < 0 {
		result.Skipped = 0
	}
	appendBoundaryCoverageIssues(&result, metrics, from, to)
	return result
}

func appendBoundaryCoverageIssues(result *EnergyMetric, metrics []BatchMetrics, from, to time.Time) {
	first := metrics[0].BatchAt
	last := metrics[len(metrics)-1].BatchAt
	if from.Before(first) {
		result.Issues = append(result.Issues, SegmentIssue{From: from, To: earlierTime(to, first), Code: MetricErrorNoCoverage})
	}
	if last.Before(to) {
		result.Issues = append(result.Issues, SegmentIssue{From: laterTime(from, last), To: to, Code: MetricErrorNoCoverage})
	}
	sort.SliceStable(result.Issues, func(left, right int) bool {
		if result.Issues[left].From.Equal(result.Issues[right].From) {
			return result.Issues[left].To.Before(result.Issues[right].To)
		}
		return result.Issues[left].From.Before(result.Issues[right].From)
	})
}

func periodCost(electrical EnergyMetric, rate float64) RatioMetric {
	if electrical.Covered == 0 {
		return RatioMetric{Error: "electrical energy has no covered duration"}
	}
	value := electrical.KilowattHours * rate
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return RatioMetric{Error: "cost is not finite"}
	}
	return RatioMetric{Value: value, Valid: true}
}

func periodTagCost(metrics []BatchMetrics, from, to time.Time, maxGap time.Duration, electrical EnergyMetric) RatioMetric {
	if electrical.Covered == 0 {
		return RatioMetric{Error: "electrical energy has no covered duration"}
	}
	var covered time.Duration
	var value float64
	for index := 1; index < len(metrics); index++ {
		left, right := metrics[index-1], metrics[index]
		segmentFrom := laterTime(from, left.BatchAt)
		segmentTo := earlierTime(to, right.BatchAt)
		if !segmentFrom.Before(segmentTo) || right.BatchAt.Sub(left.BatchAt) > maxGap || !left.Electrical.Valid || !right.Electrical.Valid {
			continue
		}
		if !left.Tariff.Valid {
			continue
		}
		fullDuration := right.BatchAt.Sub(left.BatchAt)
		startFraction := float64(segmentFrom.Sub(left.BatchAt)) / float64(fullDuration)
		endFraction := float64(segmentTo.Sub(left.BatchAt)) / float64(fullDuration)
		startPower := left.Electrical.Kilowatts + (right.Electrical.Kilowatts-left.Electrical.Kilowatts)*startFraction
		endPower := left.Electrical.Kilowatts + (right.Electrical.Kilowatts-left.Electrical.Kilowatts)*endFraction
		energy := (startPower + endPower) / 2 * segmentTo.Sub(segmentFrom).Hours()
		segmentCost := energy * left.Tariff.RatePerKWh
		if math.IsNaN(segmentCost) || math.IsInf(segmentCost, 0) || math.IsNaN(value+segmentCost) || math.IsInf(value+segmentCost, 0) {
			return RatioMetric{Error: "cost is not finite"}
		}
		value += segmentCost
		covered += segmentTo.Sub(segmentFrom)
	}
	if covered != electrical.Covered {
		return RatioMetric{Error: "tariff Tag coverage is incomplete"}
	}
	return RatioMetric{Value: value, Valid: true}
}

func periodCOP(electrical, thermal EnergyMetric) RatioMetric {
	if electrical.Covered == 0 || thermal.Covered == 0 {
		return RatioMetric{Error: "electrical and thermal energy must both have covered duration"}
	}
	if electrical.KilowattHours <= 0 {
		return RatioMetric{Error: "electrical energy must be positive"}
	}
	value := thermal.KilowattHours / electrical.KilowattHours
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return RatioMetric{Error: "COP is not finite"}
	}
	return RatioMetric{Value: value, Valid: true}
}

func cloneBatchMetrics(metrics BatchMetrics) BatchMetrics {
	metrics.Electrical.Errors = cloneMetricErrors(metrics.Electrical.Errors)
	metrics.Thermal.Errors = cloneMetricErrors(metrics.Thermal.Errors)
	metrics.Tariff.Errors = cloneMetricErrors(metrics.Tariff.Errors)
	return metrics
}

func cloneMetricErrors(values []MetricError) []MetricError {
	return append([]MetricError(nil), values...)
}

func laterTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

func earlierTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
