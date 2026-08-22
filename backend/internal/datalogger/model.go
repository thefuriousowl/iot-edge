package datalogger

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Mode string
type ScheduleUnit string

const (
	ModeInterval Mode = "interval"
	ModeSchedule Mode = "schedule"
)

const (
	ScheduleMinute ScheduleUnit = "minute"
	ScheduleHour   ScheduleUnit = "hour"
	ScheduleDay    ScheduleUnit = "day"
	ScheduleWeek   ScheduleUnit = "week"
)

const (
	DefaultIntervalSeconds int64 = 60
	MaxIntervalSeconds     int64 = int64(^uint64(0)>>1) / int64(time.Second)
	MaxScheduleEvery             = int(MaxIntervalSeconds / 3600)
)

type Logger struct {
	ID          uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name        string          `gorm:"size:100;not null;uniqueIndex" json:"name"`
	Description *string         `gorm:"type:text" json:"description"`
	Enabled     bool            `gorm:"not null;index" json:"enabled"`
	Timezone    string          `gorm:"size:100;not null" json:"timezone"`
	Mode        Mode            `gorm:"type:varchar(20);not null;index" json:"mode"`
	StartAt     time.Time       `gorm:"column:start_at;not null" json:"start_at"`
	EndAt       *time.Time      `gorm:"column:end_at" json:"end_at"`
	Config      json.RawMessage `gorm:"type:jsonb;serializer:json;not null" json:"config"`
	TagCount    int             `gorm:"column:tag_count;->;-:migration" json:"tag_count"`
	Tags        []TagReference  `gorm:"-" json:"tags,omitempty"`
	CreatedAt   time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Logger) TableName() string { return "data_loggers" }

type LoggerTag struct {
	LoggerID uuid.UUID `gorm:"column:logger_id;type:uuid;primaryKey"`
	TagID    uuid.UUID `gorm:"column:tag_id;type:uuid;primaryKey"`
	Position int       `gorm:"not null"`
}

func (LoggerTag) TableName() string { return "data_logger_tags" }

type TagReference struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	DataType string    `json:"data_type"`
	Enabled  bool      `json:"enabled"`
	Position int       `json:"-"`
}

type IntervalConfig struct {
	IntervalSeconds int64 `json:"interval_seconds"`
}

type ScheduleConfig struct {
	Unit     ScheduleUnit `json:"unit"`
	Every    int          `json:"every"`
	Times    []string     `json:"times,omitempty"`
	Weekdays []int        `json:"weekdays,omitempty"`
}
