package energypostgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/plugin/energy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MeasurementRunRepository struct{ db *gorm.DB }

func NewMeasurementRunRepository(db *gorm.DB) energy.MeasurementRunRepository {
	return &MeasurementRunRepository{db: db}
}

func (repository *MeasurementRunRepository) FindActive(ctx context.Context, pluginInstanceID uuid.UUID) (*energy.MeasurementRun, error) {
	if pluginInstanceID == uuid.Nil {
		return nil, energy.ErrInvalidMeasurementRun
	}
	var run energy.MeasurementRun
	err := repository.db.WithContext(ctx).
		Where("plugin_instance_id = ? AND status = ?", pluginInstanceID, energy.MeasurementRunActive).
		First(&run).Error
	if err != nil {
		return nil, mapMeasurementRunError(err)
	}
	return &run, nil
}

func (repository *MeasurementRunRepository) FindArchived(ctx context.Context, pluginInstanceID, runID uuid.UUID) (*energy.MeasurementRun, error) {
	if pluginInstanceID == uuid.Nil || runID == uuid.Nil {
		return nil, energy.ErrInvalidMeasurementRun
	}
	var run energy.MeasurementRun
	err := repository.db.WithContext(ctx).Where("id = ? AND plugin_instance_id = ? AND status = ?", runID, pluginInstanceID, energy.MeasurementRunArchived).First(&run).Error
	if err != nil {
		return nil, mapMeasurementRunError(err)
	}
	return &run, nil
}

func (repository *MeasurementRunRepository) ListArchived(ctx context.Context, pluginInstanceID uuid.UUID) ([]energy.MeasurementRun, error) {
	if pluginInstanceID == uuid.Nil {
		return nil, energy.ErrInvalidMeasurementRun
	}
	runs := make([]energy.MeasurementRun, 0)
	err := repository.db.WithContext(ctx).Select("id", "plugin_instance_id", "name", "reason", "status", "started_at", "ended_at", "config_version", "archive_sha256", "created_at", "archived_at").
		Where("plugin_instance_id = ? AND status = ?", pluginInstanceID, energy.MeasurementRunArchived).
		Order("ended_at DESC, id DESC").Limit(100).Find(&runs).Error
	return runs, mapMeasurementRunError(err)
}

func (repository *MeasurementRunRepository) Start(ctx context.Context, input energy.StartMeasurementRunInput) (*energy.MeasurementRun, error) {
	run := &energy.MeasurementRun{
		ID: uuid.New(), PluginInstanceID: input.PluginInstanceID, Name: strings.TrimSpace(input.Name),
		Reason: strings.TrimSpace(input.Reason), Status: energy.MeasurementRunActive,
		StartedAt: input.StartedAt.UTC(), ConfigVersion: input.ConfigVersion, ConfigSnapshot: input.ConfigSnapshot,
	}
	if err := run.Validate(); err != nil {
		return nil, err
	}
	if err := repository.db.WithContext(ctx).Create(run).Error; err != nil {
		return nil, mapMeasurementRunError(err)
	}
	return run, nil
}

func (repository *MeasurementRunRepository) ArchiveAndStart(ctx context.Context, input energy.ResetMeasurementRunInput) (*energy.MeasurementRun, error) {
	if input.ExpectedRunID == uuid.Nil || input.PluginInstanceID == uuid.Nil || input.Cutoff.IsZero() || !validResetArchive(input) {
		return nil, energy.ErrInvalidMeasurementRun
	}
	var next *energy.MeasurementRun
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current energy.MeasurementRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("plugin_instance_id = ? AND status = ?", input.PluginInstanceID, energy.MeasurementRunActive).
			First(&current).Error; err != nil {
			return mapMeasurementRunError(err)
		}
		if current.ID != input.ExpectedRunID {
			return energy.ErrMeasurementRunConflict
		}
		cutoff := input.Cutoff.UTC()
		if cutoff.Before(current.StartedAt) {
			return energy.ErrInvalidMeasurementRun
		}
		archivedAt := time.Now().UTC()
		result := tx.Model(&energy.MeasurementRun{}).
			Where("id = ? AND status = ?", current.ID, energy.MeasurementRunActive).
			Updates(map[string]any{
				"status": energy.MeasurementRunArchived, "ended_at": cutoff,
				"archive_payload": input.ArchivePayload, "archive_sha256": input.ArchiveSHA256,
				"archived_at": archivedAt,
			})
		if result.Error != nil {
			return mapMeasurementRunError(result.Error)
		}
		if result.RowsAffected != 1 {
			return energy.ErrMeasurementRunConflict
		}
		next = &energy.MeasurementRun{
			ID: uuid.New(), PluginInstanceID: input.PluginInstanceID, Name: strings.TrimSpace(input.Name),
			Reason: strings.TrimSpace(input.Reason), Status: energy.MeasurementRunActive,
			StartedAt: cutoff, ConfigVersion: input.ConfigVersion, ConfigSnapshot: input.ConfigSnapshot,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		return mapMeasurementRunError(tx.Create(next).Error)
	})
	if err != nil {
		return nil, err
	}
	return next, nil
}

func validResetArchive(input energy.ResetMeasurementRunInput) bool {
	endedAt, archivedAt := input.Cutoff.UTC(), time.Now().UTC()
	checksum := input.ArchiveSHA256
	candidate := energy.MeasurementRun{
		PluginInstanceID: input.PluginInstanceID, Name: strings.TrimSpace(input.Name), Reason: strings.TrimSpace(input.Reason),
		Status: energy.MeasurementRunArchived, StartedAt: input.Cutoff.UTC(), EndedAt: &endedAt,
		ConfigVersion: input.ConfigVersion, ConfigSnapshot: input.ConfigSnapshot,
		ArchivePayload: input.ArchivePayload, ArchiveSHA256: &checksum, ArchivedAt: &archivedAt,
	}
	return candidate.Validate() == nil
}

func mapMeasurementRunError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return energy.ErrMeasurementRunNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "idx_energy_measurement_runs_active":
		return energy.ErrMeasurementRunConflict
	case "energy_measurement_runs_plugin_type_check", "energy_measurement_runs_name_check",
		"energy_measurement_runs_reason_check", "energy_measurement_runs_status_check",
		"energy_measurement_runs_config_version_check", "energy_measurement_runs_config_snapshot_check",
		"energy_measurement_runs_archive_payload_check", "energy_measurement_runs_archive_sha256_check",
		"energy_measurement_runs_state_check", "energy_measurement_runs_archived_immutable":
		return energy.ErrInvalidMeasurementRun
	}
	switch postgresError.Code {
	case "22001", "22P02", "23502", "23503", "23505", "23514":
		return energy.ErrInvalidMeasurementRun
	default:
		return err
	}
}

var _ energy.MeasurementRunRepository = (*MeasurementRunRepository)(nil)
