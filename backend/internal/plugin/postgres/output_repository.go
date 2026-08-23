package pluginpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"gorm.io/gorm"
)

type outputRepository struct{ db *gorm.DB }

type latestOutputRow struct {
	PluginInstanceID uuid.UUID `gorm:"column:plugin_instance_id"`
	Sequence         int64     `gorm:"column:sequence"`
	PublishedAt      time.Time `gorm:"column:published_at"`
	Values           []byte    `gorm:"column:values"`
}

func NewOutputRepository(db *gorm.DB) plugin.OutputBatchRepository {
	return &outputRepository{db: db}
}

func (repository *outputRepository) StoreLatest(ctx context.Context, batch plugin.OutputBatch) (*plugin.OutputBatch, bool, error) {
	if ctx == nil || batch.InstanceID == uuid.Nil || batch.Sequence == 0 || batch.PublishedAt.IsZero() || len(batch.Values) == 0 || outputSourceAt(batch).IsZero() {
		return nil, false, plugin.ErrInvalidOutputBatch
	}
	values, err := json.Marshal(batch.Values)
	if err != nil {
		return nil, false, err
	}
	var sequence int64
	err = repository.db.WithContext(ctx).Raw(`
		INSERT INTO plugin_output_latest (plugin_instance_id,sequence,source_at,published_at,values)
		VALUES (?,1,?,?,?::jsonb)
		ON CONFLICT (plugin_instance_id) DO UPDATE SET
			sequence = plugin_output_latest.sequence + 1,
			source_at = EXCLUDED.source_at,
			published_at = EXCLUDED.published_at,
			values = EXCLUDED.values,
			updated_at = CURRENT_TIMESTAMP
		WHERE plugin_output_latest.source_at < EXCLUDED.source_at
		RETURNING sequence`, batch.InstanceID, outputSourceAt(batch), batch.PublishedAt, string(values)).Row().Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	stored := plugin.CloneOutputBatch(batch)
	stored.Sequence = uint64(sequence)
	return &stored, true, nil
}

func (repository *outputRepository) Latest(ctx context.Context, instanceID uuid.UUID) (*plugin.OutputBatch, error) {
	if ctx == nil || instanceID == uuid.Nil {
		return nil, plugin.ErrInvalidInput
	}
	var row latestOutputRow
	result := repository.db.WithContext(ctx).Table("plugin_output_latest").
		Select("plugin_instance_id,sequence,published_at,values").
		Where("plugin_instance_id = ?", instanceID).
		Take(&row)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, plugin.ErrOutputBatchNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}
	var values []plugin.OutputValue
	if err := json.Unmarshal(row.Values, &values); err != nil {
		return nil, err
	}
	return &plugin.OutputBatch{
		InstanceID: row.PluginInstanceID,
		Sequence:   uint64(row.Sequence), PublishedAt: row.PublishedAt.UTC(), Values: values,
	}, nil
}

func outputSourceAt(batch plugin.OutputBatch) time.Time {
	var latest time.Time
	for _, value := range batch.Values {
		if value.ObservedAt.After(latest) {
			latest = value.ObservedAt
		}
	}
	return latest.UTC()
}

var _ plugin.OutputBatchRepository = (*outputRepository)(nil)
