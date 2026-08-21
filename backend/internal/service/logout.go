package service

import (
	"context"

	"github.com/google/uuid"
)

func (s *authService) Logout(
	ctx context.Context,
	authenticatedUserID uuid.UUID,
	refreshToken string,
) error {
	claims, err := s.ParseToken(
		refreshToken,
		TokenTypeRefresh,
	)
	if err != nil {
		return ErrSessionExpired
	}

	tokenUserID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return ErrSessionExpired
	}

	if tokenUserID != authenticatedUserID {
		return ErrSessionExpired
	}

	revoked, err := s.users.IsTokenRevoked(
		ctx,
		claims.ID,
	)
	if err != nil {
		return err
	}

	if revoked {
		return nil
	}

	if claims.ExpiresAt == nil {
		return ErrSessionExpired
	}

	return s.users.RevokeToken(
		ctx,
		claims.ID,
		authenticatedUserID,
		claims.ExpiresAt.Time,
	)
}
