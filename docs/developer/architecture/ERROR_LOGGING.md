# Error Logging Convention

Every backend service follows a single rule: at every `if err != nil { ... }` block where the err is actively handled (returned, retried, swallowed, mapped to a user-visible response, or used to break out of a loop), emit a structured `slog.Error` (or `slog.Warn` for benign/expected errors) at the point of detection.

This document defines the convention, the helper functions, and how to correlate user-visible failures to log lines.

## Helpers

Two helper packages, both in `shared/`:

### HTTP layer — `shared/response/gin_helpers.go`

Logging-aware wrappers around `Err` / `InternalError` / `AbortErr`. Each takes the err and optional structured attrs:

```go
response.Errorf(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid request.", err,
    "op", "bind_job_request", "tool", toolType)

response.InternalErrorf(c, "SERVER_ERROR", "Something went wrong. Please try again.", err,
    "op", "db.processing_jobs.transaction", "jobId", jobID, "tool", toolType)

response.AbortErrorf(c, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "Session expired.", err,
    "op", "verify_token")
```

`*Errorf` automatically attaches `method`, `path`, and `requestId` from gin context. Pass `nil` err to behave exactly like the non-`f` variant (no log emitted).

### Non-HTTP layer — `shared/logger/oplog.go`

For internal helpers, NATS workers, goroutines — anywhere there is no `*gin.Context`:

```go
return logger.LogErr(ctx, "db.processing_jobs.create", err,
    "jobId", id, "tool", toolType)

logger.LogWarn(ctx, "redis.lookup", err, "key", key) // benign / expected
```

`LogErr` and `LogWarn` return the same `err` so they can be used inline in a `return` statement. They auto-attach `requestId` from `ctx` when present (set by `logger.GinRequestID()` middleware). Passing `nil` err is a no-op.

## Required attrs

Every error log line must include:

| Attr | Source | Notes |
|---|---|---|
| `err` | the raw error | always |
| `op` | a stable identifier for the failing operation | e.g. `"create_job_dir"`, `"db.users.create"`, `"redis.upload_chunk_lua"`, `"libreoffice.convert"` |
| `requestId` | gin context / context.Context | populated automatically by helpers |

Plus any local identifiers that help reproduce the failure: `jobId`, `userId`, `uploadId`, `tool`, `path`, `subject`.

## Where logs end up

Each service initialises its own `slog.Default` via `logger.Init("<service>", LOG_MODE)` in `main.go`. In dev (`LOG_MODE=dev`), output is human-readable text on stdout; in prod, JSON. Lines look like:

```json
{"time":"...","level":"ERROR","service":"job-service","msg":"Something went wrong. Please try again.",
 "err":"dial tcp 127.0.0.1:5432: connection refused","code":"SERVER_ERROR","status":500,
 "method":"POST","path":"/api/convert-to-pdf/word-to-pdf","requestId":"81eb81ef-...",
 "op":"db.processing_jobs.transaction","jobId":"...","tool":"word-to-pdf"}
```

## Correlating a user-visible failure to logs

The standard API response envelope includes `meta.requestId`. Take that value and grep:

```sh
# job-service stdout
grep '"requestId":"81eb81ef-491a-4bd3-96b0-7eb8b26d1712"' /var/log/job-service.log
```

The matching `slog.Error` line will name the exact `op` that failed.

## When NOT to log

- An error is already logged at a deeper layer and the caller adds no new context — propagate, don't re-log.
- Truly benign branches that aren't really errors (e.g. `errors.Is(err, gorm.ErrRecordNotFound)` followed by a `Created` response, or `os.IsNotExist` for a file we are about to create).
- Validation filters that intentionally skip non-matching items (e.g. `uuid.Parse` of a directory name to filter only UUID-named subdirs).

If in doubt, log at `Warn` level — it captures the event for telemetry without noise-marking it as a system error.
