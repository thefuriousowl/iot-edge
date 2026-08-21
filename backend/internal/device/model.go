package device

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type DeviceType string
type DatasourceType string
type Config = json.RawMessage

const (
	DeviceTypeModbus         DeviceType     = "modbus_device"
	DatasourceTypeModbusRead DatasourceType = "modbus_read"
)

type Device struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	VGatewayID  uuid.UUID  `gorm:"column:vgateway_id;type:uuid;not null;index" json:"vgateway_id"`
	Name        string     `gorm:"size:100;not null" json:"name"`
	Type        DeviceType `gorm:"type:varchar(50);not null;index" json:"type"`
	Description *string    `gorm:"type:text" json:"description"`
	Enabled     bool       `gorm:"not null" json:"enabled"`
	Config      Config     `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	CreatedAt   time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Device) TableName() string { return "devices" }

type Datasource struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DeviceID    uuid.UUID      `gorm:"column:device_id;type:uuid;not null;index" json:"device_id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	Type        DatasourceType `gorm:"type:varchar(50);not null;index" json:"type"`
	Description *string        `gorm:"type:text" json:"description"`
	Enabled     bool           `gorm:"not null" json:"enabled"`
	Config      Config         `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	CreatedAt   time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Datasource) TableName() string { return "datasources" }

type DeviceView struct {
	Device
	DatasourceCount int64 `json:"datasource_count"`
	TagCount        int64 `json:"tag_count"`
}

type DatasourceView struct {
	Datasource
	Status string `json:"status"`
}

type DatasourceSample struct {
	DatasourceID uuid.UUID       `json:"datasource_id"`
	Sequence     uint64          `json:"sequence"`
	ObservedAt   time.Time       `json:"observed_at"`
	LatencyMS    float64         `json:"latency_ms"`
	Quality      string          `json:"quality"`
	RawHex       string          `json:"raw_hex"`
	Data         json.RawMessage `json:"data"`
	Error        string          `json:"error,omitempty"`
}
