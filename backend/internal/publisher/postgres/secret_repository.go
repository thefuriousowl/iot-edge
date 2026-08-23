package publisherpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	"gorm.io/gorm"
)

type secretRepository struct{ db *gorm.DB }

type secretRow struct {
	ID           uuid.UUID            `gorm:"column:id"`
	CredentialID uuid.UUID            `gorm:"column:credential_id"`
	Name         string               `gorm:"column:name"`
	Kind         publisher.SecretKind `gorm:"column:kind"`
	KeyID        string               `gorm:"column:key_id"`
	Ciphertext   []byte               `gorm:"column:ciphertext"`
	Revision     uint64               `gorm:"column:revision"`
	CreatedAt    time.Time            `gorm:"column:created_at"`
	RotatedAt    time.Time            `gorm:"column:rotated_at"`
}

func (secretRow) TableName() string { return "credential_secrets" }

func NewSecretRepository(db *gorm.DB) publisher.SecretRepository { return &secretRepository{db: db} }

func (repository *secretRepository) Upsert(ctx context.Context, secret *publisher.EncryptedSecret) (*publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || secret == nil || secret.Metadata.PublisherID == uuid.Nil || secret.Metadata.Reference.Validate() != nil || len(secret.Ciphertext) == 0 {
		return nil, publisher.ErrInvalidInput
	}
	var metadata *publisher.SecretMetadata
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row secretRow
		result := tx.Raw(`
			INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext)
			VALUES (?,?,?,?,?)
			ON CONFLICT (credential_id,name) DO UPDATE SET
				key_id=EXCLUDED.key_id,
				ciphertext=EXCLUDED.ciphertext,
				revision=credential_secrets.revision+1,
				rotated_at=CURRENT_TIMESTAMP
			WHERE credential_secrets.kind=EXCLUDED.kind
			RETURNING id,credential_id,name,kind,key_id,ciphertext,revision,created_at,rotated_at`,
			secret.Metadata.PublisherID, secret.Metadata.Reference.Name, secret.Metadata.Kind, secret.KeyID, secret.Ciphertext,
		).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existingKind publisher.SecretKind
			if err := tx.Model(&secretRow{}).Select("kind").Where("credential_id = ? AND name = ?", secret.Metadata.PublisherID, secret.Metadata.Reference.Name).Scan(&existingKind).Error; err != nil {
				return err
			}
			if existingKind != "" && existingKind != secret.Metadata.Kind {
				return publisher.ErrSecretKindMismatch
			}
			return publisher.ErrInvalidSecretMaterial
		}
		var credentialRevision uint64
		result = tx.Raw(`UPDATE credential_profiles SET secret_revision=secret_revision+1,updated_at=CURRENT_TIMESTAMP WHERE id=? RETURNING secret_revision`, secret.Metadata.PublisherID).Scan(&credentialRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrPublisherNotFound
		}
		if err := tx.Exec(`UPDATE data_publishers SET secret_revision=secret_revision+1,updated_at=CURRENT_TIMESTAMP WHERE credential_id=?`, secret.Metadata.PublisherID).Error; err != nil {
			return err
		}
		value := metadataFromSecretRow(row, credentialRevision)
		metadata = &value
		secret.ID = row.ID
		secret.Metadata = value
		return nil
	})
	if err != nil {
		return nil, mapSecretError(err)
	}
	return metadata, nil
}

func (repository *secretRepository) FindEncrypted(ctx context.Context, credentialID uuid.UUID, reference publisher.SecretReference) (*publisher.EncryptedSecret, error) {
	if repository == nil || repository.db == nil || ctx == nil || credentialID == uuid.Nil || reference.Validate() != nil {
		return nil, publisher.ErrInvalidInput
	}
	var row secretRow
	if err := repository.db.WithContext(ctx).First(&row, "credential_id = ? AND name = ?", credentialID, reference.Name).Error; err != nil {
		return nil, mapSecretError(err)
	}
	var credentialRevision uint64
	if err := repository.db.WithContext(ctx).Table("credential_profiles").Select("secret_revision").Where("id = ?", credentialID).Scan(&credentialRevision).Error; err != nil {
		return nil, mapSecretError(err)
	}
	return &publisher.EncryptedSecret{
		ID: row.ID, Metadata: metadataFromSecretRow(row, credentialRevision), KeyID: row.KeyID,
		Ciphertext: append([]byte(nil), row.Ciphertext...),
	}, nil
}

func (repository *secretRepository) ListMetadata(ctx context.Context, credentialID uuid.UUID) ([]publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || credentialID == uuid.Nil {
		return nil, publisher.ErrInvalidInput
	}
	var credentialRevision uint64
	result := repository.db.WithContext(ctx).Table("credential_profiles").Select("secret_revision").Where("id = ?", credentialID).Scan(&credentialRevision)
	if result.Error != nil {
		return nil, mapSecretError(result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, publisher.ErrPublisherNotFound
	}
	rows := make([]secretRow, 0)
	if err := repository.db.WithContext(ctx).Where("credential_id = ?", credentialID).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, mapSecretError(err)
	}
	metadata := make([]publisher.SecretMetadata, len(rows))
	for index, row := range rows {
		metadata[index] = metadataFromSecretRow(row, credentialRevision)
	}
	return metadata, nil
}

func (repository *secretRepository) Delete(ctx context.Context, credentialID uuid.UUID, reference publisher.SecretReference) (*publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || credentialID == uuid.Nil || reference.Validate() != nil {
		return nil, publisher.ErrInvalidInput
	}
	var metadata *publisher.SecretMetadata
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row secretRow
		result := tx.Raw(`DELETE FROM credential_secrets WHERE credential_id=? AND name=? RETURNING id,credential_id,name,kind,key_id,ciphertext,revision,created_at,rotated_at`, credentialID, reference.Name).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrSecretNotFound
		}
		var values struct {
			Revision  uint64    `gorm:"column:secret_revision"`
			DeletedAt time.Time `gorm:"column:deleted_at"`
		}
		result = tx.Raw(`UPDATE credential_profiles SET secret_revision=secret_revision+1,updated_at=CURRENT_TIMESTAMP WHERE id=? RETURNING secret_revision,CURRENT_TIMESTAMP AS deleted_at`, credentialID).Scan(&values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrPublisherNotFound
		}
		if err := tx.Exec(`UPDATE data_publishers SET secret_revision=secret_revision+1,updated_at=CURRENT_TIMESTAMP WHERE credential_id=?`, credentialID).Error; err != nil {
			return err
		}
		value := metadataFromSecretRow(row, values.Revision)
		value.Revision++
		value.RotatedAt = values.DeletedAt.UTC()
		metadata = &value
		return nil
	})
	if err != nil {
		return nil, mapSecretError(err)
	}
	return metadata, nil
}

func metadataFromSecretRow(row secretRow, publisherRevision uint64) publisher.SecretMetadata {
	return publisher.SecretMetadata{
		PublisherID: row.CredentialID, Reference: publisher.SecretReference{Name: row.Name}, Kind: row.Kind,
		Revision: row.Revision, PublisherRevision: publisherRevision, CreatedAt: row.CreatedAt.UTC(), RotatedAt: row.RotatedAt.UTC(),
	}
}

func mapSecretError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return publisher.ErrSecretNotFound
	}
	if errors.Is(err, publisher.ErrSecretNotFound) || errors.Is(err, publisher.ErrSecretKindMismatch) || errors.Is(err, publisher.ErrPublisherNotFound) || errors.Is(err, publisher.ErrInvalidSecretMaterial) {
		return err
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.ConstraintName {
	case "credential_secrets_credential_id_fkey":
		return publisher.ErrPublisherNotFound
	case "credential_secrets_credential_name_key":
		return publisher.ErrSecretKindMismatch
	case "credential_secrets_pkey", "credential_secrets_name_check", "credential_secrets_kind_check",
		"credential_secrets_key_id_check", "credential_secrets_ciphertext_check", "credential_secrets_revision_check",
		"credential_secrets_rotation_check", "credential_profiles_secret_revision_check", "data_publishers_secret_revision_check":
		return publisher.ErrInvalidSecretMaterial
	default:
		return err
	}
}

var _ publisher.SecretRepository = (*secretRepository)(nil)
