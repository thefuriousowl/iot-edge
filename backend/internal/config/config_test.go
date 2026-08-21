package config

import (
	"os"
	"testing"
	"time"
)

// setupEnv resets only the configuration variables used by Load and restores
// their original values after the test finishes.
func setupEnv(t *testing.T, envs map[string]string) {
	t.Helper()

	configKeys := []string{
		"PORT",
		"ENV",
		"DATABASE_URL",
		"JWT_SECRET",
		"JWT_ACCESS_EXPIRY",
		"JWT_REFRESH_EXPIRY",
		"LOG_LEVEL",
		"CORS_ALLOW_ORIGINS",
		"COOKIE_SECURE",
	}
	for _, key := range configKeys {
		t.Setenv(key, "")
	}

	for k, v := range envs {
		t.Setenv(k, v)
	}
}

// validEnv returns a map with all required env vars set
func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/testdb",
		"JWT_SECRET":   "test-secret-key-minimum-32-characters",
	}
}

func TestLoad_MissingDatabaseURL_ReturnsError(t *testing.T) {
	// Arrange
	setupEnv(t, map[string]string{
		"JWT_SECRET": "test-secret-key-minimum-32-characters",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err == nil {
		t.Error("expected error when DATABASE_URL is missing, got nil")
	}
	if cfg != nil {
		t.Error("expected nil config when error occurs")
	}
	if err.Error() != "DATABASE_URL is required" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestLoad_MissingJWTSecret_ReturnsError(t *testing.T) {
	// Arrange
	setupEnv(t, map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/testdb",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err == nil {
		t.Error("expected error when JWT_SECRET is missing, got nil")
	}
	if cfg != nil {
		t.Error("expected nil config when error occurs")
	}
	if err.Error() != "JWT_SECRET is required" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestLoad_ValidConfig_ReturnsConfig(t *testing.T) {
	// Arrange
	setupEnv(t, validEnv())

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/testdb" {
		t.Errorf("unexpected DatabaseURL: %s", cfg.DatabaseURL)
	}
	if cfg.JWTSecret != "test-secret-key-minimum-32-characters" {
		t.Errorf("unexpected JWTSecret: %s", cfg.JWTSecret)
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	// Arrange
	setupEnv(t, validEnv())

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check default values
	if cfg.Port != "8080" {
		t.Errorf("expected default Port '8080', got '%s'", cfg.Port)
	}
	if cfg.Env != "development" {
		t.Errorf("expected default Env 'development', got '%s'", cfg.Env)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel 'info', got '%s'", cfg.LogLevel)
	}
	if cfg.JWTAccessExpiry != 15*time.Minute {
		t.Errorf("expected default JWTAccessExpiry 15m, got %v", cfg.JWTAccessExpiry)
	}
	if cfg.JWTRefreshExpiry != 168*time.Hour {
		t.Errorf("expected default JWTRefreshExpiry 168h, got %v", cfg.JWTRefreshExpiry)
	}
	if cfg.CORSAllowOrigins != "http://localhost:5173,http://127.0.0.1:5173,http://iot-edge.home.arpa:5173" {
		t.Errorf("unexpected default CORSAllowOrigins: %q", cfg.CORSAllowOrigins)
	}
	if cfg.CookieSecure {
		t.Error("expected development cookies to allow HTTP")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	// Arrange
	setupEnv(t, map[string]string{
		"DATABASE_URL":       "postgres://custom:pass@localhost:5432/customdb",
		"JWT_SECRET":         "custom-secret-key-minimum-32-characters",
		"PORT":               "3000",
		"ENV":                "production",
		"LOG_LEVEL":          "error",
		"JWT_ACCESS_EXPIRY":  "30m",
		"JWT_REFRESH_EXPIRY": "24h",
		"CORS_ALLOW_ORIGINS": "https://iot-edge.example.com",
		"COOKIE_SECURE":      "true",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "3000" {
		t.Errorf("expected Port '3000', got '%s'", cfg.Port)
	}
	if cfg.Env != "production" {
		t.Errorf("expected Env 'production', got '%s'", cfg.Env)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("expected LogLevel 'error', got '%s'", cfg.LogLevel)
	}
	if cfg.JWTAccessExpiry != 30*time.Minute {
		t.Errorf("expected JWTAccessExpiry 30m, got %v", cfg.JWTAccessExpiry)
	}
	if cfg.JWTRefreshExpiry != 24*time.Hour {
		t.Errorf("expected JWTRefreshExpiry 24h, got %v", cfg.JWTRefreshExpiry)
	}
	if cfg.CORSAllowOrigins != "https://iot-edge.example.com" {
		t.Errorf("unexpected CORSAllowOrigins: %q", cfg.CORSAllowOrigins)
	}
	if !cfg.CookieSecure {
		t.Error("expected custom COOKIE_SECURE=true")
	}
}

func TestLoad_ProductionDefaultsToSecureCookies(t *testing.T) {
	// Arrange
	setupEnv(t, map[string]string{
		"DATABASE_URL": "postgres://user:pass@localhost:5432/testdb",
		"JWT_SECRET":   "test-secret-key-minimum-32-characters",
		"ENV":          "production",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("expected production cookies to be secure by default")
	}
	if cfg.CORSAllowOrigins != "https://iot-edge.home.arpa" {
		t.Errorf("unexpected production CORSAllowOrigins: %q", cfg.CORSAllowOrigins)
	}
}

func TestLoad_InvalidCookieSecureReturnsError(t *testing.T) {
	// Arrange
	setupEnv(t, map[string]string{
		"DATABASE_URL":  "postgres://user:pass@localhost:5432/testdb",
		"JWT_SECRET":    "test-secret-key-minimum-32-characters",
		"COOKIE_SECURE": "sometimes",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err == nil {
		t.Fatal("expected an error for invalid COOKIE_SECURE")
	}
	if cfg != nil {
		t.Errorf("expected nil config, got %#v", cfg)
	}
}

func TestGetEnv_WithValue_ReturnsValue(t *testing.T) {
	// Arrange
	os.Setenv("TEST_KEY", "test_value")
	defer os.Unsetenv("TEST_KEY")

	// Act
	result := getEnv("TEST_KEY", "default")

	// Assert
	if result != "test_value" {
		t.Errorf("expected 'test_value', got '%s'", result)
	}
}

func TestGetEnv_WithoutValue_ReturnsDefault(t *testing.T) {
	// Arrange
	os.Unsetenv("NONEXISTENT_KEY")

	// Act
	result := getEnv("NONEXISTENT_KEY", "default_value")

	// Assert
	if result != "default_value" {
		t.Errorf("expected 'default_value', got '%s'", result)
	}
}

func TestGetEnv_EmptyValue_ReturnsDefault(t *testing.T) {
	// Arrange
	os.Setenv("EMPTY_KEY", "")
	defer os.Unsetenv("EMPTY_KEY")

	// Act
	result := getEnv("EMPTY_KEY", "default_value")

	// Assert
	if result != "default_value" {
		t.Errorf("expected 'default_value' for empty env var, got '%s'", result)
	}
}
