package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/repository"
)

type refreshUserRepository struct {
	repository.UserRepository
	revoked            bool
	revokedErr         error
	user               *domain.User
	findErr            error
	receivedRevokedCtx context.Context
	receivedFindCtx    context.Context
	receivedJTI        string
	receivedUserID     uuid.UUID
	revokedCalls       int
	findCalls          int
}

func (r *refreshUserRepository) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	r.revokedCalls++
	r.receivedRevokedCtx = ctx
	r.receivedJTI = jti
	return r.revoked, r.revokedErr
}

func (r *refreshUserRepository) FindByID(
	ctx context.Context,
	userID uuid.UUID,
) (*domain.User, error) {
	r.findCalls++
	r.receivedFindCtx = ctx
	r.receivedUserID = userID
	return r.user, r.findErr
}

func TestRefresh_ReturnsNewAccessToken(t *testing.T) {
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	users := &refreshUserRepository{user: user}
	auth := newRefreshTestService(t, users, now)
	refreshToken, refreshClaims := newRefreshTestToken(t, auth, user, now)
	ctx := context.WithValue(context.Background(), struct{}{}, "refresh")

	var authInterface AuthService = auth
	result, err := authInterface.Refresh(ctx, refreshToken)
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if result == nil || result.AccessToken == "" {
		t.Fatalf("Refresh() result = %#v, want access token", result)
	}
	wantExpiry := now.Add(auth.accessExpiry)
	if !result.AccessExpiresAt.Equal(wantExpiry) {
		t.Errorf("access expiry = %s, want %s", result.AccessExpiresAt, wantExpiry)
	}
	claims, err := auth.ParseToken(result.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("parsing refreshed access token: %v", err)
	}
	if claims.Subject != user.ID.String() || claims.Username != user.Username {
		t.Errorf("access claims = %#v, want refreshed user", claims)
	}
	if users.revokedCalls != 1 || users.receivedJTI != refreshClaims.ID {
		t.Errorf("IsTokenRevoked() calls/JTI = %d/%q, want 1/%q", users.revokedCalls, users.receivedJTI, refreshClaims.ID)
	}
	if users.findCalls != 1 || users.receivedUserID != user.ID {
		t.Errorf("FindByID() calls/ID = %d/%s, want 1/%s", users.findCalls, users.receivedUserID, user.ID)
	}
	if users.receivedRevokedCtx != ctx || users.receivedFindCtx != ctx {
		t.Error("Refresh() did not pass context to repository calls")
	}
}

func TestRefresh_MapsInvalidTokensToSessionExpired(t *testing.T) {
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}

	t.Run("malformed token", func(t *testing.T) {
		users := &refreshUserRepository{}
		auth := newRefreshTestService(t, users, now)

		result, err := auth.Refresh(context.Background(), "not-a-token")
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Refresh() error = %v, want ErrSessionExpired", err)
		}
		if result != nil {
			t.Errorf("Refresh() result = %#v, want nil", result)
		}
		if users.revokedCalls != 0 || users.findCalls != 0 {
			t.Error("invalid token reached repository")
		}
	})

	t.Run("access token has wrong type", func(t *testing.T) {
		users := &refreshUserRepository{}
		auth := newRefreshTestService(t, users, now)
		access, err := auth.generateAccessToken(user, now)
		if err != nil {
			t.Fatalf("generateAccessToken() error: %v", err)
		}

		result, err := auth.Refresh(context.Background(), access.AccessToken)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Refresh() error = %v, want ErrSessionExpired", err)
		}
		if result != nil {
			t.Errorf("Refresh() result = %#v, want nil", result)
		}
	})

	t.Run("expired refresh token", func(t *testing.T) {
		users := &refreshUserRepository{}
		auth := newRefreshTestService(t, users, now)
		refreshToken, _ := newRefreshTestToken(t, auth, user, now)
		auth.now = func() time.Time { return now.Add(auth.refreshExpiry + time.Second) }

		result, err := auth.Refresh(context.Background(), refreshToken)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Refresh() error = %v, want ErrSessionExpired", err)
		}
		if result != nil {
			t.Errorf("Refresh() result = %#v, want nil", result)
		}
	})
}

func TestRefresh_MapsRevokedAndMissingSessions(t *testing.T) {
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	tests := []struct {
		name     string
		users    *refreshUserRepository
		wantFind bool
	}{
		{
			name:  "revoked token",
			users: &refreshUserRepository{revoked: true},
		},
		{
			name:     "missing user",
			users:    &refreshUserRepository{findErr: repository.ErrUserNotFound},
			wantFind: true,
		},
		{
			name:     "nil user",
			users:    &refreshUserRepository{},
			wantFind: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := newRefreshTestService(t, tt.users, now)
			refreshToken, _ := newRefreshTestToken(t, auth, user, now)

			result, err := auth.Refresh(context.Background(), refreshToken)
			if !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("Refresh() error = %v, want ErrSessionExpired", err)
			}
			if result != nil {
				t.Errorf("Refresh() result = %#v, want nil", result)
			}
			if (tt.users.findCalls == 1) != tt.wantFind {
				t.Errorf("FindByID() calls = %d, wantFind %t", tt.users.findCalls, tt.wantFind)
			}
		})
	}
}

func TestRefresh_RejectsLockedAccount(t *testing.T) {
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	user := &domain.User{
		ID:          uuid.New(),
		Username:    "admin",
		IsLocked:    true,
		LockedUntil: timePointer(now.Add(time.Minute)),
	}
	users := &refreshUserRepository{user: user}
	auth := newRefreshTestService(t, users, now)
	refreshToken, _ := newRefreshTestToken(t, auth, user, now)

	result, err := auth.Refresh(context.Background(), refreshToken)
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Refresh() error = %v, want ErrAccountLocked", err)
	}
	if result != nil {
		t.Errorf("Refresh() result = %#v, want nil", result)
	}
}

func TestRefresh_ReturnsRepositoryErrors(t *testing.T) {
	now := time.Date(2026, time.August, 21, 10, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	repositoryError := errors.New("test repository error")
	tests := []struct {
		name  string
		users *refreshUserRepository
	}{
		{
			name:  "revocation lookup error",
			users: &refreshUserRepository{revokedErr: repositoryError},
		},
		{
			name:  "user lookup error",
			users: &refreshUserRepository{findErr: repositoryError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := newRefreshTestService(t, tt.users, now)
			refreshToken, _ := newRefreshTestToken(t, auth, user, now)

			result, err := auth.Refresh(context.Background(), refreshToken)
			if !errors.Is(err, repositoryError) {
				t.Fatalf("Refresh() error = %v, want repository error", err)
			}
			if result != nil {
				t.Errorf("Refresh() result = %#v, want nil", result)
			}
		})
	}
}

func newRefreshTestService(
	t *testing.T,
	users repository.UserRepository,
	now time.Time,
) *authService {
	t.Helper()

	auth, err := NewAuthService(users, validAuthConfig())
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}
	concrete := auth.(*authService)
	concrete.now = func() time.Time { return now }
	return concrete
}

func newRefreshTestToken(
	t *testing.T,
	auth *authService,
	user *domain.User,
	now time.Time,
) (string, *TokenClaims) {
	t.Helper()

	result, err := auth.generateRefreshToken(user, now)
	if err != nil {
		t.Fatalf("generateRefreshToken() error: %v", err)
	}
	claims, err := auth.ParseToken(result.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("ParseToken(refresh) error: %v", err)
	}
	return result.RefreshToken, claims
}
