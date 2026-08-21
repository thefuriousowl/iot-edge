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

type logoutUserRepository struct {
	repository.UserRepository
	revoked            bool
	revokedErr         error
	revokeErr          error
	receivedRevokedCtx context.Context
	receivedRevokeCtx  context.Context
	receivedJTI        string
	receivedUserID     uuid.UUID
	receivedExpiresAt  time.Time
	revokedCalls       int
	revokeCalls        int
}

func (r *logoutUserRepository) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	r.revokedCalls++
	r.receivedRevokedCtx = ctx
	r.receivedJTI = jti
	return r.revoked, r.revokedErr
}

func (r *logoutUserRepository) RevokeToken(
	ctx context.Context,
	jti string,
	userID uuid.UUID,
	expiresAt time.Time,
) error {
	r.revokeCalls++
	r.receivedRevokeCtx = ctx
	r.receivedJTI = jti
	r.receivedUserID = userID
	r.receivedExpiresAt = expiresAt
	return r.revokeErr
}

func TestLogout_RevokesAuthenticatedUsersRefreshToken(t *testing.T) {
	now := time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	users := &logoutUserRepository{}
	auth := newRefreshTestService(t, users, now)
	refreshToken, claims := newRefreshTestToken(t, auth, user, now)
	ctx := context.WithValue(context.Background(), struct{}{}, "logout")

	var authInterface AuthService = auth
	if err := authInterface.Logout(ctx, user.ID, refreshToken); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}
	if users.revokedCalls != 1 || users.revokeCalls != 1 {
		t.Errorf("repository calls = revoked %d/revoke %d, want 1/1", users.revokedCalls, users.revokeCalls)
	}
	if users.receivedJTI != claims.ID {
		t.Errorf("revoke JTI = %q, want %q", users.receivedJTI, claims.ID)
	}
	if users.receivedUserID != user.ID {
		t.Errorf("revoke user ID = %s, want %s", users.receivedUserID, user.ID)
	}
	if claims.ExpiresAt == nil || !users.receivedExpiresAt.Equal(claims.ExpiresAt.Time) {
		t.Errorf("revoke expiry = %s, want token expiry", users.receivedExpiresAt)
	}
	if users.receivedRevokedCtx != ctx || users.receivedRevokeCtx != ctx {
		t.Error("Logout() did not pass context to repository calls")
	}
}

func TestLogout_RejectsInvalidAndMismatchedTokens(t *testing.T) {
	now := time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}

	t.Run("malformed token", func(t *testing.T) {
		users := &logoutUserRepository{}
		auth := newRefreshTestService(t, users, now)

		err := auth.Logout(context.Background(), user.ID, "not-a-token")
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Logout() error = %v, want ErrSessionExpired", err)
		}
		if users.revokedCalls != 0 || users.revokeCalls != 0 {
			t.Error("malformed token reached repository")
		}
	})

	t.Run("access token has wrong type", func(t *testing.T) {
		users := &logoutUserRepository{}
		auth := newRefreshTestService(t, users, now)
		access, err := auth.generateAccessToken(user, now)
		if err != nil {
			t.Fatalf("generateAccessToken() error: %v", err)
		}

		err = auth.Logout(context.Background(), user.ID, access.AccessToken)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Logout() error = %v, want ErrSessionExpired", err)
		}
	})

	t.Run("token belongs to another user", func(t *testing.T) {
		users := &logoutUserRepository{}
		auth := newRefreshTestService(t, users, now)
		refreshToken, _ := newRefreshTestToken(t, auth, user, now)

		err := auth.Logout(context.Background(), uuid.New(), refreshToken)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Logout() error = %v, want ErrSessionExpired", err)
		}
		if users.revokedCalls != 0 || users.revokeCalls != 0 {
			t.Error("mismatched token reached repository")
		}
	})

	t.Run("expired refresh token", func(t *testing.T) {
		users := &logoutUserRepository{}
		auth := newRefreshTestService(t, users, now)
		refreshToken, _ := newRefreshTestToken(t, auth, user, now)
		auth.now = func() time.Time { return now.Add(auth.refreshExpiry + time.Second) }

		err := auth.Logout(context.Background(), user.ID, refreshToken)
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("Logout() error = %v, want ErrSessionExpired", err)
		}
	})
}

func TestLogout_AlreadyRevokedIsIdempotent(t *testing.T) {
	now := time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	users := &logoutUserRepository{revoked: true}
	auth := newRefreshTestService(t, users, now)
	refreshToken, _ := newRefreshTestToken(t, auth, user, now)

	if err := auth.Logout(context.Background(), user.ID, refreshToken); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}
	if users.revokedCalls != 1 || users.revokeCalls != 0 {
		t.Errorf("repository calls = revoked %d/revoke %d, want 1/0", users.revokedCalls, users.revokeCalls)
	}
}

func TestLogout_ReturnsRepositoryErrors(t *testing.T) {
	now := time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC)
	user := &domain.User{ID: uuid.New(), Username: "admin"}
	repositoryError := errors.New("test repository error")
	tests := []struct {
		name  string
		users *logoutUserRepository
	}{
		{
			name:  "revocation lookup error",
			users: &logoutUserRepository{revokedErr: repositoryError},
		},
		{
			name:  "revoke error",
			users: &logoutUserRepository{revokeErr: repositoryError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := newRefreshTestService(t, tt.users, now)
			refreshToken, _ := newRefreshTestToken(t, auth, user, now)

			err := auth.Logout(context.Background(), user.ID, refreshToken)
			if !errors.Is(err, repositoryError) {
				t.Fatalf("Logout() error = %v, want repository error", err)
			}
		})
	}
}
