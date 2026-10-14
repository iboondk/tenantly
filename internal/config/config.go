// Package config loads runtime settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	defaultAppEnv             = "dev"
	defaultHTTPAddr           = ":8080"
	defaultDevDatabaseURL     = "postgres://tenantly_app:changeme@localhost:5432/tenantly?sslmode=disable"
	defaultMigrateDatabaseURL = "postgres://postgres:changeme@localhost:5432/tenantly?sslmode=disable"
	defaultRedisURL           = "redis://localhost:6379/0"
	defaultJWTSecret          = "replace-with-32+-random-bytes"
	defaultJWTTTL             = "1h"
	defaultOAuthRedirectURL   = "http://localhost:8080/v1/auth/oauth/callback"
	defaultRateLimitPerMinute = "120"
	defaultCacheTTL           = "30s"
	defaultOTelExporter       = "stdout"
	defaultOTLPEndpoint       = "localhost:4317"

	minJWTSecretBytes = 32
)

// Config holds all runtime settings.
type Config struct {
	AppEnv             string
	HTTPAddr           string
	DatabaseURL        string
	MigrateDatabaseURL string
	RedisURL           string
	JWTSecret          []byte
	JWTTTL             time.Duration
	OAuth              OAuthConfig
	RateLimitPerMinute int
	CacheTTL           time.Duration
	OTelExporter       string
	OTLPEndpoint       string
}

// OAuthConfig holds settings for the generic OAuth2 provider.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	RedirectURL  string
}

// Load builds a Config from getenv (normally os.Getenv).
//
// Defaults match .env.example. DATABASE_URL has a default only in dev and
// test, so production must set it explicitly. JWT_SECRET must be at least 32
// bytes unless APP_ENV is dev or test.
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}

	appEnv := get("APP_ENV", defaultAppEnv)
	isDev := appEnv == "dev" || appEnv == "test"

	dbURL := getenv("DATABASE_URL")
	if dbURL == "" && isDev {
		dbURL = defaultDevDatabaseURL
	}
	if dbURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required outside dev/test")
	}

	secret := get("JWT_SECRET", defaultJWTSecret)
	if len(secret) < minJWTSecretBytes && !isDev {
		return Config{}, fmt.Errorf("config: JWT_SECRET must be at least %d bytes unless APP_ENV is dev or test", minJWTSecretBytes)
	}

	jwtTTL, err := parseDuration("JWT_TTL", get("JWT_TTL", defaultJWTTTL))
	if err != nil {
		return Config{}, err
	}
	cacheTTL, err := parseDuration("CACHE_TTL", get("CACHE_TTL", defaultCacheTTL))
	if err != nil {
		return Config{}, err
	}

	rateLimit, err := strconv.Atoi(get("RATE_LIMIT_PER_MINUTE", defaultRateLimitPerMinute))
	if err != nil {
		return Config{}, fmt.Errorf("config: RATE_LIMIT_PER_MINUTE: %w", err)
	}
	if rateLimit <= 0 {
		return Config{}, errors.New("config: RATE_LIMIT_PER_MINUTE must be positive")
	}

	exporter := get("OTEL_EXPORTER", defaultOTelExporter)
	switch exporter {
	case "stdout", "otlp", "none":
	default:
		return Config{}, fmt.Errorf("config: OTEL_EXPORTER must be stdout, otlp or none, got %q", exporter)
	}

	return Config{
		AppEnv:             appEnv,
		HTTPAddr:           get("HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:        dbURL,
		MigrateDatabaseURL: get("MIGRATE_DATABASE_URL", defaultMigrateDatabaseURL),
		RedisURL:           get("REDIS_URL", defaultRedisURL),
		JWTSecret:          []byte(secret),
		JWTTTL:             jwtTTL,
		OAuth: OAuthConfig{
			ClientID:     getenv("OAUTH_CLIENT_ID"),
			ClientSecret: getenv("OAUTH_CLIENT_SECRET"),
			AuthURL:      getenv("OAUTH_AUTH_URL"),
			TokenURL:     getenv("OAUTH_TOKEN_URL"),
			UserInfoURL:  getenv("OAUTH_USERINFO_URL"),
			RedirectURL:  get("OAUTH_REDIRECT_URL", defaultOAuthRedirectURL),
		},
		RateLimitPerMinute: rateLimit,
		CacheTTL:           cacheTTL,
		OTelExporter:       exporter,
		OTLPEndpoint:       get("OTEL_EXPORTER_OTLP_ENDPOINT", defaultOTLPEndpoint),
	}, nil
}

func parseDuration(key, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: %s must be positive", key)
	}
	return d, nil
}
