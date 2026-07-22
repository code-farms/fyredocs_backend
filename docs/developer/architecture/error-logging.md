# Error Logging Convention

Every backend service follows a single rule: at every `if err != nil { ... }` block where the err is actively handled (returned, retried, swallowed, mapped to a user-visible response, or used to break out of a loop), emit a structured `slog.Error` (or `slog.Warn` for benign/expected errors) at the point of detection.

This document defines the convention, the helper functions, and how to correlate user-visible failures to log lines.

## Error-handling contract (SaaS-grade)

Companion rules to the logging convention above:

- **Envelope + codes.** All errors return the standard `{success:false,message,error:{code,details}}`. `code` comes from the central registry `shared/response/codes.go` (e.g. `CodeInvalidInput`, `CodeUnauthorized="AUTH_UNAUTHORIZED"`, `CodeTokenExpired="AUTH_TOKEN_EXPIRED"`, `CodeForbidden`, `CodeNotFound`, `CodeServerError`, `CodeUpstreamUnavailable`, `CodeServiceUnavailable`) — prefer a constant over a new literal.
- **No internal-detail leak.** The client gets a generic `message`; the real error/stack goes to the server log only. Never put `err.Error()`, DB driver text, or a stack into the response.
- **Panic recovery.** Every Gin service installs `response.GinRecovery()`; the api-gateway wraps its chain in `response.HTTPRecovery`. A panic becomes a logged stack + a clean 500 envelope — never a dropped connection or leaked stack.
- **HTTP status discipline.** validation→400, auth→401 (`AUTH_TOKEN_EXPIRED` when expired so the SPA can silently refresh), forbidden→403, missing→404, conflict→409, too-large→413, rate-limit→429, upstream unreachable→502 (`CodeUpstreamUnavailable`), dependency down→503 (`CodeServiceUnavailable`), unexpected→500.
- **Resilience.** Outbound HTTP clients set timeouts; servers set `ApplyServerTimeouts`; DB carries a server-side `statement_timeout`; the gateway proxy has a 3s dial timeout. A flaky internal dependency is guarded by `shared/circuitbreaker` (fail fast when open) — e.g. document-service→user-service membership checks.
- **Async / jobs.** Workers use bounded retry (`MaxDeliver`+`BackOff`) → DLQ on exhaustion, a per-job timeout, and a panic-recover that moves the job to `failed` (never stuck in `processing`). Event consumers (notification/analytics/document) use the same bounded-retry+DLQ pattern (no hot-loop on a DB outage). Dead-lettered worker jobs can be re-dispatched by a super-admin via `POST /api/jobs/dlq/redrive` (idempotent — redriven messages are removed from the DLQ).

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

`*Errorf` automatically attaches `method` + `path`, and logs via the request context so the shared `contextHandler` adds `request_id`/`trace_id`/`span_id`. Pass `nil` err to behave exactly like the non-`f` variant (no log emitted).

### Non-HTTP layer — `shared/logger/oplog.go`

For internal helpers, NATS workers, goroutines — anywhere there is no `*gin.Context`:

```go
return logger.LogErr(ctx, "db.processing_jobs.create", err,
    "jobId", id, "tool", toolType)

logger.LogWarn(ctx, "redis.lookup", err, "key", key) // benign / expected
```

`LogErr` and `LogWarn` return the same `err` so they can be used inline in a `return` statement. They log via `ctx` (the `*Context` slog variants), so the shared `contextHandler` auto-attaches `request_id` (set by `logger.GinRequestID()`) and `trace_id`/`span_id` (from the OTel span) when present. Passing `nil` err is a no-op.

## Required attrs

Every error log line must include:

| Attr | Source | Notes |
|---|---|---|
| `error` | the raw error | always |
| `op` | a stable identifier for the failing operation | e.g. `"create_job_dir"`, `"db.users.create"`, `"redis.upload_chunk_lua"`, `"libreoffice.convert"` |
| `request_id` | context | injected automatically by the shared `contextHandler` for any `*Context` log |
| `trace_id` / `span_id` | OpenTelemetry span in context | injected automatically by the `contextHandler` — ties the log to its Tempo trace |

Plus any local identifiers that help reproduce the failure: `jobId`, `userId`, `uploadId`, `tool`, `path`, `subject`.

`op` is dot-namespaced by subsystem so logs group cleanly in Loki. Conventions in use:
- `db.<table>.<action>` — database ops (e.g. `db.documents.create`, `db.memberships.update_role`).
- `s3.<action>` / `redis.<action>` / `nats.<action>` — external stores/broker.
- Tool-exec on the worker path: `ghostscript.{compress,repair,pdfa}`, `libreoffice.convert`, `unoconvert.convert`, `pdftoppm.render`, `pdftohtml.convert`, `pdftotext.convert`, `tesseract.ocr_page`.
- Cross-service clients: `orgclient.membership`, `notify.post`. Gateway: `gateway.proxy`.
- Worker/async lifecycle: `worker.process_panic`, `export.generate`, `apisampler.panic`.

## Where logs end up

Each service initialises its own `slog.Default` via `logger.Init("<service>", LOG_MODE)` in `main.go`. In dev (`LOG_MODE=dev`), output is human-readable text on stdout; in prod, JSON. Lines look like:

```json
{"time":"...","level":"ERROR","service":"job-service","msg":"Something went wrong. Please try again.",
 "error":"dial tcp 127.0.0.1:5432: connection refused","code":"SERVER_ERROR","status":500,
 "method":"POST","path":"/api/convert-to-pdf/word-to-pdf",
 "request_id":"81eb81ef-...","trace_id":"4bf92f...","span_id":"00f067...",
 "op":"db.processing_jobs.transaction","jobId":"...","tool":"word-to-pdf"}
```

Timestamps are UTC RFC3339; sensitive keys (`password`, `token`, `authorization`,
`secret`, …) are auto-redacted to `[REDACTED]` by the shared logger.

## Correlating a user-visible failure to logs

The standard API response envelope includes `meta.requestId`, and the gateway
propagates the same `request_id` + trace downstream, so one request is a single
thread across every service. In **Grafana → Explore → Loki**:

```logql
{service="job-service"} | json | request_id = "81eb81ef-491a-4bd3-96b0-7eb8b26d1712"
```

Drop the `service` label to see the same request across **all** services. The
matching line names the exact `op` that failed, and its `trace_id` derived-field
links straight to the full **Tempo** trace (and back). Fallback for a single host
without the observability stack running: `docker logs <container> | grep <request_id>`.

## When NOT to log

- An error is already logged at a deeper layer and the caller adds no new context — propagate, don't re-log.
- Truly benign branches that aren't really errors (e.g. `errors.Is(err, gorm.ErrRecordNotFound)` followed by a `Created` response, or `os.IsNotExist` for a file we are about to create).
- Validation filters that intentionally skip non-matching items (e.g. `uuid.Parse` of a directory name to filter only UUID-named subdirs).

If in doubt, log at `Warn` level — it captures the event for telemetry without noise-marking it as a system error.
