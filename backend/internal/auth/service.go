package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidAuthConfig      = errors.New("invalid auth configuration")
	ErrUserRepositoryRequired = errors.New("user repository is required")
	ErrSetupAlreadyCompleted  = errors.New("initial setup already completed")
)

type AuthService interface {
	IsSetupRequired(ctx context.Context) (bool, error)
	ValidatePassword(username, password string) error
	HashPassword(password string) (string, error)
	VerifyPassword(password, passwordHash string) bool
	GenerateTokenPair(user *User) (*TokenPair, error)
	ParseToken(tokenString, expectedType string) (*TokenClaims, error)
	Setup(
		ctx context.Context,
		username string,
		password string,
	) (*User, error)
	Login(
		ctx context.Context,
		username string,
		password string,
	) (*TokenPair, error)
	Refresh(
		ctx context.Context,
		refreshToken string,
	) (*AccessTokenResult, error)
	Logout(
		ctx context.Context,
		authenticatedUserID uuid.UUID,
		refreshToken string,
	) error
	CurrentUser(
		ctx context.Context,
		userID uuid.UUID,
	) (*User, error)
	ChangePassword(
		ctx context.Context,
		userID uuid.UUID,
		currentPassword string,
		newPassword string,
	) error
}

type ServiceConfig struct {
	JWTSecret        string
	JWTAccessExpiry  time.Duration
	JWTRefreshExpiry time.Duration
}

type authService struct {
	users         UserRepository
	jwtSecret     []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
	now           func() time.Time
}

func NewAuthService(users UserRepository, cfg *ServiceConfig) (AuthService, error) {
	if users == nil {
		return nil, ErrUserRepositoryRequired
	}
	if cfg == nil || len([]byte(cfg.JWTSecret)) < 32 || cfg.JWTAccessExpiry <= 0 || cfg.JWTRefreshExpiry <= 0 {
		return nil, ErrInvalidAuthConfig
	}

	return &authService{
		users:         users,
		jwtSecret:     []byte(cfg.JWTSecret),
		accessExpiry:  cfg.JWTAccessExpiry,
		refreshExpiry: cfg.JWTRefreshExpiry,
		now:           time.Now,
	}, nil
}

func (s *authService) IsSetupRequired(ctx context.Context) (bool, error) {
	count, err := s.users.CountUsers(ctx)
	if err != nil {
		return false, err
	}

	return count == 0, nil
}

func (s *authService) Setup(
	ctx context.Context,
	username string,
	password string,
) (*User, error) {

	if err := s.ValidatePassword(username, password); err != nil {
		return nil, err
	}

	passwordHash, err := s.HashPassword(password)
	if err != nil {
		return nil, err
	}

	user := &User{
		Username:     username,
		PasswordHash: passwordHash,
	}

	if err := s.users.CreateInitialUser(ctx, user); err != nil {
		if errors.Is(err, ErrInitialUserExists) {
			return nil, ErrSetupAlreadyCompleted
		}

		return nil, err
	}

	return user, nil
}

func (s *authService) Login(
	ctx context.Context,
	username string,
	password string,
) (*TokenPair, error) {
	user, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}

		return nil, err
	}

	now := s.now().UTC()
	if user.IsLocked &&
		(user.LockedUntil == nil || now.Before(*user.LockedUntil)) {
		return nil, ErrAccountLocked
	}

	if !s.VerifyPassword(password, user.PasswordHash) {
		user.FailedAttempts++

		lockDuration := lockDurationForFailedAttempts(
			user.FailedAttempts,
		)

		if lockDuration > 0 {
			lockedUntil := now.Add(lockDuration)

			user.IsLocked = true
			user.LockedUntil = &lockedUntil
		}

		if err := s.users.Update(ctx, user); err != nil {
			return nil, err
		}

		return nil, ErrInvalidCredentials
	}

	user.FailedAttempts = 0
	user.IsLocked = false
	user.LockedUntil = nil
	user.LastLogin = &now

	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}

	return s.GenerateTokenPair(user)
}

var _ AuthService = (*authService)(nil)
