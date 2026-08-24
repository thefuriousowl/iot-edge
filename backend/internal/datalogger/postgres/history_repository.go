package dataloggerpostgres

import (
	"context"
	"database/sql"
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
	defaultHistoryPerPage        = 100
	maxHistoryPerPage            = 500
	estimatedIndexBytesPerRawRow = 192
)

type HistoryRepositoryOption func(*historyRepository)

func WithCommittedBatchPublisher(publisher datalogger.CommittedBatchPublisher) HistoryRepositoryOption {
	return func(repository *historyRepository) {
		if publisher != nil {
			repository.publisher = publisher
		}
	}
}

type historyRepository struct {
	db        *gorm.DB
	publisher datalogger.CommittedBatchPublisher
}

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

type selectedBatchRawValueRow struct {
	BatchAt     time.Time       `gorm:"column:batch_at"`
	HasSample   bool            `gorm:"column:has_sample"`
	LoggerID    *uuid.UUID      `gorm:"column:logger_id"`
	TagID       *uuid.UUID      `gorm:"column:tag_id"`
	ObservedAt  *time.Time      `gorm:"column:observed_at"`
	DataType    *string         `gorm:"column:data_type"`
	Value       json.RawMessage `gorm:"column:value"`
	Quality     *string         `gorm:"column:quality"`
	Error       *string         `gorm:"column:error_message"`
	PersistedAt *time.Time      `gorm:"column:persisted_at"`
}

type selectedRawTag struct {
	ID       uuid.UUID `gorm:"column:id"`
	DataType string    `gorm:"column:data_type"`
}

type normalizedRawSample struct {
	datalogger.RawSample
	payload any
}

type rawHistoryOverview struct {
	LastBatchAt *time.Time `gorm:"column:last_batch_at"`
}

type loggerStoragePolicy struct {
	MaxSizeBytes  *int64 `gorm:"column:max_size_bytes"`
	MaxAgeSeconds *int64 `gorm:"column:max_age_seconds"`
}

type batchStorageRow struct {
	BatchAt            time.Time `gorm:"column:batch_at"`
	RowCount           int64     `gorm:"column:row_count"`
	EstimatedSizeBytes int64     `gorm:"column:estimated_size_bytes"`
}

type storageOverview struct {
	RowCount           int64      `gorm:"column:row_count"`
	BatchCount         int64      `gorm:"column:batch_count"`
	EstimatedSizeBytes int64      `gorm:"column:estimated_size_bytes"`
	OldestBatchAt      *time.Time `gorm:"column:oldest_batch_at"`
	NewestBatchAt      *time.Time `gorm:"column:newest_batch_at"`
}

type managementLoggerCounts struct {
	LoggerCount        int64 `gorm:"column:logger_count"`
	EnabledLoggerCount int64 `gorm:"column:enabled_logger_count"`
	PolicyLoggerCount  int64 `gorm:"column:policy_logger_count"`
}

type physicalStorageOverview struct {
	RawHistoryBytes      int64 `gorm:"column:raw_history_bytes"`
	BatchAccountingBytes int64 `gorm:"column:batch_accounting_bytes"`
}

type retentionSelection struct {
	Plan       datalogger.RetentionPlan
	Candidates []batchStorageRow
}

type retentionStatusRow struct {
	LastStartedAt   time.Time       `gorm:"column:last_started_at"`
	LastCompletedAt time.Time       `gorm:"column:last_completed_at"`
	LastResult      json.RawMessage `gorm:"column:last_result"`
	LastError       *string         `gorm:"column:last_error"`
}

func NewHistoryRepository(db *gorm.DB, options ...HistoryRepositoryOption) datalogger.HistoryRepository {
	repository := &historyRepository{db: db}
	for _, option := range options {
		if option != nil {
			option(repository)
		}
	}
	return repository
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

	var committed *datalogger.RawBatch
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := lockLoggerStoragePolicy(tx, batch.LoggerID)
		if err != nil {
			return err
		}
		if err := validateSelectedRawTags(tx, batch.LoggerID, samples); err != nil {
			return err
		}
		if err := ensureRawPartition(tx, batch.BatchAt); err != nil {
			return err
		}
		inserted, err := insertRawBatch(tx, batch.LoggerID, batch.BatchAt, samples)
		if err != nil {
			return err
		}
		if err := refreshBatchStorage(tx, batch.LoggerID, batch.BatchAt); err != nil {
			return err
		}
		if err := enforceRetention(tx, batch.LoggerID, policy, time.Now().UTC()); err != nil {
			return err
		}
		if !inserted {
			return nil
		}
		committed, err = readRawBatch(tx, batch.LoggerID, batch.BatchAt)
		if errors.Is(err, datalogger.ErrRawBatchNotFound) {
			committed = nil
			return nil
		}
		return err
	})
	if err != nil {
		return mapHistoryError(err)
	}
	if committed != nil && repository.publisher != nil {
		publishCommittedBatch(repository.publisher, *committed)
	}
	return nil
}

func (repository *historyRepository) LatestBatch(ctx context.Context, loggerID uuid.UUID) (*datalogger.RawBatch, error) {
	if loggerID == uuid.Nil {
		return nil, datalogger.ErrInvalidInput
	}
	var latest struct {
		BatchAt time.Time `gorm:"column:batch_at"`
	}
	result := repository.db.WithContext(ctx).Table("data_logger_batches").
		Select("batch_at").
		Where("logger_id = ?", loggerID).
		Order("batch_at DESC").
		Limit(1).
		Scan(&latest)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, datalogger.ErrRawBatchNotFound
	}
	return readRawBatch(repository.db.WithContext(ctx), loggerID, latest.BatchAt)
}

func (repository *historyRepository) ListBatches(ctx context.Context, input datalogger.RawBatchListInput) ([]datalogger.RawBatch, error) {
	if input.LoggerID == uuid.Nil || len(input.TagIDs) == 0 || input.From.IsZero() || input.To.IsZero() || input.To.Before(input.From) {
		return nil, datalogger.ErrInvalidInput
	}
	seenTags := make(map[uuid.UUID]struct{}, len(input.TagIDs))
	for _, tagID := range input.TagIDs {
		if tagID == uuid.Nil {
			return nil, datalogger.ErrInvalidInput
		}
		if _, duplicate := seenTags[tagID]; duplicate {
			return nil, datalogger.ErrInvalidInput
		}
		seenTags[tagID] = struct{}{}
	}
	rows := make([]selectedBatchRawValueRow, 0)
	if err := repository.db.WithContext(ctx).Raw(`
		WITH selected_batches AS (
			SELECT batch_at
			FROM data_logger_batches
			WHERE logger_id = ? AND batch_at >= ? AND batch_at <= ?
			UNION
			SELECT batch_at
			FROM (
				SELECT batch_at
				FROM data_logger_batches
				WHERE ? AND logger_id = ? AND batch_at < ?
				ORDER BY batch_at DESC
				LIMIT 1
			) AS preceding_batch
			UNION
			SELECT batch_at
			FROM (
				SELECT batch_at
				FROM data_logger_batches
				WHERE ? AND logger_id = ? AND batch_at > ?
				ORDER BY batch_at ASC
				LIMIT 1
			) AS following_batch
		)
		SELECT selected_batches.batch_at,
			raw.tag_id IS NOT NULL AS has_sample,
			raw.logger_id, raw.tag_id, raw.observed_at, raw.data_type, raw.value,
			raw.quality, raw.error_message, raw.persisted_at
		FROM selected_batches
		LEFT JOIN tag_values_raw AS raw
			ON raw.logger_id = ?
			AND raw.batch_at = selected_batches.batch_at
			AND raw.tag_id IN ?
		ORDER BY selected_batches.batch_at ASC, raw.tag_id ASC`,
		input.LoggerID, input.From.UTC(), input.To.UTC(),
		input.IncludeNeighbors, input.LoggerID, input.From.UTC(),
		input.IncludeNeighbors, input.LoggerID, input.To.UTC(),
		input.LoggerID, input.TagIDs,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	batches := make([]datalogger.RawBatch, 0)
	for _, row := range rows {
		batchAt := row.BatchAt.UTC()
		if len(batches) == 0 || !batches[len(batches)-1].BatchAt.Equal(batchAt) {
			batches = append(batches, datalogger.RawBatch{LoggerID: input.LoggerID, BatchAt: batchAt, Samples: []datalogger.RawSample{}})
		}
		if !row.HasSample {
			continue
		}
		value, err := decodeSelectedBatchRawValue(row)
		if err != nil {
			return nil, err
		}
		batches[len(batches)-1].Samples = append(batches[len(batches)-1].Samples, datalogger.RawSample{TagID: value.TagID, ObservedAt: value.ObservedAt, DataType: value.DataType, Value: value.Value, Quality: value.Quality, Error: value.Error})
	}
	return batches, nil
}

func decodeSelectedBatchRawValue(row selectedBatchRawValueRow) (datalogger.RawValue, error) {
	if row.LoggerID == nil || row.TagID == nil || row.ObservedAt == nil || row.DataType == nil || row.Quality == nil || row.PersistedAt == nil {
		return datalogger.RawValue{}, errors.New("incomplete raw history sample")
	}
	return decodeRawValue(rawValueRow{
		LoggerID: *row.LoggerID, TagID: *row.TagID, BatchAt: row.BatchAt, ObservedAt: *row.ObservedAt,
		DataType: *row.DataType, Value: row.Value, Quality: *row.Quality, Error: row.Error, PersistedAt: *row.PersistedAt,
	})
}

func (repository *historyRepository) ManagementOverview(ctx context.Context, evaluatedAt time.Time) (*datalogger.DataManagementOverview, error) {
	if ctx == nil || evaluatedAt.IsZero() {
		return nil, datalogger.ErrInvalidInput
	}
	overview := &datalogger.DataManagementOverview{EvaluatedAt: evaluatedAt.UTC()}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var counts managementLoggerCounts
		if err := tx.Raw(`
			SELECT COUNT(*) AS logger_count,
				COUNT(*) FILTER (WHERE enabled) AS enabled_logger_count,
				COUNT(*) FILTER (WHERE max_size_bytes IS NOT NULL OR max_age_seconds IS NOT NULL) AS policy_logger_count
			FROM data_loggers
		`).Scan(&counts).Error; err != nil {
			return err
		}
		var logical storageOverview
		if err := tx.Table("data_logger_batches").
			Select("COALESCE(SUM(row_count), 0) AS row_count, COUNT(*) AS batch_count, COALESCE(SUM(estimated_size_bytes), 0) AS estimated_size_bytes, MIN(batch_at) AS oldest_batch_at, MAX(batch_at) AS newest_batch_at").
			Scan(&logical).Error; err != nil {
			return err
		}
		var physical physicalStorageOverview
		if err := tx.Raw(`
			SELECT
				COALESCE((SELECT SUM(pg_total_relation_size(relid)) FROM pg_partition_tree('tag_values_raw'::regclass)), 0)::BIGINT AS raw_history_bytes,
				pg_total_relation_size('data_logger_batches'::regclass)::BIGINT AS batch_accounting_bytes
		`).Scan(&physical).Error; err != nil {
			return err
		}
		overview.LoggerCount = counts.LoggerCount
		overview.EnabledLoggerCount = counts.EnabledLoggerCount
		overview.PolicyLoggerCount = counts.PolicyLoggerCount
		overview.LogicalHistory = datalogger.RetentionMetrics{
			RowCount: logical.RowCount, BatchCount: logical.BatchCount, EstimatedSizeBytes: logical.EstimatedSizeBytes,
			OldestBatchAt: utcTimePointer(logical.OldestBatchAt), NewestBatchAt: utcTimePointer(logical.NewestBatchAt),
		}
		overview.PostgreSQLPhysicalAllocation = datalogger.PostgreSQLPhysicalAllocation{
			RawHistoryBytes: physical.RawHistoryBytes, BatchAccountingBytes: physical.BatchAccountingBytes,
			TotalBytes: physical.RawHistoryBytes + physical.BatchAccountingBytes,
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, mapHistoryError(err)
	}
	return overview, nil
}

func (repository *historyRepository) Storage(ctx context.Context, loggerID uuid.UUID, tagCount int, maxSizeBytes *int64) (*datalogger.StorageStats, error) {
	if loggerID == uuid.Nil || tagCount < 0 {
		return nil, datalogger.ErrInvalidInput
	}
	var overview storageOverview
	err := repository.db.WithContext(ctx).Table("data_logger_batches").
		Select("COALESCE(SUM(row_count), 0) AS row_count, COUNT(*) AS batch_count, COALESCE(SUM(estimated_size_bytes), 0) AS estimated_size_bytes, MIN(batch_at) AS oldest_batch_at, MAX(batch_at) AS newest_batch_at").
		Where("logger_id = ?", loggerID).
		Scan(&overview).Error
	if err != nil {
		return nil, err
	}
	averageRowBytes := datalogger.DefaultEstimatedRowBytes
	if overview.RowCount > 0 {
		averageRowBytes = max(1, (overview.EstimatedSizeBytes+overview.RowCount-1)/overview.RowCount)
	}
	stats := &datalogger.StorageStats{
		RowCount: overview.RowCount, BatchCount: overview.BatchCount, EstimatedSizeBytes: overview.EstimatedSizeBytes,
		AverageRowBytes: averageRowBytes, OldestBatchAt: utcTimePointer(overview.OldestBatchAt), NewestBatchAt: utcTimePointer(overview.NewestBatchAt),
	}
	if maxSizeBytes != nil {
		capacityBatches := int64(0)
		if overview.BatchCount > 0 {
			averageBatchBytes := max(1, (overview.EstimatedSizeBytes+overview.BatchCount-1)/overview.BatchCount)
			capacityBatches = *maxSizeBytes / averageBatchBytes
		} else if tagCount > 0 {
			capacityBatches = *maxSizeBytes / (averageRowBytes * int64(tagCount))
		}
		capacityRows := capacityBatches * int64(tagCount)
		stats.EstimatedCapacityRows = &capacityRows
		stats.EstimatedCapacityBatches = &capacityBatches
	}
	return stats, nil
}

func (repository *historyRepository) Retention(ctx context.Context, loggerID uuid.UUID, evaluatedAt time.Time) (*datalogger.RetentionStatus, error) {
	if loggerID == uuid.Nil || evaluatedAt.IsZero() {
		return nil, datalogger.ErrInvalidInput
	}
	var status datalogger.RetentionStatus
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := readLoggerStoragePolicy(tx, loggerID)
		if err != nil {
			return err
		}
		batches, err := listBatchStorage(tx, loggerID)
		if err != nil {
			return err
		}
		selection, err := selectRetention(policy, batches, evaluatedAt.UTC())
		if err != nil {
			return err
		}
		status.Plan = selection.Plan
		status.LastRun, err = readRetentionRunStatus(tx, loggerID)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, mapHistoryError(err)
	}
	return &status, nil
}

func (repository *historyRepository) CleanupRetention(ctx context.Context, loggerID uuid.UUID, input datalogger.RetentionCleanupInput) (*datalogger.RetentionCleanupResult, error) {
	if loggerID == uuid.Nil || input.EvaluatedAt.IsZero() || input.BatchLimit < 1 || input.BatchLimit > datalogger.MaxRetentionBatchLimit {
		return nil, datalogger.ErrInvalidInput
	}
	startedAt := time.Now().UTC()
	var cleanupResult *datalogger.RetentionCleanupResult
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := lockLoggerStoragePolicy(tx, loggerID)
		if err != nil {
			return err
		}
		batches, err := listBatchStorage(tx, loggerID)
		if err != nil {
			return err
		}
		selection, err := selectRetention(policy, batches, input.EvaluatedAt.UTC())
		if err != nil {
			return err
		}
		remove := selection.Candidates
		if len(remove) > input.BatchLimit {
			remove = remove[:input.BatchLimit]
		}
		if err := deleteRetentionBatches(tx, loggerID, remove); err != nil {
			return err
		}
		retainedBatches, err := listBatchStorage(tx, loggerID)
		if err != nil {
			return err
		}
		remaining, err := selectRetention(policy, retainedBatches, input.EvaluatedAt.UTC())
		if err != nil {
			return err
		}
		completedAt := time.Now().UTC()
		cleanupResult = &datalogger.RetentionCleanupResult{
			StartedAt: startedAt, CompletedAt: completedAt, EvaluatedAt: input.EvaluatedAt.UTC(), CutoffAt: selection.Plan.CutoffAt,
			Policy: selection.Plan.Policy, Deleted: retentionMetrics(remove), Retained: retentionMetrics(retainedBatches),
			RemainingRemoval: remaining.Plan.Remove, Complete: remaining.Plan.Remove.BatchCount == 0,
		}
		return writeRetentionSuccess(tx, loggerID, *cleanupResult)
	})
	if err == nil {
		return cleanupResult, nil
	}
	mapped := mapHistoryError(err)
	if !errors.Is(mapped, datalogger.ErrLoggerNotFound) {
		repository.writeRetentionFailure(ctx, loggerID, startedAt, mapped)
	}
	return nil, publicRetentionError(mapped)
}

func (repository *historyRepository) EnforceRetention(ctx context.Context, loggerID uuid.UUID, expected datalogger.RetentionPolicy) error {
	if loggerID == uuid.Nil {
		return datalogger.ErrInvalidInput
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		policy, err := lockLoggerStoragePolicy(tx, loggerID)
		if err != nil {
			return err
		}
		if !equalOptionalInt64(policy.MaxSizeBytes, expected.MaxSizeBytes) || !equalOptionalInt64(policy.MaxAgeSeconds, expected.MaxAgeSeconds) {
			return datalogger.ErrInvalidInput
		}
		return enforceRetention(tx, loggerID, policy, time.Now().UTC())
	})
	return mapHistoryError(err)
}

func (repository *historyRepository) ValidateStorageLimit(ctx context.Context, loggerID uuid.UUID, maxSizeBytes *int64) error {
	if loggerID == uuid.Nil {
		return datalogger.ErrInvalidInput
	}
	if maxSizeBytes == nil {
		return nil
	}
	var newest batchStorageRow
	result := repository.db.WithContext(ctx).Table("data_logger_batches").
		Select("batch_at, row_count, estimated_size_bytes").
		Where("logger_id = ?", loggerID).
		Order("batch_at DESC").
		Limit(1).
		Scan(&newest)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 && newest.EstimatedSizeBytes > *maxSizeBytes {
		return datalogger.ErrStorageLimitTooSmall
	}
	return nil
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
	baseQuery := repository.db.WithContext(ctx).Table("tag_values_raw").Where("logger_id = ?", input.LoggerID)
	var overview rawHistoryOverview
	if err := baseQuery.Select("MAX(batch_at) AS last_batch_at").Scan(&overview).Error; err != nil {
		return nil, err
	}
	query := baseQuery
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
	return &datalogger.RawValueListResult{Data: values, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages, LastBatchAt: overview.LastBatchAt}, nil
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

func insertRawBatch(tx *gorm.DB, loggerID uuid.UUID, batchAt time.Time, samples []normalizedRawSample) (bool, error) {
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
	result := tx.Exec(statement, arguments...)
	return result.RowsAffected > 0, result.Error
}

func readRawBatch(query *gorm.DB, loggerID uuid.UUID, batchAt time.Time) (*datalogger.RawBatch, error) {
	rows := make([]rawValueRow, 0)
	result := query.Table("tag_values_raw").
		Select("logger_id, tag_id, batch_at, observed_at, data_type, value, quality, error_message, persisted_at").
		Where("logger_id = ? AND batch_at = ?", loggerID, batchAt.UTC()).
		Order("tag_id ASC").
		Scan(&rows)
	if result.Error != nil {
		return nil, result.Error
	}
	if len(rows) == 0 {
		return nil, datalogger.ErrRawBatchNotFound
	}
	batch := &datalogger.RawBatch{LoggerID: loggerID, BatchAt: batchAt.UTC(), Samples: make([]datalogger.RawSample, 0, len(rows))}
	for _, row := range rows {
		value, err := decodeRawValue(row)
		if err != nil {
			return nil, err
		}
		batch.Samples = append(batch.Samples, datalogger.RawSample{
			TagID: value.TagID, ObservedAt: value.ObservedAt, DataType: value.DataType,
			Value: value.Value, Quality: value.Quality, Error: value.Error,
		})
	}
	return batch, nil
}

func publishCommittedBatch(publisher datalogger.CommittedBatchPublisher, batch datalogger.RawBatch) {
	defer func() { _ = recover() }()
	publisher.PublishCommittedBatch(batch)
}

func lockLoggerStoragePolicy(tx *gorm.DB, loggerID uuid.UUID) (loggerStoragePolicy, error) {
	var policy loggerStoragePolicy
	result := tx.Raw("SELECT max_size_bytes, max_age_seconds FROM data_loggers WHERE id = ? FOR UPDATE", loggerID).Scan(&policy)
	if result.Error != nil {
		return loggerStoragePolicy{}, result.Error
	}
	if result.RowsAffected == 0 {
		return loggerStoragePolicy{}, datalogger.ErrLoggerNotFound
	}
	return policy, nil
}

func readLoggerStoragePolicy(tx *gorm.DB, loggerID uuid.UUID) (loggerStoragePolicy, error) {
	var policy loggerStoragePolicy
	result := tx.Raw("SELECT max_size_bytes, max_age_seconds FROM data_loggers WHERE id = ?", loggerID).Scan(&policy)
	if result.Error != nil {
		return loggerStoragePolicy{}, result.Error
	}
	if result.RowsAffected == 0 {
		return loggerStoragePolicy{}, datalogger.ErrLoggerNotFound
	}
	return policy, nil
}

func refreshBatchStorage(tx *gorm.DB, loggerID uuid.UUID, batchAt time.Time) error {
	return tx.Exec(`
		INSERT INTO data_logger_batches (logger_id, batch_at, row_count, estimated_size_bytes)
		SELECT logger_id, batch_at, COUNT(*)::INTEGER, SUM(pg_column_size(raw_values) + ?)::BIGINT
		FROM tag_values_raw AS raw_values
		WHERE logger_id = ? AND batch_at = ?
		GROUP BY logger_id, batch_at
		ON CONFLICT (logger_id, batch_at) DO UPDATE SET
			row_count = EXCLUDED.row_count,
			estimated_size_bytes = EXCLUDED.estimated_size_bytes
	`, estimatedIndexBytesPerRawRow, loggerID, batchAt).Error
}

func enforceRetention(tx *gorm.DB, loggerID uuid.UUID, policy loggerStoragePolicy, evaluatedAt time.Time) error {
	batches, err := listBatchStorage(tx, loggerID)
	if err != nil {
		return err
	}
	selection, err := selectRetention(policy, batches, evaluatedAt)
	if err != nil {
		return err
	}
	remove := selection.Candidates
	if len(remove) > datalogger.MaxRetentionBatchLimit {
		remove = remove[:datalogger.MaxRetentionBatchLimit]
	}
	return deleteRetentionBatches(tx, loggerID, remove)
}

func listBatchStorage(tx *gorm.DB, loggerID uuid.UUID) ([]batchStorageRow, error) {
	batches := make([]batchStorageRow, 0)
	err := tx.Table("data_logger_batches").
		Select("batch_at, row_count, estimated_size_bytes").
		Where("logger_id = ?", loggerID).
		Order("batch_at DESC").
		Scan(&batches).Error
	return batches, err
}

func selectRetention(policy loggerStoragePolicy, batches []batchStorageRow, evaluatedAt time.Time) (retentionSelection, error) {
	evaluatedAt = evaluatedAt.UTC()
	remove := make(map[int]struct{}, len(batches))
	var cutoffAt *time.Time
	if policy.MaxAgeSeconds != nil {
		cutoff := evaluatedAt.Add(-time.Duration(*policy.MaxAgeSeconds) * time.Second)
		cutoffAt = &cutoff
		for index, batch := range batches {
			if batch.BatchAt.Before(cutoff) {
				remove[index] = struct{}{}
			}
		}
	}
	if policy.MaxSizeBytes != nil {
		retainedBytes := int64(0)
		newestRetained := -1
		for index, batch := range batches {
			if _, expired := remove[index]; expired {
				continue
			}
			if newestRetained == -1 {
				newestRetained = index
			}
			retainedBytes += batch.EstimatedSizeBytes
		}
		if newestRetained >= 0 && batches[newestRetained].EstimatedSizeBytes > *policy.MaxSizeBytes {
			return retentionSelection{}, datalogger.ErrStorageLimitTooSmall
		}
		for index := len(batches) - 1; index >= 0 && retainedBytes > *policy.MaxSizeBytes; index-- {
			if _, expired := remove[index]; expired {
				continue
			}
			remove[index] = struct{}{}
			retainedBytes -= batches[index].EstimatedSizeBytes
		}
	}
	candidates := make([]batchStorageRow, 0, len(remove))
	retained := make([]batchStorageRow, 0, len(batches)-len(remove))
	for index, batch := range batches {
		if _, selected := remove[index]; selected {
			continue
		}
		retained = append(retained, batch)
	}
	for index := len(batches) - 1; index >= 0; index-- {
		if _, selected := remove[index]; selected {
			candidates = append(candidates, batches[index])
		}
	}
	return retentionSelection{
		Plan: datalogger.RetentionPlan{
			EvaluatedAt: evaluatedAt,
			CutoffAt:    cutoffAt,
			Policy: datalogger.RetentionPolicy{
				MaxSizeBytes: policy.MaxSizeBytes, MaxAgeSeconds: policy.MaxAgeSeconds,
			},
			Current:           retentionMetrics(batches),
			Remove:            retentionMetrics(candidates),
			EstimatedRetained: retentionMetrics(retained),
		},
		Candidates: candidates,
	}, nil
}

func retentionMetrics(batches []batchStorageRow) datalogger.RetentionMetrics {
	metrics := datalogger.RetentionMetrics{}
	for _, batch := range batches {
		batchAt := batch.BatchAt.UTC()
		metrics.RowCount += batch.RowCount
		metrics.BatchCount++
		metrics.EstimatedSizeBytes += batch.EstimatedSizeBytes
		if metrics.OldestBatchAt == nil || batchAt.Before(*metrics.OldestBatchAt) {
			value := batchAt
			metrics.OldestBatchAt = &value
		}
		if metrics.NewestBatchAt == nil || batchAt.After(*metrics.NewestBatchAt) {
			value := batchAt
			metrics.NewestBatchAt = &value
		}
	}
	return metrics
}

func deleteRetentionBatches(tx *gorm.DB, loggerID uuid.UUID, batches []batchStorageRow) error {
	if len(batches) == 0 {
		return nil
	}
	batchTimes := make([]time.Time, 0, len(batches))
	for _, batch := range batches {
		batchTimes = append(batchTimes, batch.BatchAt.UTC())
	}
	if err := tx.Exec("DELETE FROM tag_values_raw WHERE logger_id = ? AND batch_at IN ?", loggerID, batchTimes).Error; err != nil {
		return err
	}
	return tx.Exec("DELETE FROM data_logger_batches WHERE logger_id = ? AND batch_at IN ?", loggerID, batchTimes).Error
}

func readRetentionRunStatus(tx *gorm.DB, loggerID uuid.UUID) (*datalogger.RetentionRunStatus, error) {
	var row retentionStatusRow
	result := tx.Table("data_logger_retention_status").
		Select("last_started_at, last_completed_at, last_result, last_error").
		Where("logger_id = ?", loggerID).
		Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	status := &datalogger.RetentionRunStatus{
		StartedAt: row.LastStartedAt.UTC(), CompletedAt: row.LastCompletedAt.UTC(), Error: row.LastError,
	}
	if len(row.LastResult) > 0 {
		var cleanupResult datalogger.RetentionCleanupResult
		if err := json.Unmarshal(row.LastResult, &cleanupResult); err != nil {
			return nil, err
		}
		status.Result = &cleanupResult
	}
	return status, nil
}

func writeRetentionSuccess(tx *gorm.DB, loggerID uuid.UUID, result datalogger.RetentionCleanupResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Exec(`
		INSERT INTO data_logger_retention_status (
			logger_id, last_started_at, last_completed_at, last_result, last_error, updated_at
		) VALUES (?, ?, ?, ?::jsonb, NULL, CURRENT_TIMESTAMP)
		ON CONFLICT (logger_id) DO UPDATE SET
			last_started_at = EXCLUDED.last_started_at,
			last_completed_at = EXCLUDED.last_completed_at,
			last_result = EXCLUDED.last_result,
			last_error = NULL,
			updated_at = CURRENT_TIMESTAMP
	`, loggerID, result.StartedAt, result.CompletedAt, string(payload)).Error
}

func (repository *historyRepository) writeRetentionFailure(ctx context.Context, loggerID uuid.UUID, startedAt time.Time, cleanupError error) {
	statusContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	completedAt := time.Now().UTC()
	message := sanitizedRetentionError(cleanupError)
	_ = repository.db.WithContext(statusContext).Exec(`
		INSERT INTO data_logger_retention_status (
			logger_id, last_started_at, last_completed_at, last_result, last_error, updated_at
		) VALUES (?, ?, ?, NULL, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (logger_id) DO UPDATE SET
			last_started_at = EXCLUDED.last_started_at,
			last_completed_at = EXCLUDED.last_completed_at,
			last_result = NULL,
			last_error = EXCLUDED.last_error,
			updated_at = CURRENT_TIMESTAMP
	`, loggerID, startedAt, completedAt, message).Error
}

func sanitizedRetentionError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "retention cleanup canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "retention cleanup timed out"
	case errors.Is(err, datalogger.ErrStorageLimitTooSmall):
		return "storage limit cannot hold the newest retained batch"
	default:
		return "retention cleanup failed"
	}
}

func publicRetentionError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, datalogger.ErrLoggerNotFound), errors.Is(err, datalogger.ErrInvalidInput), errors.Is(err, datalogger.ErrStorageLimitTooSmall):
		return err
	default:
		return datalogger.ErrRetentionCleanup
	}
}

func equalOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
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
	case "tag_values_raw_data_type_check", "tag_values_raw_quality_check", "tag_values_raw_payload_check", "data_logger_batches_row_count_check", "data_logger_batches_size_check":
		return datalogger.ErrInvalidRawBatch
	default:
		return err
	}
}

var _ datalogger.HistoryRepository = (*historyRepository)(nil)
