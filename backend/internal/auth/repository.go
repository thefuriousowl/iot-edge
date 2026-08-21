package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrInitialUserExists = errors.New("initial user already exists")
)

type UserRepository interface {
	CountUsers(ctx context.Context) (int64, error)
	Create(ctx context.Context, user *User) error
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByUsername(ctx context.Context, username string) (*User, error)
	Update(ctx context.Context, user *User) error
	AddPasswordHistory(ctx context.Context, userID uuid.UUID, passwordHash string) error
	RecentPasswordHashes(ctx context.Context, userID uuid.UUID, limit int) ([]string, error)
	RevokeToken(ctx context.Context, jti string, userID uuid.UUID, expiresAt time.Time) error
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
	CreateInitialUser(ctx context.Context, user *User) error
}
