package pluginpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) plugin.Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, instance *plugin.Instance) error {
	if instance == nil {
		return plugin.ErrInvalidInstance
	}
	if instance.ID == uuid.Nil {
		instance.ID = uuid.New()
	}
	return mapError(repository.db.WithContext(ctx).Create(instance).Error)
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*plugin.Instance, error) {
	var instance plugin.Instance
	if err := repository.db.WithContext(ctx).First(&instance, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	return &instance, nil
}

func (repository *repository) List(ctx context.Context, input plugin.ListInput) (*plugin.ListResult, error) {
	query := repository.db.WithContext(ctx).Model(&plugin.Instance{})
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
	instances := make([]plugin.Instance, 0)
	if err := query.Order("created_at ASC, id ASC").Offset((input.Page - 1) * input.PerPage).Limit(input.PerPage).Find(&instances).Error; err != nil {
		return nil, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &plugin.ListResult{Data: instances, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func (repository *repository) ListEnabled(ctx context.Context) ([]plugin.Instance, error) {
	instances := make([]plugin.Instance, 0)
	err := repository.db.WithContext(ctx).Where("enabled = ?", true).Order("created_at ASC, id ASC").Find(&instances).Error
	return instances, err
}

func (repository *repository) Update(ctx context.Context, instance *plugin.Instance) error {
	if instance == nil {
		return plugin.ErrInvalidInstance
	}
	result := repository.db.WithContext(ctx).Model(&plugin.Instance{}).Where("id = ?", instance.ID).Updates(map[string]any{
		"name":           instance.Name,
		"enabled":        instance.Enabled,
		"config":         instance.Config,
		"config_version": instance.ConfigVersion,
		"updated_at":     gorm.Expr("CURRENT_TIMESTAMP"),
	})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return plugin.ErrInstanceNotFound
	}
	return nil
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result := repository.db.WithContext(ctx).Delete(&plugin.Instance{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return plugin.ErrInstanceNotFound
	}
	return nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return plugin.ErrInstanceNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "idx_plugin_instances_name_ci":
		return plugin.ErrInstanceNameExists
	case "plugin_instances_pkey", "plugin_instances_type_check", "plugin_instances_name_check", "plugin_instances_config_check", "plugin_instances_config_version_check":
		return plugin.ErrInvalidInstance
	}
	switch postgresError.Code {
	case "22001", "22P02", "23502", "23514":
		return plugin.ErrInvalidInstance
	default:
		return err
	}
}

var _ plugin.Repository = (*repository)(nil)
