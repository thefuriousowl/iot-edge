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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"gorm.io/gorm"
)

const (
	defaultHistoryPerPage = 100
	maxHistoryPerPage     = 500
)

type historyRepository struct{ db *gorm.DB }

type rawValueRow struct {
	LoggerID    uuid.UUID       `gorm:"column:logger_id"`
	TagID       uuid.UUID       `gorm:"column:tag_id"`
	BatchAt     time.Time       `gorm:"column:batch_at"`
	ObservedAt  time.Time       `gorm:"column:observed_at"`
	DataType    string          `gorm:"column:data_type"`
	Value       json.RawMessage `gorm:"column:value"`
	Quality     string          `gorm:"column:quality"`
	Error       *string         `gorm:"column:error_message"`
	PersistedAt time.Time       `gorm:"column:persisted_at"`
}

type selectedRawTag struct {
	ID       uuid.UUID `gorm:"column:id"`
	DataType string    `gorm:"column:data_type"`
}

type normalizedRawSample struct {
	datalogger.RawSample
	payload any
}

func NewHistoryRepository(db *gorm.DB) datalogger.HistoryRepository {
	return &historyRepository{db: db}
}

func (repository *historyRepository) WriteBatch(ctx context.Context, batch datalogger.RawBatch) error {
	batch.BatchAt = batch.BatchAt.UTC()
	if batch.LoggerID == uuid.Nil || batch.BatchAt.IsZero() || len(batch.Samples) == 0 {
		return datalogger.ErrInvalidRawBatch
	}
	samples := make([]normalizedRawSample, 0, len(batch.Samples))
	seen := make(map[uuid.UUID]struct{}, len(batch.Samples))
	for _, sample := range batch.Samples {
		if _, exists := seen[sample.TagID]; exists {
			return fmt.Errorf("%w: duplicate Tag %s", datalogger.ErrInvalidRawBatch, sample.TagID)
		}
		seen[sample.TagID] = struct{}{}
		normalized, err := normalizeRawSample(sample)
		if err != nil {
			return err
		}
		samples = append(samples, normalized)
	}

	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateSelectedRawTags(tx, batch.LoggerID, samples); err != nil {
			return err
		}
		if err := ensureRawPartition(tx, batch.BatchAt); err != nil {
			return err
		}
		return insertRawBatch(tx, batch.LoggerID, batch.BatchAt, samples)
	})
	return mapHistoryError(err)
}

func (repository *historyRepository) ListValues(ctx context.Context, input datalogger.RawValueListInput) (*datalogger.RawValueListResult, error) {
	if input.LoggerID == uuid.Nil || input.TagID != nil && *input.TagID == uuid.Nil {
		return nil, datalogger.ErrInvalidRawBatch
	}
	if input.From != nil {
		from := input.From.UTC()
		input.From = &from
	}
	if input.To != nil {
		to := input.To.UTC()
		input.To = &to
	}
	if input.From != nil && input.To != nil && !input.To.After(*input.From) {
		return nil, datalogger.ErrInvalidRawBatch
	}
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = defaultHistoryPerPage
	}
	if input.PerPage > maxHistoryPerPage {
		return nil, datalogger.ErrInvalidRawBatch
	}
	query := repository.db.WithContext(ctx).Table("tag_values_raw").Where("logger_id = ?", input.LoggerID)
	if input.TagID != nil {
		query = query.Where("tag_id = ?", *input.TagID)
	}
	if input.From != nil {
		query = query.Where("batch_at >= ?", *input.From)
	}
	if input.To != nil {
		query = query.Where("batch_at < ?", *input.To)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	rows := make([]rawValueRow, 0)
	err := query.Select("logger_id, tag_id, batch_at, observed_at, data_type, value, quality, error_message, persisted_at").
		Order("batch_at DESC, tag_id ASC").
		Offset((input.Page - 1) * input.PerPage).
		Limit(input.PerPage).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	values := make([]datalogger.RawValue, 0, len(rows))
	for _, row := range rows {
		value, decodeErr := decodeRawValue(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		values = append(values, value)
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &datalogger.RawValueListResult{Data: values, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func normalizeRawSample(sample datalogger.RawSample) (normalizedRawSample, error) {
	sample.ObservedAt = sample.ObservedAt.UTC()
	if sample.TagID == uuid.Nil || sample.ObservedAt.IsZero() || !validRawDataType(sample.DataType) {
		return normalizedRawSample{}, datalogger.ErrInvalidRawBatch
	}
	normalized := normalizedRawSample{RawSample: sample}
	switch sample.Quality {
	case datalogger.RawQualityGood:
		if sample.Error != "" {
			return normalizedRawSample{}, fmt.Errorf("%w: good quality cannot include an error", datalogger.ErrInvalidRawBatch)
		}
		payload, err := json.Marshal(sample.Value)
		if err != nil || string(payload) == "null" {
			return normalizedRawSample{}, fmt.Errorf("%w: invalid %s value", datalogger.ErrInvalidRawBatch, sample.DataType)
		}
		normalized.payload = string(payload)
	case datalogger.RawQualityBad:
		sample.Error = strings.TrimSpace(sample.Error)
		if sample.Value != nil || sample.Error == "" {
			return normalizedRawSample{}, fmt.Errorf("%w: bad quality requires only an error", datalogger.ErrInvalidRawBatch)
		}
		normalized.RawSample = sample
	default:
		return normalizedRawSample{}, fmt.Errorf("%w: unsupported quality %q", datalogger.ErrInvalidRawBatch, sample.Quality)
	}
	return normalized, nil
}

func validateSelectedRawTags(tx *gorm.DB, loggerID uuid.UUID, samples []normalizedRawSample) error {
	var exists bool
	if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM data_loggers WHERE id = ?)", loggerID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return datalogger.ErrLoggerNotFound
	}
	tagIDs := make([]uuid.UUID, 0, len(samples))
	for _, sample := range samples {
		tagIDs = append(tagIDs, sample.TagID)
	}
	selected := make([]selectedRawTag, 0, len(samples))
	err := tx.Table("data_logger_tags AS selections").
		Select("tags.id, tags.data_type").
		Joins("JOIN tags ON tags.id = selections.tag_id").
		Where("selections.logger_id = ? AND selections.tag_id IN ?", loggerID, tagIDs).
		Scan(&selected).Error
	if err != nil {
		return err
	}
	if len(selected) != len(samples) {
		return datalogger.ErrRawTagNotSelected
	}
	dataTypes := make(map[uuid.UUID]string, len(selected))
	for _, tag := range selected {
		dataTypes[tag.ID] = tag.DataType
	}
	for _, sample := range samples {
		if dataTypes[sample.TagID] != sample.DataType {
			return fmt.Errorf("%w: Tag %s data type does not match", datalogger.ErrInvalidRawBatch, sample.TagID)
		}
	}
	return nil
}

func ensureRawPartition(tx *gorm.DB, batchAt time.Time) error {
	start := time.Date(batchAt.Year(), batchAt.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	name := fmt.Sprintf("tag_values_raw_%04d_%02d", start.Year(), int(start.Month()))
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext('iot_edge_tag_values_raw_partition'))").Error; err != nil {
		return err
	}
	statement := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF tag_values_raw FOR VALUES FROM ('%s') TO ('%s')",
		name,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	return tx.Exec(statement).Error
}

func insertRawBatch(tx *gorm.DB, loggerID uuid.UUID, batchAt time.Time, samples []normalizedRawSample) error {
	values := make([]string, 0, len(samples))
	arguments := make([]any, 0, len(samples)*8)
	for _, sample := range samples {
		values = append(values, "(?, ?, ?, ?, ?, CAST(? AS JSONB), ?, ?, CURRENT_TIMESTAMP)")
		var errorMessage any
		if sample.Quality == datalogger.RawQualityBad {
			errorMessage = sample.Error
		}
		arguments = append(arguments, loggerID, sample.TagID, batchAt, sample.ObservedAt, sample.DataType, sample.payload, sample.Quality, errorMessage)
	}
	statement := `
		INSERT INTO tag_values_raw (
			logger_id, tag_id, batch_at, observed_at, data_type, value, quality, error_message, persisted_at
		) VALUES ` + strings.Join(values, ",") + `
		ON CONFLICT (logger_id, tag_id, batch_at) DO NOTHING
	`
	return tx.Exec(statement, arguments...).Error
}

func decodeRawValue(row rawValueRow) (datalogger.RawValue, error) {
	value := datalogger.RawValue{
		LoggerID: row.LoggerID, TagID: row.TagID, BatchAt: row.BatchAt.UTC(), ObservedAt: row.ObservedAt.UTC(),
		DataType: row.DataType, Quality: row.Quality, PersistedAt: row.PersistedAt.UTC(),
	}
	if row.Quality == datalogger.RawQualityBad {
		if row.Error != nil {
			value.Error = *row.Error
		}
		return value, nil
	}
	var target any
	switch row.DataType {
	case "bool":
		target = new(bool)
	case "int16":
		target = new(int16)
	case "uint16":
		target = new(uint16)
	case "int32":
		target = new(int32)
	case "uint32":
		target = new(uint32)
	case "float32":
		target = new(float32)
	case "float64":
		target = new(float64)
	default:
		return datalogger.RawValue{}, fmt.Errorf("%w: unsupported persisted data type %q", datalogger.ErrInvalidRawBatch, row.DataType)
	}
	decoder := json.NewDecoder(strings.NewReader(string(row.Value)))
	if err := decoder.Decode(target); err != nil {
		return datalogger.RawValue{}, fmt.Errorf("%w: decoding persisted %s value: %v", datalogger.ErrInvalidRawBatch, row.DataType, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return datalogger.RawValue{}, fmt.Errorf("%w: persisted value contains multiple documents", datalogger.ErrInvalidRawBatch)
	}
	switch typed := target.(type) {
	case *bool:
		value.Value = *typed
	case *int16:
		value.Value = *typed
	case *uint16:
		value.Value = *typed
	case *int32:
		value.Value = *typed
	case *uint32:
		value.Value = *typed
	case *float32:
		value.Value = *typed
	case *float64:
		value.Value = *typed
	}
	return value, nil
}

func validRawDataType(value string) bool {
	switch value {
	case "bool", "int16", "uint16", "int32", "uint32", "float32", "float64":
		return true
	default:
		return false
	}
}

func mapHistoryError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "tag_values_raw_logger_id_fkey":
		return datalogger.ErrLoggerNotFound
	case "tag_values_raw_tag_id_fkey":
		return datalogger.ErrRawTagNotSelected
	case "tag_values_raw_data_type_check", "tag_values_raw_quality_check", "tag_values_raw_payload_check":
		return datalogger.ErrInvalidRawBatch
	default:
		return err
	}
}

var _ datalogger.HistoryRepository = (*historyRepository)(nil)
