# Optimize PDF Service

PDF optimization microservice providing compression, repair, and OCR capabilities.

## Overview

| Property | Value |
|----------|-------|
| Port | 8085 |
| Route Prefix | `/api/optimize-pdf` |
| Bus | NATS JetStream — pulls from `jobs.dispatch.optimize-pdf` (durable consumer), publishes events to `jobs.events.<jobId>.*`, DLQ `jobs.dlq.optimize-pdf` |
| Engines | Ghostscript (`compress-pdf`, `repair-pdf`), Tesseract + Poppler `pdftoppm` (`ocr-pdf`) |
| Allowed tools | `compress-pdf`, `repair-pdf`, `ocr-pdf` (3 — see `optimize-pdf/main.go:59`) |

## Supported Operations

### 1. Compress PDF (`compress-pdf`)
Reduces PDF file size using Ghostscript optimization.

**Options:**
- `quality`: Compression level (frontend names → Ghostscript settings)
  - `low` - Light compression (`/printer`, 300dpi)
  - `medium` - Balanced compression (`/ebook`, 150dpi) [default]
  - `high` - Aggressive compression (`/ebook`, 72dpi, forced downsampling, JPEG quality reduction)
  - `extreme` - Maximum compression (`/screen`, 36dpi, forced downsampling, JPEG quality reduction, grayscale conversion)

**Example:**
```bash
curl -X POST http://localhost:8080/api/optimize-pdf/compress-pdf \
  -F "files=@large-document.pdf" \
  -F 'options={"quality":"ebook"}'
```

### 2. Repair PDF (`repair-pdf`)
Fixes corrupted or damaged PDFs using Ghostscript to rebuild the PDF structure.

**Example:**
```bash
curl -X POST http://localhost:8080/api/optimize-pdf/repair-pdf \
  -F "files=@corrupted-document.pdf"
```

### 3. OCR PDF (`ocr-pdf`)
Adds a searchable text layer to scanned PDFs using Tesseract OCR.

**Options:**
- `language`: OCR language code (default: `eng`)
- `dpi`: Resolution for conversion (default: `300`)

**Example:**
```bash
curl -X POST http://localhost:8080/api/optimize-pdf/ocr-pdf \
  -F "files=@scanned-document.pdf" \
  -F 'options={"language":"eng","dpi":"300"}'
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/:tool` | Create optimization job |
| GET | `/:tool` | List jobs by tool type |
| GET | `/:tool/:id` | Get job status |
| PATCH | `/:tool/:id` | Update job |
| DELETE | `/:tool/:id` | Delete job |
| GET | `/:tool/:id/download` | Download result |

## Job Status Flow

```
pending → processing → completed
                    ↘ failed
```

## Environment Variables

```env
# Database
DATABASE_URL="postgresql://user:password@db:5432/fyredocs?sslmode=disable"

# Redis
REDIS_ADDR="redis:6379"
REDIS_PASSWORD=""
REDIS_DB="0"

# Processing
NATS_URL="nats://nats:4222"
PROCESSING_TIMEOUT="30m"   # honoured via NATS AckWait
PORT="8085"
WORKER_CONCURRENCY="2"     # parallel jobs per container (semaphore-bounded goroutines; OCR also parallelizes pages internally)

# Object storage (S3 / MinIO) — OUTPUT_DIR has been removed; inputs are
# downloaded from the uploads bucket to a container-local scratch dir and
# outputs are uploaded to the outputs bucket under jobs/{jobID}/...
S3_ENDPOINT="minio:9000"               # required
S3_ACCESS_KEY="minioadmin"             # required
S3_SECRET_KEY="minioadmin"             # required
S3_USE_SSL="false"
S3_BUCKET_UPLOADS="fyredocs-uploads"
S3_BUCKET_OUTPUTS="fyredocs-outputs"
S3_REGION="us-east-1"

# JWT
JWT_HS256_SECRET="..."
JWT_ALLOWED_ALGS="HS256"
JWT_ISSUER="fyredocs"
JWT_AUDIENCE="fyredocs-api"

# Auth
AUTH_GUEST_PREFIX="guest"
AUTH_GUEST_SUFFIX="jobs"
AUTH_DENYLIST_ENABLED="true"

# OCR
OCR_DEFAULT_LANGUAGE="eng"
OCR_DEFAULT_DPI="300"
```

## Dependencies

### Go Packages
- `github.com/gin-gonic/gin` - HTTP framework
- `github.com/pdfcpu/pdfcpu` - PDF manipulation
- `gorm.io/gorm` - ORM
- `github.com/redis/go-redis/v9` - Redis client
- `golang.org/x/sync/errgroup` - Parallel OCR worker pool

### System Dependencies (Alpine)
- `poppler-utils` - PDF to image conversion (pdftoppm)
- `ghostscript` - PDF repair and rebuild
- `tesseract-ocr` - OCR engine
- `tesseract-ocr-data-eng` - English language data

## Architecture

```
┌─────────────────┐
│   API Gateway   │
│   (port 8080)   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Optimize PDF   │
│   (port 8085)   │
├─────────────────┤
│ ┌─────────────┐ │
│ │  Handlers   │ │
│ └──────┬──────┘ │
│        │        │
│ ┌──────▼──────┐ │
│ │  Processing │ │
│ │  - Compress │ │
│ │  - Repair   │ │
│ │  - OCR      │ │
│ └──────┬──────┘ │
│        │        │
│ ┌──────▼──────┐ │
│ │   Worker    │ │
│ └─────────────┘ │
└────────┬────────┘
         │
    ┌────┼─────────┐
    ▼    ▼         ▼
┌───────┐ ┌────────┐ ┌────────────┐
│ Redis │ │Postgres│ │ MinIO / S3 │
└───────┘ └────────┘ └────────────┘
```

### Object Storage Flow

Inputs and outputs live in S3-compatible object storage (MinIO in compose), not on a shared volume:

1. `payload.inputPaths` carries **object keys** in the uploads bucket. The worker creates a job-scoped scratch dir (`os.MkdirTemp "job-<jobId>-*"`) and downloads each key to `<scratch>/in/<basename>` — Ghostscript/Tesseract/pdftoppm need real local files.
2. Processing writes its result to `<scratch>/out`.
3. The output is uploaded to the outputs bucket as `jobs/{jobID}/{filename}` with an extension-derived `Content-Type`; `file_metadata.path` stores the object key and `size_bytes` the uploaded size.
4. The scratch dir is removed after every job. Download/upload failures are marked recoverable and retried via NAK + backoff.

## Docker

### Build
```bash
docker build -t optimize-pdf:latest .
```

### Test Dependencies
```bash
docker run --rm optimize-pdf:latest sh -c "
  gs --version && echo 'Ghostscript: OK'
  tesseract --version && echo 'Tesseract: OK'
  pdftoppm -v 2>&1 | head -1 && echo 'Poppler: OK'
"
```

## Development

### Local Setup
```bash
cd fyredocs_backend/optimize-pdf
cp .env.example .env
go mod tidy
go run .
```

### Health Check
```bash
curl http://localhost:8085/healthz
```

## Sequence Diagrams

### Job Processing Flow (NATS Worker)

```mermaid
sequenceDiagram
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as optimize-pdf Worker
    participant DB as PostgreSQL
    participant S3 as MinIO / S3
    participant T as Tool (pdfcpu/GS/Tesseract)

    JS->>NATS: Publish JobCreated to dispatch.optimize-pdf
    NATS->>W: Deliver JobCreated event

    W->>W: Validate toolType in AllowedTools
    W->>DB: UPDATE processing_jobs SET status='processing'

    W->>W: Create scratch dir (job-scoped temp)
    W->>S3: Download inputPaths keys (uploads bucket → scratch/in)
    alt Download fails
        W->>NATS: NAK with backoff (recoverable)
    end

    alt compress-pdf
        W->>T: pdfcpu.OptimizeFile(input, output, config)
    else repair-pdf
        W->>T: gs -dBATCH -dNOPAUSE -sDEVICE=pdfwrite -sOutputFile=output input
    else ocr-pdf
        W->>T: pdftoppm (PDF to images)
        W->>T: tesseract (OCR each image to PDF)
        W->>T: pdfcpu merge (combine pages)
    end

    alt Processing succeeds
        T-->>W: Output file produced in scratch/out
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

### OCR PDF Multi-Step Flow

```mermaid
sequenceDiagram
    participant W as Worker
    participant FS as Scratch Dir (container-local)
    participant PP as pdftoppm
    participant Pool as errgroup Pool (N workers)
    participant TS as Tesseract
    participant PC as pdfcpu

    W->>FS: Read input PDF (downloaded to scratch/in)
    W->>PP: Convert PDF pages to PNG images (configurable DPI)
    PP-->>FS: page1.png, page2.png, ...

    W->>Pool: Launch parallel OCR (capped at NumCPU, max 4)
    par Parallel OCR
        Pool->>TS: tesseract page_1.png ... pdf
        Pool->>TS: tesseract page_2.png ... pdf
        Pool->>TS: tesseract page_N.png ... pdf
    end
    TS-->>FS: page_N.pdf (searchable PDF pages)

    W->>PC: Merge all page PDFs into final output
    PC-->>FS: scratch/out/output.pdf (searchable PDF)
    W->>FS: Cleanup temporary PNG and individual PDF files
    W-->>W: Return outputPath = scratch/out/output.pdf (then uploaded to outputs bucket)
```

### Compress PDF Flow

```mermaid
sequenceDiagram
    participant W as Worker
    participant FS as Scratch Dir (container-local)
    participant GS as Ghostscript

    W->>FS: Read input PDF (downloaded to scratch/in)
    W->>W: Parse quality option (low/medium/high/extreme)
    W->>W: Build Ghostscript args (DPI, threshold, QFactor, grayscale)
    W->>GS: Execute gs with compression args
    GS->>GS: Downsample images to target DPI
    GS->>GS: Compress embedded fonts
    GS->>GS: Apply JPEG quality (high/extreme only)
    GS->>GS: Convert to grayscale (extreme only)
    GS-->>FS: Compressed scratch/out/output.pdf
    W->>FS: Compare file sizes (log compression ratio)
    W-->>W: Return outputPath + metadata (output then uploaded to outputs bucket)
```

### Health Check Flow

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant W as optimize-pdf :8085
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

## Error Flows

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

### Processing Error Matrix

| Error Type | Tool(s) Affected | Handling | Retry |
|------------|-----------------|----------|-------|
| Invalid tool type | All | Reject, status=failed | No |
| Input file missing | All | status=failed | No |
| Corrupted PDF | compress, repair | Tool returns error, status=failed | No |
| Ghostscript failure | repair-pdf | status=failed with stderr | No |
| Tesseract not installed | ocr-pdf | status=failed | No |
| Invalid language code | ocr-pdf | Fallback to "eng" or status=failed | No |
| No text found by OCR | ocr-pdf | Returns PDF anyway (empty text layer) | No |
| Disk full | All | status=failed | No |
| Worker crash | All | NATS redelivery (MaxDeliver) | Yes |
| Database failure | All | Message not acked, NATS redelivery | Yes |

### NATS Redelivery

NATS JetStream handles retries via `AckWait` and `MaxDeliver`:
- Transient failures (worker crash, DB timeout) trigger redelivery
- Permanent failures (invalid input, missing tools) are acked to prevent infinite retry

When retries are exhausted (MaxDeliver reached), the failed job payload is published to `jobs.dlq.optimize-pdf` on the `JOBS_DLQ` stream (7-day retention) before the original message is acknowledged. This preserves failed jobs for debugging and replay.

## Processing Details

### Compress PDF
Uses Ghostscript with quality-differentiated settings:
- **low/medium**: Standard `-dPDFSETTINGS` with default downsample threshold (1.5x)
- **high**: Forced downsample threshold (1.0x) + JPEG quality reduction via `setdistillerparams` (QFactor 0.76)
- **extreme**: Forced downsample threshold (1.0x) + aggressive JPEG quality (QFactor 2.4) + grayscale conversion (`-dColorConversionStrategy=/Gray`)
- All levels: duplicate image detection, font compression, image downsampling

### Repair PDF
Uses Ghostscript to rebuild the PDF:
1. Reads the damaged PDF
2. Reconstructs the object structure
3. Rebuilds cross-reference tables
4. Outputs a valid PDF

### OCR PDF
Multi-step process:
1. Convert PDF pages to PNG images (pdftoppm)
2. Run Tesseract OCR on pages **in parallel** (errgroup worker pool, capped at `min(NumCPU, 4)`)
3. Generate searchable PDF pages
4. Merge all pages into final PDF (pdfcpu)

## tmpfs capacity guard

OCR and compression are scratch-heavy. Before downloading, the worker sums input
object sizes and rejects jobs whose projected footprint
(`inputs × (1 + TMPFS_OUTPUT_FACTOR_PCT/100)`) exceeds `TMPFS_BUDGET_MB`
(default 900, under the 1 GiB tmpfs), and serializes jobs larger than
`LARGE_JOB_THRESHOLD_MB` (default 100) through a per-pod semaphore so two large
jobs never co-occupy the scratch area. See `internal/worker/tmpfs.go`.
