package credentialpostgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/credential"
	"gorm.io/gorm"
)

type repository struct{ db *gorm.DB }

type profileRow struct {
	credential.Profile
}

func (profileRow) TableName() string { return "credential_profiles" }

func NewRepository(db *gorm.DB) credential.Repository { return &repository{db: db} }

func (repository *repository) Create(ctx context.Context, profile *credential.Profile) error {
	if repository == nil || repository.db == nil || ctx == nil || profile == nil {
		return credential.ErrInvalidInput
	}
	if err := repository.db.WithContext(ctx).Table("credential_profiles").Create(profile).Error; err != nil {
		return mapError(err)
	}
	return nil
}

func (repository *repository) Find(ctx context.Context, id uuid.UUID) (*credential.Profile, error) {
	if repository == nil || repository.db == nil || ctx == nil || id == uuid.Nil {
		return nil, credential.ErrInvalidInput
	}
	var profile credential.Profile
	result := repository.db.WithContext(ctx).Table("credential_profiles").
		Select("credential_profiles.*, (SELECT COUNT(*) FROM data_publishers WHERE credential_id = credential_profiles.id) AS usage_count").
		Where("id = ?", id).First(&profile)
	if result.Error != nil {
		return nil, mapError(result.Error)
	}
	return &profile, nil
}

func (repository *repository) List(ctx context.Context, input credential.ListInput) ([]credential.Profile, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return nil, credential.ErrInvalidInput
	}
	query := repository.db.WithContext(ctx).Table("credential_profiles").
		Select("credential_profiles.*, (SELECT COUNT(*) FROM data_publishers WHERE credential_id = credential_profiles.id) AS usage_count")
	if input.Type != nil {
		query = query.Where("type = ?", *input.Type)
	}
	if input.Search != "" {
		query = query.Where(`LOWER(name) LIKE ? ESCAPE '\'`, "%"+escapeLike(strings.ToLower(input.Search))+"%")
	}
	profiles := make([]credential.Profile, 0)
	if err := query.Order("created_at ASC, id ASC").Limit(200).Find(&profiles).Error; err != nil {
		return nil, mapError(err)
	}
	return profiles, nil
}

func (repository *repository) Update(ctx context.Context, profile *credential.Profile) error {
	if repository == nil || repository.db == nil || ctx == nil || profile == nil || profile.ID == uuid.Nil {
		return credential.ErrInvalidInput
	}
	result := repository.db.WithContext(ctx).Table("credential_profiles").Where("id = ?", profile.ID).Updates(map[string]any{
		"name": profile.Name, "description": profile.Description, "updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
	})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return credential.ErrNotFound
	}
	return nil
}

func (repository *repository) Delete(ctx context.Context, id uuid.UUID) error {
	if repository == nil || repository.db == nil || ctx == nil || id == uuid.Nil {
		return credential.ErrInvalidInput
	}
	result := repository.db.WithContext(ctx).Table("credential_profiles").Where("id = ?", id).Delete(&credential.Profile{})
	if result.Error != nil {
		return mapError(result.Error)
	}
	if result.RowsAffected == 0 {
		return credential.ErrNotFound
	}
	return nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func mapError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return credential.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "idx_credential_profiles_name_ci":
		return credential.ErrNameExists
	case "data_publishers_credential_id_fkey":
		return credential.ErrInUse
	case "credential_profiles_pkey", "credential_profiles_type_check", "credential_profiles_name_check", "credential_profiles_description_check", "credential_profiles_secret_revision_check":
		return credential.ErrInvalid
	}
	if postgresError.Code == "23503" {
		return credential.ErrInUse
	}
	return err
}

var _ credential.Repository = (*repository)(nil)
