package tagpostgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"gorm.io/gorm"
)

type latestValueRepository struct{ db *gorm.DB }

type latestValueRow struct {
	TagID        uuid.UUID       `gorm:"column:tag_id"`
	Sequence     int64           `gorm:"column:sequence"`
	DataType     tag.DataType    `gorm:"column:data_type"`
	Value        json.RawMessage `gorm:"column:value"`
	Quality      string          `gorm:"column:quality"`
	ObservedAt   time.Time       `gorm:"column:observed_at"`
	StoredAt     time.Time       `gorm:"column:stored_at"`
	ErrorMessage *string         `gorm:"column:error_message"`
}

func NewLatestValueRepository(db *gorm.DB) tag.LatestValueRepository {
	return &latestValueRepository{db: db}
}

func (repository *latestValueRepository) ListLatest(ctx context.Context) ([]tag.TagValue, error) {
	rows := make([]latestValueRow, 0)
	err := repository.db.WithContext(ctx).
		Table("tag_values_latest AS latest").
		Select("latest.tag_id, latest.sequence, latest.data_type, latest.value, latest.quality, latest.observed_at, latest.stored_at, latest.error_message").
		Joins("JOIN tags ON tags.id = latest.tag_id AND tags.data_type = latest.data_type").
		Order("latest.sequence ASC, latest.tag_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	values := make([]tag.TagValue, 0, len(rows))
	for _, row := range rows {
		value, decodeErr := decodeLatestValue(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		values = append(values, value)
	}
	return values, nil
}

func (repository *latestValueRepository) UpsertLatest(ctx context.Context, value tag.TagValue) error {
	if value.Sequence == 0 || value.Sequence > math.MaxInt64 || value.StoredAt.IsZero() {
		return fmt.Errorf("%w: sequence and stored_at are invalid", tag.ErrInvalidTagValue)
	}
	payload, err := encodeLatestValue(value)
	if err != nil {
		return err
	}
	var errorMessage any
	if value.Quality == tag.ValueQualityBad {
		errorMessage = value.Error
	}
	return repository.db.WithContext(ctx).Exec(`
		INSERT INTO tag_values_latest (
			tag_id, sequence, data_type, value, quality, observed_at, stored_at, error_message, persisted_at
		) VALUES (?, ?, ?, CAST(? AS JSONB), ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (tag_id) DO UPDATE SET
			sequence = EXCLUDED.sequence,
			data_type = EXCLUDED.data_type,
			value = EXCLUDED.value,
			quality = EXCLUDED.quality,
			observed_at = EXCLUDED.observed_at,
			stored_at = EXCLUDED.stored_at,
			error_message = EXCLUDED.error_message,
			persisted_at = CURRENT_TIMESTAMP
		WHERE tag_values_latest.sequence < EXCLUDED.sequence
	`, value.TagID, int64(value.Sequence), value.DataType, payload, value.Quality, value.ObservedAt, value.StoredAt, errorMessage).Error
}

func encodeLatestValue(value tag.TagValue) (any, error) {
	if value.Quality == tag.ValueQualityBad {
		if value.Error == "" {
			return nil, fmt.Errorf("%w: bad quality requires an error", tag.ErrInvalidTagValue)
		}
		return nil, nil
	}
	if value.Quality != tag.ValueQualityGood {
		return nil, fmt.Errorf("%w: unsupported quality %q", tag.ErrInvalidTagValue, value.Quality)
	}
	payload, err := json.Marshal(value.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding latest value: %v", tag.ErrInvalidTagValue, err)
	}
	return string(payload), nil
}

func decodeLatestValue(row latestValueRow) (tag.TagValue, error) {
	value := tag.TagValue{
		TagID:      row.TagID,
		Sequence:   uint64(row.Sequence),
		ObservedAt: row.ObservedAt.UTC(),
		StoredAt:   row.StoredAt.UTC(),
		Quality:    row.Quality,
		DataType:   row.DataType,
	}
	if row.Quality == tag.ValueQualityBad {
		if row.ErrorMessage != nil {
			value.Error = *row.ErrorMessage
		}
		return value, nil
	}
	var target any
	switch row.DataType {
	case tag.DataTypeBool:
		target = new(bool)
	case tag.DataTypeInt16:
		target = new(int16)
	case tag.DataTypeUInt16:
		target = new(uint16)
	case tag.DataTypeInt32:
		target = new(int32)
	case tag.DataTypeUInt32:
		target = new(uint32)
	case tag.DataTypeFloat32:
		target = new(float32)
	case tag.DataTypeFloat64:
		target = new(float64)
	default:
		return tag.TagValue{}, fmt.Errorf("%w: unsupported persisted data type %q", tag.ErrInvalidTagValue, row.DataType)
	}
	if err := json.Unmarshal(row.Value, target); err != nil {
		return tag.TagValue{}, fmt.Errorf("%w: decoding persisted %s value: %v", tag.ErrInvalidTagValue, row.DataType, err)
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

var _ tag.LatestValueRepository = (*latestValueRepository)(nil)
