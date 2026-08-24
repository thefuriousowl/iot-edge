package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestGenerateAndParseTokenPair(t *testing.T) {
	service := newTestAuthService(t)
	now := time.Date(2026, time.August, 20, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user := &User{ID: uuid.New(), Username: "admin", SessionVersion: 7}

	pair, err := service.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("GenerateTokenPair() returned an empty token")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access and refresh tokens are identical")
	}
	if !pair.AccessExpiresAt.Equal(now.Add(service.accessExpiry)) {
		t.Errorf("access expiry = %v, want %v", pair.AccessExpiresAt, now.Add(service.accessExpiry))
	}
	if !pair.RefreshExpiresAt.Equal(now.Add(service.refreshExpiry)) {
		t.Errorf("refresh expiry = %v, want %v", pair.RefreshExpiresAt, now.Add(service.refreshExpiry))
	}

	accessClaims, err := service.ParseToken(pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseToken(access) error: %v", err)
	}
	if accessClaims.Subject != user.ID.String() || accessClaims.Username != user.Username {
		t.Errorf("access claims = %#v, want user ID and username", accessClaims)
	}
	if accessClaims.TokenType != TokenTypeAccess || accessClaims.ID != "" {
		t.Errorf("access token type/JTI = %q/%q, want access with no JTI", accessClaims.TokenType, accessClaims.ID)
	}
	if accessClaims.SessionVersion != 0 {
		t.Errorf("access session version = %d, want omitted zero", accessClaims.SessionVersion)
	}

	refreshClaims, err := service.ParseToken(pair.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("ParseToken(refresh) error: %v", err)
	}
	if refreshClaims.Subject != user.ID.String() || refreshClaims.Username != "" {
		t.Errorf("refresh claims = %#v, want user ID without username", refreshClaims)
	}
	if refreshClaims.TokenType != TokenTypeRefresh {
		t.Errorf("refresh token type = %q, want %q", refreshClaims.TokenType, TokenTypeRefresh)
	}
	if refreshClaims.SessionVersion != user.SessionVersion {
		t.Errorf("refresh session version = %d, want %d", refreshClaims.SessionVersion, user.SessionVersion)
	}
	if _, err := uuid.Parse(refreshClaims.ID); err != nil {
		t.Errorf("refresh token JTI = %q, want UUID: %v", refreshClaims.ID, err)
	}
}

func TestGenerateAccessToken(t *testing.T) {
	service := newTestAuthService(t)
	now := time.Date(2026, time.August, 21, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user := &User{ID: uuid.New(), Username: "admin"}

	result, err := service.generateAccessToken(user, now)
	if err != nil {
		t.Fatalf("generateAccessToken() error: %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("generateAccessToken() returned an empty token")
	}
	wantExpiry := now.Add(service.accessExpiry)
	if !result.AccessExpiresAt.Equal(wantExpiry) {
		t.Errorf("access expiry = %s, want %s", result.AccessExpiresAt, wantExpiry)
	}

	claims, err := service.ParseToken(result.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseToken(access) error: %v", err)
	}
	if claims.Subject != user.ID.String() || claims.Username != user.Username {
		t.Errorf("access claims = %#v, want user ID and username", claims)
	}
	if claims.TokenType != TokenTypeAccess || claims.ID != "" {
		t.Errorf("access token type/JTI = %q/%q, want access with no JTI", claims.TokenType, claims.ID)
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	service := newTestAuthService(t)
	now := time.Date(2026, time.August, 21, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user := &User{ID: uuid.New(), Username: "admin"}

	result, err := service.generateRefreshToken(user, now)
	if err != nil {
		t.Fatalf("generateRefreshToken() error: %v", err)
	}
	if result.RefreshToken == "" {
		t.Fatal("generateRefreshToken() returned an empty token")
	}
	wantExpiry := now.Add(service.refreshExpiry)
	if !result.RefreshExpiresAt.Equal(wantExpiry) {
		t.Errorf("refresh expiry = %s, want %s", result.RefreshExpiresAt, wantExpiry)
	}

	claims, err := service.ParseToken(result.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("ParseToken(refresh) error: %v", err)
	}
	if claims.Subject != user.ID.String() || claims.Username != "" {
		t.Errorf("refresh claims = %#v, want user ID without username", claims)
	}
	if claims.TokenType != TokenTypeRefresh {
		t.Errorf("refresh token type = %q, want %q", claims.TokenType, TokenTypeRefresh)
	}
	if _, err := uuid.Parse(claims.ID); err != nil {
		t.Errorf("refresh token JTI = %q, want UUID: %v", claims.ID, err)
	}
}

func TestParseToken_RejectsInvalidTokens(t *testing.T) {
	service := newTestAuthService(t)
	now := time.Date(2026, time.August, 20, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user := &User{ID: uuid.New(), Username: "admin"}
	pair, err := service.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error: %v", err)
	}

	t.Run("wrong token type", func(t *testing.T) {
		_, err := service.ParseToken(pair.AccessToken, TokenTypeRefresh)
		if !errors.Is(err, ErrUnexpectedTokenType) {
			t.Fatalf("ParseToken() error = %v, want ErrUnexpectedTokenType", err)
		}
	})

	t.Run("unsupported expected type", func(t *testing.T) {
		_, err := service.ParseToken(pair.AccessToken, "other")
		if !errors.Is(err, ErrUnexpectedTokenType) {
			t.Fatalf("ParseToken() error = %v, want ErrUnexpectedTokenType", err)
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		_, err := service.ParseToken(pair.AccessToken+"tampered", TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		other := newTestAuthService(t)
		other.jwtSecret = []byte(strings.Repeat("x", 32))
		other.now = service.now
		_, err := other.ParseToken(pair.AccessToken, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		service.now = func() time.Time { return now.Add(service.accessExpiry + time.Second) }
		t.Cleanup(func() { service.now = func() time.Time { return now } })
		_, err := service.ParseToken(pair.AccessToken, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("wrong signing algorithm", func(t *testing.T) {
		claims := TokenClaims{
			Username:  user.Username,
			TokenType: TokenTypeAccess,
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   user.ID.String(),
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
		}
		rawToken, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).SignedString(service.jwtSecret)
		if err != nil {
			t.Fatalf("signing HS384 token: %v", err)
		}
		_, err = service.ParseToken(rawToken, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("unsigned none algorithm", func(t *testing.T) {
		claims := TokenClaims{
			Username:  user.Username,
			TokenType: TokenTypeAccess,
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   user.ID.String(),
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			},
		}
		rawToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("signing unsigned token: %v", err)
		}
		_, err = service.ParseToken(rawToken, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
		}
	})

	for _, test := range []struct {
		name     string
		expected string
		claims   TokenClaims
	}{
		{
			name: "missing expiration",
			claims: TokenClaims{Username: user.Username, TokenType: TokenTypeAccess, RegisteredClaims: jwt.RegisteredClaims{
				Subject: user.ID.String(), IssuedAt: jwt.NewNumericDate(now),
			}},
		},
		{
			name: "missing issued at",
			claims: TokenClaims{Username: user.Username, TokenType: TokenTypeAccess, RegisteredClaims: jwt.RegisteredClaims{
				Subject: user.ID.String(), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}},
		},
		{
			name: "invalid subject",
			claims: TokenClaims{Username: user.Username, TokenType: TokenTypeAccess, RegisteredClaims: jwt.RegisteredClaims{
				Subject: "not-a-uuid", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}},
		},
		{
			name: "access missing username",
			claims: TokenClaims{TokenType: TokenTypeAccess, RegisteredClaims: jwt.RegisteredClaims{
				Subject: user.ID.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}},
		},
		{
			name:     "refresh missing jti",
			expected: TokenTypeRefresh,
			claims: TokenClaims{TokenType: TokenTypeRefresh, RegisteredClaims: jwt.RegisteredClaims{
				Subject: user.ID.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}},
		},
		{
			name: "negative session version",
			claims: TokenClaims{Username: user.Username, TokenType: TokenTypeAccess, SessionVersion: -1, RegisteredClaims: jwt.RegisteredClaims{
				Subject: user.ID.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawToken, err := service.signToken(test.claims)
			if err != nil {
				t.Fatalf("signToken() error: %v", err)
			}
			expected := test.expected
			if expected == "" {
				expected = TokenTypeAccess
			}
			_, err = service.ParseToken(rawToken, expected)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestGenerateTokenPair_RejectsInvalidUser(t *testing.T) {
	service := newTestAuthService(t)
	tests := []*User{
		nil,
		{Username: "admin"},
		{ID: uuid.New()},
		{ID: uuid.New(), Username: "admin", SessionVersion: -1},
	}
	for _, user := range tests {
		if _, err := service.GenerateTokenPair(user); !errors.Is(err, ErrInvalidTokenUser) {
			t.Errorf("GenerateTokenPair(%#v) error = %v, want ErrInvalidTokenUser", user, err)
		}
	}
}
