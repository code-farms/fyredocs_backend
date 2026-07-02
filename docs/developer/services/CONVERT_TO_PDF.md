# Convert To PDF Service

## Overview

The Convert To PDF service converts Office (Word/Excel/PowerPoint), HTML, and image files into PDF. It runs as a NATS JetStream worker alongside a small HTTP server for health and metrics. Office and HTML conversions go through a persistent `unoserver` LibreOffice daemon (with a direct `libreoffice --headless` fallback). Image conversions use `pdfcpu`.

**Port**: 8083 (internal — not exposed via the API gateway)
**Type**: NATS Worker + HTTP health/metrics server
**Framework**: Gin (Go) for HTTP, custom NATS pull-consumer for jobs
**Processing**: LibreOffice (via unoserver/unoconvert), pdfcpu

## Responsibilities

1. **Office → PDF** — Convert Word/Excel/PowerPoint documents to PDF.
2. **HTML → PDF** — Convert HTML documents to PDF.
3. **Image → PDF** — Convert one or more JPG/PNG/GIF/WebP/BMP images into a single PDF.
4. **Job lifecycle** — Pull jobs from JetStream, run conversions concurrently (semaphore-bound), update `processing_jobs`, publish progress + completion + failure events.
5. **Object storage I/O** — Download input objects from the S3 uploads bucket into a container-local scratch directory, and upload the conversion output to the S3 outputs bucket under `jobs/{jobID}/...`.
6. **DLQ** — On `MaxDeliver` exhaustion, publish the failed job to `jobs.dlq.convert-to-pdf` (JOBS_DLQ stream, 7-day retention) before acking.

## Architecture

```
NATS JetStream JOBS_DISPATCH (jobs.dispatch.convert-to-pdf)
  ↓ pull-consumer (durable=convert-to-pdf, MaxDeliver=4, AckWait=30m, MaxAckPending=2×concurrency, BackOff 10s/30s/2m)
convert-to-pdf worker
  ├─ Fetch up to WORKER_CONCURRENCY messages (default 2)
  ├─ Per message → goroutine guarded by a semaphore
  │     ├─ duplicate-job guard (skip if already completed/processing)
  │     ├─ DB status → "processing" (progress 20)
  │     ├─ create scratch dir (os.MkdirTemp "job-<jobId>-*", removed when done)
  │     ├─ download inputs: S3 uploads bucket → <scratch>/in/ (failure → recoverable retry)
  │     ├─ time-based progress reporter (smooth 20→90% with ease-out curve)
  │     ├─ processing.ProcessFile() — local files in <scratch>/in, output to <scratch>/out
  │     │     ├─ word/excel/ppt/html → officeToPDF() → unoconvert (port 2002) — fallback: libreoffice --headless
  │     │     └─ image-to-pdf / img-to-pdf → pdfcpu (multiple images = multi-page output)
  │     ├─ upload output: <scratch>/out/<file> → S3 outputs bucket jobs/<jobId>/<file> (failure → recoverable retry)
  │     ├─ DB status → "completed" + record output FileMetadata (Path = object key, size = uploaded bytes)
  │     └─ Publish jobs.events.<jobId>.{processing,completed,failed}
  └─ On MaxDeliver exhaustion → publish JobFailed to jobs.dlq.convert-to-pdf
```

### Object Storage

Inputs and outputs live in S3-compatible object storage (MinIO in compose), not on a shared volume:

- `payload.InputPaths` carries **object keys** in the uploads bucket. The worker downloads each key to `<scratch>/in/<basename>` before processing, because LibreOffice/pdfcpu need real local files.
- The output file is uploaded to the outputs bucket as `jobs/{jobID}/{filename}` with an extension-derived `Content-Type`; `file_metadata.path` stores the object key and `size_bytes` the uploaded size.
- The scratch directory is deleted after every job (success or failure). Download/upload failures are treated as recoverable and retried via NAK + backoff.

### unoserver Daemon

The container image runs a persistent LibreOffice instance via `unoserver`, started by `entrypoint.sh` before the Go binary. The `officeToPDF()` function dispatches via `unoconvert` over a local socket (`UNOSERVER_HOST:UNOSERVER_PORT`, default `127.0.0.1:2002`), eliminating LibreOffice cold-start cost per conversion. If the daemon is unreachable, the function falls back to spawning `libreoffice --headless` directly. The `/readyz` probe reports the unoserver state as informational only — it does not affect readiness, since the fallback path is always available.

## Supported Tools

The worker's `AllowedTools` whitelist (in `main.go`) is the authoritative source. Any other tool type that reaches the consumer is acked with status `failed` and reason `[UNSUPPORTED_TOOL] <tool>`.

| Tool | Aliases | Input | Output | Implementation |
|------|---------|-------|--------|----------------|
| `word-to-pdf` | — | `.doc`, `.docx` | `.pdf` | LibreOffice Writer (unoconvert + fallback) |
| `excel-to-pdf` | — | `.xls`, `.xlsx` | `.pdf` | LibreOffice Calc |
| `ppt-to-pdf` | `powerpoint-to-pdf` | `.ppt`, `.pptx` | `.pdf` | LibreOffice Impress |
| `html-to-pdf` | — | `.html`, `.htm` | `.pdf` | LibreOffice Writer |
| `image-to-pdf` | `img-to-pdf` | `.jpg`, `.png`, `.gif`, `.webp`, `.bmp` | `.pdf` | pdfcpu (one image per page) |

> **Heads-up:** the worker's `AllowedTools` map is the definitive list. PDF-manipulation tools (compress / merge / split / watermark / sign / etc.) live in **organize-pdf** and **optimize-pdf**, not here. The job-service [routing](./JOB_SERVICE.md#toolservicemap-routinggo) table is what the gateway and clients see — keep both in sync if you add a new tool.

## API Endpoints (via job-service through API gateway)

This worker has no public API of its own. Clients hit `job-service` via the gateway:

```http
POST /api/convert-to-pdf/{tool}                  # create job (json with uploadIds, or multipart)
GET  /api/convert-to-pdf/{tool}                  # list jobs (paginated)
GET  /api/convert-to-pdf/{tool}/{jobId}           # get job status
GET  /api/convert-to-pdf/{tool}/{jobId}/download  # stream output
DELETE /api/convert-to-pdf/{tool}/{jobId}         # delete job + files
```

See [JOBS_API.md](../api/JOBS_API.md) for full request/response shapes.

## Concurrency

- `WORKER_CONCURRENCY` (default 2) controls how many in-flight conversions run in parallel.
- Implementation: a Go semaphore (`chan struct{}`) sized at `WORKER_CONCURRENCY`. Each fetched message takes a slot before launching a goroutine; the slot is released on completion.
- Fetch batch size matches `WORKER_CONCURRENCY` so the consumer doesn't pull more than it can run.

## Progress Reporting

Two strategies, selected by `hasRealProgress(toolType)` — for this service, **all** office and image conversions are time-estimated (none of the tools surface page-by-page progress):

- **Time-based reporter** — `startProgressReporter()` smoothly ramps progress from 20% → 90% over an estimated duration (`estimateConversionTime`, scaled by input file size). Uses an ease-out curve so the bar slows as it nears the cap. Stops on success or failure.
- A real-progress callback path exists in the shared worker but is unused in convert-to-pdf today.

DB updates and `jobs.events.<jobId>.processing` events are emitted from the reporter loop, but only when the computed percentage actually advances — once progress plateaus (e.g. the estimate has elapsed and the bar is pinned at 90%) redundant writes are skipped to reduce DB write amplification. Final state (`completed` / `failed`) is published from `processMessage` after the conversion returns.

## NATS

- **Stream**: `JOBS_DISPATCH` (WorkQueue, 24h)
- **Subject pulled**: `jobs.dispatch.convert-to-pdf`
- **Consumer**: durable `convert-to-pdf`, `AckExplicit`, `MaxDeliver=4`, `AckWait=30m`, `MaxAckPending=2×WORKER_CONCURRENCY`, `BackOff=[10s, 30s, 2m]`
- **Events emitted**: `jobs.events.<jobId>.{processing,completed,failed}` (Interest, 1h retention)
- **DLQ**: `jobs.dlq.convert-to-pdf` (Limits, 7-day retention) — published when `MaxDeliver` is exhausted, then the original message is acked.

## DB Schema (read/write)

This worker writes to `processing_jobs` and `file_metadata` (the same tables owned by job-service — each microservice has its own GORM connection but the underlying schema is shared via Postgres). Updates are scoped to status, progress, failure reason, completion timestamp, and inserting `output` rows in `file_metadata`.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8083` | HTTP server port (internal) |
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `NATS_URL` | **Required** | NATS server URL |
| `S3_ENDPOINT` | **Required** | S3-compatible endpoint for data operations (e.g. `minio:9000`) |
| `S3_ACCESS_KEY` | **Required** | S3 access key |
| `S3_SECRET_KEY` | **Required** | S3 secret key |
| `S3_USE_SSL` | `false` | Use TLS to reach the S3 endpoint |
| `S3_BUCKET_UPLOADS` | `uploads` | Bucket holding job input objects |
| `S3_BUCKET_OUTPUTS` | `outputs` | Bucket receiving job outputs (`jobs/{jobID}/...`) |
| `S3_REGION` | `us-east-1` | Signing region (MinIO ignores it; AWS must match) |
| `WORKER_CONCURRENCY` | `2` | Max concurrent jobs processed in parallel |
| `UNOSERVER_HOST` | `127.0.0.1` | unoserver daemon host |
| `UNOSERVER_PORT` | `2002` | unoserver daemon port |
| `UNOSERVER_INSTANCES` | `WORKER_CONCURRENCY + 1` | Size of the unoserver daemon pool. When unset, auto-sizes to one warm daemon per concurrent job plus a spare so the cold-start fallback is unreachable. Set explicitly to pin. |
| `RESULT_CACHE_TTL_SECONDS` | `3600` | Lifetime of result-cache entries. Keep ≤ outputs bucket TTL. `0` disables caching. |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing (currently honoured via `AckWait` rather than a context deadline in code) |

## Result Caching

Identical conversions are deduplicated. Before downloading inputs, the worker derives a cache key from the tool type, the canonicalised options, and the **content identity of every input** — the latter via each upload object's ETag, fetched with a cheap `StatObject` (no download). The key is looked up in Redis (`rescache:v1:convert-to-pdf:<sha256>`):

- **Hit:** the previously produced output is verified to still exist (it may have been TTL-cleaned), then **server-side copied** (`CopyObject`, no bytes through the worker) to the new job's output key. The download and the LibreOffice/pdfcpu conversion are skipped entirely.
- **Miss:** the job runs normally; on success the output key + metadata are written to Redis with `RESULT_CACHE_TTL_SECONDS`.

Caching is **best-effort**: any cache-path error (Redis down, stat/copy failure, expired output) logs and falls through to a normal conversion, so it can never fail a job. Disabled when Redis is unavailable or `RESULT_CACHE_TTL_SECONDS=0`.

## Dependencies

### LibreOffice + unoserver
Used for Office and HTML conversions. Installed in the container via the `fyredocs-base` image (full LibreOffice suite, ttf-liberation, `unoserver` from PyPI).

Fast path:
```bash
unoconvert --host 127.0.0.1 --port 2002 --convert-to pdf input.docx output.pdf
```

Fallback (when unoserver is unreachable):
```bash
libreoffice --headless --convert-to pdf --outdir /output /input/file.docx
```

### pdfcpu
Pure-Go PDF library used for `image-to-pdf` / `img-to-pdf`. No external runtime dependencies.

## Health & Readiness

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness — pings Redis + checks NATS connection |
| `GET /readyz` | Readiness — Redis + NATS + Postgres + unoserver (informational) |
| `GET /metrics` | Prometheus metrics |

## Sequence Diagrams

### Job processing

```mermaid
sequenceDiagram
    participant JS as job-service
    participant NATS as JOBS_DISPATCH
    participant W as convert-to-pdf worker
    participant DB as PostgreSQL
    participant S3 as MinIO / S3
    participant Tool as unoserver / pdfcpu
    participant EV as jobs.events.&lt;jobId&gt;.*

    JS->>NATS: Publish JobMessage (jobs.dispatch.convert-to-pdf)
    W->>NATS: Pull (batch up to WORKER_CONCURRENCY)
    NATS-->>W: msg

    W->>W: Validate tool against AllowedTools
    alt unsupported
        W->>DB: status='failed', reason '[UNSUPPORTED_TOOL] ...'
        W->>EV: failed
        W->>NATS: Ack (drop)
    else allowed
        W->>DB: SELECT status FROM processing_jobs WHERE id=?
        alt already 'completed'/'processing'
            W->>NATS: Ack (skip duplicate)
        else proceed
            W->>DB: UPDATE status='processing', progress=20
            W->>EV: processing
            W->>W: create scratch dir (job-scoped temp)
            W->>S3: Download InputPaths keys (uploads bucket → scratch/in)
            alt download fails
                W->>NATS: NAK with backoff (recoverable)
            end
            W->>W: startProgressReporter (20→90%, ease-out)

            alt office or html
                W->>Tool: unoconvert (--host UNOSERVER_HOST --port UNOSERVER_PORT)
                alt daemon unreachable
                    W->>Tool: libreoffice --headless (fallback)
                end
            else image-to-pdf / img-to-pdf
                W->>Tool: pdfcpu Import (one image per page)
            end
            Tool-->>W: output file in scratch/out

            W->>W: stop reporter
            alt success
                W->>S3: Upload output (outputs bucket, jobs/<jobId>/<file>)
                alt upload fails
                    W->>NATS: NAK with backoff (recoverable)
                end
                W->>DB: INSERT file_metadata (kind=output, path=object key, size=uploaded bytes)
                W->>DB: status='completed', progress=100
                W->>EV: completed (with uploaded fileSize)
                W->>NATS: Ack
            else failure
                W->>DB: status='failed', reason
                W->>EV: failed
                opt MaxDeliver exhausted
                    W->>NATS: Publish jobs.dlq.convert-to-pdf
                end
                W->>NATS: Ack
            end
            W->>W: remove scratch dir
        end
    end
```

### Health Check

```mermaid
sequenceDiagram
    participant LB as Probe
    participant W as convert-to-pdf :8083
    participant R as Redis
    participant N as NATS

    LB->>W: GET /healthz
    W->>R: PING (2s timeout)
    alt Redis unhealthy
        W-->>LB: 503 {"status":"unhealthy","redis":"<err>"}
    else
        W->>N: Conn.IsConnected()
        alt NATS disconnected
            W-->>LB: 503 {"status":"unhealthy","nats":"disconnected"}
        else
            W-->>LB: 200 {"status":"healthy"}
        end
    end
```

## Error Flows

### Structured Error Codes

Failure reasons use structured prefixes. `classifyError()` categorizes failures automatically.

| Code | Meaning |
|------|---------|
| `UNSUPPORTED_TOOL` | Tool not in this worker's AllowedTools |
| `CONVERSION_FAILED` | Default for unclassified errors |
| `INVALID_PAYLOAD` | Malformed or unparseable job message |
| `OUTPUT_FAILED` | Failed to write or record output file |
| `TIMEOUT` | Processing exceeded deadline |

Example: `[TIMEOUT] context deadline exceeded`

### Retry / DLQ

NATS handles retries via `MaxDeliver=4` (1 initial + 3 retries) with exponential backoff `10s / 30s / 2m`. Permanent failures (invalid input, unsupported tool, missing input file) are acked immediately to prevent infinite retry. Object-storage download/upload failures and scratch-dir creation failures are marked recoverable and NAKed for redelivery. When the delivery count hits 4, the failed payload is published to `jobs.dlq.convert-to-pdf` and the original message is acked.

## Deployment

### Docker Compose

```yaml
convert-to-pdf:
  build:
    context: ./convert-to-pdf
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    NATS_URL: nats://nats:4222
    S3_ENDPOINT: minio:9000
    S3_ACCESS_KEY: minioadmin
    S3_SECRET_KEY: minioadmin
    S3_BUCKET_UPLOADS: uploads
    S3_BUCKET_OUTPUTS: outputs
    WORKER_CONCURRENCY: "2"
  depends_on:
    - db
    - redis
    - nats
    - minio
```

### Local Development

```bash
docker compose up -d db redis nats minio
cd convert-to-pdf
export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
export REDIS_ADDR="localhost:6379"
export NATS_URL="nats://localhost:4222"
export S3_ENDPOINT="localhost:9000"
export S3_ACCESS_KEY="minioadmin"
export S3_SECRET_KEY="minioadmin"
go run main.go
```

(Requires LibreOffice + python `unoserver` installed on the host for full parity with the container image.)

## Related Documentation

- [Convert From PDF](./CONVERT_FROM_PDF.md) — PDF → DOCX/PPTX/JPG/PNG/TXT
- [Organize PDF](./ORGANIZE_PDF.md) — pdfcpu-based merge/split/rotate/extract/watermark/etc.
- [Optimize PDF](./OPTIMIZE_PDF.md) — compress/repair/OCR
- [Job Service](./JOB_SERVICE.md) — Job creation and dispatch
- [API Gateway](./API_GATEWAY.md) — Request routing
- [Error Logging](../architecture/ERROR_LOGGING.md) — Backend-wide error logging convention

## unoserver daemon pool

Office→PDF conversions run via `unoconvert` against a **pool** of unoserver
(LibreOffice) daemons rather than a single one, so conversions run in parallel
instead of serializing on one port.

- The entrypoint launches `UNOSERVER_INSTANCES` daemons (default 2) on
  consecutive ports from `UNOSERVER_PORT` (2002, 2003, …), each with its own
  LibreOffice `--user-installation` profile (required — a shared profile
  conflicts on the lock). `deploy.sh` always **pins** `UNOSERVER_INSTANCES`
  explicitly (see Sizing below), so the entrypoint's daemon count and the Go
  pool's `UnoserverPorts()` always agree — leaving it unset risks the Go pool
  expecting `WORKER_CONCURRENCY + 1` ports while the entrypoint launches only 2.
- `processing.tryUnoconvert` acquires a port from a buffered-channel pool
  (`unopool.go`), bounding concurrent conversions to the pool size and blocking
  when all daemons are busy; the port is released **before** any direct-LibreOffice
  fallback so the fallback never holds a daemon slot.
- `UnoserverPorts()` is the single source of truth shared by the pool and the
  `/healthz` check (healthy if ≥1 daemon port is reachable).
- **Sizing**: handled automatically by `deploy.sh`'s 70% resource budget — it
  derives `UNOSERVER_INSTANCES` from this container's *scaled* memory cap
  (~300 MB per daemon + ~200 MB Go overhead), bounded by its CPU allowance and
  6, then sets `WORKER_CONCURRENCY = instances − 1`. Because the pool is sized
  to fit the cap, the container cannot OOM-kill itself regardless of host size.
  To pin manually, set `UNOSERVER_INSTANCES`/`CONVERT_TO_PDF_CONCURRENCY` in
  `.env` (deploy.sh honors a pre-set value) — but a pin larger than the auto
  size can exceed the memory cap and risk an OOM-kill.

## tmpfs capacity guard

Inputs/outputs are staged on a 1 GiB tmpfs scratch. Before downloading, the
worker sums input object sizes and rejects jobs whose projected footprint
(`inputs × (1 + TMPFS_OUTPUT_FACTOR_PCT/100)`) exceeds `TMPFS_BUDGET_MB`
(default 900), and serializes jobs larger than `LARGE_JOB_THRESHOLD_MB`
(default 100) through a per-pod semaphore so two large jobs never co-occupy the
scratch area. See `internal/worker/tmpfs.go`.

## Support

- Logs: `docker compose logs -f convert-to-pdf`
- Test LibreOffice: `docker compose exec convert-to-pdf libreoffice --version`
- Test unoserver: `docker compose exec convert-to-pdf python -m unoserver.client --help`
- Inspect jobs: query `processing_jobs` in PostgreSQL
- DLQ inspection: `nats consumer info JOBS_DLQ ...`
