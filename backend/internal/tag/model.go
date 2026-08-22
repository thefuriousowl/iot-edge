package tag

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Type string
type DataType string
type Config = json.RawMessage

const (
	TypeReading    Type = "reading"
	TypeConstant   Type = "constant"
	TypeCalculated Type = "calculated"
)

const (
	DataTypeBool    DataType = "bool"
	DataTypeInt16   DataType = "int16"
	DataTypeUInt16  DataType = "uint16"
	DataTypeInt32   DataType = "int32"
	DataTypeUInt32  DataType = "uint32"
	DataTypeFloat32 DataType = "float32"
	DataTypeFloat64 DataType = "float64"
)

type Tag struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DatasourceID *uuid.UUID `gorm:"column:datasource_id;type:uuid;index" json:"datasource_id"`
	Name         string     `gorm:"size:100;not null;uniqueIndex" json:"name"`
	Type         Type       `gorm:"type:varchar(20);not null;index" json:"type"`
	DataType     DataType   `gorm:"column:data_type;type:varchar(20);not null" json:"data_type"`
	Description  *string    `gorm:"type:text" json:"description"`
	Enabled      bool       `gorm:"not null;index" json:"enabled"`
	Config       Config     `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	CreatedAt    time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Tag) TableName() string { return "tags" }

type Dependency struct {
	TagID          uuid.UUID `gorm:"column:tag_id;type:uuid;primaryKey" json:"tag_id"`
	DependsOnTagID uuid.UUID `gorm:"column:depends_on_tag_id;type:uuid;primaryKey" json:"depends_on_tag_id"`
}

func (Dependency) TableName() string { return "tag_dependencies" }
