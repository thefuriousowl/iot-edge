package authpostgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/auth"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) auth.UserRepository {
	return &userRepository{db: db}
}

type passwordHistoryRecord struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID       uuid.UUID
	PasswordHash string
	CreatedAt    time.Time
}

func (passwordHistoryRecord) TableName() string {
	return "password_history"
}

type revokedTokenRecord struct {
	JTI       string `gorm:"column:jti;primaryKey"`
	UserID    uuid.UUID
	ExpiresAt time.Time
	RevokedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

func (revokedTokenRecord) TableName() string {
	return "revoked_tokens"
}

func (r *userRepository) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&auth.User{}).Count(&count).Error
	return count, err
}

func (r *userRepository) Create(ctx context.Context, user *auth.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *userRepository) FindByID(ctx context.Context, id uuid.UUID) (*auth.User, error) {
	var user auth.User
	if err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, mapUserError(err)
	}
	return &user, nil
}

func (r *userRepository) FindByUsername(ctx context.Context, username string) (*auth.User, error) {
	var user auth.User
	if err := r.db.WithContext(ctx).First(&user, "username = ?", username).Error; err != nil {
		return nil, mapUserError(err)
	}
	return &user, nil
}

func (r *userRepository) Update(ctx context.Context, user *auth.User) error {
	result := r.db.WithContext(ctx).
		Model(&auth.User{}).
		Where("id = ?", user.ID).
		Select(
			"username",
			"password_hash",
			"is_locked",
			"failed_attempts",
			"locked_until",
			"last_login",
		).
		Updates(user)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return auth.ErrUserNotFound
	}
	return nil
}

func (r *userRepository) AddPasswordHistory(
	ctx context.Context,
	userID uuid.UUID,
	passwordHash string,
) error {
	record := passwordHistoryRecord{
		UserID:       userID,
		PasswordHash: passwordHash,
	}
	return r.db.WithContext(ctx).Create(&record).Error
}

func (r *userRepository) RecentPasswordHashes(
	ctx context.Context,
	userID uuid.UUID,
	limit int,
) ([]string, error) {
	hashes := make([]string, 0)
	if limit <= 0 {
		return hashes, nil
	}

	err := r.db.WithContext(ctx).
		Model(&passwordHistoryRecord{}).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Pluck("password_hash", &hashes).Error
	return hashes, err
}

func (r *userRepository) ChangePassword(ctx context.Context, userID uuid.UUID, expectedHash, newHash string, previousPasswordLimit int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user auth.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
			return mapUserError(err)
		}
		if subtle.ConstantTimeCompare([]byte(user.PasswordHash), []byte(expectedHash)) != 1 {
			return auth.ErrPasswordChangeConflict
		}
		if err := tx.Create(&passwordHistoryRecord{UserID: userID, PasswordHash: user.PasswordHash}).Error; err != nil {
			return err
		}
		if previousPasswordLimit >= 0 {
			if err := tx.Exec(`
				DELETE FROM password_history
				WHERE user_id = ?
				AND id NOT IN (
					SELECT id FROM password_history
					WHERE user_id = ?
					ORDER BY created_at DESC, id DESC
					LIMIT ?
				)
			`, userID, userID, previousPasswordLimit).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&auth.User{}).
			Where("id = ? AND password_hash = ?", userID, expectedHash).
			Updates(map[string]any{
				"password_hash":   newHash,
				"session_version": gorm.Expr("session_version + 1"),
				"is_locked":       false,
				"failed_attempts": 0,
				"locked_until":    nil,
				"updated_at":      gorm.Expr("CURRENT_TIMESTAMP"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return auth.ErrPasswordChangeConflict
		}
		return nil
	})
}

func (r *userRepository) RevokeToken(
	ctx context.Context,
	jti string,
	userID uuid.UUID,
	expiresAt time.Time,
) error {
	record := revokedTokenRecord{
		JTI:       jti,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}
	return r.db.WithContext(ctx).Create(&record).Error
}

func (r *userRepository) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&revokedTokenRecord{}).
		Where("jti = ?", jti).
		Count(&count).Error
	return count > 0, err
}

func mapUserError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return auth.ErrUserNotFound
	}
	return err
}

func (r *userRepository) CreateInitialUser(
	ctx context.Context,
	user *auth.User,
) error {

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"LOCK TABLE users IN EXCLUSIVE MODE",
		).Error; err != nil {
			return err
		}

		var count int64
		if err := tx.Model(&auth.User{}).Count(&count).Error; err != nil {
			return err
		}

		if count > 0 {
			return auth.ErrInitialUserExists
		}

		return tx.Create(user).Error

	})
}

var _ auth.UserRepository = (*userRepository)(nil)
