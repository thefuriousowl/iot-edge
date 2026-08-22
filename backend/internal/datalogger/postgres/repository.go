package dataloggerpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"gorm.io/gorm"
)

type Repository interface {
	datalogger.Repository
	datalogger.RuntimeRepository
}

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, entity *datalogger.Logger, tagIDs []uuid.UUID) error {
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(entity).Error; err != nil {
			return err
		}
		return replaceTags(tx, entity.ID, tagIDs)
	})
	return mapError(err)
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*datalogger.Logger, error) {
	var entity datalogger.Logger
	if err := repository.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	tags := make([]datalogger.TagReference, 0)
	err := repository.db.WithContext(ctx).Table("data_logger_tags AS selections").
		Select("tags.id, tags.name, tags.type, tags.data_type, tags.enabled, selections.position").
		Joins("JOIN tags ON tags.id = selections.tag_id").
		Where("selections.logger_id = ?", id).
		Order("selections.position ASC").
		Scan(&tags).Error
	if err != nil {
		return nil, err
	}
	entity.Tags = tags
	entity.TagCount = len(tags)
	return &entity, nil
}

func (repository *repository) List(ctx context.Context, input datalogger.ListInput) (*datalogger.ListResult, error) {
	query := repository.db.WithContext(ctx).Model(&datalogger.Logger{})
	if input.Mode != nil {
		query = query.Where("mode = ?", *input.Mode)
	}
	if input.Enabled != nil {
		query = query.Where("enabled = ?", *input.Enabled)
	}
	if input.Search != "" {
		query = query.Where(`LOWER(name) LIKE ? ESCAPE '\'`, "%"+escapeLike(strings.ToLower(input.Search))+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	entities := make([]datalogger.Logger, 0)
	err := query.Select("data_loggers.*, (SELECT COUNT(*) FROM data_logger_tags WHERE data_logger_tags.logger_id = data_loggers.id) AS tag_count").
		Order("created_at ASC, id ASC").
		Offset((input.Page - 1) * input.PerPage).
		Limit(input.PerPage).
		Find(&entities).Error
	if err != nil {
		return nil, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &datalogger.ListResult{Data: entities, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func (repository *repository) ListEnabledLoggers(ctx context.Context) ([]datalogger.Logger, error) {
	entities := make([]datalogger.Logger, 0)
	err := repository.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("created_at ASC, id ASC").
		Find(&entities).Error
	if err != nil || len(entities) == 0 {
		return entities, err
	}

	type selectedTag struct {
		LoggerID uuid.UUID `gorm:"column:logger_id"`
		datalogger.TagReference
	}
	loggerIDs := make([]uuid.UUID, 0, len(entities))
	byID := make(map[uuid.UUID]*datalogger.Logger, len(entities))
	for index := range entities {
		loggerIDs = append(loggerIDs, entities[index].ID)
		byID[entities[index].ID] = &entities[index]
	}
	selections := make([]selectedTag, 0)
	err = repository.db.WithContext(ctx).Table("data_logger_tags AS selections").
		Select("selections.logger_id, tags.id, tags.name, tags.type, tags.data_type, tags.enabled, selections.position").
		Joins("JOIN tags ON tags.id = selections.tag_id").
		Where("selections.logger_id IN ?", loggerIDs).
		Order("selections.logger_id ASC, selections.position ASC").
		Scan(&selections).Error
	if err != nil {
		return nil, err
	}
	for _, selection := range selections {
		entity := byID[selection.LoggerID]
		entity.Tags = append(entity.Tags, selection.TagReference)
		entity.TagCount++
	}
	return entities, nil
}

func (repository *repository) Update(ctx context.Context, entity *datalogger.Logger, tagIDs []uuid.UUID) error {
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&datalogger.Logger{}).Where("id = ?", entity.ID).Select("name", "description", "enabled", "timezone", "mode", "start_at", "end_at", "config", "updated_at").Updates(map[string]any{
			"name": entity.Name, "description": entity.Description, "enabled": entity.Enabled, "timezone": entity.Timezone, "mode": entity.Mode, "start_at": entity.StartAt, "end_at": entity.EndAt, "config": entity.Config, "updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return datalogger.ErrLoggerNotFound
		}
		return replaceTags(tx, entity.ID, tagIDs)
	})
	return mapError(err)
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result := repository.db.WithContext(ctx).Delete(&datalogger.Logger{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return datalogger.ErrLoggerNotFound
	}
	return nil
}

func replaceTags(tx *gorm.DB, loggerID uuid.UUID, tagIDs []uuid.UUID) error {
	if err := tx.Delete(&datalogger.LoggerTag{}, "logger_id = ?", loggerID).Error; err != nil {
		return err
	}
	selections := make([]datalogger.LoggerTag, 0, len(tagIDs))
	for position, tagID := range tagIDs {
		selections = append(selections, datalogger.LoggerTag{LoggerID: loggerID, TagID: tagID, Position: position})
	}
	if len(selections) == 0 {
		return nil
	}
	return tx.Create(&selections).Error
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return datalogger.ErrLoggerNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "data_loggers_name_key":
		return datalogger.ErrLoggerNameExists
	case "data_logger_tags_tag_id_fkey":
		return datalogger.ErrLoggerTagNotFound
	case "data_loggers_mode_check", "data_loggers_end_check", "data_loggers_config_check", "data_loggers_mode_config_check":
		return datalogger.ErrInvalidLogger
	case "data_logger_tags_pkey", "data_logger_tags_position_key", "data_logger_tags_position_check":
		return datalogger.ErrInvalidLoggerTag
	default:
		return err
	}
}

var _ datalogger.Repository = (*repository)(nil)
var _ datalogger.RuntimeRepository = (*repository)(nil)
