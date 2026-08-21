package authhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/auth"
)

type fakeSetupStatusService struct {
	auth.AuthService
	required    bool
	err         error
	receivedCtx context.Context
}

func (f *fakeSetupStatusService) IsSetupRequired(ctx context.Context) (bool, error) {
	f.receivedCtx = ctx
	return f.required, f.err
}

type fakeSetupService struct {
	auth.AuthService
	user             *auth.User
	err              error
	receivedCtx      context.Context
	receivedUsername string
	receivedPassword string
	calls            int
}

type fakeLoginService struct {
	auth.AuthService
	tokens           *auth.TokenPair
	err              error
	receivedCtx      context.Context
	receivedUsername string
	receivedPassword string
	calls            int
}

type fakeRefreshService struct {
	auth.AuthService
	result               *auth.AccessTokenResult
	err                  error
	receivedCtx          context.Context
	receivedRefreshToken string
	calls                int
}

type fakeLogoutService struct {
	auth.AuthService
	err                  error
	receivedCtx          context.Context
	receivedUserID       uuid.UUID
	receivedRefreshToken string
	calls                int
}

type fakeCurrentUserService struct {
	auth.AuthService
	user           *auth.User
	err            error
	receivedCtx    context.Context
	receivedUserID uuid.UUID
	calls          int
}

func (f *fakeCurrentUserService) CurrentUser(
	ctx context.Context,
	userID uuid.UUID,
) (*auth.User, error) {
	f.calls++
	f.receivedCtx = ctx
	f.receivedUserID = userID
	return f.user, f.err
}

func (f *fakeLogoutService) Logout(
	ctx context.Context,
	userID uuid.UUID,
	refreshToken string,
) error {
	f.calls++
	f.receivedCtx = ctx
	f.receivedUserID = userID
	f.receivedRefreshToken = refreshToken
	return f.err
}

func (f *fakeRefreshService) Refresh(
	ctx context.Context,
	refreshToken string,
) (*auth.AccessTokenResult, error) {
	f.calls++
	f.receivedCtx = ctx
	f.receivedRefreshToken = refreshToken
	return f.result, f.err
}

func (f *fakeLoginService) Login(
	ctx context.Context,
	username string,
	password string,
) (*auth.TokenPair, error) {
	f.calls++
	f.receivedCtx = ctx
	f.receivedUsername = username
	f.receivedPassword = password
	return f.tokens, f.err
}

func (f *fakeSetupService) Setup(
	ctx context.Context,
	username string,
	password string,
) (*auth.User, error) {
	f.calls++
	f.receivedCtx = ctx
	f.receivedUsername = username
	f.receivedPassword = password
	return f.user, f.err
}

func TestAuthHandlerSetupStatus_ReturnsSetupState(t *testing.T) {
	tests := []struct {
		name     string
		required bool
	}{
		{name: "setup required", required: true},
		{name: "setup already completed", required: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeSetupStatusService{required: tt.required}
			handler := NewAuthHandler(auth)
			expectedCtx := context.WithValue(context.Background(), testContextKey{}, tt.name)
			app := fiber.New()
			app.Use(func(c *fiber.Ctx) error {
				c.SetUserContext(expectedCtx)
				return c.Next()
			})
			app.Get("/api/auth/setup/status", handler.SetupStatus)

			request := httptest.NewRequest(http.MethodGet, "/api/auth/setup/status", nil)
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusOK {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
			}
			var body map[string]bool
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decoding response: %v", err)
			}
			got, exists := body["setup_required"]
			if !exists {
				t.Fatal("response does not contain setup_required")
			}
			if got != tt.required {
				t.Errorf("setup_required = %t, want %t", got, tt.required)
			}
			if auth.receivedCtx != expectedCtx {
				t.Error("SetupStatus() did not pass the request context to AuthService")
			}
		})
	}
}

func TestAuthHandlerSetupStatus_ServiceErrorReturnsInternalServerError(t *testing.T) {
	auth := &fakeSetupStatusService{err: errors.New("test service error")}
	handler := NewAuthHandler(auth)
	app := fiber.New()
	app.Get("/api/auth/setup/status", handler.SetupStatus)

	request := httptest.NewRequest(http.MethodGet, "/api/auth/setup/status", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusInternalServerError)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Error.Code != "INTERNAL_ERROR" || body.Error.Message != "Internal server error" {
		t.Errorf("error response = %#v, want INTERNAL_ERROR", body.Error)
	}
}

func TestAuthHandlerSetup_CreatesInitialUser(t *testing.T) {
	createdAt := time.Date(2026, time.August, 20, 10, 30, 0, 0, time.UTC)
	createdUser := &auth.User{
		ID:           uuid.New(),
		Username:     "admin",
		PasswordHash: "must-not-be-returned",
		CreatedAt:    createdAt,
	}
	auth := &fakeSetupService{user: createdUser}
	handler := NewAuthHandler(auth)
	expectedCtx := context.WithValue(context.Background(), testContextKey{}, "setup")
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(expectedCtx)
		return c.Next()
	})
	app.Post("/api/auth/setup", handler.Setup)

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
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusCreated)
	}
	var body struct {
		Message string         `json:"message"`
		User    map[string]any `json:"user"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Message != "Setup completed successfully" {
		t.Errorf("message = %q, want setup success message", body.Message)
	}
	if body.User["id"] != createdUser.ID.String() {
		t.Errorf("user.id = %v, want %s", body.User["id"], createdUser.ID)
	}
	if body.User["username"] != createdUser.Username {
		t.Errorf("user.username = %v, want %q", body.User["username"], createdUser.Username)
	}
	if body.User["created_at"] != createdAt.Format(time.RFC3339) {
		t.Errorf("user.created_at = %v, want %s", body.User["created_at"], createdAt.Format(time.RFC3339))
	}
	if _, exists := body.User["password_hash"]; exists {
		t.Error("response exposes password_hash")
	}
	if auth.calls != 1 || auth.receivedUsername != "admin" || auth.receivedPassword != "SecureP@ss123" {
		t.Errorf(
			"Setup() call = (%q, %q), calls %d; want request credentials and one call",
			auth.receivedUsername,
			auth.receivedPassword,
			auth.calls,
		)
	}
	if auth.receivedCtx != expectedCtx {
		t.Error("Setup() did not pass the request context to AuthService")
	}
}

func TestAuthHandlerSetup_RejectsMalformedAndInvalidRequests(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
		wantMessage string
	}{
		{
			name:        "malformed JSON",
			requestBody: `{"username":`,
			wantMessage: "Invalid request body",
		},
		{
			name: "password confirmation mismatch",
			requestBody: `{
				"username":"admin",
				"password":"SecureP@ss123",
				"confirm_password":"DifferentP@ss123"
			}`,
			wantMessage: "Invalid request data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeSetupService{}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Post("/api/auth/setup", handler.Setup)

			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/setup",
				strings.NewReader(tt.requestBody),
			)
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusBadRequest {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusBadRequest)
			}
			assertErrorResponse(t, response, "VALIDATION_ERROR", tt.wantMessage)
			if auth.calls != 0 {
				t.Errorf("Setup() calls = %d, want 0", auth.calls)
			}
		})
	}
}

func TestAuthHandlerSetup_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "password requirements",
			serviceErr: auth.ErrPasswordRequirements,
			wantStatus: fiber.StatusBadRequest,
			wantCode:   "AUTH006",
			wantMsg:    "Password requirements not met",
		},
		{
			name:       "setup already completed",
			serviceErr: auth.ErrSetupAlreadyCompleted,
			wantStatus: fiber.StatusBadRequest,
			wantCode:   "AUTH008",
			wantMsg:    "Setup already completed",
		},
		{
			name:       "unexpected service error",
			serviceErr: errors.New("test service error"),
			wantStatus: fiber.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeSetupService{err: tt.serviceErr}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Post("/api/auth/setup", handler.Setup)

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

			if response.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMsg)
		})
	}
}

func TestAuthHandlerLogin_ReturnsAccessTokenAndRefreshCookie(t *testing.T) {
	now := time.Now().UTC()
	tokens := &auth.TokenPair{
		AccessToken:      "test-access-token",
		RefreshToken:     "test-refresh-token",
		AccessExpiresAt:  now.Add(15 * time.Minute),
		RefreshExpiresAt: now.Add(7 * 24 * time.Hour),
	}
	auth := &fakeLoginService{tokens: tokens}
	handler := NewAuthHandler(auth)
	expectedCtx := context.WithValue(context.Background(), testContextKey{}, "login")
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(expectedCtx)
		return c.Next()
	})
	app.Post("/api/auth/login", handler.Login)

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
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.AccessToken != tokens.AccessToken || body.TokenType != "Bearer" {
		t.Errorf("login response = %#v, want access token and Bearer type", body)
	}
	if body.ExpiresIn < 899 || body.ExpiresIn > 900 {
		t.Errorf("expires_in = %d, want approximately 900", body.ExpiresIn)
	}
	if auth.calls != 1 || auth.receivedUsername != "admin" || auth.receivedPassword != "SecureP@ss123" {
		t.Errorf(
			"Login() call = (%q, %q), calls %d; want request credentials and one call",
			auth.receivedUsername,
			auth.receivedPassword,
			auth.calls,
		)
	}
	if auth.receivedCtx != expectedCtx {
		t.Error("Login() did not receive request context")
	}

	var refreshCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == refreshTokenCookieName {
			refreshCookie = cookie
			break
		}
	}
	if refreshCookie == nil {
		t.Fatal("response does not contain refresh_token cookie")
	}
	if refreshCookie.Value != tokens.RefreshToken {
		t.Errorf("refresh cookie value = %q, want refresh token", refreshCookie.Value)
	}
	if refreshCookie.Path != "/api/auth" || !refreshCookie.HttpOnly || !refreshCookie.Secure {
		t.Errorf("refresh cookie security attributes = %#v", refreshCookie)
	}
	if refreshCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("refresh cookie SameSite = %v, want Strict", refreshCookie.SameSite)
	}
	if difference := refreshCookie.Expires.Sub(tokens.RefreshExpiresAt); difference < -time.Second || difference > time.Second {
		t.Errorf("refresh cookie expiry = %s, want %s", refreshCookie.Expires, tokens.RefreshExpiresAt)
	}
}

func TestAuthHandlerLogin_AllowsHTTPRefreshCookieWhenConfigured(t *testing.T) {
	// Arrange
	tokens := &auth.TokenPair{
		AccessToken:      "test-access-token",
		RefreshToken:     "test-refresh-token",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}
	handler := NewAuthHandler(
		&fakeLoginService{tokens: tokens},
		WithSecureCookies(false),
	)
	app := fiber.New()
	app.Post("/api/auth/login", handler.Login)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"SecureP@ss123"}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	// Act
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	// Assert
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == refreshTokenCookieName {
			if cookie.Secure {
				t.Error("refresh cookie is Secure when HTTP development mode is configured")
			}
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("refresh cookie security attributes = %#v", cookie)
			}
			return
		}
	}

	t.Fatal("response does not contain refresh_token cookie")
}

func TestAuthHandlerLogin_RejectsMalformedAndInvalidRequests(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
		wantMessage string
	}{
		{
			name:        "malformed JSON",
			requestBody: `{"username":`,
			wantMessage: "Invalid request body",
		},
		{
			name:        "missing password",
			requestBody: `{"username":"admin"}`,
			wantMessage: "Invalid request data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeLoginService{}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Post("/api/auth/login", handler.Login)

			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/login",
				strings.NewReader(tt.requestBody),
			)
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusBadRequest {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusBadRequest)
			}
			assertErrorResponse(t, response, "VALIDATION_ERROR", tt.wantMessage)
			if auth.calls != 0 {
				t.Errorf("Login() calls = %d, want 0", auth.calls)
			}
		})
	}
}

func TestAuthHandlerLogin_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "invalid credentials",
			serviceErr: auth.ErrInvalidCredentials,
			wantStatus: fiber.StatusUnauthorized,
			wantCode:   "AUTH001",
			wantMsg:    "Invalid credentials",
		},
		{
			name:       "account locked",
			serviceErr: auth.ErrAccountLocked,
			wantStatus: fiber.StatusUnauthorized,
			wantCode:   "AUTH002",
			wantMsg:    "Account locked",
		},
		{
			name:       "unexpected error",
			serviceErr: errors.New("test login error"),
			wantStatus: fiber.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeLoginService{err: tt.serviceErr}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Post("/api/auth/login", handler.Login)

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

			if response.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMsg)
			if len(response.Cookies()) != 0 {
				t.Error("error response unexpectedly sets a cookie")
			}
		})
	}
}

func TestAuthHandlerRefresh_ReturnsNewAccessToken(t *testing.T) {
	now := time.Now().UTC()
	auth := &fakeRefreshService{result: &auth.AccessTokenResult{
		AccessToken:     "new-access-token",
		AccessExpiresAt: now.Add(15 * time.Minute),
	}}
	handler := NewAuthHandler(auth)
	expectedCtx := context.WithValue(context.Background(), testContextKey{}, "refresh")
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(expectedCtx)
		return c.Next()
	})
	app.Post("/api/auth/refresh", handler.Refresh)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: "current-refresh-token",
	})
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.AccessToken != auth.result.AccessToken {
		t.Errorf("access_token = %q, want %q", body.AccessToken, auth.result.AccessToken)
	}
	if body.ExpiresIn < 899 || body.ExpiresIn > 900 {
		t.Errorf("expires_in = %d, want approximately 900", body.ExpiresIn)
	}
	if auth.calls != 1 || auth.receivedRefreshToken != "current-refresh-token" {
		t.Errorf("Refresh() calls/token = %d/%q, want 1/current-refresh-token", auth.calls, auth.receivedRefreshToken)
	}
	if auth.receivedCtx != expectedCtx {
		t.Error("Refresh() did not receive request context")
	}
	if len(response.Cookies()) != 0 {
		t.Error("refresh response unexpectedly rotates refresh cookie")
	}
}

func TestAuthHandlerRefresh_MissingCookieReturnsSessionExpired(t *testing.T) {
	auth := &fakeRefreshService{}
	handler := NewAuthHandler(auth)
	app := fiber.New()
	app.Post("/api/auth/refresh", handler.Refresh)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
	}
	assertErrorResponse(t, response, "AUTH005", "Session expired")
	if auth.calls != 0 {
		t.Errorf("Refresh() calls = %d, want 0", auth.calls)
	}
}

func TestAuthHandlerRefresh_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		result     *auth.AccessTokenResult
		serviceErr error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "session expired",
			serviceErr: auth.ErrSessionExpired,
			wantStatus: fiber.StatusUnauthorized,
			wantCode:   "AUTH005",
			wantMsg:    "Session expired",
		},
		{
			name:       "account locked",
			serviceErr: auth.ErrAccountLocked,
			wantStatus: fiber.StatusUnauthorized,
			wantCode:   "AUTH002",
			wantMsg:    "Account locked",
		},
		{
			name:       "unexpected error",
			serviceErr: errors.New("test refresh error"),
			wantStatus: fiber.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error",
		},
		{
			name:       "nil result",
			wantStatus: fiber.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeRefreshService{result: tt.result, err: tt.serviceErr}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Post("/api/auth/refresh", handler.Refresh)

			request := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
			request.AddCookie(&http.Cookie{
				Name:  refreshTokenCookieName,
				Value: "current-refresh-token",
			})
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMsg)
			if len(response.Cookies()) != 0 {
				t.Error("error response unexpectedly sets a cookie")
			}
		})
	}
}

func TestAuthHandlerLogout_RevokesSessionAndClearsCookie(t *testing.T) {
	userID := uuid.New()
	auth := &fakeLogoutService{}
	handler := NewAuthHandler(auth)
	expectedCtx := context.WithValue(context.Background(), testContextKey{}, "logout")
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(expectedCtx)
		c.Locals(LocalUserID, userID)
		return c.Next()
	})
	app.Post("/api/auth/logout", handler.Logout)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: "current-refresh-token",
	})
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Message != "Logged out successfully" {
		t.Errorf("message = %q, want logout success message", body.Message)
	}
	if auth.calls != 1 || auth.receivedUserID != userID || auth.receivedRefreshToken != "current-refresh-token" {
		t.Errorf(
			"Logout() call = user %s/token %q/calls %d, want request session once",
			auth.receivedUserID,
			auth.receivedRefreshToken,
			auth.calls,
		)
	}
	if auth.receivedCtx != expectedCtx {
		t.Error("Logout() did not receive request context")
	}
	assertRefreshCookieCleared(t, response)
}

func TestAuthHandlerLogout_RejectsMissingAuthenticationAndSession(t *testing.T) {
	tests := []struct {
		name        string
		setUserID   bool
		setCookie   bool
		wantCode    string
		wantMessage string
	}{
		{
			name:        "missing authenticated user",
			setCookie:   true,
			wantCode:    "AUTH004",
			wantMessage: "Invalid token",
		},
		{
			name:        "missing refresh cookie",
			setUserID:   true,
			wantCode:    "AUTH005",
			wantMessage: "Session expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeLogoutService{}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			if tt.setUserID {
				app.Use(func(c *fiber.Ctx) error {
					c.Locals(LocalUserID, uuid.New())
					return c.Next()
				})
			}
			app.Post("/api/auth/logout", handler.Logout)

			request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			if tt.setCookie {
				request.AddCookie(&http.Cookie{
					Name:  refreshTokenCookieName,
					Value: "current-refresh-token",
				})
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMessage)
			if auth.calls != 0 {
				t.Errorf("Logout() calls = %d, want 0", auth.calls)
			}
			assertRefreshCookieCleared(t, response)
		})
	}
}

func TestAuthHandlerLogout_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantCleared bool
	}{
		{
			name:        "session expired",
			serviceErr:  auth.ErrSessionExpired,
			wantStatus:  fiber.StatusUnauthorized,
			wantCode:    "AUTH005",
			wantMessage: "Session expired",
			wantCleared: true,
		},
		{
			name:        "repository error",
			serviceErr:  errors.New("test logout error"),
			wantStatus:  fiber.StatusInternalServerError,
			wantCode:    "INTERNAL_ERROR",
			wantMessage: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeLogoutService{err: tt.serviceErr}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Use(func(c *fiber.Ctx) error {
				c.Locals(LocalUserID, uuid.New())
				return c.Next()
			})
			app.Post("/api/auth/logout", handler.Logout)

			request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			request.AddCookie(&http.Cookie{
				Name:  refreshTokenCookieName,
				Value: "current-refresh-token",
			})
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMessage)
			if tt.wantCleared {
				assertRefreshCookieCleared(t, response)
			} else if len(response.Cookies()) != 0 {
				t.Error("internal error response unexpectedly clears refresh cookie")
			}
		})
	}
}

func TestAuthHandlerMe_ReturnsSafeCurrentUser(t *testing.T) {
	userID := uuid.New()
	createdAt := time.Date(2026, time.August, 20, 10, 30, 0, 0, time.UTC)
	lastLogin := time.Date(2026, time.August, 21, 8, 15, 0, 0, time.UTC)
	auth := &fakeCurrentUserService{user: &auth.User{
		ID:             userID,
		Username:       "admin",
		PasswordHash:   "must-not-be-returned",
		IsLocked:       true,
		FailedAttempts: 10,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt.Add(time.Hour),
		LastLogin:      &lastLogin,
	}}
	handler := NewAuthHandler(auth)
	expectedCtx := context.WithValue(context.Background(), testContextKey{}, "me")
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(expectedCtx)
		c.Locals(LocalUserID, userID)
		return c.Next()
	})
	app.Get("/api/auth/me", handler.Me)

	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body["id"] != userID.String() {
		t.Errorf("id = %v, want %s", body["id"], userID)
	}
	if body["username"] != "admin" {
		t.Errorf("username = %v, want admin", body["username"])
	}
	if body["created_at"] != createdAt.Format(time.RFC3339) {
		t.Errorf("created_at = %v, want %s", body["created_at"], createdAt.Format(time.RFC3339))
	}
	if body["last_login"] != lastLogin.Format(time.RFC3339) {
		t.Errorf("last_login = %v, want %s", body["last_login"], lastLogin.Format(time.RFC3339))
	}
	if len(body) != 4 {
		t.Errorf("response fields = %v, want only id, username, created_at, and last_login", body)
	}
	for _, field := range []string{
		"password_hash",
		"is_locked",
		"failed_attempts",
		"locked_until",
		"updated_at",
	} {
		if _, exists := body[field]; exists {
			t.Errorf("response exposes %s", field)
		}
	}
	if auth.calls != 1 || auth.receivedUserID != userID {
		t.Errorf("CurrentUser() user/calls = %s/%d, want %s/1", auth.receivedUserID, auth.calls, userID)
	}
	if auth.receivedCtx != expectedCtx {
		t.Error("CurrentUser() did not receive request context")
	}
}

func TestAuthHandlerMe_MissingUserLocalReturnsInvalidToken(t *testing.T) {
	auth := &fakeCurrentUserService{}
	handler := NewAuthHandler(auth)
	app := fiber.New()
	app.Get("/api/auth/me", handler.Me)

	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
	}
	assertErrorResponse(t, response, "AUTH004", "Invalid token")
	if auth.calls != 0 {
		t.Errorf("CurrentUser() calls = %d, want 0", auth.calls)
	}
}

func TestAuthHandlerMe_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		user        *auth.User
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "session expired",
			serviceErr:  auth.ErrSessionExpired,
			wantStatus:  fiber.StatusUnauthorized,
			wantCode:    "AUTH005",
			wantMessage: "Session expired",
		},
		{
			name:        "account locked",
			serviceErr:  auth.ErrAccountLocked,
			wantStatus:  fiber.StatusUnauthorized,
			wantCode:    "AUTH002",
			wantMessage: "Account locked",
		},
		{
			name:        "unexpected service error",
			serviceErr:  errors.New("test current-user error"),
			wantStatus:  fiber.StatusInternalServerError,
			wantCode:    "INTERNAL_ERROR",
			wantMessage: "Internal server error",
		},
		{
			name:        "nil user without error",
			wantStatus:  fiber.StatusInternalServerError,
			wantCode:    "INTERNAL_ERROR",
			wantMessage: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := uuid.New()
			auth := &fakeCurrentUserService{user: tt.user, err: tt.serviceErr}
			handler := NewAuthHandler(auth)
			app := fiber.New()
			app.Use(func(c *fiber.Ctx) error {
				c.Locals(LocalUserID, userID)
				return c.Next()
			})
			app.Get("/api/auth/me", handler.Me)

			request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			assertErrorResponse(t, response, tt.wantCode, tt.wantMessage)
			if auth.calls != 1 || auth.receivedUserID != userID {
				t.Errorf("CurrentUser() user/calls = %s/%d, want %s/1", auth.receivedUserID, auth.calls, userID)
			}
		})
	}
}

func assertRefreshCookieCleared(t *testing.T, response *http.Response) {
	t.Helper()

	var clearedCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == refreshTokenCookieName {
			clearedCookie = cookie
			break
		}
	}
	if clearedCookie == nil {
		t.Fatal("response does not clear refresh_token cookie")
	}
	if clearedCookie.Value != "" || clearedCookie.Path != "/api/auth" || clearedCookie.MaxAge > 0 {
		t.Errorf("cleared refresh cookie values = %#v", clearedCookie)
	}
	if !clearedCookie.HttpOnly || !clearedCookie.Secure || clearedCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cleared refresh cookie security attributes = %#v", clearedCookie)
	}
	if !clearedCookie.Expires.Before(time.Now()) {
		t.Errorf("cleared refresh cookie expiry = %s, want past", clearedCookie.Expires)
	}
}

func assertErrorResponse(t *testing.T, response *http.Response, wantCode, wantMessage string) {
	t.Helper()

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Error.Code != wantCode || body.Error.Message != wantMessage {
		t.Errorf(
			"error response = (%q, %q), want (%q, %q)",
			body.Error.Code,
			body.Error.Message,
			wantCode,
			wantMessage,
		)
	}
}

type testContextKey struct{}
