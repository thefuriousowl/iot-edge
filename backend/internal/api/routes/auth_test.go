package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thefuriousowl/iot-edge/internal/api/handlers"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/service"
)

type routeAuthService struct {
	service.AuthService
	userID                uuid.UUID
	parseErr              error
	receivedToken         string
	receivedExpectedType  string
	receivedLogoutUserID  uuid.UUID
	receivedRefreshToken  string
	receivedCurrentUserID uuid.UUID
	logoutCalls           int
	currentUserCalls      int
}

func (r *routeAuthService) IsSetupRequired(context.Context) (bool, error) {
	return true, nil
}

func (r *routeAuthService) Setup(context.Context, string, string) (*domain.User, error) {
	return &domain.User{Username: "admin"}, nil
}

func (r *routeAuthService) Login(context.Context, string, string) (*service.TokenPair, error) {
	return &service.TokenPair{
		AccessToken:      "test-access-token",
		RefreshToken:     "test-refresh-token",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}, nil
}

func (r *routeAuthService) Refresh(context.Context, string) (*service.AccessTokenResult, error) {
	return &service.AccessTokenResult{
		AccessToken:     "new-test-access-token",
		AccessExpiresAt: time.Now().Add(15 * time.Minute),
	}, nil
}

func (r *routeAuthService) ParseToken(
	tokenString string,
	expectedType string,
) (*service.TokenClaims, error) {
	r.receivedToken = tokenString
	r.receivedExpectedType = expectedType
	if r.parseErr != nil {
		return nil, r.parseErr
	}
	return &service.TokenClaims{
		Username:  "admin",
		TokenType: service.TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: r.userID.String(),
		},
	}, nil
}

func (r *routeAuthService) Logout(
	_ context.Context,
	userID uuid.UUID,
	refreshToken string,
) error {
	r.logoutCalls++
	r.receivedLogoutUserID = userID
	r.receivedRefreshToken = refreshToken
	return nil
}

func (r *routeAuthService) CurrentUser(
	_ context.Context,
	userID uuid.UUID,
) (*domain.User, error) {
	r.currentUserCalls++
	r.receivedCurrentUserID = userID
	return &domain.User{
		ID:       userID,
		Username: "admin",
	}, nil
}

func TestRegisterAuthRoutes_RegistersSetupStatusGETRoute(t *testing.T) {
	app := fiber.New()
	auth := &routeAuthService{}
	handler := handlers.NewAuthHandler(auth)

	RegisterAuthRoutes(
		app.Group("/api"),
		handler,
		auth,
	)

	request := httptest.NewRequest(http.MethodGet, "/api/auth/setup/status", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("GET setup status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
}

func TestRegisterAuthRoutes_DoesNotRegisterSetupStatusPOSTRoute(t *testing.T) {
	app := fiber.New()
	auth := &routeAuthService{}
	handler := handlers.NewAuthHandler(auth)

	RegisterAuthRoutes(
		app.Group("/api"),
		handler,
		auth,
	)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup/status", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusMethodNotAllowed {
		t.Errorf("POST setup status = %d, want %d", response.StatusCode, fiber.StatusMethodNotAllowed)
	}
}

func TestRegisterAuthRoutes_RegistersSetupPOSTRoute(t *testing.T) {
	app := fiber.New()
	auth := &routeAuthService{}
	handler := handlers.NewAuthHandler(auth)

	RegisterAuthRoutes(
		app.Group("/api"),
		handler,
		auth,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{
			"username":"admin",
			"password":"SecureP@ss123",
			"confirm_password":"SecureP@ss123"
		}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusCreated {
		t.Errorf("POST setup = %d, want %d", response.StatusCode, fiber.StatusCreated)
	}
}

func TestRegisterAuthRoutes_RegistersLoginPOSTRoute(t *testing.T) {
	app := fiber.New()
	auth := &routeAuthService{}
	handler := handlers.NewAuthHandler(auth)

	RegisterAuthRoutes(
		app.Group("/api"),
		handler,
		auth,
	)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"SecureP@ss123"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("POST login = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
}

func TestRegisterAuthRoutes_RegistersRefreshPOSTRoute(t *testing.T) {
	app := fiber.New()
	auth := &routeAuthService{}
	handler := handlers.NewAuthHandler(auth)

	RegisterAuthRoutes(
		app.Group("/api"),
		handler,
		auth,
	)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	request.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: "test-refresh-token",
	})
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("POST refresh = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
}

func TestRegisterAuthRoutes_ProtectsMeRoute(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		parseErr      error
	}{
		{name: "missing authorization"},
		{
			name:          "invalid access token",
			authorization: "Bearer invalid-access-token",
			parseErr:      service.ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &routeAuthService{userID: uuid.New(), parseErr: tt.parseErr}
			handler := handlers.NewAuthHandler(auth)
			app := fiber.New()
			RegisterAuthRoutes(app.Group("/api"), handler, auth)

			request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			if tt.authorization != "" {
				request.Header.Set(fiber.HeaderAuthorization, tt.authorization)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("GET me = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
			}
			if auth.currentUserCalls != 0 {
				t.Errorf("CurrentUser() calls = %d, want 0", auth.currentUserCalls)
			}
		})
	}
}

func TestRegisterAuthRoutes_AuthenticatedMeReachesHandler(t *testing.T) {
	userID := uuid.New()
	auth := &routeAuthService{userID: userID}
	handler := handlers.NewAuthHandler(auth)
	app := fiber.New()
	RegisterAuthRoutes(app.Group("/api"), handler, auth)

	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set(fiber.HeaderAuthorization, "Bearer valid-access-token")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("GET me = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		ID       uuid.UUID `json:"id"`
		Username string    `json:"username"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.ID != userID || body.Username != "admin" {
		t.Errorf("GET me user = %#v, want %s/admin", body, userID)
	}
	if auth.receivedToken != "valid-access-token" || auth.receivedExpectedType != service.TokenTypeAccess {
		t.Errorf(
			"ParseToken() received token/type = %q/%q, want valid-access-token/access",
			auth.receivedToken,
			auth.receivedExpectedType,
		)
	}
	if auth.currentUserCalls != 1 || auth.receivedCurrentUserID != userID {
		t.Errorf(
			"CurrentUser() calls/user = %d/%s, want 1/%s",
			auth.currentUserCalls,
			auth.receivedCurrentUserID,
			userID,
		)
	}
}

func TestRegisterAuthRoutes_ProtectsLogoutRoute(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		parseErr      error
	}{
		{name: "missing authorization"},
		{
			name:          "invalid access token",
			authorization: "Bearer invalid-access-token",
			parseErr:      service.ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &routeAuthService{userID: uuid.New(), parseErr: tt.parseErr}
			handler := handlers.NewAuthHandler(auth)
			app := fiber.New()
			RegisterAuthRoutes(app.Group("/api"), handler, auth)

			request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			if tt.authorization != "" {
				request.Header.Set(fiber.HeaderAuthorization, tt.authorization)
			}
			request.AddCookie(&http.Cookie{
				Name:  "refresh_token",
				Value: "test-refresh-token",
			})
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("POST logout = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
			}
			if auth.logoutCalls != 0 {
				t.Errorf("Logout() calls = %d, want 0", auth.logoutCalls)
			}
		})
	}
}

func TestRegisterAuthRoutes_AuthenticatedLogoutReachesHandler(t *testing.T) {
	userID := uuid.New()
	auth := &routeAuthService{userID: userID}
	handler := handlers.NewAuthHandler(auth)
	app := fiber.New()
	RegisterAuthRoutes(app.Group("/api"), handler, auth)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.Header.Set(fiber.HeaderAuthorization, "Bearer valid-access-token")
	request.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: "test-refresh-token",
	})
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("POST logout = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	if auth.receivedToken != "valid-access-token" || auth.receivedExpectedType != service.TokenTypeAccess {
		t.Errorf(
			"ParseToken() received token/type = %q/%q, want valid-access-token/access",
			auth.receivedToken,
			auth.receivedExpectedType,
		)
	}
	if auth.logoutCalls != 1 || auth.receivedLogoutUserID != userID {
		t.Errorf("Logout() calls/user = %d/%s, want 1/%s", auth.logoutCalls, auth.receivedLogoutUserID, userID)
	}
	if auth.receivedRefreshToken != "test-refresh-token" {
		t.Errorf("Logout() refresh token = %q, want test-refresh-token", auth.receivedRefreshToken)
	}
}
