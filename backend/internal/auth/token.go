package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

var (
	ErrInvalidToken        = errors.New("invalid token")
	ErrUnexpectedTokenType = errors.New("unexpected token type")
	ErrInvalidTokenUser    = errors.New("token user is invalid")
)

type TokenClaims struct {
	Username       string `json:"username,omitempty"`
	TokenType      string `json:"type"`
	SessionVersion int64  `json:"session_version,omitempty"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type AccessTokenResult struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

type RefreshTokenResult struct {
	RefreshToken     string
	RefreshExpiresAt time.Time
}

func (s *authService) GenerateTokenPair(user *User) (*TokenPair, error) {
	if user == nil || user.ID == uuid.Nil || user.Username == "" || user.SessionVersion < 0 {
		return nil, ErrInvalidTokenUser
	}

	now := s.now().UTC()

	access, err := s.generateAccessToken(user, now)
	if err != nil {
		return nil, err
	}

	refresh, err := s.generateRefreshToken(user, now)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:      access.AccessToken,
		RefreshToken:     refresh.RefreshToken,
		AccessExpiresAt:  access.AccessExpiresAt,
		RefreshExpiresAt: refresh.RefreshExpiresAt,
	}, nil
}

func (s *authService) ParseToken(tokenString, expectedType string) (*TokenClaims, error) {
	if expectedType != TokenTypeAccess && expectedType != TokenTypeRefresh {
		return nil, ErrUnexpectedTokenType
	}

	claims := &TokenClaims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return s.jwtSecret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.IssuedAt == nil || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, ErrInvalidToken
	}
	if claims.TokenType != expectedType {
		return nil, ErrUnexpectedTokenType
	}
	if expectedType == TokenTypeAccess && claims.Username == "" {
		return nil, ErrInvalidToken
	}
	if expectedType == TokenTypeRefresh && claims.ID == "" {
		return nil, ErrInvalidToken
	}
	if claims.SessionVersion < 0 {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func (s *authService) signToken(claims TokenClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

func (s *authService) generateAccessToken(
	user *User,
	now time.Time,
) (*AccessTokenResult, error) {
	if user == nil || user.ID == uuid.Nil || user.Username == "" || user.SessionVersion < 0 {
		return nil, ErrInvalidTokenUser
	}

	accessExpiresAt := now.Add(s.accessExpiry)

	accessToken, err := s.signToken(TokenClaims{
		Username:  user.Username,
		TokenType: TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(accessExpiresAt),
		},
	})
	if err != nil {
		return nil, err
	}

	return &AccessTokenResult{
		AccessToken:     accessToken,
		AccessExpiresAt: accessExpiresAt,
	}, nil
}

func (s *authService) generateRefreshToken(
	user *User,
	now time.Time,
) (*RefreshTokenResult, error) {
	if user == nil || user.ID == uuid.Nil || user.Username == "" {
		return nil, ErrInvalidTokenUser
	}

	refreshExpiresAt := now.Add(s.refreshExpiry)

	refreshToken, err := s.signToken(TokenClaims{
		TokenType:      TokenTypeRefresh,
		SessionVersion: user.SessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(refreshExpiresAt),
		},
	})
	if err != nil {
		return nil, err
	}

	return &RefreshTokenResult{
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil

}
