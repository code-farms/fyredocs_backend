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
5. **DLQ** — On `MaxDeliver` exhaustion, publish the failed job to `jobs.dlq.convert-to-pdf` (JOBS_DLQ stream, 7-day retention) before acking.

## Architecture

```
NATS JetStream JOBS_DISPATCH (jobs.dispatch.convert-to-pdf)
  ↓ pull-consumer (durable=convert-to-pdf, MaxDeliver=4, AckWait=30m, BackOff 10s/30s/2m)
convert-to-pdf worker
  ├─ Fetch up to WORKER_CONCURRENCY messages (default 2)
  ├─ Per message → goroutine guarded by a semaphore
  │     ├─ duplicate-job guard (skip if already completed/processing)
  │     ├─ DB status → "processing" (progress 20)
  │     ├─ time-based progress reporter (smooth 20→90% with ease-out curve)
  │     ├─ processing.ProcessFile()
  │     │     ├─ word/excel/ppt/html → officeToPDF() → unoconvert (port 2002) — fallback: libreoffice --headless
  │     │     └─ image-to-pdf / img-to-pdf → pdfcpu (multiple images = multi-page output)
  │     ├─ DB status → "completed" + record output FileMetadata
  │     └─ Publish jobs.events.<jobId>.{processing,completed,failed}
  └─ On MaxDeliver exhaustion → publish JobFailed to jobs.dlq.convert-to-pdf
```

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

DB updates and `jobs.events.<jobId>.processing` events are emitted from the reporter loop. Final state (`completed` / `failed`) is published from `processMessage` after the conversion returns.

## NATS

- **Stream**: `JOBS_DISPATCH` (WorkQueue, 24h)
- **Subject pulled**: `jobs.dispatch.convert-to-pdf`
- **Consumer**: durable `convert-to-pdf`, `AckExplicit`, `MaxDeliver=4`, `AckWait=30m`, `BackOff=[10s, 30s, 2m]`
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
| `UPLOAD_DIR` | `uploads` | Input files directory |
| `OUTPUT_DIR` | `outputs` | Output files directory |
| `WORKER_CONCURRENCY` | `2` | Max concurrent jobs processed in parallel |
| `UNOSERVER_HOST` | `127.0.0.1` | unoserver daemon host |
| `UNOSERVER_PORT` | `2002` | unoserver daemon port |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing (currently honoured via `AckWait` rather than a context deadline in code) |

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
    participant Tool as unoserver / pdfcpu
    participant Disk as File System
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
            W->>W: startProgressReporter (20→90%, ease-out)

            alt office or html
                W->>Tool: unoconvert (--host UNOSERVER_HOST --port UNOSERVER_PORT)
                alt daemon unreachable
                    W->>Tool: libreoffice --headless (fallback)
                end
            else image-to-pdf / img-to-pdf
                W->>Tool: pdfcpu Import (one image per page)
            end
            Tool-->>Disk: output file

            W->>W: stop reporter
            alt success
                W->>DB: INSERT file_metadata (kind=output)
                W->>DB: status='completed', progress=100
                W->>EV: completed (with fileSize)
                W->>NATS: Ack
            else failure
                W->>DB: status='failed', reason
                W->>EV: failed
                opt MaxDeliver exhausted
                    W->>NATS: Publish jobs.dlq.convert-to-pdf
                end
                W->>NATS: Ack
            end
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

NATS handles retries via `MaxDeliver=4` (1 initial + 3 retries) with exponential backoff `10s / 30s / 2m`. Permanent failures (invalid input, unsupported tool, missing input file) are acked immediately to prevent infinite retry. When the delivery count hits 4, the failed payload is published to `jobs.dlq.convert-to-pdf` and the original message is acked.

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
    UPLOAD_DIR: /app/uploads
    OUTPUT_DIR: /app/outputs
    WORKER_CONCURRENCY: "2"
  volumes:
    - uploads_data:/app/uploads
    - outputs_data:/app/outputs
  depends_on:
    - db
    - redis
    - nats
```

### Local Development

```bash
docker compose up -d db redis nats
cd convert-to-pdf
export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
export REDIS_ADDR="localhost:6379"
export NATS_URL="nats://localhost:4222"
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

## Support

- Logs: `docker compose logs -f convert-to-pdf`
- Test LibreOffice: `docker compose exec convert-to-pdf libreoffice --version`
- Test unoserver: `docker compose exec convert-to-pdf python -m unoserver.client --help`
- Inspect jobs: query `processing_jobs` in PostgreSQL
- DLQ inspection: `nats consumer info JOBS_DLQ ...`
