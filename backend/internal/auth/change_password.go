package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const passwordHistoryDepth = 3

var ErrPasswordRecentlyUsed = errors.New("password recently used")

func (s *authService) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	if userID == uuid.Nil {
		return ErrSessionExpired
	}
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrSessionExpired
		}
		return err
	}
	if user == nil {
		return ErrSessionExpired
	}
	now := s.now().UTC()
	if user.IsLocked && (user.LockedUntil == nil || now.Before(*user.LockedUntil)) {
		return ErrAccountLocked
	}
	if !s.VerifyPassword(currentPassword, user.PasswordHash) {
		return ErrInvalidCredentials
	}
	if err := s.ValidatePassword(user.Username, newPassword); err != nil {
		return err
	}
	if s.VerifyPassword(newPassword, user.PasswordHash) {
		return ErrPasswordRecentlyUsed
	}
	previousPasswordLimit := passwordHistoryDepth - 1
	hashes, err := s.users.RecentPasswordHashes(ctx, user.ID, previousPasswordLimit)
	if err != nil {
		return err
	}
	for _, hash := range hashes {
		if s.VerifyPassword(newPassword, hash) {
			return ErrPasswordRecentlyUsed
		}
	}
	newHash, err := s.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.users.ChangePassword(ctx, user.ID, user.PasswordHash, newHash, previousPasswordLimit); err != nil {
		if errors.Is(err, ErrPasswordChangeConflict) || errors.Is(err, ErrUserNotFound) {
			return ErrSessionExpired
		}
		return err
	}
	return nil
}
