package energy

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMeasurementRunValidateLifecycle(t *testing.T) {
	startedAt := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	active := MeasurementRun{
		PluginInstanceID: uuid.New(), Name: "Boiler room", Status: MeasurementRunActive,
		StartedAt: startedAt, ConfigVersion: 1, ConfigSnapshot: json.RawMessage(`{"logger_id":"source"}`),
	}
	if err := active.Validate(); err != nil {
		t.Fatalf("valid active run rejected: %v", err)
	}

	endedAt, archivedAt := startedAt.Add(time.Hour), startedAt.Add(2*time.Hour)
	checksum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	archived := active
	archived.Status, archived.EndedAt, archived.ArchivedAt = MeasurementRunArchived, &endedAt, &archivedAt
	archived.ArchivePayload, archived.ArchiveSHA256 = json.RawMessage(`{"schema_version":1,"series":[]}`), &checksum
	if err := archived.Validate(); err != nil {
		t.Fatalf("valid archived run rejected: %v", err)
	}
}

func TestMeasurementRunValidateRejectsInvalidStates(t *testing.T) {
	startedAt := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(-time.Second)
	archivedAt := startedAt
	checksum := "not-a-checksum"
	base := MeasurementRun{
		PluginInstanceID: uuid.New(), Name: "Point A", Status: MeasurementRunActive,
		StartedAt: startedAt, ConfigVersion: 1, ConfigSnapshot: json.RawMessage(`{"logger_id":"source"}`),
	}
	tests := map[string]MeasurementRun{
		"missing Plugin":        func() MeasurementRun { value := base; value.PluginInstanceID = uuid.Nil; return value }(),
		"blank name":            func() MeasurementRun { value := base; value.Name = "  "; return value }(),
		"array config":          func() MeasurementRun { value := base; value.ConfigSnapshot = json.RawMessage(`[]`); return value }(),
		"active archive fields": func() MeasurementRun { value := base; value.EndedAt = &startedAt; return value }(),
		"invalid archive": func() MeasurementRun {
			value := base
			value.Status, value.EndedAt, value.ArchivedAt = MeasurementRunArchived, &endedAt, &archivedAt
			value.ArchivePayload, value.ArchiveSHA256 = json.RawMessage(`{}`), &checksum
			return value
		}(),
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run.Validate(); !errors.Is(err, ErrInvalidMeasurementRun) {
				t.Fatalf("Validate() error = %v, want ErrInvalidMeasurementRun", err)
			}
		})
	}
}
