package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/thefuriousowl/iot-edge/internal/winruntime"
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
	LogFile              string
	HMIEnergyPluginID    string
	HMIAPIKey            string
}

type nativeConfig struct {
	Port                 string `json:"port"`
	Env                  string `json:"env"`
	JWTAccessExpiry      string `json:"jwt_access_expiry"`
	JWTRefreshExpiry     string `json:"jwt_refresh_expiry"`
	LogLevel             string `json:"log_level"`
	CORSAllowOrigins     string `json:"cors_allow_origins"`
	CookieSecure         *bool  `json:"cookie_secure"`
	InternetCheckAddress string `json:"internet_check_address"`
	InternetCheckTimeout string `json:"internet_check_timeout"`
}

func LoadNative(paths winruntime.Paths) (*Config, error) {
	raw, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("read native runtime config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var public nativeConfig
	if err := decoder.Decode(&public); err != nil {
		return nil, errors.New("native runtime config is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("native runtime config contains trailing data")
	}
	secrets, err := winruntime.ReadSecrets(paths.SecretsFile)
	if err != nil {
		return nil, fmt.Errorf("read protected runtime secrets: %w", err)
	}
	values := map[string]string{"DATABASE_URL": secrets.DatabaseURL, "JWT_SECRET": secrets.JWTSecret, "PORT": public.Port, "ENV": public.Env, "JWT_ACCESS_EXPIRY": public.JWTAccessExpiry, "JWT_REFRESH_EXPIRY": public.JWTRefreshExpiry, "LOG_LEVEL": public.LogLevel, "CORS_ALLOW_ORIGINS": public.CORSAllowOrigins, "INTERNET_CHECK_ADDRESS": public.InternetCheckAddress, "INTERNET_CHECK_TIMEOUT": public.InternetCheckTimeout, "PUBLISHER_MASTER_KEY_ID": secrets.PublisherMasterKeyID, "PUBLISHER_MASTER_KEY_BASE64": secrets.PublisherMasterKeyBase64}
	if public.CookieSecure != nil {
		values["COOKIE_SECURE"] = strconv.FormatBool(*public.CookieSecure)
	}
	cfg, err := loadValues(func(key string) string { return values[key] })
	if err != nil {
		return nil, err
	}
	cfg.LogFile = paths.LogFile
	return cfg, nil
}

// LoadWindowsNative resolves the installed ProgramData configuration. It is
// intended for native maintenance commands running outside the service host.
func LoadWindowsNative() (*Config, error) {
	paths, err := winruntime.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return LoadNative(paths)
}

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load environment file: %w", err)
	}
	return loadValues(os.Getenv)
}

func loadValues(value func(string) string) (*Config, error) {
	get := func(key, fallback string) string {
		if configured := value(key); configured != "" {
			return configured
		}
		return fallback
	}
	port := get("PORT", "8080")
	env := get("ENV", "development")

	dbURL := value("DATABASE_URL")
	if dbURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	jwtSecret := value("JWT_SECRET")
	if jwtSecret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}

	accessExpiry, _ := time.ParseDuration(get("JWT_ACCESS_EXPIRY", "15m"))
	refreshExpiry, _ := time.ParseDuration(get("JWT_REFRESH_EXPIRY", "168h"))
	defaultCORSAllowOrigins := "http://localhost:5173,http://127.0.0.1:5173,http://iot-edge.home.arpa:5173"
	if env == "production" {
		defaultCORSAllowOrigins = "https://iot-edge.home.arpa"
	}
	cookieSecure, err := strconv.ParseBool(get(
		"COOKIE_SECURE",
		strconv.FormatBool(env == "production"),
	))
	if err != nil {
		return nil, fmt.Errorf("COOKIE_SECURE must be true or false: %w", err)
	}
	internetCheckTimeout, err := time.ParseDuration(get("INTERNET_CHECK_TIMEOUT", "2s"))
	if err != nil || internetCheckTimeout <= 0 {
		return nil, errors.New("INTERNET_CHECK_TIMEOUT must be a positive duration")
	}
	publisherMasterKeyID := value("PUBLISHER_MASTER_KEY_ID")
	publisherMasterKeyEncoded := value("PUBLISHER_MASTER_KEY_BASE64")
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
	hmiEnergyPluginID := value("HMI_ENERGY_PLUGIN_ID")
	hmiAPIKey := value("HMI_API_KEY")
	if (hmiEnergyPluginID == "") != (hmiAPIKey == "") {
		return nil, errors.New("HMI_ENERGY_PLUGIN_ID and HMI_API_KEY must be configured together")
	}
	if hmiAPIKey != "" && len(hmiAPIKey) < 32 {
		return nil, errors.New("HMI_API_KEY must be at least 32 characters")
	}

	return &Config{
		Port:             port,
		Env:              env,
		DatabaseURL:      dbURL,
		JWTSecret:        jwtSecret,
		JWTAccessExpiry:  accessExpiry,
		JWTRefreshExpiry: refreshExpiry,
		LogLevel:         get("LOG_LEVEL", "info"),
		CORSAllowOrigins: get(
			"CORS_ALLOW_ORIGINS",
			defaultCORSAllowOrigins,
		),
		CookieSecure:         cookieSecure,
		InternetCheckAddress: get("INTERNET_CHECK_ADDRESS", "1.1.1.1:443"),
		InternetCheckTimeout: internetCheckTimeout,
		PublisherMasterKeyID: publisherMasterKeyID,
		PublisherMasterKey:   publisherMasterKey,
		HMIEnergyPluginID:    hmiEnergyPluginID,
		HMIAPIKey:            hmiAPIKey,
	}, nil
}

// Helper function
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
