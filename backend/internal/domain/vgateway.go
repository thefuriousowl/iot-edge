package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type VGatewayType string

const (
	VGatewayTypeModbusTCP VGatewayType = "modbus_tcp"
)

type VGatewayConnectionStatus string

const (
	VGatewayStatusStopped      VGatewayConnectionStatus = "stopped"
	VGatewayStatusConnecting   VGatewayConnectionStatus = "connecting"
	VGatewayStatusConnected    VGatewayConnectionStatus = "connected"
	VGatewayStatusDisconnected VGatewayConnectionStatus = "disconnected"
	VGatewayStatusError        VGatewayConnectionStatus = "error"
)

// VGatewayConfig remains opaque at the domain and persistence boundaries.
// Protocol drivers own decoding, defaulting, and validation for their type.
type VGatewayConfig = json.RawMessage

type VGateway struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name        string         `gorm:"size:100;not null;uniqueIndex" json:"name"`
	Type        VGatewayType   `gorm:"type:varchar(50);not null;index" json:"type"`
	Description *string        `gorm:"type:text" json:"description"`
	Enabled     bool           `gorm:"not null;index" json:"enabled"`
	Config      VGatewayConfig `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	CreatedAt   time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (VGateway) TableName() string {
	return "vgateways"
}

type VGatewayStats struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	VGatewayID     uuid.UUID  `gorm:"column:vgateway_id;type:uuid;not null;index" json:"vgateway_id"`
	ConnectedAt    *time.Time `json:"connected_at"`
	DisconnectedAt *time.Time `json:"disconnected_at"`
	RequestCount   int64      `gorm:"not null;default:0" json:"request_count"`
	ErrorCount     int64      `gorm:"not null;default:0" json:"error_count"`
	BytesReceived  int64      `gorm:"not null;default:0" json:"bytes_received"`
	AvgLatencyMS   *float64   `gorm:"type:numeric(10,2)" json:"avg_latency_ms"`
	RecordedAt     time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"recorded_at"`
	VGateway       *VGateway  `gorm:"foreignKey:VGatewayID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (VGatewayStats) TableName() string {
	return "vgateway_stats"
}
