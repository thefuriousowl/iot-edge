package device

import (
	"context"

	"github.com/google/uuid"
)

type GatewayContext struct {
	ID      uuid.UUID
	Type    string
	Enabled bool
	Config  Config
}

type DeviceContext struct {
	Device
	Gateway GatewayContext
}

type DatasourceContext struct {
	Datasource
	Device DeviceContext
}

type DeviceInventoryInput struct {
	VGatewayID *uuid.UUID
	Type       *DeviceType
	Enabled    *bool
	Search     string
	Page       int
	PerPage    int
}

type DeviceInventoryResult struct {
	Data       []DeviceInventoryItem
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	FindGateway(context.Context, uuid.UUID) (*GatewayContext, error)
	CreateDevice(context.Context, *Device) error
	FindDevice(context.Context, uuid.UUID) (*DeviceContext, error)
	ListDevices(context.Context, uuid.UUID) ([]DeviceView, error)
	ListDeviceInventory(context.Context, DeviceInventoryInput) (*DeviceInventoryResult, error)
	UpdateDevice(context.Context, *Device) error
	DeleteDevice(context.Context, uuid.UUID) error
	CreateDatasource(context.Context, *Datasource) error
	FindDatasource(context.Context, uuid.UUID) (*DatasourceContext, error)
	ListDatasources(context.Context, uuid.UUID) ([]Datasource, error)
	UpdateDatasource(context.Context, *Datasource) error
	DeleteDatasource(context.Context, uuid.UUID) error
}
