package plugin

import (
	"time"

	"github.com/google/uuid"
)

type Instance struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Type          Type      `gorm:"type:varchar(64);not null;index" json:"type"`
	Name          string    `gorm:"size:100;not null" json:"name"`
	Enabled       bool      `gorm:"not null;index" json:"enabled"`
	Config        Config    `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	ConfigVersion uint      `gorm:"column:config_version;not null" json:"config_version"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Instance) TableName() string { return "plugin_instances" }
