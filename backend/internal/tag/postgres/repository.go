package tagpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"gorm.io/gorm"
)

const defaultPerPage = 20

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) tag.Repository { return &repository{db: db} }

func (r *repository) Create(ctx context.Context, entity *tag.Tag, dependencyIDs []uuid.UUID) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(entity).Error; err != nil {
			return err
		}
		return createDependencies(tx, entity.ID, dependencyIDs)
	})
	return mapError(err)
}

func (r *repository) Find(ctx context.Context, id uuid.UUID) (*tag.Tag, error) {
	var entity tag.Tag
	if err := r.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	return &entity, nil
}

func (r *repository) List(ctx context.Context, input tag.ListInput) (*tag.ListResult, error) {
	page := input.Page
	if page < 1 {
		page = 1
	}
	perPage := input.PerPage
	if perPage < 1 {
		perPage = defaultPerPage
	}
	query := r.db.WithContext(ctx).Model(&tag.Tag{})
	if input.Type != nil {
		query = query.Where("type = ?", *input.Type)
	}
	if input.DataType != nil {
		query = query.Where("data_type = ?", *input.DataType)
	}
	if input.Enabled != nil {
		query = query.Where("enabled = ?", *input.Enabled)
	}
	if input.DatasourceID != nil {
		query = query.Where("datasource_id = ?", *input.DatasourceID)
	}
	if search := strings.TrimSpace(input.Search); search != "" {
		query = query.Where(`LOWER(name) LIKE ? ESCAPE '\'`, "%"+escapeLike(strings.ToLower(search))+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	entities := make([]tag.Tag, 0)
	if err := query.Order("created_at ASC, id ASC").Offset((page - 1) * perPage).Limit(perPage).Find(&entities).Error; err != nil {
		return nil, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return &tag.ListResult{Data: entities, Page: page, PerPage: perPage, Total: total, TotalPages: totalPages}, nil
}

func (r *repository) Update(ctx context.Context, entity *tag.Tag, dependencyIDs []uuid.UUID) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&tag.Tag{}).Where("id = ?", entity.ID).Select("datasource_id", "name", "data_type", "description", "enabled", "config", "updated_at").Updates(map[string]any{
			"datasource_id": entity.DatasourceID,
			"name":          entity.Name,
			"data_type":     entity.DataType,
			"description":   entity.Description,
			"enabled":       entity.Enabled,
			"config":        entity.Config,
			"updated_at":    gorm.Expr("CURRENT_TIMESTAMP"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tag.ErrTagNotFound
		}
		if err := tx.Delete(&tag.Dependency{}, "tag_id = ?", entity.ID).Error; err != nil {
			return err
		}
		return createDependencies(tx, entity.ID, dependencyIDs)
	})
	return mapError(err)
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&tag.Tag{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return tag.ErrTagNotFound
	}
	return nil
}

func (r *repository) ListDependencies(ctx context.Context) ([]tag.Dependency, error) {
	dependencies := make([]tag.Dependency, 0)
	err := r.db.WithContext(ctx).Order("tag_id ASC, depends_on_tag_id ASC").Find(&dependencies).Error
	return dependencies, err
}

func createDependencies(tx *gorm.DB, tagID uuid.UUID, dependencyIDs []uuid.UUID) error {
	if len(dependencyIDs) == 0 {
		return nil
	}
	dependencies := make([]tag.Dependency, 0, len(dependencyIDs))
	for _, dependencyID := range dependencyIDs {
		dependencies = append(dependencies, tag.Dependency{TagID: tagID, DependsOnTagID: dependencyID})
	}
	return tx.Create(&dependencies).Error
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tag.ErrTagNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "tags_name_key":
		return tag.ErrTagNameExists
	case "tags_datasource_id_fkey":
		return tag.ErrDatasourceMissing
	case "tag_dependencies_tag_id_fkey", "tag_dependencies_depends_on_tag_id_fkey":
		return tag.ErrDependencyMissing
	case "tags_type_check", "tags_data_type_check", "tags_datasource_scope_check", "tags_config_check":
		return tag.ErrInvalidTag
	case "tag_dependencies_pkey", "tag_dependencies_not_self":
		return tag.ErrInvalidDependency
	default:
		return err
	}
}

var _ tag.Repository = (*repository)(nil)
