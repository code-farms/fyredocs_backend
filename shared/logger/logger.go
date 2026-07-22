// Package logger configures the shared structured logger (slog) and provides
// request-ID propagation and operation-logging helpers. Each service calls Init
// once at startup; there is no global shared instance across processes.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// sensitiveKeys are redacted (value → "[REDACTED]") anywhere they appear as a
// log attribute key, case-insensitively. A structural safety net — the real fix
// is never logging secrets, but this keeps an accidental one out of the sink.
var sensitiveKeys = map[string]bool{
	"password": true, "passwd": true, "token": true, "authorization": true,
	"secret": true, "reset_url": true, "reseturl": true, "otp": true,
	"api_key": true, "apikey": true, "access_token": true, "refresh_token": true,
}

// Init configures the global slog default logger.
// mode: "dev" for pretty console output, "prod" (or anything else) for JSON.
// service: the service name to include in every log line.
func Init(service string, mode string) {
	dev := strings.EqualFold(mode, "dev")

	opts := &slog.HandlerOptions{
		Level:       parseLevel(os.Getenv("LOG_LEVEL")),
		AddSource:   dev,
		ReplaceAttr: replaceAttr,
	}

	var base slog.Handler
	if dev {
		base = slog.NewTextHandler(os.Stdout, opts)
	} else {
		base = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(&contextHandler{Handler: base}).With("service", service)
	slog.SetDefault(logger)
}

// contextHandler enriches every record with correlation IDs pulled from the
// context: the OpenTelemetry trace_id/span_id and the request_id. This is why
// the logging helpers use the *Context slog variants (InfoContext/ErrorContext);
// a bare slog.Info carries context.Background() and so simply omits these fields.
type contextHandler struct{ slog.Handler }

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			r.AddAttrs(
				slog.String("trace_id", sc.TraceID().String()),
				slog.String("span_id", sc.SpanID().String()),
			)
		}
		if id := RequestIDFromContext(ctx); id != "" {
			r.AddAttrs(slog.String("request_id", id))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

// replaceAttr normalizes timestamps to UTC and redacts sensitive attribute keys.
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		a.Value = slog.TimeValue(a.Value.Time().UTC())
		return a
	}
	if sensitiveKeys[strings.ToLower(a.Key)] {
		a.Value = slog.StringValue("[REDACTED]")
	}
	return a
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
