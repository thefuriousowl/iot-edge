package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/thefuriousowl/iot-edge/internal/auth"
	authhttp "github.com/thefuriousowl/iot-edge/internal/auth/http"
	authpostgres "github.com/thefuriousowl/iot-edge/internal/auth/postgres"
)

func TestHealthCheck_ReturnsOKWithVersion(t *testing.T) {
	// Arrange
	app := newApp("http://localhost:5173")
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)

	// Act
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("health-check request returned an unexpected error: %v", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("closing response body: %v", err)
		}
	}()

	// Assert
	if response.StatusCode != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.StatusCode, http.StatusOK)
	}

	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Version != "0.1.0" {
		t.Errorf("version = %q, want %q", body.Version, "0.1.0")
	}
}

func TestCORS_AllowsConfiguredHostnameWithCredentials(t *testing.T) {
	// Arrange
	const origin = "http://iot-edge.home.arpa:5173"
	app := newApp(origin)
	request := httptest.NewRequest(http.MethodOptions, "/api/health", nil)
	request.Header.Set(fiber.HeaderOrigin, origin)
	request.Header.Set(fiber.HeaderAccessControlRequestMethod, http.MethodGet)
	request.Header.Set(fiber.HeaderAccessControlRequestHeaders, fiber.HeaderAuthorization)

	// Act
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("CORS preflight returned an unexpected error: %v", err)
	}
	defer response.Body.Close()

	// Assert
	if response.StatusCode != fiber.StatusNoContent {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusNoContent)
	}
	if got := response.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := response.Header.Get(fiber.HeaderAccessControlAllowCredentials); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := response.Header.Get(fiber.HeaderAccessControlAllowHeaders); !strings.Contains(got, fiber.HeaderAuthorization) {
		t.Errorf("Access-Control-Allow-Headers = %q, want Authorization", got)
	}
}

func TestCORS_DoesNotAllowUnconfiguredOrigin(t *testing.T) {
	// Arrange
	app := newApp("http://iot-edge.home.arpa:5173")
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	request.Header.Set(fiber.HeaderOrigin, "http://untrusted.example:5173")

	// Act
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("request returned an unexpected error: %v", err)
	}
	defer response.Body.Close()

	// Assert
	if got := response.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestInitialSetup_Integration(t *testing.T) {
	db := newInitialSetupTestDatabase(t)
	users := authpostgres.NewUserRepository(db)
	authService, err := auth.NewAuthService(users, &auth.ServiceConfig{
		JWTSecret:        strings.Repeat("s", 32),
		JWTAccessExpiry:  15 * time.Minute,
		JWTRefreshExpiry: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewAuthService() error: %v", err)
	}

	app := newApp("http://localhost:5173")
	authhttp.RegisterAuthRoutes(
		app.Group("/api"),
		authhttp.NewAuthHandler(authService),
		authService,
	)

	setupBody := `{
		"username":"admin",
		"password":"SecureP@ss123",
		"confirm_password":"SecureP@ss123"
	}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(setupBody),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("POST setup error: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("POST setup status = %d, want %d", response.StatusCode, fiber.StatusCreated)
	}

	var setupResponse struct {
		Message string `json:"message"`
		User    struct {
			ID       uuid.UUID `json:"id"`
			Username string    `json:"username"`
		} `json:"user"`
	}
	if err := json.NewDecoder(response.Body).Decode(&setupResponse); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}
	if setupResponse.Message != "Setup completed successfully" {
		t.Errorf("setup message = %q, want success message", setupResponse.Message)
	}
	if setupResponse.User.ID == uuid.Nil || setupResponse.User.Username != "admin" {
		t.Errorf("setup user = %#v, want generated ID and admin username", setupResponse.User)
	}

	storedUser, err := users.FindByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("FindByUsername() after setup: %v", err)
	}
	if !authService.VerifyPassword("SecureP@ss123", storedUser.PasswordHash) {
		t.Error("stored password hash does not verify setup password")
	}

	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"SecureP@ss123"}`),
	)
	loginRequest.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	loginResponse, err := app.Test(loginRequest, -1)
	if err != nil {
		t.Fatalf("POST login error: %v", err)
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("POST login status = %d, want %d", loginResponse.StatusCode, fiber.StatusOK)
	}

	var loginBody struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(loginResponse.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decoding login response: %v", err)
	}
	if loginBody.AccessToken == "" || loginBody.TokenType != "Bearer" {
		t.Errorf("login response = %#v, want access token and Bearer type", loginBody)
	}
	if loginBody.ExpiresIn < 899 || loginBody.ExpiresIn > 900 {
		t.Errorf("login expires_in = %d, want approximately 900", loginBody.ExpiresIn)
	}
	accessClaims, err := authService.ParseToken(loginBody.AccessToken, auth.TokenTypeAccess)
	if err != nil {
		t.Fatalf("parsing login access token: %v", err)
	}
	if accessClaims.Subject != storedUser.ID.String() || accessClaims.Username != storedUser.Username {
		t.Errorf("login access claims = %#v, want stored user", accessClaims)
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meRequest.Header.Set(
		fiber.HeaderAuthorization,
		"Bearer "+loginBody.AccessToken,
	)
	meResponse, err := app.Test(meRequest, -1)
	if err != nil {
		t.Fatalf("GET me error: %v", err)
	}
	defer meResponse.Body.Close()
	if meResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("GET me status = %d, want %d", meResponse.StatusCode, fiber.StatusOK)
	}
	var meBody map[string]any
	if err := json.NewDecoder(meResponse.Body).Decode(&meBody); err != nil {
		t.Fatalf("decoding me response: %v", err)
	}
	if meBody["id"] != storedUser.ID.String() || meBody["username"] != storedUser.Username {
		t.Errorf("GET me user = %#v, want stored user", meBody)
	}
	if len(meBody) != 4 {
		t.Errorf("GET me fields = %#v, want only safe current-user fields", meBody)
	}
	if _, exists := meBody["password_hash"]; exists {
		t.Error("GET me response exposes password_hash")
	}

	var refreshToken string
	for _, cookie := range loginResponse.Cookies() {
		if cookie.Name == "refresh_token" {
			refreshToken = cookie.Value
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("refresh cookie security attributes = %#v", cookie)
			}
			break
		}
	}
	if refreshToken == "" {
		t.Fatal("login response does not contain refresh_token cookie")
	}
	refreshClaims, err := authService.ParseToken(refreshToken, auth.TokenTypeRefresh)
	if err != nil {
		t.Fatalf("parsing login refresh token: %v", err)
	}
	if refreshClaims.Subject != storedUser.ID.String() || refreshClaims.ID == "" {
		t.Errorf("login refresh claims = %#v, want stored user and JTI", refreshClaims)
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	refreshRequest.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: refreshToken,
	})
	refreshResponse, err := app.Test(refreshRequest, -1)
	if err != nil {
		t.Fatalf("POST refresh error: %v", err)
	}
	defer refreshResponse.Body.Close()
	if refreshResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("POST refresh status = %d, want %d", refreshResponse.StatusCode, fiber.StatusOK)
	}
	var refreshBody struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(refreshResponse.Body).Decode(&refreshBody); err != nil {
		t.Fatalf("decoding refresh response: %v", err)
	}
	if refreshBody.AccessToken == "" {
		t.Fatal("refresh response access token is empty")
	}
	if refreshBody.ExpiresIn < 899 || refreshBody.ExpiresIn > 900 {
		t.Errorf("refresh expires_in = %d, want approximately 900", refreshBody.ExpiresIn)
	}
	refreshedClaims, err := authService.ParseToken(refreshBody.AccessToken, auth.TokenTypeAccess)
	if err != nil {
		t.Fatalf("parsing refreshed access token: %v", err)
	}
	if refreshedClaims.Subject != storedUser.ID.String() || refreshedClaims.Username != storedUser.Username {
		t.Errorf("refreshed access claims = %#v, want stored user", refreshedClaims)
	}
	if len(refreshResponse.Cookies()) != 0 {
		t.Error("refresh response unexpectedly rotates refresh cookie")
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutRequest.Header.Set(
		fiber.HeaderAuthorization,
		"Bearer "+loginBody.AccessToken,
	)
	logoutRequest.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: refreshToken,
	})
	logoutResponse, err := app.Test(logoutRequest, -1)
	if err != nil {
		t.Fatalf("POST logout error: %v", err)
	}
	defer logoutResponse.Body.Close()
	if logoutResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("POST logout status = %d, want %d", logoutResponse.StatusCode, fiber.StatusOK)
	}
	var logoutBody struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(logoutResponse.Body).Decode(&logoutBody); err != nil {
		t.Fatalf("decoding logout response: %v", err)
	}
	if logoutBody.Message != "Logged out successfully" {
		t.Errorf("logout message = %q, want success message", logoutBody.Message)
	}
	refreshCookieCleared := false
	for _, cookie := range logoutResponse.Cookies() {
		if cookie.Name == "refresh_token" && cookie.Value == "" && cookie.Expires.Before(time.Now()) {
			refreshCookieCleared = true
			break
		}
	}
	if !refreshCookieCleared {
		t.Error("logout response does not clear refresh_token cookie")
	}
	revoked, err := users.IsTokenRevoked(context.Background(), refreshClaims.ID)
	if err != nil {
		t.Fatalf("IsTokenRevoked() after logout: %v", err)
	}
	if !revoked {
		t.Error("refresh token is not revoked after logout")
	}

	revokedRefreshRequest := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	revokedRefreshRequest.AddCookie(&http.Cookie{
		Name:  "refresh_token",
		Value: refreshToken,
	})
	revokedRefreshResponse, err := app.Test(revokedRefreshRequest, -1)
	if err != nil {
		t.Fatalf("POST refresh after logout error: %v", err)
	}
	defer revokedRefreshResponse.Body.Close()
	if revokedRefreshResponse.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf(
			"POST refresh after logout status = %d, want %d",
			revokedRefreshResponse.StatusCode,
			fiber.StatusUnauthorized,
		)
	}
	var revokedRefreshBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(revokedRefreshResponse.Body).Decode(&revokedRefreshBody); err != nil {
		t.Fatalf("decoding refresh-after-logout response: %v", err)
	}
	if revokedRefreshBody.Error.Code != "AUTH005" {
		t.Errorf("refresh after logout error code = %q, want AUTH005", revokedRefreshBody.Error.Code)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/auth/setup/status", nil)
	statusResponse, err := app.Test(statusRequest, -1)
	if err != nil {
		t.Fatalf("GET setup status error: %v", err)
	}
	defer statusResponse.Body.Close()
	if statusResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("GET setup status = %d, want %d", statusResponse.StatusCode, fiber.StatusOK)
	}
	var statusBody struct {
		SetupRequired bool `json:"setup_required"`
	}
	if err := json.NewDecoder(statusResponse.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decoding setup status: %v", err)
	}
	if statusBody.SetupRequired {
		t.Error("setup_required = true after initial user creation, want false")
	}

	secondRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(setupBody),
	)
	secondRequest.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	secondResponse, err := app.Test(secondRequest, -1)
	if err != nil {
		t.Fatalf("second POST setup error: %v", err)
	}
	defer secondResponse.Body.Close()
	if secondResponse.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("second POST setup = %d, want %d", secondResponse.StatusCode, fiber.StatusBadRequest)
	}
	var secondBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(secondResponse.Body).Decode(&secondBody); err != nil {
		t.Fatalf("decoding second setup response: %v", err)
	}
	if secondBody.Error.Code != "AUTH008" {
		t.Errorf("second setup error code = %q, want AUTH008", secondBody.Error.Code)
	}
}

func newInitialSetupTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the initial setup integration test")
	}

	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Close(); err != nil {
			t.Errorf("closing PostgreSQL connection: %v", err)
		}
	})

	schemaName := "initial_setup_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec("CREATE SCHEMA " + schemaName); err != nil {
		t.Fatalf("creating isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(
			context.Background(),
			"DROP SCHEMA IF EXISTS "+schemaName+" CASCADE",
		); err != nil {
			t.Errorf("dropping isolated test schema: %v", err)
		}
	})

	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parsing test database URL: %v", err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schemaName)
	parsedURL.RawQuery = query.Encode()

	db, err := gorm.Open(postgres.Open(parsedURL.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening isolated GORM connection: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting isolated SQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing isolated GORM connection: %v", err)
		}
	})

	migration, err := os.ReadFile("../../migrations/000001_create_users.up.sql")
	if err != nil {
		t.Fatalf("reading users migration: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying users migration: %v", err)
	}

	return db
}
