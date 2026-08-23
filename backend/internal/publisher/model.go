package publisher

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Type string
type Config = json.RawMessage

const (
	TypeHTTPServer      Type = "http_server"
	TypeMQTT            Type = "mqtt"
	TypeModbusTCPServer Type = "modbus_tcp_server"
)

type Publisher struct {
	ID             uuid.UUID         `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Type           Type              `gorm:"type:varchar(32);not null;index" json:"type"`
	Name           string            `gorm:"size:100;not null" json:"name"`
	Description    *string           `gorm:"type:text" json:"description,omitempty"`
	Enabled        bool              `gorm:"not null;index" json:"enabled"`
	Config         Config            `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	ConfigVersion  uint              `gorm:"column:config_version;not null" json:"config_version"`
	SecretRevision uint64            `gorm:"column:secret_revision;->;-:migration" json:"-"`
	SourceCount    int               `gorm:"column:source_count;->;-:migration" json:"source_count"`
	Sources        []SourceSelection `gorm:"-" json:"sources,omitempty"`
	CreatedAt      time.Time         `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt      time.Time         `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Publisher) TableName() string { return "data_publishers" }

func clonePublisher(entity Publisher) Publisher {
	cloned := entity
	if entity.Description != nil {
		description := *entity.Description
		cloned.Description = &description
	}
	cloned.Config = cloneConfig(entity.Config)
	cloned.Sources = append([]SourceSelection(nil), entity.Sources...)
	return cloned
}

func cloneConfig(config Config) Config {
	if config == nil {
		return nil
	}
	return append(Config(nil), config...)
}
