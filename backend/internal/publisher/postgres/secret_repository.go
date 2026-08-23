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
	ID          uuid.UUID            `gorm:"column:id"`
	PublisherID uuid.UUID            `gorm:"column:publisher_id"`
	Name        string               `gorm:"column:name"`
	Kind        publisher.SecretKind `gorm:"column:kind"`
	KeyID       string               `gorm:"column:key_id"`
	Ciphertext  []byte               `gorm:"column:ciphertext"`
	Revision    uint64               `gorm:"column:revision"`
	CreatedAt   time.Time            `gorm:"column:created_at"`
	RotatedAt   time.Time            `gorm:"column:rotated_at"`
}

func (secretRow) TableName() string { return "data_publisher_secrets" }

func NewSecretRepository(db *gorm.DB) publisher.SecretRepository { return &secretRepository{db: db} }

func (repository *secretRepository) Upsert(ctx context.Context, secret *publisher.EncryptedSecret) (*publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || secret == nil || secret.Metadata.PublisherID == uuid.Nil || secret.Metadata.Reference.Validate() != nil || len(secret.Ciphertext) == 0 {
		return nil, publisher.ErrInvalidInput
	}
	var metadata *publisher.SecretMetadata
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row secretRow
		result := tx.Raw(`
			INSERT INTO data_publisher_secrets (publisher_id,name,kind,key_id,ciphertext)
			VALUES (?,?,?,?,?)
			ON CONFLICT (publisher_id,name) DO UPDATE SET
				key_id=EXCLUDED.key_id,
				ciphertext=EXCLUDED.ciphertext,
				revision=data_publisher_secrets.revision+1,
				rotated_at=CURRENT_TIMESTAMP
			WHERE data_publisher_secrets.kind=EXCLUDED.kind
			RETURNING id,publisher_id,name,kind,key_id,ciphertext,revision,created_at,rotated_at`,
			secret.Metadata.PublisherID, secret.Metadata.Reference.Name, secret.Metadata.Kind, secret.KeyID, secret.Ciphertext,
		).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existingKind publisher.SecretKind
			if err := tx.Model(&secretRow{}).Select("kind").Where("publisher_id = ? AND name = ?", secret.Metadata.PublisherID, secret.Metadata.Reference.Name).Scan(&existingKind).Error; err != nil {
				return err
			}
			if existingKind != "" && existingKind != secret.Metadata.Kind {
				return publisher.ErrSecretKindMismatch
			}
			return publisher.ErrInvalidSecretMaterial
		}
		var publisherRevision uint64
		result = tx.Raw(`UPDATE data_publishers SET secret_revision=secret_revision+1 WHERE id=? RETURNING secret_revision`, secret.Metadata.PublisherID).Scan(&publisherRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrPublisherNotFound
		}
		value := metadataFromSecretRow(row, publisherRevision)
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

func (repository *secretRepository) FindEncrypted(ctx context.Context, publisherID uuid.UUID, reference publisher.SecretReference) (*publisher.EncryptedSecret, error) {
	if repository == nil || repository.db == nil || ctx == nil || publisherID == uuid.Nil || reference.Validate() != nil {
		return nil, publisher.ErrInvalidInput
	}
	var row secretRow
	if err := repository.db.WithContext(ctx).First(&row, "publisher_id = ? AND name = ?", publisherID, reference.Name).Error; err != nil {
		return nil, mapSecretError(err)
	}
	var publisherRevision uint64
	if err := repository.db.WithContext(ctx).Model(&publisher.Publisher{}).Select("secret_revision").Where("id = ?", publisherID).Scan(&publisherRevision).Error; err != nil {
		return nil, mapSecretError(err)
	}
	return &publisher.EncryptedSecret{
		ID: row.ID, Metadata: metadataFromSecretRow(row, publisherRevision), KeyID: row.KeyID,
		Ciphertext: append([]byte(nil), row.Ciphertext...),
	}, nil
}

func (repository *secretRepository) ListMetadata(ctx context.Context, publisherID uuid.UUID) ([]publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || publisherID == uuid.Nil {
		return nil, publisher.ErrInvalidInput
	}
	var publisherRevision uint64
	result := repository.db.WithContext(ctx).Model(&publisher.Publisher{}).Select("secret_revision").Where("id = ?", publisherID).Scan(&publisherRevision)
	if result.Error != nil {
		return nil, mapSecretError(result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, publisher.ErrPublisherNotFound
	}
	rows := make([]secretRow, 0)
	if err := repository.db.WithContext(ctx).Where("publisher_id = ?", publisherID).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, mapSecretError(err)
	}
	metadata := make([]publisher.SecretMetadata, len(rows))
	for index, row := range rows {
		metadata[index] = metadataFromSecretRow(row, publisherRevision)
	}
	return metadata, nil
}

func (repository *secretRepository) Delete(ctx context.Context, publisherID uuid.UUID, reference publisher.SecretReference) (*publisher.SecretMetadata, error) {
	if repository == nil || repository.db == nil || ctx == nil || publisherID == uuid.Nil || reference.Validate() != nil {
		return nil, publisher.ErrInvalidInput
	}
	var metadata *publisher.SecretMetadata
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row secretRow
		result := tx.Raw(`DELETE FROM data_publisher_secrets WHERE publisher_id=? AND name=? RETURNING id,publisher_id,name,kind,key_id,ciphertext,revision,created_at,rotated_at`, publisherID, reference.Name).Scan(&row)
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
		result = tx.Raw(`UPDATE data_publishers SET secret_revision=secret_revision+1 WHERE id=? RETURNING secret_revision,CURRENT_TIMESTAMP AS deleted_at`, publisherID).Scan(&values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return publisher.ErrPublisherNotFound
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
		PublisherID: row.PublisherID, Reference: publisher.SecretReference{Name: row.Name}, Kind: row.Kind,
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
	case "data_publisher_secrets_publisher_id_fkey":
		return publisher.ErrPublisherNotFound
	case "data_publisher_secrets_publisher_name_key":
		return publisher.ErrSecretKindMismatch
	case "data_publisher_secrets_pkey", "data_publisher_secrets_name_check", "data_publisher_secrets_kind_check",
		"data_publisher_secrets_key_id_check", "data_publisher_secrets_ciphertext_check", "data_publisher_secrets_revision_check",
		"data_publisher_secrets_rotation_check", "data_publishers_secret_revision_check":
		return publisher.ErrInvalidSecretMaterial
	default:
		return err
	}
}

var _ publisher.SecretRepository = (*secretRepository)(nil)
