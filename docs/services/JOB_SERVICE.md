# Job Service

## Overview

The Job Service is the central orchestration service for all file processing operations in EsyDocs. It manages the full job lifecycle: receiving file uploads (chunked), creating processing jobs, dispatching work to downstream worker services via NATS JetStream, tracking job status, and serving file downloads. It also handles guest user job tracking through Redis.

**Port**: 8081
**Type**: REST API (Job Orchestrator)
**Framework**: Gin (Go)
**Database**: PostgreSQL (via GORM)
**Queue**: NATS JetStream
**Cache**: Redis

## Service Responsibility

1. **Chunked File Uploads** -- Initialize upload sessions, receive file chunks, assemble completed uploads
2. **Job Creation** -- Validate tool types, create processing jobs in PostgreSQL, dispatch events to NATS
3. **Job Querying** -- List jobs by tool type with pagination, retrieve individual job details
4. **Job Deletion** -- Delete jobs and their associated files from disk and database
5. **File Download** -- Serve completed output files with proper Content-Type and Content-Disposition headers
6. **Job History** -- Return full job history for authenticated users
7. **Guest Job Tracking** -- Manage guest tokens in Redis to allow anonymous users to track their jobs
8. **Tool-to-Service Routing** -- Use a centralized `ToolServiceMap` to dispatch jobs to the correct worker service
9. **MIME-Type Validation** -- Validate uploaded file content types using `http.DetectContentType()` against an allowlist per tool category (pdf, word, excel, ppt, image)
10. **Idempotency** -- Support `Idempotency-Key` header on job creation to prevent duplicate jobs within a 10-minute window

## Design Constraints

- **Microservice boundary**: The job-service owns all job CRUD, upload management, and tool dispatch logic. It does NOT perform file conversions itself.
- **No cross-service DB access**: Worker services receive jobs via NATS events and update status in their own database replicas.
- **Stateless HTTP layer**: All session/upload state is stored in Redis; the service can scale horizontally.
- **JWT authentication**: All routes (except `/healthz` and `/metrics`) pass through auth middleware that validates JWT tokens or guest tokens.
- **NATS JetStream**: All job dispatch uses NATS JetStream for durable, at-least-once delivery to worker services.

## Internal Architecture

```
Client
  |
API Gateway :8080
  |
Job Service :8081
  |--- Upload Handlers (init, chunk, status, complete)
  |--- Job Handlers (create, list, get, delete, download, history)
  |--- Auth Middleware (JWT / Guest Token)
  |--- PostgreSQL (processing_jobs, file_metadata)
  |--- Redis (upload state, guest job sets, rate limits)
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
| `handlers/jobs.go` | Job CRUD, file download, job history |
| `handlers/sse.go` | SSE endpoint for real-time job status updates via NATS JetStream |
| `handlers/uploads.go` | Chunked upload init, chunk receive, assembly |
| `handlers/auth.go` | Extract authenticated user ID from auth context |
| `internal/routing/routing.go` | `ToolServiceMap` -- single source of truth for tool-to-service mapping |
| `internal/models/` | GORM models for `ProcessingJob` and `FileMetadata` |
| `internal/authverify/` | JWT verification middleware (local copy) |
| `routes/upload_routes.go` | Gin route registration with rate limiting |
| `middleware/` | Rate limiting middleware |

### ToolServiceMap (routing.go)

The `routing.go` file contains the centralized mapping from tool types to their processing service names. This is the single source of truth for queue routing:

| Service | Tools |
|---------|-------|
| `convert-from-pdf` | pdf-to-word, pdf-to-docx, pdf-to-excel, pdf-to-xlsx, pdf-to-powerpoint, pdf-to-ppt, pdf-to-pptx, pdf-to-image, pdf-to-img, pdf-to-html, pdf-to-text, pdf-to-txt, pdf-to-pdfa, ocr |
| `convert-to-pdf` | word-to-pdf, excel-to-pdf, powerpoint-to-pdf, ppt-to-pdf, html-to-pdf, image-to-pdf, img-to-pdf, merge-pdf, page-reorder, page-rotate, watermark-pdf, protect-pdf, unlock-pdf, sign-pdf, edit-pdf, add-page-numbers |
| `organize-pdf` | split-pdf, rotate-pdf, remove-pages, extract-pages, organize-pdf, scan-to-pdf |
| `optimize-pdf` | compress-pdf, repair-pdf, ocr-pdf |

## Routes

### Upload Endpoints

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/api/uploads/init` | `InitUpload` | Initialize a chunked upload session |
| PUT | `/api/uploads/:uploadId/chunk?index=N` | `UploadChunk` | Upload a single chunk |
| GET | `/api/uploads/:uploadId/status` | `GetUploadStatus` | Get upload progress |
| POST | `/api/uploads/:uploadId/complete` | `CompleteUpload` | Assemble chunks into final file |

### Job Endpoints (per tool category)

These are registered under `/api/convert-from-pdf`, `/api/convert-to-pdf`, `/api/organize-pdf`, and `/api/optimize-pdf`:

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| POST | `/api/{category}/:tool` | `CreateJobFromTool` | Create a processing job |
| GET | `/api/{category}/:tool` | `GetJobsByTool` | List jobs by tool (paginated) |
| GET | `/api/{category}/:tool/:id` | `GetJobByID` | Get single job details |
| DELETE | `/api/{category}/:tool/:id` | `DeleteJobByID` | Delete job and files |
| GET | `/api/{category}/:tool/:id/download` | `DownloadJobFile` | Download completed output |

Where `{category}` is one of: `convert-from-pdf`, `convert-to-pdf`, `organize-pdf`, `optimize-pdf`.

### History Endpoint

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/api/jobs/history` | `GetJobHistory` | Full job history for authenticated users |

### SSE (Server-Sent Events) Endpoint

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/api/jobs/:id/events` | `SSEJobUpdates` | Stream real-time job status updates via SSE |

The SSE endpoint creates an ephemeral NATS JetStream consumer on the `JOBS_EVENTS` stream and filters events server-side by job ID. The stream auto-closes when the job completes or fails, and has a 5-minute timeout to prevent zombie connections. A keepalive comment is sent every 15 seconds to prevent proxy timeouts.

**SSE Event Types:**
| Event | Description |
|-------|-------------|
| `connected` | Initial connection confirmation with jobId |
| `job-update` | Job status change with jobId, status, progress, toolType |
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
| `X-User-Plan-Max-File-MB` | `10` | Rejects individual files that exceed the size limit (multipart path only); error code `FILE_TOO_LARGE` |

Both the JSON (uploadId) and multipart form paths check the file-count limit. The per-file size check (`X-User-Plan-Max-File-MB`) applies only to the multipart path, because the upload service already enforces size on the chunked upload path.

## NATS Events / Queues

### Published Events

When a job is created, the service publishes a `JobCreated` event to NATS JetStream:

**Subject**: `dispatch.<service-name>` (e.g., `dispatch.convert-from-pdf`)

**Event Schema** (`queue.JobEvent`):
```json
{
  "eventType": "JobCreated",
  "jobId": "uuid",
  "toolType": "pdf-to-word",
  "inputPaths": ["/uploads/job-id/file.pdf"],
  "options": { ... },
  "correlationId": "uuid",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

The target service name is resolved via `routing.ServiceForTool(toolType)` and the subject is built with `queue.SubjectForDispatch(serviceName)`.

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
    expires_at    TIMESTAMP     NULL        -- guest: GUEST_JOB_TTL (30m), free: FREE_JOB_TTL (24h), pro: NULL (never)
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
| `upload:<uploadId>` | Hash | `UPLOAD_TTL` (30m) | Upload session state (fileName, fileSize, totalChunks, createdAt) |
| `upload:<uploadId>:chunks` | Set | `UPLOAD_TTL` (30m) | Set of received chunk indices |
| `guest:<token>:jobs` | Set | `GUEST_JOB_TTL` (2h) | Job IDs belonging to a guest session |
| `ratelimit:upload:<ip>` | String | Rate limit window | Upload rate limit counter |
| `idempotency:<key>` | String | 10 minutes | Maps idempotency key to job ID for deduplication |

## Sequence Diagrams

### Chunked Upload Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant JS as Job Service
    participant R as Redis
    participant FS as File System

    C->>GW: POST /api/upload/init {fileName, fileSize, totalChunks}
    GW->>JS: Proxy request
    JS->>R: HSET upload:<id> {fileName, fileSize, totalChunks, createdAt}
    JS->>R: EXPIRE upload:<id> 2h
    JS-->>GW: 201 {uploadId}
    GW-->>C: 201 {uploadId}

    loop For each chunk
        C->>GW: PUT /api/upload/<id>/chunk?index=N [chunk data]
        GW->>JS: Proxy request
        JS->>FS: Save chunk to uploads/tmp/<id>/<index>.part
        JS->>R: SADD upload:<id>:chunks N
        JS-->>GW: 200 {uploadId, receivedChunks, complete}
        GW-->>C: 200 {uploadId, receivedChunks, complete}
    end

    Note over JS,R: The UploadChunk handler uses a Redis Lua script (uploadChunkLua) for atomic state updates — checking upload existence, recording the chunk index, and refreshing TTLs in a single round-trip, preventing TOCTOU race conditions.

    C->>GW: POST /api/upload/<id>/complete
    GW->>JS: Proxy request
    JS->>R: SCARD upload:<id>:chunks (verify all chunks received)
    JS->>FS: Assemble chunks into uploads/<id>/<fileName>
    JS->>FS: Remove tmp/<id>/ chunk directory
    JS-->>GW: 200 {uploadId, storedPath}
    GW-->>C: 200 {uploadId, storedPath}
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
        R-->>JS: {fileName, fileSize, ...}
        JS->>JS: Move file from uploads/<uploadId>/ to uploads/<jobId>/
        JS->>R: DEL upload:<uploadId> (consume upload)
    else Multipart form upload
        JS->>JS: Save uploaded files directly to uploads/<jobId>/
    end

    JS->>JS: Validate MIME type (http.DetectContentType) against allowlist

    JS->>DB: INSERT processing_job (status=queued)
    JS->>DB: INSERT file_metadata (kind=input)

    JS->>JS: routing.ServiceForTool("pdf-to-word") => "convert-from-pdf"
    JS->>NATS: Publish JobCreated to dispatch.convert-from-pdf
    NATS-->>W: Deliver JobCreated event

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
    participant FS as File System

    C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>
    GW->>JS: Proxy request
    JS->>DB: SELECT * FROM processing_jobs WHERE id=<jobId> AND tool_type='pdf-to-word'
    JS->>JS: Authorize access (user_id match or guest token)
    JS-->>GW: 200 {job details}
    GW-->>C: 200 {job details}

    C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>/download
    GW->>JS: Proxy request
    JS->>DB: SELECT * FROM processing_jobs WHERE id=<jobId>
    JS->>JS: Check status == 'completed'
    JS->>DB: SELECT * FROM file_metadata WHERE job_id=<jobId> AND kind='output'
    JS->>FS: Read output file
    JS-->>GW: 200 [binary file] Content-Disposition: attachment
    GW-->>C: 200 [binary file]
```

### Guest User Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant R as Redis
    participant DB as PostgreSQL

    Note over C: No JWT token -- anonymous user
    C->>JS: POST /api/convert-to-pdf/word-to-pdf {uploadId}
    JS->>JS: authUserID() returns nil (guest)
    JS->>DB: INSERT processing_job (user_id=NULL, expires_at=NOW+GUEST_JOB_TTL)

    JS->>R: SADD guest:<token>:jobs <jobId>
    JS->>R: EXPIRE guest:<token>:jobs 2h
    JS-->>C: 201 {job} + Set-Cookie: guest_token=<token>

    C->>JS: GET /api/convert-to-pdf/word-to-pdf
    Note over C: Cookie: guest_token=<token>
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
| `INVALID_INPUT` | 400 | File MIME content type does not match the expected type for the tool |
| `FILE_TOO_LARGE` | 413 | File size exceeds plan limit from `X-User-Plan-Max-File-MB` (multipart path; default 10 MB) |
| `TOO_MANY_FILES` | 400 | Number of files exceeds plan limit from `X-User-Plan-Max-Files` (default 5 files) |
| `SERVER_ERROR` | 500 | Failed to create upload directory |
| `SERVER_ERROR` | 500 | Failed to save file |
| `SERVER_ERROR` | 500 | Database insert failed |
| `SERVER_ERROR` | 500 | NATS publish failed |

### Upload Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing fileName, fileSize, or totalChunks |
| `INVALID_INPUT` | 400 | Invalid or missing chunk index |
| `NOT_FOUND` | 404 | Upload session expired or not found in Redis |
| `BAD_REQUEST` | 400 | Attempting to complete an incomplete upload |
| `FILE_TOO_LARGE` | 400 | Assembled file exceeds maximum size |
| `SERVER_ERROR` | 500 | Redis or filesystem failure |

### Job Query / Download Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `NOT_FOUND` | 404 | Job not found or not authorized |
| `NOT_READY` | 400 | Download requested but job status is not 'completed' |
| `UNAUTHORIZED` | 401 | Job history requested without authentication |
| `SERVER_ERROR` | 500 | Database query failure |

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
- The job directory (`uploads/<jobId>/`) is removed via a deferred cleanup
- The `jobCreated` flag ensures cleanup only runs if the DB insert did not complete
- Upload state in Redis is only consumed (deleted) after successful file move

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_ADDR` | Redis server address |
| `JWT_HS256_SECRET` | JWT signing secret (min 32 characters) |
| `NATS_URL` | NATS server URL |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8081` | HTTP server port |
| `UPLOAD_DIR` | `uploads` | Base directory for uploaded files |
| `MAX_UPLOAD_MB` | `50` | Server-side hard cap on file size in MB (per-user plan limits are enforced via `X-User-Plan-Max-File-MB` header) |
| `UPLOAD_TTL` | `30m` | Upload session expiration |
| `GUEST_JOB_TTL` | `30m` | Guest job expiration (no user) |
| `FREE_JOB_TTL` | `24h` | Free plan user job expiration |
| `RATE_LIMIT_UPLOAD` | `30` | Upload rate limit per window |
| `RATE_LIMIT_WINDOW` | `60s` | Rate limit time window |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | Trusted proxy IP addresses |
| `AUTH_DENYLIST_ENABLED` | `true` | Enable token denylist |
| `AUTH_DENYLIST_PREFIX` | `denylist:jwt` | Redis key prefix for denylist |
| `AUTH_GUEST_PREFIX` | `guest` | Guest token Redis key prefix |
| `AUTH_GUEST_SUFFIX` | `jobs` | Guest token Redis key suffix |
| `AUTH_TRUST_GATEWAY_HEADERS` | `false` | Trust X-User-ID from gateway |
| `LOG_MODE` | `""` | Logging mode |

## Scaling Constraints

1. **Horizontal scaling**: The job-service is stateless (all state in Redis/PostgreSQL/NATS). Multiple instances can run behind a load balancer.
2. **Database connection pool**: Configured with `MaxOpenConns=20`, `MaxIdleConns=10`. Adjust for high-concurrency deployments.
3. **File storage**: The service writes to a shared filesystem (`UPLOAD_DIR`). When scaling horizontally, all instances must share the same volume mount.
4. **NATS JetStream**: Provides durable message delivery with at-least-once semantics. Workers acknowledge messages after processing. No messages are lost if the job-service restarts.
5. **Redis dependency**: Upload state and guest tokens rely on Redis. If Redis is unavailable, uploads and guest tracking will fail. Consider Redis Sentinel or Cluster for HA.
6. **Rate limiting**: Uses Redis-backed rate limiters. All instances share the same counters via Redis.
7. **Single writer per job**: Each job has a unique UUID. No concurrent writes to the same job record.
8. **Memory**: `MaxMultipartMemory` is set to 50 MB. Large file uploads are streamed to disk via chunks, not held in memory.

## Deployment

### Docker Compose

```yaml
job-service:
  build:
    context: ./job-service
  ports:
    - "8081:8081"
  environment:
    PORT: "8081"
    DATABASE_URL: postgresql://user:password@db:5432/esydocs
    REDIS_ADDR: redis:6379
    NATS_URL: nats://nats:4222
    JWT_HS256_SECRET: ${JWT_HS256_SECRET}
    UPLOAD_DIR: /app/uploads
    MAX_UPLOAD_MB: "50"
    GUEST_JOB_TTL: "30m"
    FREE_JOB_TTL: "24h"
  volumes:
    - uploads_data:/app/uploads
  depends_on:
    - db
    - redis
    - nats
```

### Local Development

1. Start dependencies:
   ```bash
   docker compose up -d db redis nats
   ```

2. Set environment variables:
   ```bash
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs?sslmode=disable"
   export REDIS_ADDR="localhost:6379"
   export NATS_URL="nats://localhost:4222"
   export JWT_HS256_SECRET=$(openssl rand -hex 32)
   ```

3. Run the service:
   ```bash
   cd job-service
   go run main.go
   ```

## Related Documentation

- [API Gateway](./API_GATEWAY.md) -- Request routing and CORS
- [Auth Service](./AUTH_SERVICE.md) -- Authentication and user management
- [Upload Service](./UPLOAD_SERVICE.md) -- Legacy upload service (being replaced)
- [Convert From PDF](./CONVERT_FROM_PDF.md) -- PDF-to-other-format worker
- [Convert To PDF](./CONVERT_TO_PDF.md) -- Other-format-to-PDF worker
- [Organize PDF](./ORGANIZE_PDF.md) -- PDF organization worker
- [Optimize PDF](./OPTIMIZE_PDF.md) -- PDF optimization worker
- [Cleanup Worker](./CLEANUP_WORKER.md) -- Expired job/upload cleanup
