package logger

import (
	"log/slog"
	"os"
	"strings"
)

// Init configures the global slog default logger.
// mode: "dev" for pretty console output, "prod" (or anything else) for JSON.
// service: the service name to include in every log line.
func Init(service string, mode string) {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level:     parseLevel(os.Getenv("LOG_LEVEL")),
		AddSource: strings.EqualFold(mode, "dev"),
	}

	if strings.EqualFold(mode, "dev") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler).With("service", service)
	slog.SetDefault(logger)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
