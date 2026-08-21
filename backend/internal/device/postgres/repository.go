package devicepostgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *repository { return &repository{db: db} }

func (r *repository) CountByVGatewayIDs(ctx context.Context, gatewayIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	counts := make(map[uuid.UUID]int64, len(gatewayIDs))
	if len(gatewayIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		VGatewayID uuid.UUID `gorm:"column:vgateway_id"`
		Count      int64
	}
	if err := r.db.WithContext(ctx).Table("devices").Select("vgateway_id, COUNT(*) AS count").Where("vgateway_id IN ?", gatewayIDs).Group("vgateway_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.VGatewayID] = row.Count
	}
	return counts, nil
}

func (r *repository) FindGateway(ctx context.Context, id uuid.UUID) (*device.GatewayContext, error) {
	var gateway device.GatewayContext
	err := r.db.WithContext(ctx).Table("vgateways").Select("id, type, enabled, config").Where("id = ?", id).Take(&gateway).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, device.ErrGatewayNotFound
		}
		return nil, mapError(err)
	}
	return &gateway, nil
}

func (r *repository) CreateDevice(ctx context.Context, entity *device.Device) error {
	return mapError(r.db.WithContext(ctx).Create(entity).Error)
}

func (r *repository) FindDevice(ctx context.Context, id uuid.UUID) (*device.DeviceContext, error) {
	var entity device.Device
	if err := r.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, mapError(err)
	}
	gateway, err := r.FindGateway(ctx, entity.VGatewayID)
	if err != nil {
		return nil, err
	}
	return &device.DeviceContext{Device: entity, Gateway: *gateway}, nil
}

func (r *repository) ListDevices(ctx context.Context, gatewayID uuid.UUID) ([]device.DeviceView, error) {
	views := make([]device.DeviceView, 0)
	err := r.db.WithContext(ctx).Table("devices").
		Select("devices.*, (SELECT COUNT(*) FROM datasources WHERE datasources.device_id = devices.id) AS datasource_count, 0 AS tag_count").
		Where("devices.vgateway_id = ?", gatewayID).
		Order("devices.created_at ASC, devices.id ASC").Scan(&views).Error
	return views, err
}

func (r *repository) UpdateDevice(ctx context.Context, entity *device.Device) error {
	result := r.db.WithContext(ctx).Model(&device.Device{}).Where("id = ?", entity.ID).Select("name", "description", "enabled", "config", "updated_at").Updates(map[string]any{"name": entity.Name, "description": entity.Description, "enabled": entity.Enabled, "config": entity.Config, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return device.ErrDeviceNotFound
	}
	return nil
}

func (r *repository) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&device.Device{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return device.ErrDeviceNotFound
	}
	return nil
}

func (r *repository) CreateDatasource(ctx context.Context, entity *device.Datasource) error {
	return mapError(r.db.WithContext(ctx).Create(entity).Error)
}

func (r *repository) FindDatasource(ctx context.Context, id uuid.UUID) (*device.DatasourceContext, error) {
	var entity device.Datasource
	if err := r.db.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, device.ErrDatasourceNotFound
		}
		return nil, mapError(err)
	}
	parent, err := r.FindDevice(ctx, entity.DeviceID)
	if err != nil {
		return nil, err
	}
	return &device.DatasourceContext{Datasource: entity, Device: *parent}, nil
}

func (r *repository) ListDatasources(ctx context.Context, deviceID uuid.UUID) ([]device.Datasource, error) {
	entities := make([]device.Datasource, 0)
	err := r.db.WithContext(ctx).Where("device_id = ?", deviceID).Order("created_at ASC, id ASC").Find(&entities).Error
	return entities, err
}

func (r *repository) UpdateDatasource(ctx context.Context, entity *device.Datasource) error {
	result := r.db.WithContext(ctx).Model(&device.Datasource{}).Where("id = ?", entity.ID).Select("name", "description", "enabled", "config", "updated_at").Updates(map[string]any{"name": entity.Name, "description": entity.Description, "enabled": entity.Enabled, "config": entity.Config, "updated_at": gorm.Expr("CURRENT_TIMESTAMP")})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return device.ErrDatasourceNotFound
	}
	return nil
}

func (r *repository) DeleteDatasource(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&device.Datasource{}, "id = ?", id)
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return device.ErrDatasourceNotFound
	}
	return nil
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return device.ErrDeviceNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.ConstraintName {
		case "devices_vgateway_name_key":
			return device.ErrDeviceNameExists
		case "datasources_device_name_key":
			return device.ErrDatasourceNameExists
		case "devices_vgateway_id_fkey":
			return device.ErrGatewayNotFound
		case "datasources_device_id_fkey":
			return device.ErrDeviceNotFound
		}
	}
	return err
}

var _ device.Repository = (*repository)(nil)
