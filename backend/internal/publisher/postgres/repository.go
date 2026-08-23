package publisherpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

type sourceRow struct {
	PublisherID      uuid.UUID  `gorm:"column:publisher_id"`
	Position         int        `gorm:"column:position"`
	Alias            string     `gorm:"column:alias"`
	Kind             string     `gorm:"column:kind"`
	TagID            *uuid.UUID `gorm:"column:tag_id"`
	PluginInstanceID *uuid.UUID `gorm:"column:plugin_instance_id"`
	OutputKey        *string    `gorm:"column:output_key"`
}

func (sourceRow) TableName() string { return "data_publisher_sources" }

func NewRepository(db *gorm.DB) publisher.Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, entity *publisher.Publisher) error {
	if entity == nil {
		return publisher.ErrInvalidPublisher
	}
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(entity).Error; err != nil {
			return err
		}
		return insertSources(tx, entity.ID, entity.Sources)
	})
	if err != nil {
		return mapError(err)
	}
	return repository.refreshTimestamps(ctx, entity)
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*publisher.Publisher, error) {
	var entity publisher.Publisher
	if err := repository.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	sources, err := repository.loadSources(ctx, id)
	if err != nil {
		return nil, err
	}
	entity.Sources = sources
	entity.SourceCount = len(sources)
	return &entity, nil
}

func (repository *repository) List(ctx context.Context, input publisher.ListInput) (*publisher.ListResult, error) {
	query := repository.db.WithContext(ctx).Model(&publisher.Publisher{})
	if input.Type != nil {
		query = query.Where("type = ?", *input.Type)
	}
	if input.Enabled != nil {
		query = query.Where("enabled = ?", *input.Enabled)
	}
	if input.Search != "" {
		pattern := "%" + escapeLike(strings.ToLower(input.Search)) + "%"
		query = query.Where(`LOWER(name) LIKE ? ESCAPE '\'`, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	entities := make([]publisher.Publisher, 0)
	err := query.Select("data_publishers.*, (SELECT COUNT(*) FROM data_publisher_sources WHERE publisher_id = data_publishers.id) AS source_count").
		Order("created_at ASC, id ASC").Offset((input.Page - 1) * input.PerPage).Limit(input.PerPage).Find(&entities).Error
	if err != nil {
		return nil, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &publisher.ListResult{Data: entities, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func (repository *repository) ListEnabled(ctx context.Context) ([]publisher.Publisher, error) {
	entities := make([]publisher.Publisher, 0)
	if err := repository.db.WithContext(ctx).Where("enabled = ?", true).Order("created_at ASC, id ASC").Find(&entities).Error; err != nil {
		return nil, err
	}
	for index := range entities {
		sources, err := repository.loadSources(ctx, entities[index].ID)
		if err != nil {
			return nil, err
		}
		entities[index].Sources = sources
		entities[index].SourceCount = len(sources)
	}
	return entities, nil
}

func (repository *repository) Update(ctx context.Context, entity *publisher.Publisher) error {
	if entity == nil || entity.ID == uuid.Nil {
		return publisher.ErrInvalidPublisher
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&publisher.Publisher{}).Where("id = ?", entity.ID).Updates(map[string]any{
			"name": entity.Name, "description": entity.Description, "enabled": entity.Enabled,
			"config": entity.Config, "config_version": entity.ConfigVersion, "credential_id": entity.CredentialID,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrPublisherNotFound
		}
		if err := tx.Delete(&sourceRow{}, "publisher_id = ?", entity.ID).Error; err != nil {
			return err
		}
		return insertSources(tx, entity.ID, entity.Sources)
	})
	return mapError(err)
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result := repository.db.WithContext(ctx).Delete(&publisher.Publisher{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return publisher.ErrPublisherNotFound
	}
	return nil
}

func (repository *repository) loadSources(ctx context.Context, publisherID uuid.UUID) ([]publisher.SourceSelection, error) {
	rows := make([]sourceRow, 0)
	if err := repository.db.WithContext(ctx).Where("publisher_id = ?", publisherID).Order("position ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	selections := make([]publisher.SourceSelection, len(rows))
	for index, row := range rows {
		var reference publisher.SourceReference
		switch publisher.SourceKind(row.Kind) {
		case publisher.SourceKindTag:
			if row.TagID == nil || row.PluginInstanceID != nil || row.OutputKey != nil {
				return nil, publisher.ErrInvalidPublisher
			}
			reference = publisher.TagSource(*row.TagID)
		case publisher.SourceKindPluginOutput:
			if row.TagID != nil || row.PluginInstanceID == nil || row.OutputKey == nil {
				return nil, publisher.ErrInvalidPublisher
			}
			reference = publisher.PluginOutputSource(*row.PluginInstanceID, *row.OutputKey)
		default:
			return nil, publisher.ErrInvalidPublisher
		}
		selections[index] = publisher.SourceSelection{Alias: row.Alias, Reference: reference}
	}
	if len(selections) > 0 {
		normalized, err := publisher.NormalizeSourceSelections(selections)
		if err != nil {
			return nil, publisher.ErrInvalidPublisher
		}
		selections = normalized
	}
	return selections, nil
}

func (repository *repository) refreshTimestamps(ctx context.Context, entity *publisher.Publisher) error {
	return repository.db.WithContext(ctx).Model(&publisher.Publisher{}).Select("created_at", "updated_at").First(entity, "id = ?", entity.ID).Error
}

func insertSources(tx *gorm.DB, publisherID uuid.UUID, selections []publisher.SourceSelection) error {
	normalized, err := publisher.NormalizeSourceSelections(selections)
	if err != nil {
		return publisher.ErrInvalidPublisher
	}
	for position, selection := range normalized {
		row := sourceRow{PublisherID: publisherID, Position: position, Alias: selection.Alias, Kind: string(selection.Reference.Kind)}
		switch selection.Reference.Kind {
		case publisher.SourceKindTag:
			tagID := selection.Reference.TagID
			row.TagID = &tagID
		case publisher.SourceKindPluginOutput:
			instanceID := selection.Reference.PluginInstanceID
			outputKey := selection.Reference.OutputKey
			row.PluginInstanceID = &instanceID
			row.OutputKey = &outputKey
		default:
			return publisher.ErrInvalidPublisher
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return publisher.ErrPublisherNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "idx_data_publishers_name_ci":
		return publisher.ErrPublisherNameExists
	case "data_publisher_sources_tag_id_fkey", "data_publisher_sources_plugin_instance_id_fkey":
		return publisher.ErrSourceNotFound
	case "data_publishers_credential_id_fkey":
		return publisher.ErrCredentialNotFound
	case "data_publishers_pkey", "data_publishers_type_check", "data_publishers_name_check",
		"data_publishers_description_check", "data_publishers_config_check", "data_publishers_config_version_check",
		"data_publisher_sources_pkey", "data_publisher_sources_alias_key", "data_publisher_sources_position_check",
		"data_publisher_sources_alias_check", "data_publisher_sources_kind_check", "data_publisher_sources_shape_check",
		"idx_data_publisher_sources_tag_unique", "idx_data_publisher_sources_plugin_output_unique":
		return publisher.ErrInvalidPublisher
	}
	switch postgresError.Code {
	case "22001", "22P02", "23502", "23503", "23505", "23514":
		return publisher.ErrInvalidPublisher
	default:
		return err
	}
}

var _ publisher.Repository = (*repository)(nil)
