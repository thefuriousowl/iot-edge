package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type currentUserRepository struct {
	UserRepository
	user           *User
	err            error
	receivedCtx    context.Context
	receivedUserID uuid.UUID
	calls          int
}

func (r *currentUserRepository) FindByID(
	ctx context.Context,
	userID uuid.UUID,
) (*User, error) {
	r.calls++
	r.receivedCtx = ctx
	r.receivedUserID = userID
	return r.user, r.err
}

func TestCurrentUser_ReturnsAuthenticatedUser(t *testing.T) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	user := &User{
		ID:          uuid.New(),
		Username:    "admin",
		IsLocked:    true,
		LockedUntil: timePointer(now.Add(-time.Minute)),
	}
	users := &currentUserRepository{user: user}
	auth := newRefreshTestService(t, users, now)
	ctx := context.WithValue(context.Background(), struct{}{}, "current user")

	var authInterface AuthService = auth
	result, err := authInterface.CurrentUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("CurrentUser() error: %v", err)
	}
	if result != user {
		t.Errorf("CurrentUser() = %p, want repository user %p", result, user)
	}
	if users.calls != 1 || users.receivedUserID != user.ID {
		t.Errorf("FindByID() calls/ID = %d/%s, want 1/%s", users.calls, users.receivedUserID, user.ID)
	}
	if users.receivedCtx != ctx {
		t.Error("CurrentUser() did not pass context to repository")
	}
}

func TestCurrentUser_MapsMissingUserToSessionExpired(t *testing.T) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		users *currentUserRepository
	}{
		{
			name:  "repository not found",
			users: &currentUserRepository{err: ErrUserNotFound},
		},
		{
			name:  "nil user",
			users: &currentUserRepository{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := newRefreshTestService(t, tt.users, now)

			result, err := auth.CurrentUser(context.Background(), uuid.New())
			if !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("CurrentUser() error = %v, want ErrSessionExpired", err)
			}
			if result != nil {
				t.Errorf("CurrentUser() = %#v, want nil", result)
			}
		})
	}
}

func TestCurrentUser_RejectsActiveAccountLock(t *testing.T) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		lockedUntil *time.Time
	}{
		{name: "lock without expiry"},
		{name: "temporary lock", lockedUntil: timePointer(now.Add(time.Minute))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &currentUserRepository{user: &User{
				ID:          uuid.New(),
				Username:    "admin",
				IsLocked:    true,
				LockedUntil: tt.lockedUntil,
			}}
			auth := newRefreshTestService(t, users, now)

			result, err := auth.CurrentUser(context.Background(), users.user.ID)
			if !errors.Is(err, ErrAccountLocked) {
				t.Fatalf("CurrentUser() error = %v, want ErrAccountLocked", err)
			}
			if result != nil {
				t.Errorf("CurrentUser() = %#v, want nil", result)
			}
		})
	}
}

func TestCurrentUser_ReturnsRepositoryError(t *testing.T) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	repositoryError := errors.New("test repository error")
	users := &currentUserRepository{err: repositoryError}
	auth := newRefreshTestService(t, users, now)

	result, err := auth.CurrentUser(context.Background(), uuid.New())
	if !errors.Is(err, repositoryError) {
		t.Fatalf("CurrentUser() error = %v, want repository error", err)
	}
	if result != nil {
		t.Errorf("CurrentUser() = %#v, want nil", result)
	}
}
