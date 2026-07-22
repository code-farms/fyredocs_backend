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
	// *Context so the shared contextHandler attaches trace_id/span_id/request_id.
	slog.ErrorContext(ctx, op+" failed", buildOpAttrs(op, err, attrs)...)
	return err
}

// LogWarn is the same shape as LogErr but emits at slog.LevelWarn for expected
// or benign errors (redis.Nil, EOF, "not found" lookups, etc.). Returns the
// same err so callers can chain it.
func LogWarn(ctx context.Context, op string, err error, attrs ...any) error {
	if err == nil {
		return nil
	}
	slog.WarnContext(ctx, op, buildOpAttrs(op, err, attrs)...)
	return err
}

// buildOpAttrs assembles the op/error attrs. trace_id/span_id/request_id are added
// automatically by the shared contextHandler from the ctx passed to *Context logs.
func buildOpAttrs(op string, err error, extra []any) []any {
	return append([]any{"error", err, "op", op}, extra...)
}
