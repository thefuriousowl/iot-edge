package vgatewaypostgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
	"gorm.io/gorm"
)

type vGatewayRepository struct {
	db *gorm.DB
}

func NewVGatewayRepository(db *gorm.DB) vgateway.VGatewayRepository {
	return &vGatewayRepository{db: db}
}

func (r *vGatewayRepository) Create(
	ctx context.Context,
	gateway *vgateway.VGateway,
) error {
	err := r.db.WithContext(ctx).Create(gateway).Error
	return mapVGatewayError(err)
}

func (r *vGatewayRepository) FindByID(
	ctx context.Context,
	id uuid.UUID,
) (*vgateway.VGateway, error) {
	var gateway vgateway.VGateway

	err := r.db.WithContext(ctx).
		First(&gateway, "id = ?", id).
		Error
	if err != nil {
		return nil, mapVGatewayError(err)
	}

	return &gateway, nil
}

func (r *vGatewayRepository) List(
	ctx context.Context,
	options vgateway.VGatewayListOptions,
) ([]vgateway.VGateway, int64, error) {
	query := r.db.WithContext(ctx).Model(&vgateway.VGateway{})

	if options.Type != nil {
		query = query.Where("type = ?", *options.Type)
	}
	if options.Enabled != nil {
		query = query.Where("enabled = ?", *options.Enabled)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	gateways := make([]vgateway.VGateway, 0)

	query = query.Order("created_at DESC, id ASC")
	if options.Limit > 0 {
		query = query.Limit(options.Limit)
	}
	if options.Offset > 0 {
		query = query.Offset(options.Offset)
	}

	if err := query.Find(&gateways).Error; err != nil {
		return nil, 0, err
	}

	return gateways, total, nil
}

func (r *vGatewayRepository) Update(
	ctx context.Context,
	gateway *vgateway.VGateway,
) error {
	result := r.db.WithContext(ctx).
		Model(&vgateway.VGateway{}).
		Where("id = ?", gateway.ID).
		Select(
			"name",
			"description",
			"enabled",
			"config",
		).
		Updates(gateway)

	if result.Error != nil {
		return mapVGatewayError(result.Error)
	}
	if result.RowsAffected == 0 {
		return vgateway.ErrVGatewayNotFound
	}

	return nil
}

func (r *vGatewayRepository) Delete(
	ctx context.Context,
	id uuid.UUID,
) error {
	result := r.db.WithContext(ctx).
		Delete(&vgateway.VGateway{}, "id = ?", id)

	if result.Error != nil {
		return mapVGatewayError(result.Error)
	}
	if result.RowsAffected == 0 {
		return vgateway.ErrVGatewayNotFound
	}

	return nil
}

func mapVGatewayError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return vgateway.ErrVGatewayNotFound
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == "vgateways_name_key" {
		return vgateway.ErrVGatewayNameExists
	}

	return err
}

var _ vgateway.VGatewayRepository = (*vGatewayRepository)(nil)
