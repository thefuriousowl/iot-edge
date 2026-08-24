package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                 string
	Env                  string
	DatabaseURL          string
	JWTSecret            string
	JWTAccessExpiry      time.Duration
	JWTRefreshExpiry     time.Duration
	LogLevel             string
	CORSAllowOrigins     string
	CookieSecure         bool
	InternetCheckAddress string
	InternetCheckTimeout time.Duration
	PublisherMasterKeyID string
	PublisherMasterKey   []byte
}

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load environment file: %w", err)
	}
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
	internetCheckTimeout, err := time.ParseDuration(getEnv("INTERNET_CHECK_TIMEOUT", "2s"))
	if err != nil || internetCheckTimeout <= 0 {
		return nil, errors.New("INTERNET_CHECK_TIMEOUT must be a positive duration")
	}
	publisherMasterKeyID := os.Getenv("PUBLISHER_MASTER_KEY_ID")
	publisherMasterKeyEncoded := os.Getenv("PUBLISHER_MASTER_KEY_BASE64")
	var publisherMasterKey []byte
	if publisherMasterKeyID != "" || publisherMasterKeyEncoded != "" {
		if publisherMasterKeyID == "" || publisherMasterKeyEncoded == "" {
			return nil, errors.New("PUBLISHER_MASTER_KEY_ID and PUBLISHER_MASTER_KEY_BASE64 must be configured together")
		}
		publisherMasterKey, err = base64.StdEncoding.DecodeString(publisherMasterKeyEncoded)
		if err != nil || len(publisherMasterKey) != 32 {
			return nil, errors.New("PUBLISHER_MASTER_KEY_BASE64 must encode exactly 32 bytes")
		}
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
		CookieSecure:         cookieSecure,
		InternetCheckAddress: getEnv("INTERNET_CHECK_ADDRESS", "1.1.1.1:443"),
		InternetCheckTimeout: internetCheckTimeout,
		PublisherMasterKeyID: publisherMasterKeyID,
		PublisherMasterKey:   publisherMasterKey,
	}, nil
}

// Helper function
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
