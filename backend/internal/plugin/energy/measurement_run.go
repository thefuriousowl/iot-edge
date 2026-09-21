package energy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MaxMeasurementRunNameLength   = 100
	MaxMeasurementRunReasonLength = 500
)

var (
	ErrMeasurementRunNotFound = errors.New("Energy measurement run not found")
	ErrMeasurementRunConflict = errors.New("Energy measurement run changed; refresh and retry")
	ErrInvalidMeasurementRun  = errors.New("invalid Energy measurement run")
)

type MeasurementRunStatus string

const (
	MeasurementRunActive   MeasurementRunStatus = "active"
	MeasurementRunArchived MeasurementRunStatus = "archived"
)

type MeasurementRun struct {
	ID               uuid.UUID            `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	PluginInstanceID uuid.UUID            `gorm:"type:uuid;not null;index" json:"plugin_instance_id"`
	Name             string               `gorm:"size:100;not null" json:"name"`
	Reason           string               `gorm:"size:500;not null" json:"reason"`
	Status           MeasurementRunStatus `gorm:"size:16;not null" json:"status"`
	StartedAt        time.Time            `gorm:"not null" json:"started_at"`
	EndedAt          *time.Time           `json:"ended_at,omitempty"`
	ConfigVersion    uint                 `gorm:"not null" json:"config_version"`
	ConfigSnapshot   json.RawMessage      `gorm:"type:jsonb;not null" json:"config_snapshot"`
	ArchivePayload   json.RawMessage      `gorm:"type:jsonb" json:"-"`
	ArchiveSHA256    *string              `gorm:"type:char(64)" json:"archive_sha256,omitempty"`
	CreatedAt        time.Time            `gorm:"not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	ArchivedAt       *time.Time           `json:"archived_at,omitempty"`
}

func (MeasurementRun) TableName() string { return "energy_measurement_runs" }

func (run MeasurementRun) Validate() error {
	if run.PluginInstanceID == uuid.Nil || run.ConfigVersion == 0 || !validJSONObject(run.ConfigSnapshot) {
		return ErrInvalidMeasurementRun
	}
	run.Name = strings.TrimSpace(run.Name)
	run.Reason = strings.TrimSpace(run.Reason)
	if run.Name == "" || len(run.Name) > MaxMeasurementRunNameLength || len(run.Reason) > MaxMeasurementRunReasonLength || run.StartedAt.IsZero() {
		return ErrInvalidMeasurementRun
	}
	switch run.Status {
	case MeasurementRunActive:
		if run.EndedAt != nil || run.ArchivedAt != nil || len(run.ArchivePayload) != 0 || run.ArchiveSHA256 != nil {
			return ErrInvalidMeasurementRun
		}
	case MeasurementRunArchived:
		if run.EndedAt == nil || run.ArchivedAt == nil || run.EndedAt.Before(run.StartedAt) || !validJSONObject(run.ArchivePayload) || run.ArchiveSHA256 == nil || !validSHA256(*run.ArchiveSHA256) {
			return ErrInvalidMeasurementRun
		}
	default:
		return ErrInvalidMeasurementRun
	}
	return nil
}

type StartMeasurementRunInput struct {
	PluginInstanceID uuid.UUID
	Name             string
	Reason           string
	StartedAt        time.Time
	ConfigVersion    uint
	ConfigSnapshot   json.RawMessage
}

type ResetMeasurementRunInput struct {
	PluginInstanceID uuid.UUID
	ExpectedRunID    uuid.UUID
	Name             string
	Reason           string
	Cutoff           time.Time
	ConfigVersion    uint
	ConfigSnapshot   json.RawMessage
	ArchivePayload   json.RawMessage
	ArchiveSHA256    string
}

type MeasurementRunRepository interface {
	FindActive(context.Context, uuid.UUID) (*MeasurementRun, error)
	FindArchived(context.Context, uuid.UUID, uuid.UUID) (*MeasurementRun, error)
	ListArchived(context.Context, uuid.UUID) ([]MeasurementRun, error)
	Start(context.Context, StartMeasurementRunInput) (*MeasurementRun, error)
	ArchiveAndStart(context.Context, ResetMeasurementRunInput) (*MeasurementRun, error)
}

type MeasurementRunArchive struct {
	SchemaVersion    int             `json:"schema_version"`
	RunID            uuid.UUID       `json:"run_id"`
	PluginInstanceID uuid.UUID       `json:"plugin_instance_id"`
	LoggerID         uuid.UUID       `json:"logger_id"`
	Timezone         string          `json:"timezone"`
	Currency         string          `json:"currency"`
	Bucket           string          `json:"bucket"`
	From             time.Time       `json:"from"`
	To               time.Time       `json:"to"`
	ConfigVersion    uint            `json:"config_version"`
	ConfigSnapshot   json.RawMessage `json:"config_snapshot"`
	Summary          PeriodSummary   `json:"summary"`
	Series           []HistoryRow    `json:"series"`
}

func validJSONObject(value json.RawMessage) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
