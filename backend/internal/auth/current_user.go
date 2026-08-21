package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

func (s *authService) CurrentUser(
	ctx context.Context,
	userID uuid.UUID,
) (*User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrSessionExpired
		}
		return nil, err
	}
	if user == nil {
		return nil, ErrSessionExpired
	}

	now := s.now().UTC()
	if user.IsLocked &&
		(user.LockedUntil == nil || now.Before(*user.LockedUntil)) {
		return nil, ErrAccountLocked
	}

	return user, nil
}
