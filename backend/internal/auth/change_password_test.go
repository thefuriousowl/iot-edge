package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type changePasswordRepository struct {
	UserRepository
	user                 *User
	findErr              error
	recentHashes         []string
	recentErr            error
	changeErr            error
	receivedCtx          context.Context
	receivedUserID       uuid.UUID
	receivedExpectedHash string
	receivedNewHash      string
	receivedHistoryLimit int
	recentCalls          int
	changeCalls          int
}

func (repository *changePasswordRepository) FindByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	repository.receivedCtx = ctx
	repository.receivedUserID = userID
	return repository.user, repository.findErr
}

func (repository *changePasswordRepository) RecentPasswordHashes(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	repository.receivedCtx = ctx
	repository.receivedUserID = userID
	repository.receivedHistoryLimit = limit
	repository.recentCalls++
	return repository.recentHashes, repository.recentErr
}

func (repository *changePasswordRepository) ChangePassword(ctx context.Context, userID uuid.UUID, expectedHash, newHash string, previousPasswordLimit int) error {
	repository.receivedCtx = ctx
	repository.receivedUserID = userID
	repository.receivedExpectedHash = expectedHash
	repository.receivedNewHash = newHash
	repository.receivedHistoryLimit = previousPasswordLimit
	repository.changeCalls++
	return repository.changeErr
}

func TestChangePassword_VerifiesPolicyHistoryAndPersistsHashedPassword(t *testing.T) {
	service := newTestAuthService(t)
	currentHash, err := service.HashPassword("CurrentP@ss1")
	if err != nil {
		t.Fatalf("HashPassword(current) error = %v", err)
	}
	olderHash, err := service.HashPassword("OlderP@ss2")
	if err != nil {
		t.Fatalf("HashPassword(older) error = %v", err)
	}
	userID := uuid.New()
	repository := &changePasswordRepository{user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, recentHashes: []string{olderHash}}
	service.users = repository
	ctx := context.WithValue(context.Background(), struct{}{}, "change-password")
	if err := service.ChangePassword(ctx, userID, "CurrentP@ss1", "FreshP@ss3"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if repository.recentCalls != 1 || repository.changeCalls != 1 || repository.receivedCtx != ctx || repository.receivedUserID != userID || repository.receivedHistoryLimit != passwordHistoryDepth-1 || repository.receivedExpectedHash != currentHash {
		t.Errorf("repository calls = %#v", repository)
	}
	if repository.receivedNewHash == "" || repository.receivedNewHash == "FreshP@ss3" || !service.VerifyPassword("FreshP@ss3", repository.receivedNewHash) {
		t.Errorf("new password hash = %q", repository.receivedNewHash)
	}
}

func TestChangePassword_RejectsInvalidAndReusedPasswordsWithoutMutation(t *testing.T) {
	service := newTestAuthService(t)
	currentHash, err := service.HashPassword("CurrentP@ss1")
	if err != nil {
		t.Fatalf("HashPassword(current) error = %v", err)
	}
	olderHash, err := service.HashPassword("OlderP@ss2")
	if err != nil {
		t.Fatalf("HashPassword(older) error = %v", err)
	}
	userID := uuid.New()
	tests := []struct {
		name       string
		userID     uuid.UUID
		user       *User
		findErr    error
		current    string
		candidate  string
		hashes     []string
		recentErr  error
		changeErr  error
		wantErr    error
		wantRecent int
		wantChange int
	}{
		{name: "nil user ID", current: "CurrentP@ss1", candidate: "FreshP@ss3", wantErr: ErrSessionExpired},
		{name: "missing user", userID: userID, findErr: ErrUserNotFound, current: "CurrentP@ss1", candidate: "FreshP@ss3", wantErr: ErrSessionExpired},
		{name: "wrong current password", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "WrongP@ss9", candidate: "FreshP@ss3", wantErr: ErrInvalidCredentials},
		{name: "active lock", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash, IsLocked: true}, current: "CurrentP@ss1", candidate: "FreshP@ss3", wantErr: ErrAccountLocked},
		{name: "invalid new password", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "CurrentP@ss1", candidate: "short", wantErr: ErrPasswordRequirements},
		{name: "current password reuse", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "CurrentP@ss1", candidate: "CurrentP@ss1", wantErr: ErrPasswordRecentlyUsed},
		{name: "historical password reuse", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "CurrentP@ss1", candidate: "OlderP@ss2", hashes: []string{olderHash}, wantErr: ErrPasswordRecentlyUsed, wantRecent: 1},
		{name: "history failure", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "CurrentP@ss1", candidate: "FreshP@ss3", recentErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded, wantRecent: 1},
		{name: "concurrent change", userID: userID, user: &User{ID: userID, Username: "owner", PasswordHash: currentHash}, current: "CurrentP@ss1", candidate: "FreshP@ss3", changeErr: ErrPasswordChangeConflict, wantErr: ErrSessionExpired, wantRecent: 1, wantChange: 1},
	}
	service.now = func() time.Time { return time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC) }
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &changePasswordRepository{user: test.user, findErr: test.findErr, recentHashes: test.hashes, recentErr: test.recentErr, changeErr: test.changeErr}
			service.users = repository
			err := service.ChangePassword(context.Background(), test.userID, test.current, test.candidate)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ChangePassword() error = %v, want %v", err, test.wantErr)
			}
			if repository.recentCalls != test.wantRecent || repository.changeCalls != test.wantChange {
				t.Errorf("repository calls = recent %d/change %d, want %d/%d", repository.recentCalls, repository.changeCalls, test.wantRecent, test.wantChange)
			}
		})
	}
}
