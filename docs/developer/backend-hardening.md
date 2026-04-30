# Backend Hardening — Review & Implementation Record

**Date:** 2026-03-19
**Scope:** Full backend architecture, security, reliability, and operational review

---

## 1. Overall Assessment

The Fyredocs backend is a well-architected microservices system. Clean service boundaries, solid observability (OpenTelemetry + Prometheus + structured logging), proper auth flow with token denylist, and production-aware Docker setup. This document records the hardening review, what was implemented, and what remains planned.

---

## 2. What Was Already Done Well

- Clean microservice boundaries with shared utilities (not shared business logic)
- Proper chunked upload implementation with progress tracking
- Redis-backed rate limiting using atomic Lua scripts
- Comprehensive observability stack (OpenTelemetry traces, Prometheus metrics, slog structured logs)
- Docker multi-stage builds producing minimal scratch images (~10-20MB)
- Token denylist for immediate logout revocation
- Plan-based resource limits enforced at gateway level
- Thorough documentation, OpenAPI specs, and Mermaid diagrams
- Dangerous JWT secret detection on startup
- Guest session support with proper TTL management
- NATS JetStream for reliable job dispatch with exactly-once semantics
- Graceful shutdown already present on auth-service, job-service, and all worker services
- Worker retry backoff already configured via NATS consumer `BackOff: []time.Duration{10s, 30s, 2m}` and `NakWithDelay`
- Pagination max limit already enforced via `clampInt(queryInt(c, "limit", 25), 1, 100)`

---

## 3. Implemented Changes

### 3.1 Security Headers Middleware `Critical`

**Finding:** The API gateway returned responses with no browser-security headers, leaving the application vulnerable to MIME sniffing attacks, clickjacking, and XSS amplification.

**Why:** These headers are a baseline requirement for any web-facing service and cost nothing at runtime. Without `X-Content-Type-Options: nosniff`, browsers may interpret uploaded files as executable content. Without `X-Frame-Options: DENY`, attackers can embed the app in a hidden iframe.

**What changed:**
- Added `withSecurityHeaders()` middleware in the gateway handler chain
- Every response now includes: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`

**Files:** `api-gateway/main.go`

---

### 3.2 MIME-Type Validation on File Uploads `Critical`

**Finding:** Only file extensions were validated, not actual file content. An attacker could upload a malicious binary renamed to `.pdf`.

**Why:** `http.DetectContentType()` reads the first 512 bytes of a file and identifies the real content type based on magic bytes, making extension-only attacks ineffective. Office formats (`.docx`, `.xlsx`, `.pptx`) are ZIP archives internally, so `application/zip` is accepted for those categories.

**What changed:**
- Added `allowedMIMETypes` map defining expected MIME types per tool category (pdf, word, excel, ppt, image)
- Added `mimeCategory()` helper to map tool types to their expected MIME category
- Added `validateMIMEType()` that reads the first 512 bytes and checks against the allowlist
- Called after every file save — both multipart direct uploads and chunked upload consumption paths

**Files:** `job-service/handlers/jobs.go`

---

### 3.3 JWT Token ID (JTI) Claim `Critical`

**Finding:** Tokens were issued without a unique identifier (`jti` claim). The denylist had to store full token strings, individual tokens couldn't be audited, and replay detection was impossible.

**Why:** Adding a UUID as the JTI is a JWT best practice defined in RFC 7519 Section 4.1.7. It enables efficient denylist matching by ID and per-token audit trails.

**What changed:**
- Every issued access token now includes `claims.ID = uuid.NewString()` — a unique v4 UUID per token
- The JTI is set as part of `RegisteredClaims` so it appears in the standard `jti` field

**Files:** `auth-service/internal/token/issuer.go`, `auth-service/internal/token/issuer_test.go`

---

### 3.4 Enforce JWT Issuer and Audience as Required `Critical`

**Finding:** `JWT_ISSUER` and `JWT_AUDIENCE` were optional. If unset, tokens were issued without these claims and the verifier would skip validation. Any HS256 token signed with the same secret — even from a different application — would be accepted.

**Why:** By making both required, the system enforces token provenance: only tokens issued by `fyredocs` for the `fyredocs-api` audience are valid. The service fails fast on startup if not configured.

**What changed:**
- `NewIssuerFromEnv()` returns an error if `JWT_ISSUER` or `JWT_AUDIENCE` is empty
- `IssueAccessToken()` always sets `Issuer` and `Audience` unconditionally

**Files:** `auth-service/internal/token/issuer.go`, `auth-service/internal/token/issuer_test.go`

---

### 3.5 API Gateway Graceful Shutdown `High`

**Finding:** The gateway used `http.ListenAndServe()` which blocks until killed. During deployments, in-flight requests (especially file uploads) were terminated mid-stream.

**Why:** Graceful shutdown catches SIGTERM/SIGINT, stops accepting new connections, waits up to 30 seconds for in-flight requests to complete, then exits cleanly. All other services already had this — the gateway was the only one missing it.

**What changed:**
- Replaced `http.ListenAndServe()` with `http.Server` + `srv.Shutdown(ctx)`
- Added signal handling for SIGTERM and SIGINT with a 30-second drain timeout
- Redis client is closed after server shutdown

**Files:** `api-gateway/main.go`

---

### 3.6 Dead Letter Queue (DLQ) for Failed Jobs `High`

**Finding:** When a NATS message failed processing after all retries (MaxDeliver=4), it was simply acked and logged. The message was gone forever with no way to investigate or replay.

**Why:** In production, permanently failed jobs need to be inspectable for debugging (corrupt file? tool crash? OOM?) and replayable once the root cause is fixed.

**What changed:**
- Added `JOBS_DLQ` stream in NATS JetStream (7-day retention, `jobs.dlq.>` subjects)
- In every worker's `handleFailure()`, when retries are exhausted, the failed payload is published to `jobs.dlq.<serviceName>` before acking the original message
- The DLQ payload includes original job data + `EventType: "JobFailed"` + delivery count

**Files:** `shared/natsconn/natsconn.go`, `convert-from-pdf/internal/worker/worker.go`, `convert-to-pdf/internal/worker/worker.go`, `organize-pdf/internal/worker/worker.go`, `optimize-pdf/internal/worker/worker.go`

---

### 3.7 Gateway Proxy Timeouts `High`

**Finding:** The reverse proxy used Go's default `http.Transport` with no explicit timeouts. If a downstream service became slow or hung, the gateway would hold connections open indefinitely, leading to connection exhaustion and cascading failures.

**Why:** Explicit timeouts prevent a single slow service from taking down the entire gateway.

**What changed:**
- Added a shared `proxyTransport` with:
  - `ResponseHeaderTimeout: 5 minutes` (allows long PDF conversions)
  - `IdleConnTimeout: 90 seconds`
  - `MaxIdleConnsPerHost: 20`
  - `MaxIdleConns: 100`
- All reverse proxies use this transport

**Files:** `api-gateway/main.go`

---

### 3.8 Readiness Probes (/readyz) `Medium`

**Finding:** `/healthz` checked if the process was alive and Redis reachable, but didn't distinguish between "alive" and "ready to serve traffic." A service could be alive but not ready (e.g., still connecting to DB).

**Why:** Without readiness probes, load balancers route traffic to services that can't handle it yet, causing request failures during startup or dependency outages.

**What changed:**
- Added `/readyz` endpoint to all 6 HTTP services
- Each checks ALL dependencies: **auth-service** (PostgreSQL + Redis), **job-service** (PostgreSQL + Redis + NATS), **workers** (PostgreSQL + Redis + NATS)
- Returns `200 {"status": "ready", "checks": {...}}` or `503 {"status": "not ready", "checks": {...}}`
- `/healthz` remains as a lightweight liveness check

**Files:** `job-service/routes/upload_routes.go`, `auth-service/routes/routes.go`, `convert-from-pdf/main.go`, `convert-to-pdf/main.go`, `organize-pdf/main.go`, `optimize-pdf/main.go`

---

### 3.9 Redis Health Check Password Leak Fix `Medium`

**Finding:** Docker-compose Redis healthcheck used `redis-cli -a ${REDIS_PASSWORD}`, exposing the password in process listings, `docker inspect`, and health check logs.

**Why:** Credential exposure via command-line arguments is a common misconfiguration. The `REDISCLI_AUTH` environment variable achieves the same result without exposure.

**What changed:**
- Switched from `["CMD", "redis-cli", "-a", "${REDIS_PASSWORD}", "ping"]` to `["CMD-SHELL", "REDISCLI_AUTH=$REDIS_PASSWORD redis-cli ping"]`

**Files:** `docker-compose.yml`

---

### 3.10 Request Body Size Limit at Gateway `Medium`

**Finding:** The gateway proxied raw request bodies with no size limit. JSON endpoints like `/auth/login` had no protection — an attacker could send a multi-GB body.

**Why:** Upload endpoints have their own limits, but JSON endpoints were unprotected. A 1MB cap for non-upload routes prevents memory exhaustion attacks.

**What changed:**
- Added `withMaxBodySize()` middleware wrapping `http.MaxBytesReader`
- Applied to all non-upload routes with a 1MB limit
- Upload routes (`/api/upload/*`) excluded — they handle their own size limits

**Files:** `api-gateway/main.go`

---

### 3.11 Upload State Atomicity (Lua Script) `Medium`

**Finding:** `UploadChunk` had a TOCTOU race: fetch state from Redis, save chunk to disk, then update Redis. Between steps, the upload could expire or be modified by a concurrent request.

**Why:** The rate limiter in the same codebase already solved this pattern using a Lua script. The same approach ensures atomicity for upload state management.

**What changed:**
- Added `uploadChunkLua` — a Redis Lua script that atomically: checks upload exists, records chunk index (SADD), refreshes TTLs, and returns state + received count in one round-trip
- `UploadChunk` now saves the chunk file first, then runs the Lua script
- Read-only functions (`GetUploadStatus`, `CompleteUpload`) still use the non-atomic `fetchUploadState`

**Files:** `job-service/handlers/uploads.go`

---

### 3.12 Cleanup Worker Distributed Lock `Medium`

**Finding:** Multiple cleanup-worker replicas would compete to delete the same expired jobs, causing unnecessary DB load and race conditions.

**Why:** A distributed lock ensures only one replica runs cleanup at a time, while still allowing multiple replicas for high availability.

**What changed:**
- Added Redis `SetNX` lock at the start of `runCleanup()`
- Lock key: `cleanup-worker:lock`, TTL: 10 minutes
- If lock is held, the instance skips the cycle (debug log)
- Lock released via `defer Del` after cleanup; auto-expires if worker crashes

**Files:** `cleanup-worker/main.go`

---

### 3.13 Server-Sent Events (SSE) for Job Status `Design`

**Finding:** Clients had to poll `GET /api/{tool}/:id` repeatedly to check job status. The backend already published lifecycle events to NATS but only consumed them internally.

**Why:** SSE reduces server load (no repeated DB queries), client complexity (no polling intervals), and latency (instant notification vs. polling delay).

**What changed:**
- Added `GET /api/jobs/:id/events` SSE endpoint in job-service
- Creates an ephemeral NATS consumer per connection, filtered by job ID
- Event types: `connected`, `job-update`, `done`, `error`
- Auto-closes when job completes/fails; 5-minute timeout; 15-second keepalive
- `X-Accel-Buffering: no` disables nginx response buffering

**Files:** `job-service/handlers/sse.go` (new), `job-service/routes/upload_routes.go`

---

### 3.14 Idempotency Keys for Job Creation `Design`

**Finding:** If a client retried a job creation request after a network timeout, a duplicate job was created, wasting compute and confusing the UI.

**Why:** Idempotency keys (used by Stripe, AWS, etc.) let the client send a unique key with the request. If already processed, the server returns the original response. Fully backward compatible — clients that don't send the header are unaffected.

**What changed:**
- `CreateJobFromTool` checks for `Idempotency-Key` header
- Looks up `idempotency:{key}` in Redis; if found, returns the existing job
- After successful creation, stores `idempotency:{key} = {jobID}` with 10-minute TTL

**Files:** `job-service/handlers/jobs.go`

---

### 3.15 Structured Error Codes in Workers `Design`

**Finding:** Worker failures were stored as free-text strings, making it impossible to build alerting dashboards, show meaningful user messages, or analyze failure trends.

**Why:** Structured codes enable all three by prefixing failure reasons with a machine-readable code.

**What changed:**
- Added error code constants: `UNSUPPORTED_TOOL`, `CONVERSION_FAILED`, `INVALID_PAYLOAD`, `OUTPUT_FAILED`, `TIMEOUT`
- Added `classifyError()` function (timeout detection, default to conversion failure)
- Failure reasons now follow format: `[ERROR_CODE] human-readable message`

**Files:** `convert-from-pdf/internal/worker/worker.go`, `convert-to-pdf/internal/worker/worker.go`, `organize-pdf/internal/worker/worker.go`, `optimize-pdf/internal/worker/worker.go`

---

### 3.16 Tool Validation Map Deduplication `Low`

**Finding:** `convertFromTools` and `convertToTools` maps duplicated information in `routing.ToolServiceMap`. Adding a tool required changes in three places.

**What changed:** Removed duplicate maps; validation now uses `routing.ServiceForTool(toolType) == ""` as single source of truth.

**Files:** `job-service/handlers/jobs.go`

---

### 3.17 Unused Import and Dead Code Cleanup `Low`

**Finding:** `var _ redis.Client` in gateway — a blank identifier suppressing an unused import from a prior refactor.

**What changed:** Removed the dead code and unused `redis` import.

**Files:** `api-gateway/main.go`

---

### 3.18 CORS Wildcard + Credentials Warning `Low`

**Finding:** When `CORS_ALLOW_ORIGINS=*` and `CORS_ALLOW_CREDENTIALS=true`, the gateway echoes back the request's Origin, effectively disabling CORS protection.

**What changed:** Added a startup `slog.Warn` when this combination is detected. Valid for local dev, dangerous in production.

**Files:** `api-gateway/main.go`

---

### 3.19 Routing Test Fix `Low`

**Finding:** `routing_test.go` expected `split-pdf` and `compress-pdf` to map to `convert-to-pdf`, but they correctly map to `organize-pdf` and `optimize-pdf`. Pre-existing bug in the test.

**What changed:** Fixed expected values to match actual routing configuration.

**Files:** `job-service/internal/routing/routing_test.go`

---

## 4. Planned Changes (Not Yet Implemented)

### 4.1 Database Migrations Tool

**Goal:** Replace GORM `AutoMigrate` with versioned, reversible migrations.

**Why deferred:** Requires generating initial SQL from the current schema, testing the migration runner against a live DB, and coordinating the cutover from AutoMigrate. Risk of schema mismatch if done without a running DB to validate against.

**Recommended tool:** `golang-migrate/migrate`

**Steps:**

1. Add dependency to each service with a database:
   ```bash
   go get -u github.com/golang-migrate/migrate/v4
   go get -u github.com/golang-migrate/migrate/v4/database/postgres
   go get -u github.com/golang-migrate/migrate/v4/source/file
   ```

2. Create migration directories per service:
   ```
   auth-service/migrations/
     000001_create_users_table.up.sql / .down.sql
     000002_create_auth_metadata_table.up.sql / .down.sql
     000003_create_subscription_plans_table.up.sql / .down.sql
   job-service/migrations/
     000001_create_processing_jobs_table.up.sql / .down.sql
     000002_create_file_metadata_table.up.sql / .down.sql
   ```

3. Generate initial migrations from current GORM models via `pg_dump --schema-only`.

4. Add migration runner to each service's startup:
   ```go
   func runMigrations(databaseURL string) error {
       m, err := migrate.New("file://migrations", databaseURL)
       if err != nil { return fmt.Errorf("migration init: %w", err) }
       if err := m.Up(); err != nil && err != migrate.ErrNoChange {
           return fmt.Errorf("migration run: %w", err)
       }
       return nil
   }
   ```

5. Replace `models.Migrate()` calls with `runMigrations()`.

6. Add migration files to Docker images in Dockerfiles.

7. Add Makefile targets for `migrate-up`, `migrate-down`, `migrate-create`.

**Risks:** Initial migration must match current schema exactly. GORM AutoMigrate and golang-migrate should not run simultaneously.

---

### 4.2 Separate Database Per Service

**Goal:** Give auth-service and job-service their own PostgreSQL databases.

**Why deferred:** Requires data migration, new credentials, init scripts, and coordination with the migrations strategy. Best done after migrations tooling is in place.

**Steps:**

1. Create `infra/init-db.sql` with `CREATE DATABASE fyredocs_auth` / `fyredocs_jobs` and per-service users.
2. Mount in docker-compose.yml via `/docker-entrypoint-initdb.d/`.
3. Add `AUTH_DATABASE_URL` and `JOBS_DATABASE_URL` environment variables.
4. Update each service to use its own URL.
5. One-time data migration via `pg_dump` / `psql`.

**Risks:** Requires downtime or blue-green deploy for data migration. `init-db.sql` only runs on first PostgreSQL startup.

---

### 4.3 Parallel Docker Builds in deploy.sh

**Goal:** Speed up deployment by building all services concurrently.

**Why deferred:** Simple change but needs testing on the target server to verify memory is sufficient.

**Steps:** Replace the sequential `for SERVICE in ... docker compose build "$SERVICE"` loop with:
```bash
docker compose build "${GO_SERVICES[@]}"
```

Docker BuildKit manages CPU/memory internally. On low-memory machines (< 4GB), sequential builds may be safer.

---

### 4.4 Separate Upload and Processing Stages

**Goal:** Decouple file upload from job creation for better error recovery and reuse.

**Why deferred:** Architectural change affecting the API contract. Requires frontend coordination and backward compatibility planning.

**Proposed flow:**
```
POST /api/uploads/init          -> initialize upload session (existing)
PUT  /api/uploads/:id/chunk     -> upload chunks (existing)
POST /api/uploads/:id/complete  -> finalize upload (existing)
POST /api/jobs                  -> create job from completed upload(s) (NEW)
```

Benefits: failed dispatch doesn't lose uploads, same file can be processed with different tools, cleaner API contract.

---

## 5. Summary

| Priority | Item | Status |
|----------|------|--------|
| Critical | Security headers | Done (3.1) |
| Critical | MIME-type validation | Done (3.2) |
| Critical | JTI claim in JWT | Done (3.3) |
| Critical | Enforce JWT issuer/audience | Done (3.4) |
| High | Graceful shutdown (gateway) | Done (3.5) |
| High | Dead letter queue | Done (3.6) |
| High | Gateway proxy timeouts | Done (3.7) |
| High | Database migrations tool | Planned (4.1) |
| High | Separate DB per service | Planned (4.2) |
| Medium | Readiness probes | Done (3.8) |
| Medium | Redis healthcheck password | Done (3.9) |
| Medium | Request body size limit | Done (3.10) |
| Medium | Upload atomicity (Lua) | Done (3.11) |
| Medium | Cleanup distributed lock | Done (3.12) |
| Medium | Parallel Docker builds | Planned (4.3) |
| Design | SSE for job status | Done (3.13) |
| Design | Idempotency keys | Done (3.14) |
| Design | Structured error codes | Done (3.15) |
| Design | Separate upload/processing | Planned (4.4) |
| Low | Tool validation dedup | Done (3.16) |
| Low | Unused import cleanup | Done (3.17) |
| Low | CORS wildcard warning | Done (3.18) |
| Low | Routing test fix | Done (3.19) |
