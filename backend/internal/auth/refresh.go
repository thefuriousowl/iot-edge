package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrSessionExpired = errors.New("session expired")

func (s *authService) Refresh(
	ctx context.Context,
	refreshToken string,
) (*AccessTokenResult, error) {

	claims, err := s.ParseToken(
		refreshToken,
		TokenTypeRefresh,
	)
	if err != nil {
		return nil, ErrSessionExpired
	}

	isRevoked, err := s.users.IsTokenRevoked(ctx, claims.ID)
	if err != nil {
		return nil, err
	}

	if isRevoked {
		return nil, ErrSessionExpired
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, ErrSessionExpired
	}

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
	if claims.SessionVersion != user.SessionVersion {
		return nil, ErrSessionExpired
	}

	now := s.now().UTC()

	if user.IsLocked && (user.LockedUntil == nil || now.Before(*user.LockedUntil)) {
		return nil, ErrAccountLocked
	}

	return s.generateAccessToken(user, now)
}
