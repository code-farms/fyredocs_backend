# Job Service

## Overview

The Job Service is the central orchestration service for all file processing operations in Fyredocs. It manages the full job lifecycle: brokering presigned S3 multipart uploads (file bytes go browser → MinIO/S3 directly, never through this service), creating processing jobs, dispatching work to downstream worker services via NATS JetStream, tracking job status, and redirecting downloads to short-lived presigned URLs. It also handles guest user job tracking through Redis.

**Port**: 8081
**Type**: REST API (Job Orchestrator)
**Framework**: Gin (Go)
**Database**: PostgreSQL (via GORM)
**Queue**: NATS JetStream
**Cache**: Redis
**Object Storage**: MinIO / AWS S3 / R2 (via `shared/storage`)

## Service Responsibility

1. **Presigned Multipart Uploads** -- Create S3 multipart upload sessions, presign part URLs, re-presign for resume, finalize and size-verify completed uploads
2. **Job Creation** -- Validate tool types, create processing jobs in PostgreSQL, dispatch events to NATS
3. **Job Querying** -- List jobs by tool type with pagination, retrieve individual job details
4. **Job Deletion** -- Delete jobs from the database and their objects from the uploads/outputs buckets
5. **File Download** -- 302-redirect to a presigned GET URL with attachment Content-Disposition and the computed Content-Type
6. **Job History** -- Return full job history for authenticated users
7. **Guest Job Tracking** -- Manage guest tokens in Redis to allow guest users to track their jobs
8. **Tool-to-Service Routing** -- Use a centralized `ToolServiceMap` to dispatch jobs to the correct worker service
9. **MIME-Type Validation** -- Validate uploaded file content types using `http.DetectContentType()` over the object's first 512 bytes (read via a ranged GET) against an allowlist per tool category (pdf, word, excel, ppt, image)
10. **Per-Tool Options Validation** -- Validate tool-specific `options` payloads against a schema at the public boundary (currently `scan-to-pdf`); rejections return 400 `INVALID_OPTIONS`
11. **Edge Detection Relay** -- `POST /api/organize-pdf/detect-edges` peeks at a completed image upload (without consuming it) and synchronously relays detection to organize-pdf's internal endpoint for the mobile scanner UI

## Presigned Upload Protocol

File bytes never stream through the job-service. The browser talks to object storage directly using presigned URLs signed against `S3_PUBLIC_ENDPOINT` (typically the Caddy edge / public MinIO origin):

1. `POST /api/uploads/init` `{fileName, fileSize, contentType}` — the plan size limit (`X-User-Plan-Max-File-MB`) is enforced on the declared size **before** any URL is issued. The service creates an S3 multipart upload at key `uploads/{uploadId}/{sanitizedFileName}` in the uploads bucket and returns `{uploadId, key, partSize, totalParts, urlExpiresAt, parts:[{partNumber,url}]}`. `partSize` comes from `UPLOAD_PART_SIZE_MB` (default 8 MiB, clamped to the S3 5 MiB minimum); `totalParts = ceil(fileSize/partSize)` and is capped at 1000.
2. The browser `PUT`s each part directly to the presigned URL and collects the returned `ETag`s.
3. `GET /api/uploads/:uploadId/parts?partNumbers=2,3` — re-presigns part URLs (resume after interruption or URL expiry); all parts when `partNumbers` is omitted.
4. `POST /api/uploads/:uploadId/complete` `{parts:[{partNumber,etag}]}` — completes the multipart upload, then `StatObject`s the result and re-verifies the **true** size against the plan limit (the declared size at init is client-supplied). Oversized objects are removed and the request gets 413.
5. `POST /api/{category}/:tool` `{uploadId}` — job creation consumes the uploaded object in place (no copy): existence + size via `StatObject`, MIME sniff via a ranged GET of the first 512 bytes.
6. `DELETE /api/uploads/:uploadId` — aborts the S3 multipart upload and clears the session (idempotent, 204).

`PUT /api/uploads/:uploadId/chunk` is a one-release migration stub for the retired chunk-streaming protocol: it always answers `410 UPLOAD_PROTOCOL_CHANGED` ("Please refresh the page to continue uploading.") so stale frontend bundles reload into the new flow. Remove it (route + handler) next release.
10. **Idempotency** -- Two layers, both within a 10-minute window:
    - `Idempotency-Key` header on job creation: returns the original job for the same key.
    - Same-`uploadId` replay: a duplicate `POST` with the same `uploadId(s)` after a successful submit returns the original job instead of `INVALID_INPUT: upload not found`. The mapping `idempotency:upload:<uploadId>` → `<jobId>` is written in Redis right before `releaseUpload` clears the upload state. Multi-upload jobs only dedupe when **all** `uploadId`s map to the same `jobId`. This makes the API self-healing against client double-submits regardless of cause (network retry, second tab, etc.).

## Design Constraints

- **Microservice boundary**: The job-service owns all job CRUD, upload management, and tool dispatch logic. It does NOT perform file conversions itself.
- **No cross-service DB access**: Worker services receive jobs via NATS events and update status in their own database replicas.
- **Stateless HTTP layer**: All session/upload state is stored in Redis; the service can scale horizontally.
- **JWT authentication**: All routes (except `/healthz` and `/metrics`) pass through auth middleware that validates JWT tokens or guest tokens.
- **NATS JetStream**: All job dispatch uses NATS JetStream for durable, at-least-once delivery to worker services.

## Internal Architecture

```
Client ──(presigned PUT/GET, file bytes)──> MinIO / S3
  |
API Gateway :8080
  |
Job Service :8081  (control plane only — no file bytes)
  |--- Upload Handlers (init, parts, complete, status, abort, chunk[410 stub])
  |--- Job Handlers (create, list, get, delete, download→302, history)
  |--- Auth Middleware (JWT / Guest Token)
  |--- PostgreSQL (processing_jobs, file_metadata)
  |--- Redis (upload sessions, guest job sets, rate limits)
  |--- MinIO/S3 (uploads + outputs buckets, presigned URLs)
  |--- NATS JetStream (job dispatch to workers)
       |
       +---> convert-from-pdf
       +---> convert-to-pdf
       +---> organize-pdf
       +---> optimize-pdf
```

### Key Internal Packages

| Package | Purpose |
|---------|---------|
| `handlers/jobs.go` | Job CRUD, presigned download redirect, job history |
| `handlers/sse.go` | SSE endpoint for real-time job status updates via NATS JetStream |
| `handlers/uploads.go` | Presigned multipart upload protocol (init, parts, complete, status, abort, 410 chunk stub) |
| `handlers/tool_options.go` | Per-tool options schema validation (`validateToolOptions`; currently scan-to-pdf) |
| `handlers/detect.go` | `DetectEdges` — synchronous relay to organize-pdf `/internal/v1/detect-edges` |
| `handlers/storage.go` | `ObjectStore` interface (narrow slice of `shared/storage.Client`) + boot-time injection |
| `handlers/auth.go` | Extract authenticated user ID from auth context |
| `internal/routing/routing.go` | `ToolServiceMap` -- single source of truth for tool-to-service mapping |
| `internal/models/` | GORM models for `ProcessingJob` and `FileMetadata` |
| `internal/cleanup/` | Sweep logic for the in-process cleanup loop (expired jobs, upload state, stale multiparts, backfill) |
| `routes/upload_routes.go` | Gin route registration with rate limiting |
| `middleware/` | Rate limiting middleware |

JWT verification middleware is imported from the shared `fyredocs/shared/authverify` package (replacing the old `internal/authverify` local copy).

### Background Cleanup Loop

The TTL sweep runs **in-process** inside the API binary (started from `main.go` as a background goroutine, sweep logic in `internal/cleanup/`). It was previously a second binary shipped as the separate `cleanup-worker` container; it was folded in to keep a strict 1 service = 1 container mapping — job-service owns all the data the sweep touches, so no service boundary is crossed either way.

Every `CLEANUP_INTERVAL` (compose sets 5m; code fallback 15m), under a Redis `SETNX` lock (`cleanup-worker:lock`, 10-min TTL, so at most one sweeper runs per tick even with multiple replicas):

1. **Expired job cleanup** — delete `processing_jobs` rows past `expires_at` (batch 100) and `RemoveObject` their input/output objects; TTLs per plan: `GUEST_JOB_TTL` 30m / `FREE_JOB_TTL` 7d / `PRO_JOB_TTL` 30d.
2. **Expired upload-session reap** — Redis `upload:*` state older than 2 × `UPLOAD_TTL`: `DEL` state, `AbortMultipart`, `RemoveObject` if the object was never consumed by a job.
3. **Stale multipart abort** — incomplete multipart uploads older than `MULTIPART_ABORT_TTL` (24h). This is the sole reclaim mechanism; there are **no bucket lifecycle rules**.
4. **Expiry backfill** — set `expires_at = created_at + FREE_JOB_TTL` on legacy authenticated jobs where it is `NULL`.

Operational notes:
- `CLEANUP_ENABLED=false` opts a replica out of sweeping entirely (the Redis lock already dedupes; the flag is the explicit off-switch for extra replicas or local runs).
- Idempotent by design: a missing object or unknown multipart upload counts as success; anything missed in one cycle is retried on the next.
- Fail-safe delete rule: an upload object is only removed if provably unreferenced by `file_metadata`; on a DB error it is kept.
- `file_metadata.path` values starting with `/` are pre-object-storage legacy filesystem paths — skipped and logged; migrate with `scripts/migrate-files-to-minio.sh`.
- Logs/metrics ship with the job-service container (`docker compose logs -f job-service`, grep `cleanup`); sweep activity appears in job-service's `/metrics`.

Retention TTL fallbacks (`GUEST_JOB_TTL` 30m, `FREE_JOB_TTL` 7d, `PRO_JOB_TTL` 30d, `UPLOAD_TTL` 30m, `CLEANUP_INTERVAL` 15m, `MULTIPART_ABORT_TTL` 24h, `MAX_UPLOAD_MB` 500, guest plan `GUEST_MAX_FILE_MB` 20 / `GUEST_MAX_FILES` 5) live once in `shared/config/defaults.go`, read by both the handlers and the sweep, so the values cannot drift.

### ToolServiceMap (routing.go)

The `routing.go` file contains the centralized mapping from tool types to their processing service names. This is the single source of truth for queue routing:

| Service | Tools |
|---------|-------|
| `convert-from-pdf` | pdf-to-word, pdf-to-docx, pdf-to-excel, pdf-to-xlsx, pdf-to-powerpoint, pdf-to-ppt, pdf-to-pptx, pdf-to-image, pdf-to-img, pdf-to-html, pdf-to-text, pdf-to-txt, pdf-to-pdfa, pdf-to-odt, pdf-to-ods, pdf-to-odp |
| `convert-to-pdf` | word-to-pdf, excel-to-pdf, powerpoint-to-pdf, ppt-to-pdf, html-to-pdf, image-to-pdf, img-to-pdf |
| `organize-pdf` | merge-pdf, split-pdf, rotate-pdf, remove-pages, extract-pages, organize-pdf, scan-to-pdf, watermark-pdf, protect-pdf, unlock-pdf, sign-pdf, edit-pdf, add-page-numbers |
| `optimize-pdf` | compress-pdf, repair-pdf, ocr-pdf |

The mapping in `routing.go` is the only authoritative source — these tables in docs are kept in sync, but **always cross-check against `internal/routing/routing.go` and each worker's `main.go` `AllowedTools` if there is any doubt.**

## Routes

### Upload Endpoints

All upload endpoints share the `RATE_LIMIT_UPLOAD` per-IP rate limiter.

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/api/uploads/init` | `InitUpload` | Create an S3 multipart upload; returns presigned part URLs (201) |
| GET | `/api/uploads/:uploadId/parts?partNumbers=2,3` | `GetUploadParts` | Re-presign part URLs (resume / refresh); all parts when omitted |
| POST | `/api/uploads/:uploadId/complete` | `CompleteUpload` | Complete the multipart upload from client-collected ETags; verifies true size |
| GET | `/api/uploads/:uploadId/status` | `GetUploadStatus` | Session metadata (fileName, declaredSize, totalParts) |
| DELETE | `/api/uploads/:uploadId` | `AbortUpload` | Abort the multipart upload + clear session (idempotent, 204) |
| PUT | `/api/uploads/:uploadId/chunk` | `UploadChunk` | **Retired protocol stub** — always 410 `UPLOAD_PROTOCOL_CHANGED` |

### Job Endpoints (per tool category)

These are registered under `/api/convert-from-pdf`, `/api/convert-to-pdf`, `/api/organize-pdf`, and `/api/optimize-pdf`:

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/api/{category}/:tool` | `CreateJobFromTool` | Create a processing job (per-IP `RATE_LIMIT_JOB_CREATE` limiter) |
| GET | `/api/{category}/:tool` | `GetJobsByTool` | List jobs by tool (paginated) |
| GET | `/api/{category}/:tool/:id` | `GetJobByID` | Get single job details |
| DELETE | `/api/{category}/:tool/:id` | `DeleteJobByID` | Delete job rows and remove its objects from the buckets |
| GET | `/api/{category}/:tool/:id/download` | `DownloadJobFile` | 302 redirect to a 5-minute presigned GET URL (attachment disposition) |

`DownloadJobFile` performs the usual ownership checks (user ID or guest token) and the `status == completed` check **before** presigning. The Location URL carries `response-content-disposition: attachment; filename=...` (built with `mime.FormatMediaType`) and `response-content-type` overrides so object storage serves the file with the correct headers. Jobs whose output predates the S3 migration (FileMetadata.Path is an absolute disk path starting with `/`) return 404 "download link has expired".

Where `{category}` is one of: `convert-from-pdf`, `convert-to-pdf`, `organize-pdf`, `optimize-pdf`.

### Detect Edges (organize-pdf group only)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/api/organize-pdf/detect-edges` | `DetectEdges` | Detect the document quad in an uploaded photo for the scanner UI (per-IP `RATE_LIMIT_DETECT` limiter, default 60/window) |

Registered in `routes/upload_routes.go` with its own IP rate limiter (`ratelimit:detect`). Auth is the same as the other tool routes (JWT or guest token via the shared middleware). Request body: `{"uploadId": "..."}` — the id of a **completed** presigned upload that sniffs as an image (PDF uploads are rejected with 400). The handler **peeks** at the upload without consuming it (no `releaseUpload`), so the same `uploadId` remains valid for the subsequent scan-to-pdf job creation, then synchronously relays `{key}` to organize-pdf's `POST /internal/v1/detect-edges` (base URL `ORGANIZE_PDF_URL`, 15s timeout, `X-Internal-Token` header when `INTERNAL_API_TOKEN` is set).

**Success (200):**
```json
{
  "success": true,
  "message": "Edges detected",
  "data": {
    "corners": {"tl":{"x":0.04,"y":0.06},"tr":{"x":0.97,"y":0.05},"br":{"x":0.96,"y":0.95},"bl":{"x":0.05,"y":0.94}},
    "confidence": 0.83,
    "width": 3024,
    "height": 4032
  }
}
```
Corners are normalized `0..1` against the decoded pixel grid. When no document is found, organize-pdf returns full-image corners with `confidence: 0` — still a 200.

**Errors:**

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing/unknown `uploadId`, or the upload is a PDF |
| relayed upstream code (e.g. `UNDECODABLE_IMAGE`, `IMAGE_TOO_LARGE`) | 422 | organize-pdf rejected the image (4xx relayed through) |
| `DETECTION_UNAVAILABLE` | 502 | organize-pdf down, 5xx, or 15s timeout |
| `RATE_LIMIT_EXCEEDED` | 429 | More than `RATE_LIMIT_DETECT` requests per IP per window |

### Per-Tool Options Validation

`CreateJobFromTool` calls `validateToolOptions(toolType, options)` (`handlers/tool_options.go`) **before** `consumeUpload`. Options were historically an opaque passthrough; scan-to-pdf is the first tool with a schema'd payload. Tools without a schema (and empty options) pass through unchanged; job-service owns its own copy of the shape — no shared DTOs across services. Workers still re-clamp defensively on their side (a bad enum never fails a job downstream).

**scan-to-pdf schema** (all fields optional; unknown fields ignored for forward compatibility; string enums case-insensitive):

| Field | Rule |
|-------|------|
| `ocr` | boolean |
| `language` | whitelist: `eng`, `deu`, `fra`, `spa`, `hin` |
| `pageSize` | whitelist: `""`, `auto`, `a4`, `letter`, `id` |
| `enhance` | whitelist: `""`, `none`, `grayscale`, `bw`, `color-boost` |
| `pages` | max 50 entries (index-aligned with `uploadIds`) |
| `pages[i].rotation` | one of `0`, `90`, `180`, `270` |
| `pages[i].corners` | all four of `tl`/`tr`/`br`/`bl` or omitted; each with numeric `x`,`y` in `[0, 1]` |

Any violation (including unparseable JSON) is rejected with 400 `INVALID_OPTIONS`.

### Job API Response

All job endpoints (`CreateJobFromTool`, `GetJobByID`, `GetJobsByTool`, `GetJobHistory`) return a `jobResponse` wrapper that includes the original `ProcessingJob` fields plus a computed `outputFileName` field. This field contains the expected output file name with the correct extension for the tool type (e.g., `"photo.pdf"` for an image-to-pdf job with input `"photo.jpeg"`, or `"report.docx"` for pdf-to-word). Frontend clients should use `outputFileName` for display and download naming.

### History Endpoint

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/api/jobs/history` | `GetJobHistory` | Full job history for authenticated users |

### SSE (Server-Sent Events) Endpoint

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/api/jobs/:id/events` | `SSEJobUpdates` | Stream real-time job status updates via SSE |

The SSE endpoint creates an ephemeral NATS JetStream consumer on the `JOBS_EVENTS` stream and filters events server-side by job ID. The stream auto-closes when the job completes or fails, and has a 5-minute timeout to prevent zombie connections. A keepalive comment is sent every 15 seconds to prevent proxy timeouts. On handler exit the consumer is explicitly deleted via `JS.DeleteConsumer` (using a fresh 5-second context, since the request context is already cancelled); the consumer's `InactiveThreshold` of 60 s acts as a safety net if the explicit delete fails.

**SSE Event Types:**
| Event | Description |
|-------|-------------|
| `connected` | Initial connection confirmation with jobId |
| `job-update` | Job status change with jobId, status, progress, toolType. Includes `fileSize` when non-zero (completed jobs) and `failureReason` when non-empty (failed jobs), so clients can show the real failure reason instead of a generic message |
| `done` | Job completed or failed; stream will close |
| `error` | Error condition (e.g., event stream unavailable) |

### Infrastructure Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check (returns "ok") |
| GET | `/readyz` | Readiness check (PostgreSQL + Redis + NATS), returns 200/503 with individual check results |
| GET | `/metrics` | Prometheus metrics |

## Plan-Based Limits Enforcement

`CreateJobFromTool` enforces per-user plan limits using headers injected by the API Gateway:

| Header | Default (absent) | Enforcement |
|--------|-----------------|-------------|
| `X-User-Plan-Max-Files` | `5` | Rejects jobs whose file count exceeds the limit; error code `TOO_MANY_FILES` |
| `X-User-Plan-Max-File-MB` | `10` | Rejects files that exceed the size limit; error code `FILE_TOO_LARGE` (413) |

Both the JSON (uploadId) and multipart form paths check the file-count limit. The per-file size limit is enforced three times on the presigned path — on the declared size at `/init` (before any presigned URL is issued), on the **true** object size at `/complete` (oversized objects are deleted), and against the `MAX_UPLOAD_MB` hard cap when the job consumes the upload — and once on `file.Size` for the direct multipart path.

## NATS Events / Queues

### Published Events

When a job is created, the service publishes a `JobCreated` event to NATS JetStream:

**Subject**: `jobs.dispatch.<service-name>` (e.g., `jobs.dispatch.convert-from-pdf`)

**Event Schema** (`queue.JobEvent`):
```json
{
  "eventType": "JobCreated",
  "jobId": "uuid",
  "toolType": "pdf-to-word",
  "inputPaths": ["uploads/<uploadId>/file.pdf"],
  "options": { ... },
  "correlationId": "uuid",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

`inputPaths` are **object keys in the uploads bucket** (not filesystem paths): `uploads/{uploadId}/{fileName}` for presigned uploads, `uploads/{jobId}/{fileName}` for direct multipart submissions. Workers download the objects from the uploads bucket and write results to the outputs bucket.

The target service name is resolved via `routing.ServiceForTool(toolType)` and the subject is built with `queue.SubjectForDispatch(serviceName)`.

### Analytics Events

Alongside the dispatch event, `job-service` publishes fire-and-forget analytics events to the `ANALYTICS` stream (`analytics.events.<eventType>`, via `queue.PublishAnalyticsEvent`; a NATS outage never blocks the request):

| Event type | When | Payload highlights |
|------------|------|--------------------|
| `analytics.events.job.created` | On successful job creation (`publishJobAnalyticsEvent`) | `jobId`, `toolType`, `fileSize`, `isGuest`, `userId?` |
| `analytics.events.plan.limit_hit` | When a plan limit blocks a job (`publishPlanLimitHit`) | `toolType`, `isGuest`, `metadata.limitType` |

These are consumed by analytics-service (see its subscriber). Job **lifecycle** events (`JobProgress`/`JobCompleted`/`JobFailed`) are published by the **workers** to `jobs.events.<jobId>.*` on the `JOBS_EVENTS` stream — job-service only reads those (for the SSE endpoint), it does not publish them.

### Dead Letter Queue (DLQ)

The `JOBS_DLQ` stream provides a dead letter queue with 7-day retention for failed jobs.

**Subjects**: `jobs.dlq.<serviceName>` (e.g., `jobs.dlq.convert-from-pdf`)

**When messages are routed to DLQ**: When retries are exhausted (MaxDeliver exceeded), the failed job payload is published to the DLQ before the original message is acknowledged.

**DLQ Payload**: Includes the original job data plus `EventType: "JobFailed"` and the delivery count.

### Stream Setup

On startup, the service calls `natsconn.EnsureStreams()` to create the required JetStream streams if they do not exist.

## DB Schema

### processing_jobs

```sql
CREATE TABLE processing_jobs (
    id            UUID PRIMARY KEY,
    user_id       UUID          NULL,       -- NULL for guest users
    tool_type     TEXT          NOT NULL,
    status        TEXT          NOT NULL DEFAULT 'queued',
    progress      INT           DEFAULT 0,
    file_name     TEXT          NOT NULL,
    file_size     BIGINT        DEFAULT 0,
    failure_reason TEXT         NULL,
    metadata      JSONB         NULL,
    created_at    TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP     DEFAULT CURRENT_TIMESTAMP,
    completed_at  TIMESTAMP     NULL,
    expires_at    TIMESTAMP     NULL        -- always finite: guest GUEST_JOB_TTL (30m), free FREE_JOB_TTL (7d), pro PRO_JOB_TTL (30d)
);

-- Indexes
CREATE INDEX idx_processing_jobs_user_id ON processing_jobs(user_id);
CREATE INDEX idx_processing_jobs_tool_type ON processing_jobs(tool_type);
CREATE INDEX idx_processing_jobs_status ON processing_jobs(status);
CREATE INDEX idx_processing_jobs_expires_at ON processing_jobs(expires_at);
```

### file_metadata

```sql
CREATE TABLE file_metadata (
    id            UUID PRIMARY KEY,
    job_id        UUID          NOT NULL,
    kind          TEXT          NOT NULL,   -- 'input' or 'output'
    original_name TEXT          NOT NULL,
    path          TEXT          NOT NULL,
    size_bytes    BIGINT        NOT NULL,
    created_at    TIMESTAMP     DEFAULT CURRENT_TIMESTAMP
);

-- Index
CREATE INDEX idx_file_metadata_job_id ON file_metadata(job_id);
```

### Redis Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `upload:<uploadId>` | Hash | `UPLOAD_TTL` (default 30m) | Upload session state: fileName, declaredSize, contentType, bucket, key, s3UploadId, partSize, totalParts, createdAt (+ `size` after `/complete`) |
| `upload:<uploadId>:chunks` | Set | — | Legacy chunked-protocol key; no longer written, still deleted on abort/release so stale sessions clean up |
| `idempotency:upload:<uploadId>` | String | 10 minutes | Maps a consumed `uploadId` → `jobId` so duplicate POSTs return the original job |
| `guest:<token>:jobs` | Set | `GUEST_JOB_TTL` | Job IDs belonging to a guest session |
| `ratelimit:upload:<ip>` | ZSet | Rate limit window | Upload rate limit counter |
| `ratelimit:jobcreate:<ip>` | ZSet | Rate limit window | Job-creation rate limit counter (`RATE_LIMIT_JOB_CREATE`, default 20/min) |
| `ratelimit:detect:<ip>` | ZSet | Rate limit window | Detect-edges rate limit counter (`RATE_LIMIT_DETECT`, default 60/window) |
| `idempotency:<key>` | String | 10 minutes | Maps idempotency key to job ID for deduplication |

## Sequence Diagrams

### Presigned Multipart Upload Flow

```mermaid
sequenceDiagram
    participant C as Client (Browser)
    participant GW as API Gateway
    participant JS as Job Service
    participant R as Redis
    participant S3 as MinIO / S3

    C->>GW: POST /api/uploads/init {fileName, fileSize, contentType}
    GW->>JS: Proxy request
    JS->>JS: Enforce plan size limit on declared size (413 FILE_TOO_LARGE)
    JS->>S3: CreateMultipartUpload uploads/<uploadId>/<fileName>
    JS->>JS: Presign PUT URL for every part (signed against S3_PUBLIC_ENDPOINT)
    JS->>R: HSET upload:<id> {fileName, declaredSize, bucket, key, s3UploadId, partSize, totalParts}
    JS->>R: EXPIRE upload:<id> UPLOAD_TTL (30m)
    JS-->>GW: 201 {uploadId, key, partSize, totalParts, urlExpiresAt, parts[]}
    GW-->>C: 201

    loop For each part
        C->>S3: PUT <presigned part URL> [part bytes] (via Caddy edge/public endpoint)
        S3-->>C: 200 + ETag header
    end

    opt Resume / URL expiry
        C->>JS: GET /api/uploads/<id>/parts?partNumbers=2,3
        JS-->>C: 200 {parts: re-presigned URLs}
    end

    C->>GW: POST /api/uploads/<id>/complete {parts:[{partNumber,etag}]}
    GW->>JS: Proxy request
    JS->>JS: part count == totalParts? (400 UPLOAD_INCOMPLETE)
    JS->>S3: CompleteMultipartUpload(etags)
    JS->>S3: StatObject → TRUE size
    alt true size > plan limit
        JS->>S3: RemoveObject
        JS->>R: DEL upload:<id>
        JS-->>C: 413 FILE_TOO_LARGE
    else within limit
        JS->>R: HSET upload:<id> size=<true size>; refresh TTL
        JS-->>C: 200 {uploadId, fileName, size, complete:true}
    end
```

### Job Creation and Dispatch Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant JS as Job Service
    participant R as Redis
    participant DB as PostgreSQL
    participant NATS as NATS JetStream
    participant W as Worker Service

    C->>GW: POST /api/convert-from-pdf/pdf-to-word {uploadId}
    GW->>JS: Proxy (with X-User-ID header)

    JS->>JS: Normalize tool type
    JS->>JS: Validate tool via routing.ServiceForTool()

    alt Idempotency-Key header present
        JS->>R: GET idempotency:<key>
        R-->>JS: existing jobId (or nil)
        alt Job already exists
            JS->>DB: SELECT * FROM processing_jobs WHERE id=<jobId>
            JS-->>GW: 201 {existing job}
            GW-->>C: 201 {existing job}
        end
    end

    alt JSON body with uploadId
        JS->>R: HGETALL upload:<uploadId>
        R-->>JS: {fileName, bucket, key, ...}
        JS->>JS: StatObject(uploads, key) — existence + TRUE size
        JS->>JS: GetObjectRange(key, 0, 512) → MIME sniff against allowlist
        Note over JS,R: The object stays at uploads/<uploadId>/... and Redis state is NOT removed yet — the upload remains intact so the request can retry on a downstream failure without re-uploading.
    else Multipart form upload (direct)
        JS->>JS: Sniff first 512 bytes, validate MIME
        JS->>JS: PutObject → uploads/<jobId>/<fileName> (streamed)
    end

    JS->>DB: INSERT processing_job (status=queued)
    JS->>DB: INSERT file_metadata (kind=input, path=<object key>)

    JS->>JS: routing.ServiceForTool("pdf-to-word") => "convert-from-pdf"
    JS->>NATS: Publish JobCreated (inputPaths = object keys) to jobs.dispatch.convert-from-pdf
    NATS-->>W: Deliver JobCreated event

    JS->>R: DEL upload:<uploadId> (release session — only after publish succeeds; object stays for the worker)

    opt Idempotency-Key header present
        JS->>R: SET idempotency:<key> <jobId> EX 600
    end

    JS-->>GW: 201 {job object}
    GW-->>C: 201 {job object}

    Note over W: Worker processes file...
    W->>DB: UPDATE processing_job SET status='completed'
```

### Job Query and Download Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant JS as Job Service
    participant DB as PostgreSQL
    participant S3 as MinIO / S3

    C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>
    GW->>JS: Proxy request
    JS->>DB: SELECT * FROM processing_jobs WHERE id=<jobId> AND tool_type='pdf-to-word'
    JS->>JS: Authorize access (user_id match or guest token)
    JS-->>GW: 200 {job details}
    GW-->>C: 200 {job details}

    C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>/download
    GW->>JS: Proxy request
    JS->>DB: SELECT * FROM processing_jobs WHERE id=<jobId>
    JS->>JS: Authorize access + check status == 'completed'
    alt Cache hit
        JS->>JS: Load FileMetadata from in-memory cache
    else Cache miss
        JS->>DB: SELECT * FROM file_metadata WHERE job_id=<jobId> AND kind='output'
        JS->>JS: Store FileMetadata in cache
    end
    alt Legacy disk path (starts with "/")
        JS-->>C: 404 NOT_FOUND "download link has expired"
    else Object key
        JS->>JS: PresignGet(outputs, key, 5m) + response-content-disposition/type overrides
        JS-->>GW: 302 Location: <presigned URL>
        GW-->>C: 302
        C->>S3: GET <presigned URL> (file bytes served by object storage)
        S3-->>C: 200 [binary file] Content-Disposition: attachment
    end
```

### Guest User Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant R as Redis
    participant DB as PostgreSQL

    Note over C: No JWT token -- guest user
    C->>JS: POST /api/convert-to-pdf/word-to-pdf {uploadId}
    JS->>JS: authUserID() returns nil (guest)
    JS->>DB: INSERT processing_job (user_id=NULL, expires_at=NOW+GUEST_JOB_TTL)

    JS->>R: SADD guest:<token>:jobs <jobId>
    JS->>R: EXPIRE guest:<token>:jobs 2h
    JS-->>C: 201 {job, guestToken} + Set-Cookie: guest_token=<token>
    Note over C: Frontend stores guestToken in localStorage

    C->>JS: GET /api/convert-to-pdf/word-to-pdf
    Note over C: X-Guest-Token: <token> (or Cookie: guest_token=<token>)
    JS->>R: SMEMBERS guest:<token>:jobs
    R-->>JS: [jobId1, jobId2]
    JS->>DB: SELECT FROM processing_jobs WHERE id IN (jobId1, jobId2)
    JS-->>C: 200 {jobs}
```

## Error Flows

### Job Creation Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Empty or invalid tool type |
| `INVALID_INPUT` | 400 | Unsupported tool |
| `INVALID_INPUT` | 400 | No file uploaded or missing uploadId |
| `INVALID_INPUT` | 400 | File type mismatch for tool (e.g., non-PDF for pdf-to-word) |
| `INVALID_INPUT` | 400 | File MIME content type (sniffed from the object's first 512 bytes) does not match the expected type for the tool |
| `INVALID_INPUT` | 400 | Uploaded object missing from the uploads bucket (upload never completed) |
| `INVALID_OPTIONS` | 400 | `options` payload fails the per-tool schema (scan-to-pdf: bad JSON, non-whitelisted language/pageSize/enhance, >50 pages entries, invalid rotation, partial corners, coordinates outside [0,1]) |
| `FILE_TOO_LARGE` | 413 | File size exceeds plan limit from `X-User-Plan-Max-File-MB` (direct multipart path; default 10 MB) |
| `TOO_MANY_FILES` | 400 | Number of files exceeds plan limit from `X-User-Plan-Max-Files` (default 5 files) |
| `RATE_LIMIT_EXCEEDED` | 429 | More than `RATE_LIMIT_JOB_CREATE` job creations per IP per window |
| `SERVER_ERROR` | 500 | Object storage put/stat failed |
| `SERVER_ERROR` | 500 | Database insert failed |
| `SERVER_ERROR` | 500 | NATS publish failed |

### Upload Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing/invalid fileName or fileSize at init; invalid part-number list |
| `INVALID_INPUT` | 400 | File needs more than 1000 parts at the configured part size |
| `FILE_TOO_LARGE` | 413 | Declared size (init) or true object size (complete) exceeds the plan limit |
| `NOT_FOUND` | 404 | Upload session expired or not found in Redis |
| `UPLOAD_INCOMPLETE` | 400 | Part count mismatch, or S3 rejected CompleteMultipartUpload (missing/invalid parts) |
| `UPLOAD_PROTOCOL_CHANGED` | 410 | Legacy `PUT /:uploadId/chunk` called — client must refresh to the presigned protocol |
| `SERVER_ERROR` | 500 | Redis or object storage failure |

### Job Query / Download Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `NOT_FOUND` | 404 | Job not found or not authorized |
| `NOT_FOUND` | 404 | Output predates the S3 migration (legacy disk path) — "download link has expired" |
| `NOT_READY` | 400 | Download requested but job status is not 'completed' |
| `UNAUTHORIZED` | 401 | Job history requested without authentication |
| `SERVER_ERROR` | 500 | Database query or presign failure |

### Error Response Format

All errors follow the standard response format:
```json
{
  "success": false,
  "message": "human readable message",
  "error": {
    "code": "ERROR_CODE",
    "details": "detailed description"
  }
}
```

### Cleanup on Failure

When job creation fails partway through:
- `consumeUpload` is **read-only**: it never moves or deletes the uploaded object, so any downstream failure (DB transaction, NATS publish) leaves the object at `uploads/<uploadId>/...` and the Redis session intact. The frontend's retry button can resubmit the same `uploadId` without forcing the user to re-upload.
- The Redis session (`upload:<uploadId>`) is released **only after** the DB transaction commits **and** the `JobCreated` event is published.
- Abandoned uploads (object + incomplete multipart uploads) are reaped by the in-process cleanup loop; the Redis session expires via `UPLOAD_TTL`.

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_ADDR` | Redis server address |
| `JWT_HS256_SECRET` | JWT signing secret (min 32 characters) |
| `NATS_URL` | NATS server URL |
| `S3_ENDPOINT` | Internal object-storage address for data operations (e.g. `minio:9000`) |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | Object-storage credentials |

The service fails fast at boot if object storage is misconfigured or the buckets cannot be ensured.

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8081` | HTTP server port |
| `S3_PUBLIC_ENDPOINT` | `S3_ENDPOINT` | Browser-reachable origin presigned URLs are signed against (e.g. the Caddy edge, `PUBLIC_ORIGIN`) |
| `S3_BUCKET_UPLOADS` | `uploads` | Bucket for input objects |
| `S3_BUCKET_OUTPUTS` | `outputs` | Bucket for worker output objects |
| `S3_REGION` | `us-east-1` | Signing region |
| `S3_USE_SSL` | `false` | TLS to the internal endpoint |
| `UPLOAD_PART_SIZE_MB` | `8` | Multipart part size in MiB (clamped to the S3 5 MiB minimum) |
| `MAX_UPLOAD_MB` | `500` | Absolute server-side hard cap on file size in MB — must be ≥ the largest plan (pro 500) or that plan is unreachable. Per-plan limits are enforced via the `X-User-Plan-Max-File-MB` header |
| `UPLOAD_TTL` | `30m` | Upload session expiration (fallback from `shared/config/defaults.go`) |
| `GUEST_JOB_TTL` | `30m` | Guest job expiration (no user) |
| `FREE_JOB_TTL` | `168h` (7d) | Free plan user job expiration |
| `PRO_JOB_TTL` | `720h` (30d) | Pro plan user job expiration |
| `RATE_LIMIT_UPLOAD` | `30` | Upload-endpoint rate limit per IP per window |
| `RATE_LIMIT_JOB_CREATE` | `20` | Job-creation rate limit per IP per window |
| `RATE_LIMIT_DETECT` | `60` | Detect-edges rate limit per IP per window |
| `RATE_LIMIT_WINDOW` | `60s` | Rate limit time window |
| `ORGANIZE_PDF_URL` | `http://organize-pdf:8084` | Base URL for the synchronous detect-edges relay to organize-pdf |
| `INTERNAL_API_TOKEN` | `""` | Optional shared secret sent as `X-Internal-Token` on internal calls to organize-pdf (organize-pdf verifies it when set on its side) |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | Trusted proxy IP addresses |
| `AUTH_DENYLIST_ENABLED` | `true` | Enable token denylist |
| `AUTH_DENYLIST_PREFIX` | `denylist:jwt` | Redis key prefix for denylist |
| `AUTH_GUEST_PREFIX` | `guest` | Guest token Redis key prefix |
| `AUTH_GUEST_SUFFIX` | `jobs` | Guest token Redis key suffix |
| `AUTH_TRUST_GATEWAY_HEADERS` | `false` | Trust X-User-ID from gateway |
| `LOG_MODE` | `""` | Logging mode |

## Scaling Constraints

1. **Horizontal scaling**: The job-service is stateless (all state in Redis/PostgreSQL/NATS). Multiple instances can run behind a load balancer.
2. **Database connection pool**: Configured with `MaxOpenConns=50`, `MaxIdleConns=25`, `ConnMaxLifetime=5m`, `ConnMaxIdleTime=2m`. The pool was raised from 20/10 when uploads moved to presigned URLs: handlers are now short DB-bound requests (no long file streams occupying connections), so each replica sustains far more concurrent requests. The idle-time bound is shorter than typical managed-Postgres idle-close windows (Neon, RDS Proxy, pgbouncer), which prevents the pool from handing out a half-dead socket.
3. **Postgres-side timeouts**: `Connect()` enriches `DATABASE_URL` via `shared/config.ApplyPostgresDSNDefaults` to set `statement_timeout=15s`, `idle_in_transaction_session_timeout=30s`, and libpq TCP keepalives (`keepalives_idle=30s`, `keepalives_interval=10s`, `keepalives_count=3`). These bound the worst-case hang on a stale or dropped connection. User-set values in `DATABASE_URL` are preserved.
4. **Per-request transaction timeout**: `CreateJobFromTool` wraps the job-create transaction in a 10-second `context.WithTimeout` so a stuck connection cannot freeze a request beyond that bound.
5. **File storage**: All files live in object storage (MinIO/S3) — no shared volume is needed; instances are fully interchangeable. File bytes flow browser ↔ object storage via presigned URLs; the service only handles metadata.
6. **NATS JetStream**: Provides durable message delivery with at-least-once semantics. Workers acknowledge messages after processing. No messages are lost if the job-service restarts.
7. **Redis dependency**: Upload state and guest tokens rely on Redis. If Redis is unavailable, uploads and guest tracking will fail. Consider Redis Sentinel or Cluster for HA.
8. **Rate limiting**: Uses Redis-backed rate limiters. All instances share the same counters via Redis.
9. **Single writer per job**: Each job has a unique UUID. No concurrent writes to the same job record.
10. **Memory**: `MaxMultipartMemory` is set to 50 MB for the direct multipart path; presigned uploads never pass file bytes through the service at all (only the first 512 bytes of each object are read for MIME sniffing).

## Deployment

### Docker Compose

```yaml
job-service:
  build:
    context: ./job-service
  expose:
    - "8081"          # internal-only; reached via the Caddy edge → api-gateway
  environment:
    PORT: "8081"
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    NATS_URL: nats://nats:4222
    JWT_HS256_SECRET: ${JWT_HS256_SECRET}
    S3_ENDPOINT: minio:9000
    S3_PUBLIC_ENDPOINT: http://localhost   # the Caddy edge (PUBLIC_ORIGIN)
    S3_ACCESS_KEY: ${S3_ACCESS_KEY}
    S3_SECRET_KEY: ${S3_SECRET_KEY}
    MAX_UPLOAD_MB: "500"
    GUEST_JOB_TTL: "30m"
    FREE_JOB_TTL: "168h"
  depends_on:
    - db
    - redis
    - nats
    - minio
```

### Local Development

1. Start dependencies:
   ```bash
   docker compose up -d db redis nats
   ```

2. Set environment variables:
   ```bash
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs?sslmode=disable"
   export REDIS_ADDR="localhost:6379"
   export NATS_URL="nats://localhost:4222"
   export JWT_HS256_SECRET=$(openssl rand -hex 32)
   ```

3. Run the service:
   ```bash
   cd job-service
   go run main.go
   ```

## Error Logging

All 5xx response sites in handlers (`CreateJobFromTool`, `InitUpload`, `GetUploadParts`, `CompleteUpload`, `AbortUpload`, list/delete/download/history endpoints) emit a structured `slog.Error` via `response.InternalErrorf` before returning. Internal helpers (`consumeUpload`, etc.) call `logger.LogErr` / `logger.LogWarn` at the point of failure detection. Each line carries `op`, `requestId`, and the local IDs (`jobId`, `uploadId`, `tool`) needed to reproduce the failure. See [Error Logging](../architecture/error-logging.md) for the convention.

To debug a user-visible failure, take `meta.requestId` from the API response and grep job-service stdout for that value — the matching `slog.Error` line names the failing `op` (e.g. `db.processing_jobs.transaction`, `s3.create_multipart`, `s3.presign_get`).

## Related Documentation

- [API Gateway](./api-gateway.md) -- Request routing and CORS
- [Auth Service](./auth-service.md) -- Authentication and user management
- [Convert From PDF](./convert-from-pdf.md) -- PDF-to-other-format worker
- [Convert To PDF](./convert-to-pdf.md) -- Other-format-to-PDF worker
- [Organize PDF](./organize-pdf.md) -- PDF organization worker
- [Optimize PDF](./optimize-pdf.md) -- PDF optimization worker
- [Error Logging](../architecture/error-logging.md) -- Backend-wide error logging convention
