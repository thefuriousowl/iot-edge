package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port             string
	Env              string
	DatabaseURL      string
	JWTSecret        string
	JWTAccessExpiry  time.Duration
	JWTRefreshExpiry time.Duration
	LogLevel         string
	CORSAllowOrigins string
	CookieSecure     bool
}

func Load() (*Config, error) {
	godotenv.Load()
	port := getEnv("PORT", "8080")
	env := getEnv("ENV", "development")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}

	accessExpiry, _ := time.ParseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"))
	refreshExpiry, _ := time.ParseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"))
	defaultCORSAllowOrigins := "http://localhost:5173,http://127.0.0.1:5173,http://iot-edge.home.arpa:5173"
	if env == "production" {
		defaultCORSAllowOrigins = "https://iot-edge.home.arpa"
	}
	cookieSecure, err := strconv.ParseBool(getEnv(
		"COOKIE_SECURE",
		strconv.FormatBool(env == "production"),
	))
	if err != nil {
		return nil, fmt.Errorf("COOKIE_SECURE must be true or false: %w", err)
	}

	return &Config{
		Port:             port,
		Env:              env,
		DatabaseURL:      dbURL,
		JWTSecret:        jwtSecret,
		JWTAccessExpiry:  accessExpiry,
		JWTRefreshExpiry: refreshExpiry,
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		CORSAllowOrigins: getEnv(
			"CORS_ALLOW_ORIGINS",
			defaultCORSAllowOrigins,
		),
		CookieSecure: cookieSecure,
	}, nil
}

// Helper function
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
