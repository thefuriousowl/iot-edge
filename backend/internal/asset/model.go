package asset

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Kind string
type Metadata = json.RawMessage

const (
	KindSite      Kind = "site"
	KindBuilding  Kind = "building"
	KindArea      Kind = "area"
	KindSystem    Kind = "system"
	KindEquipment Kind = "equipment"
	KindMeter     Kind = "meter"
	KindCustom    Kind = "custom"
)

const MaxHierarchyDepth = 16

type Asset struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ParentID    *uuid.UUID `gorm:"column:parent_id;type:uuid;index" json:"parent_id"`
	Name        string     `gorm:"size:100;not null" json:"name"`
	Kind        Kind       `gorm:"type:varchar(32);not null;index" json:"kind"`
	Description *string    `gorm:"type:text" json:"description"`
	Enabled     bool       `gorm:"not null;index" json:"enabled"`
	Timezone    *string    `gorm:"size:100" json:"timezone"`
	Position    int        `gorm:"not null;default:0" json:"position"`
	Metadata    Metadata   `gorm:"type:jsonb;serializer:json;not null" json:"metadata"`
	CreatedAt   time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Asset) TableName() string { return "assets" }

type TreeNode struct {
	Asset
	Depth            int        `json:"depth"`
	EffectiveEnabled bool       `json:"effective_enabled"`
	MeasurementCount int64      `json:"measurement_count"`
	Children         []TreeNode `json:"children"`
}

func (kind Kind) Valid() bool {
	switch kind {
	case KindSite, KindBuilding, KindArea, KindSystem, KindEquipment, KindMeter, KindCustom:
		return true
	default:
		return false
	}
}
