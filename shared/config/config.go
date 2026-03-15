package config

import (
	"log/slog"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func LoadConfig() {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found, relying on environment variables")
	}
	normalizeEnv()
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

func unquoteValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
