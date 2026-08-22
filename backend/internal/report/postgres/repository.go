package reportpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/report"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) report.Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, entity *report.Report) error {
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO reports (id,name,description,logger_id,timezone,mode,bucket) VALUES (?,?,?,?,?,?,?)`, entity.ID, entity.Name, entity.Description, entity.LoggerID, entity.Timezone, entity.Mode, nullableBucket(entity)).Error; err != nil {
			return err
		}
		return insertColumns(tx, entity)
	})
	return mapError(err)
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*report.Report, error) {
	var entity report.Report
	err := repository.db.WithContext(ctx).Table("reports").
		Select("reports.*, data_loggers.name AS logger_name").
		Joins("JOIN data_loggers ON data_loggers.id = reports.logger_id").
		Where("reports.id = ?", id).First(&entity).Error
	if err != nil {
		return nil, mapError(err)
	}
	columns := make([]report.Column, 0)
	err = repository.db.WithContext(ctx).Table("report_columns").
		Select("report_columns.*, COALESCE(report_columns.aggregate, '') AS aggregate, tags.name AS tag_name, tags.type AS tag_type, tags.data_type").
		Joins("JOIN tags ON tags.id = report_columns.tag_id").
		Where("report_columns.report_id = ?", id).
		Order("report_columns.position ASC").Scan(&columns).Error
	if err != nil {
		return nil, err
	}
	entity.Columns = columns
	entity.ColumnCount = len(columns)
	return &entity, nil
}

func (repository *repository) List(ctx context.Context, input report.ListInput) (*report.ListResult, error) {
	query := repository.db.WithContext(ctx).Table("reports").Joins("JOIN data_loggers ON data_loggers.id = reports.logger_id")
	if input.LoggerID != nil {
		query = query.Where("reports.logger_id = ?", *input.LoggerID)
	}
	if input.Mode != nil {
		query = query.Where("reports.mode = ?", *input.Mode)
	}
	if input.Search != "" {
		pattern := "%" + escapeLike(strings.ToLower(input.Search)) + "%"
		query = query.Where(`LOWER(reports.name) LIKE ? ESCAPE '\' OR LOWER(data_loggers.name) LIKE ? ESCAPE '\'`, pattern, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	entities := make([]report.Report, 0)
	err := query.Select("reports.*, data_loggers.name AS logger_name, (SELECT COUNT(*) FROM report_columns WHERE report_columns.report_id = reports.id) AS column_count").
		Order("reports.created_at ASC, reports.id ASC").
		Offset((input.Page - 1) * input.PerPage).Limit(input.PerPage).Scan(&entities).Error
	if err != nil {
		return nil, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(input.PerPage) - 1) / int64(input.PerPage))
	}
	return &report.ListResult{Data: entities, Page: input.Page, PerPage: input.PerPage, Total: total, TotalPages: totalPages}, nil
}

func (repository *repository) Update(ctx context.Context, entity *report.Report) error {
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&report.Column{}, "report_id = ?", entity.ID).Error; err != nil {
			return err
		}
		result := tx.Exec(`UPDATE reports SET name=?, description=?, logger_id=?, timezone=?, mode=?, bucket=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, entity.Name, entity.Description, entity.LoggerID, entity.Timezone, entity.Mode, nullableBucket(entity), entity.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return report.ErrNotFound
		}
		return insertColumns(tx, entity)
	})
	return mapError(err)
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result := repository.db.WithContext(ctx).Delete(&report.Report{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return report.ErrNotFound
	}
	return nil
}

func insertColumns(tx *gorm.DB, entity *report.Report) error {
	for _, column := range entity.Columns {
		var aggregate any
		if column.Aggregate != "" {
			aggregate = column.Aggregate
		}
		if err := tx.Exec(`INSERT INTO report_columns (report_id,logger_id,tag_id,position,name,aggregate) VALUES (?,?,?,?,?,?)`, entity.ID, entity.LoggerID, column.TagID, column.Position, column.Name, aggregate).Error; err != nil {
			return err
		}
	}
	return nil
}

func nullableBucket(entity *report.Report) any {
	if entity.Mode == "raw" || entity.Bucket == "" {
		return nil
	}
	return entity.Bucket
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return report.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "reports_name_key":
		return report.ErrNameExists
	case "idx_report_columns_name_ci":
		return report.ErrColumnNameExists
	case "reports_logger_id_fkey":
		return report.ErrInvalidInput
	case "report_columns_logger_tag_fkey":
		return report.ErrTagNotSelected
	case "reports_timezone_check", "reports_mode_check", "reports_mode_bucket_check", "report_columns_position_check", "report_columns_name_check", "report_columns_aggregate_check", "report_columns_position_key", "report_columns_pkey":
		return report.ErrInvalidReport
	default:
		return err
	}
}

var _ report.Repository = (*repository)(nil)
