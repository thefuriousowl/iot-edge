package report

import (
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
)

const MaxColumns = 100

type Report struct {
	ID          uuid.UUID              `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name        string                 `gorm:"size:100;not null;uniqueIndex" json:"name"`
	Description *string                `gorm:"type:text" json:"description"`
	LoggerID    uuid.UUID              `gorm:"column:logger_id;type:uuid;not null;index" json:"logger_id"`
	LoggerName  string                 `gorm:"column:logger_name;->;-:migration" json:"logger_name"`
	Timezone    string                 `gorm:"size:100;not null" json:"timezone"`
	Mode        datalogger.QueryMode   `gorm:"type:varchar(20);not null;index" json:"mode"`
	Bucket      datalogger.QueryBucket `gorm:"type:varchar(20)" json:"bucket,omitempty"`
	ColumnCount int                    `gorm:"column:column_count;->;-:migration" json:"column_count"`
	Columns     []Column               `gorm:"-" json:"columns,omitempty"`
	CreatedAt   time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
}

func (Report) TableName() string { return "reports" }

type Column struct {
	ReportID  uuid.UUID                    `gorm:"column:report_id;type:uuid;primaryKey" json:"-"`
	LoggerID  uuid.UUID                    `gorm:"column:logger_id;type:uuid;not null" json:"-"`
	TagID     uuid.UUID                    `gorm:"column:tag_id;type:uuid;primaryKey" json:"tag_id"`
	Position  int                          `gorm:"not null" json:"position"`
	Name      string                       `gorm:"size:100;not null" json:"name"`
	Aggregate datalogger.AggregateFunction `gorm:"type:varchar(20)" json:"aggregate,omitempty"`
	TagName   string                       `gorm:"column:tag_name;->;-:migration" json:"tag_name"`
	TagType   string                       `gorm:"column:tag_type;->;-:migration" json:"tag_type"`
	DataType  string                       `gorm:"column:data_type;->;-:migration" json:"data_type"`
}

func (Column) TableName() string { return "report_columns" }

type QueryInput struct {
	From    time.Time
	To      time.Time
	Page    int
	PerPage int
}

type QueryResult struct {
	Report     *Report
	Data       []datalogger.QueryRow
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}
