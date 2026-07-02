# Organize-PDF Service

A microservice for PDF organization operations including merging, splitting, removing/extracting pages, reordering, and scanning documents to PDF.

## Overview

The Organize-PDF service provides comprehensive PDF manipulation capabilities using free open-source tools. It's part of the Fyredocs microservices architecture and handles all PDF organization operations.

**Port:** 8084
**Bus:** NATS JetStream — pulls from `jobs.dispatch.organize-pdf`, publishes events to `jobs.events.<jobId>.*`, DLQ on `jobs.dlq.organize-pdf`
**Engine:** [pdfcpu](https://github.com/pdfcpu/pdfcpu) (pure Go, no LibreOffice in this container)
**OCR (optional):** Tesseract — used by `scan-to-pdf` when `options.ocr = true`

## Supported Operations

### 1. Merge PDF
Combines multiple PDF files into a single document.

**Tool:** `merge-pdf`
**Input:** Multiple PDF files
**Output:** Single merged PDF file

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/merge-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document1.pdf" \
  -F "files=@document2.pdf" \
  -F "files=@document3.pdf"
```

### 2. Split PDF
Splits a PDF into individual pages or specified page ranges.

**Tool:** `split-pdf`
**Input:** Single PDF file
**Output:** ZIP archive containing individual PDF pages
**Options:**
- `range`: Page range (e.g., "1-3,5,7-9" or "all")

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/split-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document.pdf" \
  -F 'options={"range":"1-5,10"}'
```

### 3. Remove Pages
Removes specified pages from a PDF document.

**Tool:** `remove-pages`
**Input:** Single PDF file
**Output:** PDF file without specified pages
**Options:**
- `pages`: Pages to remove (e.g., "2,4,6-8")

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/remove-pages \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document.pdf" \
  -F 'options={"pages":"2,4,6"}'
```

### 4. Extract Pages
Extracts specified pages into a new PDF document.

**Tool:** `extract-pages`
**Input:** Single PDF file
**Output:** PDF file containing only extracted pages
**Options:**
- `pages`: Pages to extract (e.g., "1,3,5-7")

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/extract-pages \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document.pdf" \
  -F 'options={"pages":"1,3,5-7"}'
```

### 5. Organize PDF
Reorders pages in a PDF according to a specified order.

**Tool:** `organize-pdf`
**Input:** Single PDF file
**Output:** PDF file with reordered pages
**Options:**
- `order`: New page order (e.g., "3,1,2,4")

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/organize-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document.pdf" \
  -F 'options={"order":"3,1,2,4,5"}'
```

### 6. Rotate PDF

Rotates pages in a PDF by a specified angle, with optional page selection.

**Tool:** `rotate-pdf`
**Input:** Single PDF file
**Output:** PDF file with rotated pages
**Options:**
- `rotation`: Rotation angle — `90`, `180`, or `270` degrees (required)
- `applyToPages`: Which pages to rotate — `all`, `odd`, or `even` (default: `all`)

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/rotate-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@document.pdf" \
  -F 'options={"rotation":90,"applyToPages":"all"}'
```

### 7. Scan to PDF
Converts images to PDF (similar to scanning documents).

**Tool:** `scan-to-pdf`
**Input:** One or more image files (JPG, PNG, etc.)
**Output:** PDF document
**Options:**
- `ocr`: Enable OCR for searchable PDF (boolean, requires tesseract)

**Example:**
```bash
curl -X POST http://localhost:8080/api/organize-pdf/scan-to-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@scan1.jpg" \
  -F "files=@scan2.jpg" \
  -F 'options={"ocr":true}'
```

### 8. Watermark PDF

Adds a text or image watermark to every page.

**Tool:** `watermark-pdf`
**Input:** Single PDF file
**Output:** PDF with watermark applied
**Options:**
- `type`: `"text"` (default) or `"image"`
- `text`: watermark text (when `type=text`, default `"CONFIDENTIAL"`)
- `imageData`: base64 data URL of the watermark image (when `type=image`)
- `position`: `"center"`, `"diagonal"` (default), or `"tiled"`
- `opacity`: 10–100 (default 50)
- `fontSize`: 12–120 (default 48; text watermarks)
- `color`: hex string (default `"#6366f1"`; text watermarks)

### 9. Protect PDF

Encrypts a PDF with a password.

**Tool:** `protect-pdf`
**Input:** Single PDF file
**Output:** Password-protected PDF
**Options:**
- `password`: required, ≥4 characters

### 10. Unlock PDF

Removes the password from an encrypted PDF.

**Tool:** `unlock-pdf`
**Input:** Single password-protected PDF
**Output:** Unprotected PDF
**Options:**
- `password`: required (existing password)

### 11. Sign PDF

Stamps a signature image onto a specific page.

**Tool:** `sign-pdf`
**Input:** Single PDF file
**Output:** PDF with image stamp
**Options:**
- `imageData`: base64 data URL of the signature image
- `page`: 1-indexed page number (default 1)
- `position`: `"top-left"`, `"top-right"`, `"bottom-left"`, `"bottom-right"`, `"center"` (default)

### 12. Edit PDF

Adds free-form text annotations to a PDF.

**Tool:** `edit-pdf`
**Input:** Single PDF file
**Output:** Modified PDF
**Options:**
- `text`: annotation text
- `page`: 1-indexed page number
- `x` / `y`: coordinates in PDF user space
- `fontSize` / `color`

### 13. Add Page Numbers

Adds page-number stamps to every page.

**Tool:** `add-page-numbers`
**Input:** Single PDF file
**Output:** PDF with page numbers
**Options:**
- `position`: top/bottom + left/center/right
- `format`: e.g. `"%d / %d"` (current / total)
- `fontSize` / `color`

## API Endpoints

All endpoints follow RESTful conventions:

### Job Creation
```
POST /api/organize-pdf/:tool
```
Creates a new processing job for the specified tool.

**Parameters:**
- `:tool` — One of: `merge-pdf`, `split-pdf`, `rotate-pdf`, `remove-pages`, `extract-pages`, `organize-pdf`, `scan-to-pdf`, `watermark-pdf`, `protect-pdf`, `unlock-pdf`, `sign-pdf`, `edit-pdf`, `add-page-numbers` (13 total — see `main.go:59` `AllowedTools`)
- Form data: `files` (multipart/form-data) **or** JSON body with `uploadId`/`uploadIds`
- Form data: `options` (JSON string, optional, tool-specific)

**Response:**
```json
{
  "id": "uuid",
  "toolType": "merge-pdf",
  "status": "pending",
  "progress": "0",
  "fileName": "merged.pdf",
  "fileSize": "1234.56 KB",
  "createdAt": "2026-01-19T00:00:00Z",
  "metadata": {}
}
```

### List Jobs
```
GET /api/organize-pdf/:tool
```
Lists all jobs for a specific tool.

### Get Job Details
```
GET /api/organize-pdf/:tool/:id
```
Retrieves details for a specific job.

### Update Job
```
PATCH /api/organize-pdf/:tool/:id
```
Updates job status or progress (typically used by workers).

### Delete Job
```
DELETE /api/organize-pdf/:tool/:id
```
Deletes a job and its associated data.

### Download Result
```
GET /api/organize-pdf/:tool/:id/download
```
Downloads the processed file once the job is completed.

## Job Status Flow

1. **pending** - Job created, waiting in queue
2. **processing** - Worker is processing the job
3. **completed** - Processing finished successfully
4. **failed** - Processing failed (see failureReason)

## Environment Variables

### Database & Redis
```env
DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs?sslmode=disable"
REDIS_ADDR="localhost:6379"
REDIS_PASSWORD=""
REDIS_DB="0"
```

### Service Configuration
```env
PORT="8084"
NATS_URL="nats://nats:4222"
PROCESSING_TIMEOUT="5m"    # honoured via NATS AckWait (pdfcpu ops are fast)
WORKER_CONCURRENCY="4"     # parallel jobs per container (semaphore-bounded goroutines)
RESULT_CACHE_TTL_SECONDS="3600"  # result-cache entry TTL (keep <= outputs bucket TTL); 0 disables
```

### Object Storage (S3 / MinIO)
```env
S3_ENDPOINT="minio:9000"               # required
S3_ACCESS_KEY="minioadmin"             # required
S3_SECRET_KEY="minioadmin"             # required
S3_USE_SSL="false"
S3_BUCKET_UPLOADS="uploads"   # job inputs (object keys in payload.inputPaths)
S3_BUCKET_OUTPUTS="outputs"   # job outputs, stored as jobs/{jobID}/...
S3_REGION="us-east-1"
```

> `OUTPUT_DIR` has been removed — outputs no longer live on a shared volume. The worker downloads inputs to a container-local scratch directory, processes them there, and uploads the result to the outputs bucket.

### JWT Authentication
```env
JWT_ALLOWED_ALGS="HS256"
JWT_HS256_SECRET="your-secret-here-min-32-chars"
JWT_ISSUER="fyredocs"
JWT_AUDIENCE="fyredocs-api"
JWT_CLOCK_SKEW="60s"
```

### Auth Settings
```env
AUTH_GUEST_PREFIX="guest"
AUTH_GUEST_SUFFIX="jobs"
AUTH_DENYLIST_ENABLED="true"
AUTH_DENYLIST_PREFIX="denylist:jwt"
AUTH_TRUST_GATEWAY_HEADERS="false"
```

## Result Caching

Identical jobs are deduplicated. Before downloading inputs, the worker derives a cache key from the tool type, the canonicalised options, and the **content identity of every input** — the latter via each upload object's ETag, fetched with a cheap `StatObject` (no download). The key is looked up in Redis (`rescache:v1:organize-pdf:<sha256>`):

- **Hit:** the previously produced output is verified to still exist (it may have been TTL-cleaned), then **server-side copied** (`CopyObject`, no bytes through the worker) to the new job's output key. The download and the pdfcpu operation are skipped entirely.
- **Miss:** the job runs normally; on success the output key + metadata are written to Redis with `RESULT_CACHE_TTL_SECONDS`.

Caching is **best-effort**: any cache-path error (Redis down, stat/copy failure, expired output) logs and falls through to a normal run, so it can never fail a job. Disabled when Redis is unavailable or `RESULT_CACHE_TTL_SECONDS=0`.

## Dependencies

### Go Packages
- **github.com/pdfcpu/pdfcpu** - PDF manipulation library
- **github.com/gin-gonic/gin** - Web framework
- **gorm.io/gorm** - Database ORM
- **github.com/redis/go-redis** - Redis client
- **github.com/golang-jwt/jwt** - JWT handling

### System Dependencies
- **poppler-utils** - PDF utilities (pdftoppm, pdftotext)
- **tesseract-ocr** - Optional OCR support for scan-to-pdf

## Architecture

### Components

1. **API Handlers** - HTTP request handling
2. **Job Queue** - Redis-based async processing
3. **Worker** - Background job processing
4. **Processing Functions** - PDF operations using pdfcpu
5. **Database** - Job persistence (PostgreSQL)

### Processing Flow

```
Client Request
  ↓
API Gateway → Job Service (creates ProcessingJob in Postgres)
  ↓
NATS JetStream (jobs.dispatch.organize-pdf) — WorkQueue
  ↓
organize-pdf worker pull-consumer (durable=organize-pdf · MaxDeliver=4 · AckWait=5m · MaxAckPending=2×concurrency)
  ↓
Fetch up to WORKER_CONCURRENCY messages (default 4); jobs run in parallel semaphore-bounded goroutines
  ↓
Create scratch dir (os.MkdirTemp "job-<jobId>-*"); download inputPaths keys
from S3 uploads bucket → <scratch>/in/ (failure → recoverable NAK + backoff)
  ↓
processing.ProcessFile() — dispatches to pdfcpu helpers (local files, output to <scratch>/out)
  ↓
Upload output → S3 outputs bucket jobs/<jobId>/<file> (failure → recoverable NAK + backoff)
  ↓
DB → status=completed, INSERT file_metadata (path=object key, size_bytes=uploaded size)
  ↓
Publish jobs.events.<jobId>.{processing,completed,failed}; scratch dir removed
```

### Object Storage

Inputs and outputs live in S3-compatible object storage (MinIO in compose), not on a shared volume:

- `payload.inputPaths` carries **object keys** in the uploads bucket. The worker downloads each key to `<scratch>/in/<basename>` before processing, because pdfcpu/Tesseract need real local files.
- The output is uploaded to the outputs bucket as `jobs/{jobID}/{filename}` with an extension-derived `Content-Type`; `file_metadata.path` stores the object key and `size_bytes` the uploaded size.
- The scratch directory is deleted after every job (success or failure). Download/upload failures are treated as recoverable and retried via NAK + backoff.

## Docker Deployment

### Build
```bash
docker build -t organize-pdf:latest .
```

### Run
```bash
docker run -d \
  -p 8084:8084 \
  -e DATABASE_URL="postgresql://..." \
  -e REDIS_ADDR="redis:6379" \
  -e JWT_HS256_SECRET="your-secret" \
  -e S3_ENDPOINT="minio:9000" \
  -e S3_ACCESS_KEY="minioadmin" \
  -e S3_SECRET_KEY="minioadmin" \
  organize-pdf:latest
```

### Docker Compose
```yaml
organize-pdf:
  build: ./organize-pdf
  ports:
    - "8084:8084"
  environment:
    PORT: "8084"
    DATABASE_URL: "postgresql://..."
    REDIS_ADDR: "redis:6379"
    S3_ENDPOINT: "minio:9000"
    S3_ACCESS_KEY: "minioadmin"
    S3_SECRET_KEY: "minioadmin"
    S3_BUCKET_UPLOADS: "uploads"
    S3_BUCKET_OUTPUTS: "outputs"
```

## Development

### Local Setup
```bash
cd fyredocs_backend/organize-pdf

# Install dependencies
go mod download

# Copy environment file
cp .env.example .env

# Edit .env with your configuration
nano .env

# Run the service
go run .
```

### Build Binary
```bash
go build -o organize-pdf
./organize-pdf
```

## Testing

### Health Check
```bash
curl http://localhost:8084/healthz
# Expected: ok
```

### Create Test Job
```bash
# Merge PDFs
curl -X POST http://localhost:8080/api/organize-pdf/merge-pdf \
  -H "Authorization: Bearer $TOKEN" \
  -F "files=@test1.pdf" \
  -F "files=@test2.pdf"
```

### Check Job Status
```bash
curl http://localhost:8080/api/organize-pdf/merge-pdf/$JOB_ID \
  -H "Authorization: Bearer $TOKEN"
```

### Download Result
```bash
curl -O http://localhost:8080/api/organize-pdf/merge-pdf/$JOB_ID/download \
  -H "Authorization: Bearer $TOKEN"
```

## Error Handling

### Retry Logic
- Failed jobs automatically retry up to MAX_RETRIES times
- Recoverable errors (network issues, timeouts) trigger retries
- Non-recoverable errors (invalid input) fail immediately

### Structured Error Codes

Failure reasons use structured error codes prefixed in brackets. The `classifyError()` function categorizes failures automatically.

| Code | Meaning |
|------|---------|
| `UNSUPPORTED_TOOL` | Tool type not handled by this service |
| `CONVERSION_FAILED` | Processing failed (default for unclassified errors) |
| `INVALID_PAYLOAD` | Malformed or unparseable job message |
| `OUTPUT_FAILED` | Failed to write or record output file |
| `TIMEOUT` | Processing exceeded deadline |

Example: `[TIMEOUT] context deadline exceeded`

### Common Errors
- **"no file uploaded"** - No files provided in request
- **"unsupported tool"** - Invalid tool type
- **"failed to read PDF"** - Corrupted or invalid PDF file
- **"invalid page range"** - Page range format error
- **"missing X option"** - Required option not provided

## Performance Considerations

### Processing Limits
- **Timeout:** 30 minutes per job (configurable)
- **File Size:** Limited by available memory
- **Concurrent Jobs:** Multiple workers can process jobs in parallel

### Optimization Tips
1. Use appropriate page ranges for split operations
2. Merge smaller batches of PDFs for better performance
3. Enable worker scaling for high load

## Security

### Authentication
- All endpoints require JWT or guest token
- Guest tokens stored in Redis with TTL
- Token revocation supported via denylist

### File Isolation
- Each job has unique upload directory
- Files automatically cleaned up after download
- Output files expire after 1 hour

### Input Validation
- Page ranges validated against PDF page count
- File types verified before processing
- Options sanitized to prevent injection

## Monitoring

### Metrics
- Job completion rates
- Processing times
- Failure rates by tool type
- Queue depth

### Logs
```bash
# View service logs
docker logs -f organize-pdf

# View worker processing
docker logs -f organize-pdf | grep "job completed"
```

### NATS Inspection
```bash
# Pending dispatch
nats stream view JOBS_DISPATCH --filter jobs.dispatch.organize-pdf

# DLQ
nats stream view JOBS_DLQ --filter jobs.dlq.organize-pdf
```

## Troubleshooting

### Service Won't Start
1. Check database connection
2. Verify Redis and NATS are running
3. Ensure JWT_HS256_SECRET is set
4. Check port 8084 is available

### Jobs Stuck in Pending
1. Verify worker is running (`docker compose logs organize-pdf`)
2. Inspect dispatch stream: `nats stream view JOBS_DISPATCH --filter jobs.dispatch.organize-pdf`
3. Review worker logs for errors

### Processing Failures
1. Check file format compatibility
2. Verify page ranges are valid
3. Ensure sufficient disk space
4. Check worker timeout settings

## Sequence Diagrams

### Job Processing Flow (NATS Worker)

```mermaid
sequenceDiagram
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant DB as PostgreSQL
    participant S3 as MinIO / S3
    participant PC as pdfcpu

    JS->>NATS: Publish JobCreated to jobs.dispatch.organize-pdf
    NATS->>W: Deliver JobCreated event

    W->>W: Validate toolType in AllowedTools
    W->>DB: UPDATE processing_jobs SET status='processing'

    W->>W: Create scratch dir (job-scoped temp)
    W->>S3: Download inputPaths keys (uploads bucket → scratch/in)
    alt Download fails
        W->>NATS: NAK with backoff (recoverable)
    end

    alt merge-pdf
        W->>PC: pdfcpu merge [file1.pdf, file2.pdf, ...] -> merged.pdf
    else split-pdf
        W->>PC: pdfcpu split input.pdf by page range → ZIP
    else remove-pages
        W->>PC: pdfcpu remove pages [2,4,6]
    else extract-pages
        W->>PC: pdfcpu CollectFile pages [1,3,5-7]
    else rotate-pdf
        W->>PC: pdfcpu rotate pages by 90/180/270 (all/odd/even)
    else organize-pdf (reorder)
        W->>PC: pdfcpu reorder pages [3,1,2,4]
    else scan-to-pdf
        W->>PC: pdfcpu import images to PDF (+ optional Tesseract OCR layer)
    else watermark-pdf
        W->>PC: pdfcpu add text/image watermark
    else protect-pdf
        W->>PC: pdfcpu encrypt with password
    else unlock-pdf
        W->>PC: pdfcpu decrypt with password
    else sign-pdf
        W->>PC: pdfcpu image stamp on selected page
    else edit-pdf
        W->>PC: pdfcpu text stamp at coordinates
    else add-page-numbers
        W->>PC: pdfcpu add page numbers (per template)
    end

    alt Processing succeeds
        PC-->>W: Output file produced in scratch/out
        W->>S3: Upload output (outputs bucket, jobs/<jobId>/<file>)
        alt Upload fails
            W->>NATS: NAK with backoff (recoverable)
        end
        W->>DB: INSERT file_metadata (kind='output', path=object key, size=uploaded bytes)
        W->>DB: UPDATE processing_jobs SET status='completed', progress=100
        W->>NATS: Ack message
    else Processing fails
        W->>DB: UPDATE processing_jobs SET status='failed', failure_reason=error
        W->>NATS: Ack message
    end
    W->>W: Remove scratch dir
```

### Merge PDF Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant S3 as MinIO / S3

    C->>JS: POST /api/organize-pdf/merge-pdf (files: [doc1.pdf, doc2.pdf, doc3.pdf])
    JS->>S3: Store uploads in uploads bucket
    JS->>NATS: Publish JobCreated {inputPaths: [object keys]}

    NATS->>W: Deliver JobCreated
    W->>S3: Download all input PDFs → scratch/in
    W->>W: pdfcpu.MergeCreateFile(local inputs, scratch/out/merged.pdf)
    W->>S3: Upload merged.pdf → outputs bucket jobs/<jobId>/merged.pdf
    W-->>W: Job completed (scratch removed)
```

### Split PDF Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant S3 as MinIO / S3

    C->>JS: POST /api/organize-pdf/split-pdf {files: [doc.pdf], options: {range: "1-3,5"}}
    JS->>NATS: Publish JobCreated {inputPaths: [object key], options: {range: "1-3,5"}}

    NATS->>W: Deliver JobCreated
    W->>S3: Download input PDF → scratch/in
    W->>W: Parse page range "1-3,5"
    W->>W: Extract pages 1, 2, 3, 5 into individual PDFs
    W->>W: Create ZIP archive in scratch/out
    W->>S3: Upload output.zip → outputs bucket jobs/<jobId>/output.zip
    W-->>W: Job completed (scratch removed)
```

### Health Check Flow

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant W as organize-pdf :8084
    participant R as Redis
    participant N as NATS

    LB->>W: GET /healthz
    W->>R: PING (2s timeout)
    alt Redis unhealthy
        W-->>LB: 503 {"status": "unhealthy", "redis": "error"}
    end
    W->>N: Check connection status
    alt NATS disconnected
        W-->>LB: 503 {"status": "unhealthy", "nats": "disconnected"}
    else All healthy
        W-->>LB: 200 {"status": "healthy"}
    end
```

### Readiness Probe

`/readyz` -- Readiness check (PostgreSQL + Redis + NATS), returns 200/503 with individual check results. Unlike `/healthz` (liveness), `/readyz` verifies all dependencies are connected.

## Error Flows (Detailed)

### Processing Error Matrix

| Error Type | Tool(s) Affected | Handling | Retry |
|------------|-----------------|----------|-------|
| Invalid tool type | All | Reject, status=failed | No |
| Input file missing | All | status=failed | No |
| Corrupted PDF | All | pdfcpu returns error, status=failed | No |
| Invalid page range | split, remove, extract, organize | status=failed, "invalid page range" | No |
| Page number out of bounds | split, remove, extract, organize | status=failed | No |
| Missing required option | remove, extract, organize | status=failed, "missing X option" | No |
| Empty merge (no files) | merge-pdf | status=failed, "no files" | No |
| Disk full | All | status=failed | No |
| Worker crash | All | NATS redelivery (MaxDeliver) | Yes |
| Database failure | All | Message not acked, NATS redelivery | Yes |

### NATS Redelivery

NATS JetStream handles retries via `AckWait` and `MaxDeliver`:
- Transient failures (worker crash, DB timeout) trigger redelivery
- Permanent failures (invalid input, corrupted PDF) are acked to prevent infinite retry

When retries are exhausted (MaxDeliver reached), the failed job payload is published to `jobs.dlq.organize-pdf` on the `JOBS_DLQ` stream (7-day retention) before the original message is acknowledged. This preserves failed jobs for debugging and replay.

## Future Enhancements

- Page thumbnails for preview
- Batch processing for multiple documents
- Advanced OCR with multi-language support
- PDF metadata editing
- Watermarking support
- PDF compression options

## License

Part of the Fyredocs project.

## tmpfs capacity guard

Before downloading, the worker sums input object sizes and rejects jobs whose
projected footprint (`inputs × (1 + TMPFS_OUTPUT_FACTOR_PCT/100)`) exceeds
`TMPFS_BUDGET_MB` (default 900, under the 1 GiB tmpfs), and serializes jobs
larger than `LARGE_JOB_THRESHOLD_MB` (default 100) through a per-pod semaphore
so two large jobs never co-occupy the scratch area. See
`internal/worker/tmpfs.go`.

## Support

For issues and questions:
- Check logs: `docker logs organize-pdf`
- Review queue status: `redis-cli`
- Verify database: Check processing_jobs table
