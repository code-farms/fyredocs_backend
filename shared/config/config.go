// Package config loads environment-based configuration shared by all services:
// .env loading, typed env-var getters with defaults, plan/server defaults, and
// the Postgres DSN builder.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// LoadConfig loads a local .env file if present, then relies on the process
// environment. A missing .env is not an error (expected in production).
func LoadConfig() {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found, relying on environment variables")
	}
	normalizeEnv()

	// Fail fast on an insecure production configuration (no-op outside
	// production). Every service calls LoadConfig first, so this guards the
	// whole fleet from one place instead of relying on a per-service call.
	if err := EnforceProductionSecurity(); err != nil {
		slog.Error("refusing to start with insecure production configuration", "error", err)
		os.Exit(1)
	}
}

func normalizeEnv() {
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		cleaned := unquoteValue(value)
		if cleaned != value {
			_ = os.Setenv(key, cleaned)
		}
	}
}

// GetEnv returns the environment variable for key, or fallback if empty.
func GetEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// GetEnvBool returns the environment variable for key parsed as a boolean, or fallback if empty/unrecognised.
func GetEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
}

// GetEnvInt returns the environment variable for key parsed as an int, or fallback if empty/invalid.
func GetEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// GetEnvDuration returns the environment variable for key parsed as a time.Duration, or fallback if empty/invalid.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// TrustedProxies returns the TRUSTED_PROXIES env var as a string slice, defaulting to loopback addresses.
func TrustedProxies() []string {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return []string{"127.0.0.1", "::1"}
	}
	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		proxy := strings.TrimSpace(part)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	if len(proxies) == 0 {
		return []string{"127.0.0.1", "::1"}
	}
	return proxies
}

// ValidateJWTSecret checks that JWT_HS256_SECRET is set, long enough, and not a known default.
func ValidateJWTSecret() error {
	secret := os.Getenv("JWT_HS256_SECRET")
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("JWT_HS256_SECRET environment variable is required but not set")
	}

	if len(secret) < 64 {
		return fmt.Errorf("JWT_HS256_SECRET must be at least 64 characters (e.g. `openssl rand -hex 32`), got %d characters", len(secret))
	}

	dangerousSecrets := []string{
		"4de0ea7311594deb860f03e5da60ac903fc4b4099bfe499a82e0fed013af32ca791ac065ea5e4d8aaade24a760e6dc58",
		// Former committed dev default (was in .env and config_test.go); permanently rejected.
		"aT9kLmW3xQr7vBn5yHs2jFp8cUe6dGi4",
		"change-me",
		"secret",
		"password",
	}
	for _, dangerous := range dangerousSecrets {
		if secret == dangerous {
			return fmt.Errorf("JWT_HS256_SECRET appears to be a default/example value - use a cryptographically random secret")
		}
	}

	slog.Info("JWT secret validation passed")
	return nil
}

// EnforceProductionSecurity fails fast when the process is running in
// production (ENVIRONMENT or APP_ENV == "production") with insecure settings
// that would otherwise be silently accepted: non-Secure auth cookies, an
// unencrypted database connection, or a plain-HTTP public origin. Outside
// production it is a no-op. Each service calls this once at startup so a
// misconfigured prod deploy crashes loudly instead of serving cleartext
// sessions.
func EnforceProductionSecurity() error {
	env := strings.ToLower(strings.TrimSpace(GetEnv("ENVIRONMENT", GetEnv("APP_ENV", ""))))
	if env != "production" && env != "prod" {
		return nil
	}

	var problems []string

	if !GetEnvBool("AUTH_COOKIE_SECURE", false) {
		problems = append(problems, "AUTH_COOKIE_SECURE must be true in production (auth cookies would otherwise be sent over plain HTTP)")
	}

	dsn := strings.ToLower(os.Getenv("DATABASE_URL"))
	if strings.Contains(dsn, "sslmode=disable") {
		problems = append(problems, "DATABASE_URL uses sslmode=disable in production (use sslmode=require or verify-full)")
	}

	for _, key := range []string{"PUBLIC_ORIGIN", "APP_BASE_URL"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "http://") && !strings.Contains(v, "localhost") && !strings.Contains(v, "127.0.0.1") {
			problems = append(problems, key+" is a plain-HTTP origin in production (use https://)")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("insecure production configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	slog.Info("production security checks passed")
	return nil
}

func unquoteValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
