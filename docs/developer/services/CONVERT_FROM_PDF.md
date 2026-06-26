# Convert From PDF Service

## Overview

The Convert From PDF service converts PDF files to other formats including images, Office documents (Word, Excel, PowerPoint), LibreOffice/OpenDocument formats (ODT, ODS, ODP), HTML, plain text, and PDF/A archival format.

**Port**: 8082 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: pdf2docx (Python; for `pdf-to-word`/`pdf-to-docx`), LibreOffice (Writer/Calc/Impress for `pdf-to-xlsx`/`pdf-to-odt`/`pdf-to-ods`/`pdf-to-odp` and the `pdf-to-word` fallback), Ghostscript (`pdf-to-pdfa`), Poppler (`pdftotext`, `pdftohtml`, `pdftoppm` for image/text/html conversions)

## Responsibilities

1. **PDF to Image** - Convert PDF pages to PNG images
2. **PDF to Office** - Convert PDF to Word/Excel/PowerPoint formats
3. **PDF to LibreOffice** - Convert PDF to ODT/ODS/ODP (OpenDocument) formats
4. **PDF to HTML** - Convert PDF to HTML format
5. **PDF to Text** - Extract plain text from PDF
6. **PDF to PDF/A** - Convert PDF to archival PDF/A format
6. **Job Processing** - Pick jobs from Redis queue and process them
7. **Status Updates** - Update job status and progress in database
8. **Object storage I/O** - Download input objects from the S3 uploads bucket into a container-local scratch directory; upload the conversion output to the S3 outputs bucket under `jobs/{jobID}/...`

## Architecture

```
NATS JetStream JOBS_DISPATCH (jobs.dispatch.convert-from-pdf)
  ↓ pull-consumer (durable=convert-from-pdf, MaxDeliver=4, AckWait=30m, MaxAckPending=2×concurrency, BackOff 10s/30s/2m)
convert-from-pdf worker
  ├─ Fetch up to WORKER_CONCURRENCY messages (default 2); each job runs in a
  │  semaphore-bounded goroutine so jobs process in parallel
  ├─ Validate AllowedTools[toolType]
  ├─ Duplicate-job guard (skip if already completed/processing)
  ├─ Update DB → status='processing', progress=20
  ├─ Create scratch dir (os.MkdirTemp "job-<jobId>-*", removed when done)
  ├─ Download inputs: S3 uploads bucket keys (payload.inputPaths) → <scratch>/in/
  │  (download failure → recoverable, NAK + backoff)
  ├─ Run processing.ProcessFile() on local files, output to <scratch>/out:
  │     ├─ pdf-to-image      → pdftoppm (Poppler) → single PNG or ZIP
  │     ├─ pdf-to-word/docx  → pdf2docx (primary) → LibreOffice writer_pdf_import (fallback)
  │     ├─ pdf-to-excel/xlsx → LibreOffice Calc (writer_pdf_import → xlsx)
  │     ├─ pdf-to-ppt/pptx   → image-based: rasterize all pages in one pdftoppm call → embed as one slide per image
  │     ├─ pdf-to-html       → pdftohtml → ZIP (HTML + images)
  │     ├─ pdf-to-text/txt   → pdftotext
  │     ├─ pdf-to-pdfa       → Ghostscript (PDF/A-2b)
  │     └─ pdf-to-odt/ods/odp → LibreOffice writer_pdf_import → odt/ods/odp
  ├─ Upload output: <scratch>/out/<file> → S3 outputs bucket jobs/<jobId>/<file>
  │  (upload failure → recoverable, NAK + backoff)
  ├─ DB → status='completed', progress=100; INSERT file_metadata
  │  (kind=output, path=object key, size_bytes=uploaded size)
  ├─ Publish jobs.events.<jobId>.{processing,completed,failed}
  └─ On MaxDeliver exhaustion → publish JobFailed to jobs.dlq.convert-from-pdf
```

### Object Storage

Inputs and outputs live in S3-compatible object storage (MinIO in compose), not on a shared volume:

- `payload.inputPaths` carries **object keys** in the uploads bucket. The worker downloads each key to `<scratch>/in/<basename>` before processing, because LibreOffice/pdf2docx/Ghostscript/Poppler need real local files.
- The output is uploaded to the outputs bucket as `jobs/{jobID}/{filename}` with an extension-derived `Content-Type`; `file_metadata.path` stores the object key and `size_bytes` the uploaded size.
- The scratch directory is deleted after every job (success or failure). Download/upload failures are treated as recoverable and retried via NAK + backoff.

## Supported Tools

| Tool | Input | Output | Implementation | Status |
|------|-------|--------|----------------|--------|
| `pdf-to-image` | .pdf | .png (single page) or .zip (multi-page PNGs) | pdftoppm (Poppler) | ✅ Implemented |
| `pdf-to-img` | .pdf | .png (single page) or .zip (multi-page PNGs) | pdftoppm (Poppler) | ✅ Alias |
| `pdf-to-word` | .pdf | .docx | pdf2docx (primary) → LibreOffice Writer (fallback) | ✅ Implemented |
| `pdf-to-docx` | .pdf | .docx | pdf2docx (primary) → LibreOffice Writer (fallback) | ✅ Alias |
| `pdf-to-excel` | .pdf | .xlsx | LibreOffice Calc | ✅ Implemented |
| `pdf-to-xlsx` | .pdf | .xlsx | LibreOffice Calc | ✅ Alias |
| `pdf-to-ppt` | .pdf | .pptx | image-based: pdftoppm → embed each page as a slide image | ✅ Implemented |
| `pdf-to-powerpoint` | .pdf | .pptx | image-based (alias of `pdf-to-ppt`) | ✅ Alias |
| `pdf-to-pptx` | .pdf | .pptx | image-based (alias of `pdf-to-ppt`) | ✅ Alias |
| `pdf-to-html` | .pdf | .zip (HTML+images) | pdftohtml (Poppler) | ✅ Implemented |
| `pdf-to-text` | .pdf | .txt | pdftotext (Poppler) | ✅ Implemented |
| `pdf-to-txt` | .pdf | .txt | pdftotext (Poppler) | ✅ Alias |
| `pdf-to-pdfa` | .pdf | .pdf (PDF/A-2b) | Ghostscript | ✅ Implemented |
| `pdf-to-odt` | .pdf | .odt | LibreOffice Writer | ✅ Implemented |
| `pdf-to-ods` | .pdf | .ods | LibreOffice Calc | ✅ Implemented |
| `pdf-to-odp` | .pdf | .odp | LibreOffice Impress | ✅ Implemented |

## API Endpoints

All endpoints are routed through the API Gateway and Upload Service.

### Create Conversion Job

**Via JSON** (using pre-uploaded file):
```http
POST /api/convert-from-pdf/{tool}
Content-Type: application/json

{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Via Multipart** (direct file upload):
```http
POST /api/convert-from-pdf/{tool}
Content-Type: multipart/form-data

file: [PDF file]
```

**Response** (200 OK):
```json
{
  "id": "job-uuid",
  "userId": "user-uuid",
  "toolType": "pdf-to-word",
  "status": "queued",
  "progress": 0,
  "fileName": "document.pdf",
  "fileSize": "123.45 KB",
  "createdAt": "2024-01-15T10:30:00Z"
}
```

---

### List Jobs by Tool

```http
GET /api/convert-from-pdf/{tool}
```

Returns all jobs for the specified tool, filtered by user/guest token.

---

### Get Job Status

```http
GET /api/convert-from-pdf/{tool}/{jobId}
```

Returns current job status and progress.

---

### Download Result

```http
GET /api/convert-from-pdf/{tool}/{jobId}/download
```

Downloads the converted file. Only available when `status = "completed"`.

---

### Delete Job

```http
DELETE /api/convert-from-pdf/{tool}/{jobId}
```

Deletes the job and its associated files.

---

## Tool Details

### pdf-to-image / pdf-to-img

Converts PDF pages to PNG images.

**Input**: `.pdf`
**Output**: Single-page PDF → `.png` file directly; Multi-page PDF → `.zip` containing PNG files (one per page)
**Implementation**: Poppler (pdftoppm)

**Output Format** (multi-page):
```
output.zip
├── page_001-1.png
├── page_002-1.png
└── page_003-1.png
```

**Output Format** (single-page):
```
output.png
```

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-image \
  -F "file=@document.pdf"
```

---

### pdf-to-word / pdf-to-docx

Converts PDF to Microsoft Word (.docx).

**Input**: `.pdf`
**Output**: `.docx`
**Implementation**: [pdf2docx](https://github.com/ArtifexSoftware/pdf2docx) (Python; primary) with LibreOffice Writer's `writer_pdf_import` filter as a fallback.

`pdf2docx` reconstructs real `<w:p>` paragraphs, lists, tables, and image runs from PDF text-with-coordinates. The previous LibreOffice-only path imported each page as a canvas of absolutely-positioned text frames (`position:absolute;margin-left:XXXpt;margin-top:YYYpt`) — visually faithful but unusable for editing/reflow/spell-check. `pdf2docx` is invoked as `pdf2docx convert <input> <output>`. On any non-zero exit or empty output the dispatcher logs a warning and falls back to LibreOffice so the request never hard-fails on an unexpected PDF shape.

The integration boundary in code is a single Go function, [`pdfToDocxNative`](../../../convert-from-pdf/processing/processing_free.go), that shells out to the binary. When a Go-native PDF→DOCX package becomes available it can replace the function body without touching any other file.

**Limitations**:
- Slide-deck PDFs (presentations rendered to PDF) will still produce sub-optimal docx because the source isn't a flowing document — no tool can fully recover a presentation as a Word doc. The output will be better than the LibreOffice-only path but not "great".
- Scanned PDFs (image-only PDFs) yield empty/near-empty docx. Use `ocr-pdf` first.
- Fonts may be substituted by the embedded font fallback list.

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-word \
  -F "file=@document.pdf"
```

---

### pdf-to-excel / pdf-to-xlsx

Converts PDF to Microsoft Excel (.xlsx).

**Input**: `.pdf`
**Output**: `.xlsx`
**Implementation**: LibreOffice Calc

**Best For**: PDFs containing tables or structured data

**Limitations**:
- Works best with simple table structures
- Complex layouts may require manual cleanup

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-excel \
  -F "file=@document.pdf"
```

---

### pdf-to-ppt / pdf-to-powerpoint / pdf-to-pptx

Converts PDF to Microsoft PowerPoint (.pptx).

**Input**: `.pdf`
**Output**: `.pptx`
**Implementation**: image-based — `pdftoppm` rasterizes every PDF page to a PNG, then a small builder embeds one image per slide. This avoids LibreOffice Impress's PDF-import path, which produces unusable empty layouts. The trade-off: text in the resulting `.pptx` is **not selectable / editable** — each slide is essentially a screenshot of the corresponding PDF page. For an editable presentation, run OCR on the PDF first or start from the source deck.

**Limitations**:
- Slides contain images, not editable text or shapes.
- Resulting `.pptx` is larger than a text-based presentation of the same content.
- Animations / transitions are not preserved (the source has none).

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-ppt \
  -F "file=@document.pdf"
```

---

### pdf-to-html

Converts PDF to HTML format with embedded images.

**Input**: `.pdf`
**Output**: `.zip` containing HTML and image files
**Implementation**: Poppler (pdftohtml)

**Output Format**:
```
output.zip
├── output.html
├── output-1.png
├── output-2.png
└── ...
```

**Features**:
- Preserves layout structure
- Images extracted separately
- CSS styling included

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-html \
  -F "file=@document.pdf"
```

---

### pdf-to-text / pdf-to-txt

Extracts plain text from PDF.

**Input**: `.pdf`
**Output**: `.txt`
**Implementation**: Poppler (pdftotext)

**Features**:
- Layout preservation mode
- Fast extraction
- Handles multi-page documents

**Limitations**:
- Scanned PDFs require OCR (use optimize-pdf/ocr-pdf first)
- Complex layouts may affect text order

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-text \
  -F "file=@document.pdf"
```

---

### pdf-to-pdfa

Converts PDF to PDF/A-2b archival format.

**Input**: `.pdf`
**Output**: `.pdf` (PDF/A-2b compliant)
**Implementation**: Ghostscript

**What is PDF/A?**
PDF/A is an ISO-standardized version of PDF designed for long-term archival:
- All fonts are embedded
- Color profiles are standardized
- No external dependencies
- Metadata is preserved

**Use Cases**:
- Legal document archival
- Government records
- Long-term preservation
- Compliance requirements

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-pdfa \
  -F "file=@document.pdf"
```

---

### pdf-to-odt

Converts PDF to LibreOffice Writer (.odt) format.

**Input**: `.pdf`
**Output**: `.odt`
**Implementation**: LibreOffice Writer (PDF import filter)

**Use Cases**:
- Users who prefer LibreOffice/OpenOffice over Microsoft Office
- Open-source document editing workflows
- Cross-platform document compatibility

**Limitations**:
- Same as pdf-to-word (complex layouts may not convert perfectly)
- Scanned PDFs will contain images, not editable text

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-odt \
  -F "file=@document.pdf"
```

---

### pdf-to-ods

Converts PDF to LibreOffice Calc (.ods) format.

**Input**: `.pdf`
**Output**: `.ods`
**Implementation**: LibreOffice Calc

**Best For**: PDFs containing tables or structured data for editing in LibreOffice Calc

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-ods \
  -F "file=@document.pdf"
```

---

### pdf-to-odp

Converts PDF to LibreOffice Impress (.odp) format.

**Input**: `.pdf`
**Output**: `.odp`
**Implementation**: LibreOffice Impress

**Behavior**: Each PDF page is converted to an Impress slide

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-odp \
  -F "file=@document.pdf"
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8082` | HTTP server port (internal) |
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `S3_ENDPOINT` | **Required** | S3-compatible endpoint for data operations (e.g. `minio:9000`) |
| `S3_ACCESS_KEY` | **Required** | S3 access key |
| `S3_SECRET_KEY` | **Required** | S3 secret key |
| `S3_USE_SSL` | `false` | Use TLS to reach the S3 endpoint |
| `S3_BUCKET_UPLOADS` | `fyredocs-uploads` | Bucket holding job input objects |
| `S3_BUCKET_OUTPUTS` | `fyredocs-outputs` | Bucket receiving job outputs (`jobs/{jobID}/...`) |
| `S3_REGION` | `us-east-1` | Signing region (MinIO ignores it; AWS must match) |
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |
| `MAX_RETRIES` | `3` | Max retry attempts for failed jobs |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing (currently honoured via NATS `AckWait` rather than a context deadline in code) |
| `WORKER_CONCURRENCY` | `2` | Max concurrent jobs processed in parallel (semaphore-bounded goroutines; one slow conversion no longer blocks the rest) |
| `RESULT_CACHE_TTL_SECONDS` | `3600` | Lifetime of result-cache entries. Keep ≤ outputs bucket TTL. `0` disables caching. |
| `NATS_URL` | **Required** | NATS server URL |

## Result Caching

Identical jobs are deduplicated. Before downloading inputs, the worker derives a cache key from the tool type, the canonicalised options, and the **content identity of every input** — the latter via each upload object's ETag, fetched with a cheap `StatObject` (no download). The key is looked up in Redis (`rescache:v1:convert-from-pdf:<sha256>`):

- **Hit:** the previously produced output is verified to still exist (it may have been TTL-cleaned), then **server-side copied** (`CopyObject`, no bytes through the worker) to the new job's output key. The download and the pdf2docx/LibreOffice conversion are skipped entirely.
- **Miss:** the job runs normally; on success the output key + metadata are written to Redis with `RESULT_CACHE_TTL_SECONDS`.

Caching is **best-effort**: any cache-path error (Redis down, stat/copy failure, expired output) logs and falls through to a normal run, so it can never fail a job. Disabled when Redis is unavailable or `RESULT_CACHE_TTL_SECONDS=0`.

## Dependencies

### LibreOffice (Optimized Installation)

Used for PDF to Office format conversions.

**Installed Packages** (minimal, not full suite):
- `libreoffice-writer` - PDF to Word
- `libreoffice-calc` - PDF to Excel
- `libreoffice-impress` - PDF to PowerPoint
- `libreoffice-common` - Shared components
- `ttf-liberation` - Font support

**Optimization**: Unused assets removed (gallery, templates, wizards, icons) to reduce image size.

**Command Line**:
```bash
libreoffice --headless --infilter=writer_pdf_import --convert-to docx --outdir /output /input/file.pdf
```

### Ghostscript

Used for PDF to PDF/A conversion.

**Command Line**:
```bash
gs -dPDFA=2 -dBATCH -dNOPAUSE -sProcessColorModel=DeviceRGB -sDEVICE=pdfwrite -sPDFACompatibilityPolicy=1 -sOutputFile=output.pdf input.pdf
```

### Poppler

PDF utilities for image/text/HTML extraction.

**Tools Used**:
- `pdftoppm` - PDF to PNG images
- `pdftotext` - PDF to plain text
- `pdftohtml` - PDF to HTML

**Installed Package**: `poppler-utils`

## Deployment

### Docker Compose

```yaml
convert-from-pdf:
  build:
    context: ./convert-from-pdf
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    S3_ENDPOINT: minio:9000
    S3_ACCESS_KEY: minioadmin
    S3_SECRET_KEY: minioadmin
    S3_BUCKET_UPLOADS: fyredocs-uploads
    S3_BUCKET_OUTPUTS: fyredocs-outputs
    MAX_RETRIES: "3"
    PROCESSING_TIMEOUT: 30m
  depends_on:
    - db
    - redis
    - minio
```

### Local Development

1. Install dependencies:
   ```bash
   # Ubuntu/Debian
   sudo apt-get install libreoffice poppler-utils ghostscript

   # macOS
   brew install libreoffice poppler ghostscript

   # Alpine
   apk add libreoffice-writer libreoffice-calc libreoffice-impress poppler-utils ghostscript
   ```

2. Start dependencies:
   ```bash
   docker compose up -d db redis
   ```

3. Run service:
   ```bash
   cd convert-from-pdf
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
   export REDIS_ADDR="localhost:6379"
   export S3_ENDPOINT="localhost:9000"
   export S3_ACCESS_KEY="minioadmin"
   export S3_SECRET_KEY="minioadmin"
   go run main.go
   ```

## Performance

### Processing Times (Typical)

| Tool | 1-page PDF | 10-page PDF | 100-page PDF |
|------|-----------|-------------|--------------|
| pdf-to-image | 1-2s | 3-5s | 20-30s |
| pdf-to-word | 2-3s | 5-10s | 30-60s |
| pdf-to-excel | 2-3s | 5-10s | 30-60s |
| pdf-to-ppt | 2-3s | 5-10s | 30-60s |
| pdf-to-html | 1-2s | 3-5s | 15-25s |
| pdf-to-text | <1s | 1-2s | 5-10s |
| pdf-to-pdfa | 1-2s | 2-4s | 10-20s |

**Note**: Times vary based on PDF complexity and server resources.

## Troubleshooting

### LibreOffice Conversion Failures

**Symptoms**: PDF to Office conversions fail

**Solutions**:
```bash
# Test LibreOffice in container
docker compose exec convert-from-pdf libreoffice --version

# Manual conversion test
docker compose exec convert-from-pdf \
  libreoffice --headless --infilter=writer_pdf_import \
  --convert-to docx --outdir /app/outputs /app/uploads/test.pdf
```

### Text Extraction Issues

**Symptoms**: pdftotext returns empty or garbled text

**Possible Causes**:
- PDF is scanned image (no text layer)
- PDF uses embedded fonts with encoding issues

**Solution**: Use OCR service (`optimize-pdf/ocr-pdf`) for scanned documents first.

### Memory Issues

**Symptoms**: Worker crashes or OOM kills

**Solutions**:
```yaml
# Add memory limits to docker-compose.yml
convert-from-pdf:
  deploy:
    resources:
      limits:
        memory: 2G
      reservations:
        memory: 1G
```

## Sequence Diagrams

### Job Processing Flow (NATS Worker)

```mermaid
sequenceDiagram
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as convert-from-pdf Worker
    participant DB as PostgreSQL
    participant S3 as MinIO / S3
    participant LO as LibreOffice/Poppler/GS

    JS->>NATS: Publish JobCreated to jobs.dispatch.convert-from-pdf
    NATS->>W: Deliver JobCreated event

    W->>W: Validate toolType in AllowedTools
    W->>DB: UPDATE processing_jobs SET status='processing'

    W->>W: Create scratch dir (job-scoped temp)
    W->>S3: Download inputPaths keys (uploads bucket → scratch/in)
    alt Download fails
        W->>NATS: NAK with backoff (recoverable)
    end
    W->>LO: Execute conversion (tool-specific command, output to scratch/out)

    alt Conversion succeeds
        LO-->>W: Output file produced in scratch/out
        W->>S3: Upload output (outputs bucket, jobs/<jobId>/<file>)
        alt Upload fails
            W->>NATS: NAK with backoff (recoverable)
        end
        W->>DB: INSERT file_metadata (kind='output', path=object key, size=uploaded bytes)
        W->>DB: UPDATE processing_jobs SET status='completed', progress=100
        W->>NATS: Ack message
    else Conversion fails
        LO-->>W: Error
        W->>DB: UPDATE processing_jobs SET status='failed', failure_reason=error
        W->>NATS: Ack message (no redelivery for permanent failures)
    end
    W->>W: Remove scratch dir
```

### PDF-to-Image Conversion Detail

```mermaid
sequenceDiagram
    participant W as Worker
    participant FS as File System
    participant PP as pdftoppm (Poppler)

    W->>FS: Read input PDF
    W->>PP: pdftoppm -png input.pdf output_prefix
    PP-->>FS: page_001-1.png, page_002-1.png, ...
    W->>FS: Create ZIP archive from PNG files
    W->>FS: Write output.zip to output directory
    W-->>W: Return outputPath = output.zip
```

### PDF-to-Office Conversion Detail

```mermaid
sequenceDiagram
    participant W as Worker
    participant FS as File System
    participant LO as LibreOffice

    W->>FS: Read input PDF
    W->>LO: libreoffice --headless --infilter=writer_pdf_import --convert-to docx --outdir /output input.pdf
    LO-->>FS: output.docx
    W->>FS: Verify output file exists
    W-->>W: Return outputPath = output.docx
```

### Health Check Flow

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant W as convert-from-pdf :8082
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

### Processing Errors

| Error Type | Handling | Retry |
|------------|----------|-------|
| Invalid tool type | Reject immediately, set status=failed | No |
| Input file missing | Set status=failed with reason | No |
| LibreOffice crash/timeout | Set status=failed, log error | NATS redelivery (MaxDeliver) |
| Poppler command failure | Set status=failed with stderr | No |
| Ghostscript failure | Set status=failed with stderr | No |
| Output file not produced | Set status=failed | No |
| Database update failure | Log error, NATS message not acked | NATS redelivery |
| Disk full | Set status=failed | No |

### Error Response Format (Health Check)

```json
{
  "status": "unhealthy",
  "redis": "connection refused",
  "nats": "disconnected"
}
```

### NATS Redelivery

NATS JetStream handles retries via `AckWait` and `MaxDeliver` settings:
- If a worker crashes mid-processing, the message is redelivered after `AckWait` timeout
- Messages are redelivered up to `MaxDeliver` times before being moved to a dead letter queue

When retries are exhausted (MaxDeliver reached), the failed job payload is published to `jobs.dlq.convert-from-pdf` on the `JOBS_DLQ` stream (7-day retention) before the original message is acknowledged. This preserves failed jobs for debugging and replay.

## Related Documentation

- [Convert To PDF](./CONVERT_TO_PDF.md) - Convert files TO PDF
- [Organize PDF](./ORGANIZE_PDF.md) - PDF manipulation (merge, split, etc.)
- [Optimize PDF](./OPTIMIZE_PDF.md) - PDF compression, repair, OCR
- [Job Service](./JOB_SERVICE.md) - Job creation and management
- [Auth Service](./AUTH_SERVICE.md) - Authentication and user management
- [API Gateway](./API_GATEWAY.md) - Request routing

## Progress DB-write throttle

The progress callback can fire many times per job. To cut DB load, writes are
throttled by `progressThrottle` (`internal/worker/progress.go`): the DB
`progress` column advances only when progress rises by at least
`PROGRESS_DB_MIN_DELTA` percent (default 10) or after `PROGRESS_DB_MIN_INTERVAL`
(default 10s), while NATS/SSE progress events are still published on every
callback so the client progress bar stays smooth. The no-regress
`WHERE progress < ?` guard on the UPDATE is retained.

## tmpfs capacity guard

Before downloading, the worker sums input object sizes and rejects jobs whose
projected footprint (`inputs × (1 + TMPFS_OUTPUT_FACTOR_PCT/100)`) exceeds
`TMPFS_BUDGET_MB` (default 900, under the 1 GiB tmpfs), and serializes jobs
larger than `LARGE_JOB_THRESHOLD_MB` (default 100) through a per-pod semaphore.
See `internal/worker/tmpfs.go`.

## Support

For issues:
- Check logs: `docker compose logs -f convert-from-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Inspect NATS subjects: `nats stream view JOBS_DISPATCH --filter jobs.dispatch.convert-from-pdf`
- Inspect DLQ: `nats stream view JOBS_DLQ --filter jobs.dlq.convert-from-pdf`
