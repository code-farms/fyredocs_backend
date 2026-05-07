package logger

import (
	"context"
	"log/slog"
)

// LogErr emits a structured slog.Error for an operation failure and returns
// the same err so it can be used inline in a return statement:
//
//	return logger.LogErr(ctx, "db.processing_jobs.create", err, "jobId", id)
//
// Pass nil err to no-op (returns nil). The context's request ID, if present,
// is automatically attached. Additional attrs are appended verbatim.
func LogErr(ctx context.Context, op string, err error, attrs ...any) error {
	if err == nil {
		return nil
	}
	slog.Error(op+" failed", buildOpAttrs(ctx, op, err, attrs)...)
	return err
}

// LogWarn is the same shape as LogErr but emits at slog.LevelWarn for expected
// or benign errors (redis.Nil, EOF, "not found" lookups, etc.). Returns the
// same err so callers can chain it.
func LogWarn(ctx context.Context, op string, err error, attrs ...any) error {
	if err == nil {
		return nil
	}
	slog.Warn(op, buildOpAttrs(ctx, op, err, attrs)...)
	return err
}

func buildOpAttrs(ctx context.Context, op string, err error, extra []any) []any {
	attrs := []any{"err", err, "op", op}
	if reqID := RequestIDFromContext(ctx); reqID != "" {
		attrs = append(attrs, "requestId", reqID)
	}
	return append(attrs, extra...)
}
