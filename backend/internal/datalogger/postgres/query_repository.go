package dataloggerpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"gorm.io/gorm"
)

const queryBucketExpression = "date_bin(make_interval(secs => ?), batch_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')"

type aggregateQueryRow struct {
	BucketAt       time.Time       `gorm:"column:bucket_at"`
	TagID          uuid.UUID       `gorm:"column:tag_id"`
	DataType       string          `gorm:"column:data_type"`
	AggregateValue json.RawMessage `gorm:"column:aggregate_value"`
	GoodCount      int64           `gorm:"column:good_count"`
	BadCount       int64           `gorm:"column:bad_count"`
	TotalCount     int64           `gorm:"column:total_count"`
	LatestError    *string         `gorm:"column:latest_error"`
	Supported      bool            `gorm:"column:supported"`
}

func (repository *historyRepository) Query(ctx context.Context, input datalogger.QueryInput) (*datalogger.QueryResult, error) {
	if err := validateQueryInput(input); err != nil {
		return nil, err
	}
	if input.Mode == datalogger.QueryModeRaw {
		return repository.queryRaw(ctx, input)
	}
	return repository.queryAggregate(ctx, input)
}

func validateQueryInput(input datalogger.QueryInput) error {
	if input.LoggerID == uuid.Nil || input.From.IsZero() || input.To.IsZero() || !input.To.After(input.From) || input.Page < 1 || input.PerPage < 1 || input.PerPage > maxHistoryPerPage || len(input.TagIDs) == 0 {
		return datalogger.ErrInvalidQuery
	}
	if input.Mode == datalogger.QueryModeRaw {
		return nil
	}
	if input.Mode != datalogger.QueryModeAggregate || input.Bucket.Seconds() == 0 {
		return datalogger.ErrInvalidQuery
	}
	if len(input.Aggregates) == 0 {
		if !input.Aggregate.Valid() {
			return datalogger.ErrInvalidQuery
		}
		return nil
	}
	if len(input.Aggregates) != len(input.TagIDs) {
		return datalogger.ErrInvalidQuery
	}
	for _, tagID := range input.TagIDs {
		if !input.Aggregates[tagID].Valid() {
			return datalogger.ErrInvalidQuery
		}
	}
	return nil
}

func applyQueryFilters(query *gorm.DB, input datalogger.QueryInput) *gorm.DB {
	return query.Where("logger_id = ?", input.LoggerID).
		Where("tag_id IN ?", input.TagIDs).
		Where("batch_at >= ? AND batch_at < ?", input.From.UTC(), input.To.UTC())
}

func (repository *historyRepository) queryRaw(ctx context.Context, input datalogger.QueryInput) (*datalogger.QueryResult, error) {
	base := applyQueryFilters(repository.db.WithContext(ctx).Table("tag_values_raw"), input)
	var total int64
	if err := repository.db.WithContext(ctx).Table("(?) AS batches", base.Select("DISTINCT batch_at")).Count(&total).Error; err != nil {
		return nil, err
	}
	batchTimes := make([]time.Time, 0, input.PerPage)
	if err := base.Select("DISTINCT batch_at").Order("batch_at DESC").Offset((input.Page-1)*input.PerPage).Limit(input.PerPage).Pluck("batch_at", &batchTimes).Error; err != nil {
		return nil, err
	}
	rows := make([]rawValueRow, 0)
	if len(batchTimes) > 0 {
		if err := applyQueryFilters(repository.db.WithContext(ctx).Table("tag_values_raw"), input).
			Where("batch_at IN ?", batchTimes).
			Select("logger_id, tag_id, batch_at, observed_at, data_type, value, quality, error_message, persisted_at").
			Order("batch_at DESC, tag_id ASC").Scan(&rows).Error; err != nil {
			return nil, err
		}
	}
	resultRows := make([]datalogger.QueryRow, 0, len(batchTimes))
	byBatch := make(map[time.Time]int, len(batchTimes))
	for _, batchAt := range batchTimes {
		batchAt = batchAt.UTC()
		byBatch[batchAt] = len(resultRows)
		resultRows = append(resultRows, datalogger.QueryRow{At: batchAt, Values: map[string]datalogger.QueryValue{}})
	}
	for _, row := range rows {
		value, err := decodeRawValue(row)
		if err != nil {
			return nil, err
		}
		index, exists := byBatch[value.BatchAt]
		if !exists {
			continue
		}
		observedAt := value.ObservedAt
		resultRows[index].Values[value.TagID.String()] = datalogger.QueryValue{TagID: value.TagID, DataType: value.DataType, Value: value.Value, Quality: value.Quality, Error: value.Error, ObservedAt: &observedAt}
	}
	return queryResult(input, resultRows, total), nil
}

func (repository *historyRepository) queryAggregate(ctx context.Context, input datalogger.QueryInput) (*datalogger.QueryResult, error) {
	seconds := input.Bucket.Seconds()
	base := applyQueryFilters(repository.db.WithContext(ctx).Table("tag_values_raw"), input)
	bucketBase := base.Select("DISTINCT "+queryBucketExpression+" AS bucket_at", seconds)
	var total int64
	if err := repository.db.WithContext(ctx).Table("(?) AS buckets", bucketBase).Count(&total).Error; err != nil {
		return nil, err
	}
	buckets := make([]time.Time, 0, input.PerPage)
	if err := bucketBase.Order("bucket_at DESC").Offset((input.Page-1)*input.PerPage).Limit(input.PerPage).Pluck("bucket_at", &buckets).Error; err != nil {
		return nil, err
	}
	rows := make([]aggregateQueryRow, 0)
	aggregateByTag := make(map[uuid.UUID]datalogger.AggregateFunction, len(input.TagIDs))
	if len(buckets) > 0 {
		groups := make(map[datalogger.AggregateFunction][]uuid.UUID)
		for _, tagID := range input.TagIDs {
			function := input.Aggregate
			if len(input.Aggregates) > 0 {
				function = input.Aggregates[tagID]
			}
			aggregateByTag[tagID] = function
			groups[function] = append(groups[function], tagID)
		}
		for function, tagIDs := range groups {
			aggregateExpression, supportedExpression := aggregateSQL(function)
			selection := queryBucketExpression + ` AS bucket_at, tag_id,
			CASE WHEN COUNT(DISTINCT data_type) = 1 THEN MAX(data_type) ELSE 'mixed' END AS data_type,
			` + aggregateExpression + ` AS aggregate_value,
			COUNT(*) FILTER (WHERE quality = 'good') AS good_count,
			COUNT(*) FILTER (WHERE quality = 'bad') AS bad_count,
			COUNT(*) AS total_count,
			(ARRAY_AGG(error_message ORDER BY batch_at DESC) FILTER (WHERE quality = 'bad'))[1] AS latest_error,
			` + supportedExpression + ` AS supported`
			groupInput := input
			groupInput.TagIDs = tagIDs
			groupRows := make([]aggregateQueryRow, 0)
			query := applyQueryFilters(repository.db.WithContext(ctx).Table("tag_values_raw"), groupInput).
				Where(queryBucketExpression+" IN ?", seconds, buckets).
				Select(selection, seconds).
				Group("bucket_at, tag_id").Order("bucket_at DESC, tag_id ASC")
			if err := query.Scan(&groupRows).Error; err != nil {
				return nil, err
			}
			rows = append(rows, groupRows...)
		}
	}
	resultRows := make([]datalogger.QueryRow, 0, len(buckets))
	byBucket := make(map[time.Time]int, len(buckets))
	for _, bucketAt := range buckets {
		bucketAt = bucketAt.UTC()
		byBucket[bucketAt] = len(resultRows)
		resultRows = append(resultRows, datalogger.QueryRow{At: bucketAt, Values: map[string]datalogger.QueryValue{}})
	}
	for _, row := range rows {
		index, exists := byBucket[row.BucketAt.UTC()]
		if !exists {
			continue
		}
		value, err := decodeAggregateValue(row.AggregateValue, row.DataType, aggregateByTag[row.TagID])
		if err != nil {
			return nil, err
		}
		supported := row.Supported
		cell := datalogger.QueryValue{TagID: row.TagID, DataType: row.DataType, Value: value, GoodCount: row.GoodCount, BadCount: row.BadCount, TotalCount: row.TotalCount, Supported: &supported}
		if row.LatestError != nil {
			cell.Error = *row.LatestError
		}
		resultRows[index].Values[row.TagID.String()] = cell
	}
	return queryResult(input, resultRows, total), nil
}

func aggregateSQL(function datalogger.AggregateFunction) (string, string) {
	supported := "BOOL_AND(data_type <> 'bool')"
	switch function {
	case datalogger.AggregateMin:
		return "TO_JSONB(MIN(CASE WHEN quality = 'good' AND data_type <> 'bool' THEN (value #>> '{}')::DOUBLE PRECISION END))", supported
	case datalogger.AggregateMax:
		return "TO_JSONB(MAX(CASE WHEN quality = 'good' AND data_type <> 'bool' THEN (value #>> '{}')::DOUBLE PRECISION END))", supported
	case datalogger.AggregateAvg:
		return "TO_JSONB(AVG(CASE WHEN quality = 'good' AND data_type <> 'bool' THEN (value #>> '{}')::DOUBLE PRECISION END))", supported
	case datalogger.AggregateSum:
		return "TO_JSONB(SUM(CASE WHEN quality = 'good' AND data_type <> 'bool' THEN (value #>> '{}')::DOUBLE PRECISION END))", supported
	case datalogger.AggregateCount:
		return "TO_JSONB(COUNT(*) FILTER (WHERE quality = 'good'))", "TRUE"
	case datalogger.AggregateFirst:
		return "(ARRAY_AGG(value ORDER BY batch_at ASC) FILTER (WHERE quality = 'good'))[1]", "TRUE"
	case datalogger.AggregateLast:
		return "(ARRAY_AGG(value ORDER BY batch_at DESC) FILTER (WHERE quality = 'good'))[1]", "TRUE"
	default:
		return "NULL::JSONB", "FALSE"
	}
}

func decodeAggregateValue(payload json.RawMessage, dataType string, function datalogger.AggregateFunction) (any, error) {
	if len(payload) == 0 || string(payload) == "null" {
		return nil, nil
	}
	if function != datalogger.AggregateFirst && function != datalogger.AggregateLast {
		var value float64
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, fmt.Errorf("%w: decoding aggregate value: %v", datalogger.ErrInvalidQuery, err)
		}
		return value, nil
	}
	if dataType == "mixed" {
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(payload)))
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: decoding mixed aggregate value: %v", datalogger.ErrInvalidQuery, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, datalogger.ErrInvalidQuery
		}
		return value, nil
	}
	decoded, err := decodeRawValue(rawValueRow{DataType: dataType, Value: payload, Quality: datalogger.RawQualityGood})
	if err != nil {
		return nil, err
	}
	return decoded.Value, nil
}

func queryResult(input datalogger.QueryInput, rows []datalogger.QueryRow, total int64) *datalogger.QueryResult {
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &datalogger.QueryResult{Data: rows, Mode: input.Mode, Bucket: input.Bucket, Aggregate: input.Aggregate, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}
}
