package config

import (
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppEnv", cfg.AppEnv, "dev"},
		{"HTTPAddr", cfg.HTTPAddr, ":8080"},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://tenantly_app:changeme@localhost:5432/tenantly?sslmode=disable"},
		{"MigrateDatabaseURL", cfg.MigrateDatabaseURL, "postgres://postgres:changeme@localhost:5432/tenantly?sslmode=disable"},
		{"RedisURL", cfg.RedisURL, "redis://localhost:6379/0"},
		{"JWTTTL", cfg.JWTTTL, time.Hour},
		{"RateLimitPerMinute", cfg.RateLimitPerMinute, 120},
		{"CacheTTL", cfg.CacheTTL, 30 * time.Second},
		{"OTelExporter", cfg.OTelExporter, "stdout"},
		{"OTLPEndpoint", cfg.OTLPEndpoint, "localhost:4317"},
		{"OAuth.RedirectURL", cfg.OAuth.RedirectURL, "http://localhost:8080/v1/auth/oauth/callback"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if string(cfg.JWTSecret) != "replace-with-32+-random-bytes" {
		t.Errorf("JWTSecret default = %q", cfg.JWTSecret)
	}
}

func TestLoadRejectsShortSecretOutsideDev(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "prod with placeholder secret",
			env:     map[string]string{"APP_ENV": "prod", "DATABASE_URL": "postgres://x"},
			wantErr: true,
		},
		{
			name:    "prod with 31-byte secret",
			env:     map[string]string{"APP_ENV": "prod", "DATABASE_URL": "postgres://x", "JWT_SECRET": strings.Repeat("a", 31)},
			wantErr: true,
		},
		{
			name:    "prod with 32-byte secret",
			env:     map[string]string{"APP_ENV": "prod", "DATABASE_URL": "postgres://x", "JWT_SECRET": strings.Repeat("a", 32)},
			wantErr: false,
		},
		{
			name:    "test with short secret",
			env:     map[string]string{"APP_ENV": "test", "JWT_SECRET": "short"},
			wantErr: false,
		},
		{
			name:    "dev with short secret",
			env:     map[string]string{"APP_ENV": "dev", "JWT_SECRET": "short"},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(envFrom(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Load error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRequiresDatabaseURLOutsideDev(t *testing.T) {
	_, err := Load(envFrom(map[string]string{"APP_ENV": "prod", "JWT_SECRET": strings.Repeat("k", 40)}))
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is empty in prod")
	}
	cfg, err := Load(envFrom(map[string]string{"APP_ENV": "prod", "JWT_SECRET": strings.Repeat("k", 40), "DATABASE_URL": "postgres://db"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://db" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadDurationsParse(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{"JWT_TTL": "15m", "CACHE_TTL": "2s", "RATE_LIMIT_PER_MINUTE": "5"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.JWTTTL != 15*time.Minute {
		t.Errorf("JWTTTL = %v", cfg.JWTTTL)
	}
	if cfg.CacheTTL != 2*time.Second {
		t.Errorf("CacheTTL = %v", cfg.CacheTTL)
	}
	if cfg.RateLimitPerMinute != 5 {
		t.Errorf("RateLimitPerMinute = %d", cfg.RateLimitPerMinute)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"bad JWT_TTL", map[string]string{"JWT_TTL": "soon"}},
		{"zero JWT_TTL", map[string]string{"JWT_TTL": "0s"}},
		{"bad CACHE_TTL", map[string]string{"CACHE_TTL": "30 seconds"}},
		{"non-numeric rate limit", map[string]string{"RATE_LIMIT_PER_MINUTE": "many"}},
		{"zero rate limit", map[string]string{"RATE_LIMIT_PER_MINUTE": "0"}},
		{"unknown exporter", map[string]string{"OTEL_EXPORTER": "jaeger"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(envFrom(tc.env)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
