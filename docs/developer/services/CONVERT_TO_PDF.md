# Convert To PDF Service

## Overview

The Convert To PDF service converts various document and image formats to PDF, and also handles cross-format Office-to-LibreOffice conversions. It handles Word, Excel, PowerPoint, HTML, image, and OpenDocument (ODT/ODS/ODP) file conversions using LibreOffice and pdfcpu.

**Port**: 8083 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: LibreOffice (via unoserver daemon), pdfcpu

## Responsibilities

1. **Office to PDF** - Convert Word/Excel/PowerPoint documents to PDF
2. **LibreOffice to PDF** - Convert ODT/ODS/ODP documents to PDF
3. **Office to LibreOffice** - Convert Word/Excel/PowerPoint to ODT/ODS/ODP
4. **HTML to PDF** - Convert HTML documents to PDF
5. **Image to PDF** - Convert images (JPG, PNG, etc.) to PDF
6. **Job Processing** - Pick jobs from Redis queue and process them
7. **Status Updates** - Update job status and progress in database

## Architecture

```
NATS JetStream (jobs.dispatch.convert-to-pdf)
  ↓
Convert-To-PDF Worker (concurrent, WORKER_CONCURRENCY=2)
  ├─ Fetch up to N messages from NATS
  ├─ Process concurrently via semaphore-limited goroutines
  │   ├─ Office docs → unoconvert (persistent LibreOffice daemon)
  │   │                 └─ Fallback: libreoffice --headless (cold start)
  │   └─ Images/PDFs → pdfcpu (pure Go)
  ├─ Record output in PostgreSQL
  └─ Ack NATS message
```

### unoserver Daemon

The container runs a persistent LibreOffice instance via `unoserver` (started by `entrypoint.sh` before the Go binary). The `officeToPDF()` function connects to it via `unoconvert` over a local socket (port 2002), eliminating the 2-5s JVM cold start per conversion. If the daemon is unavailable, conversions fall back to spawning `libreoffice --headless` directly.

## Supported Tools

| Tool | Input Formats | Output | Implementation | Status |
|------|--------------|--------|----------------|--------|
| `word-to-pdf` | .doc, .docx | .pdf | LibreOffice Writer | ✅ Implemented |
| `excel-to-pdf` | .xls, .xlsx | .pdf | LibreOffice Calc | ✅ Implemented |
| `ppt-to-pdf` | .ppt, .pptx | .pdf | LibreOffice Impress | ✅ Implemented |
| `html-to-pdf` | .html, .htm | .pdf | LibreOffice Writer | ✅ Implemented |
| `image-to-pdf` | .jpg, .png, .gif, .webp, .bmp | .pdf | pdfcpu | ✅ Implemented |
| `img-to-pdf` | .jpg, .png, .gif, .webp, .bmp | .pdf | pdfcpu | ✅ Alias |
| `odt-to-pdf` | .odt | .pdf | LibreOffice Writer | ✅ Implemented |
| `ods-to-pdf` | .ods | .pdf | LibreOffice Calc | ✅ Implemented |
| `odp-to-pdf` | .odp | .pdf | LibreOffice Impress | ✅ Implemented |
| `word-to-odt` | .doc, .docx | .odt | LibreOffice Writer | ✅ Implemented |
| `excel-to-ods` | .xls, .xlsx | .ods | LibreOffice Calc | ✅ Implemented |
| `powerpoint-to-odp` | .ppt, .pptx | .odp | LibreOffice Impress | ✅ Implemented |
| `merge-pdf` | .pdf | .pdf | pdfcpu | ✅ Implemented |
| `split-pdf` | .pdf | .zip | pdfcpu | ✅ Implemented |
| `compress-pdf` | .pdf | .pdf | pdfcpu | ✅ Implemented |
| `protect-pdf` | .pdf | .pdf | pdfcpu | ✅ Implemented |
| `unlock-pdf` | .pdf | .pdf | pdfcpu | ✅ Implemented |
| `watermark-pdf` | .pdf | .pdf | pdfcpu text/image watermarks | ✅ Implemented |
| `add-page-numbers` | .pdf | .pdf | pdfcpu text stamps | ✅ Implemented |
| `sign-pdf` | .pdf | .pdf | pdfcpu image stamps | ✅ Implemented |
| `edit-pdf` | .pdf | .pdf | pdfcpu text stamps | ✅ Implemented |

## API Endpoints

All endpoints are routed through the API Gateway and Upload Service.

### Create Conversion Job

**Via JSON** (using pre-uploaded file):
```http
POST /api/convert-to-pdf/{tool}
Content-Type: application/json

{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Via JSON** (multiple files for image-to-pdf):
```http
POST /api/convert-to-pdf/image-to-pdf
Content-Type: application/json

{
  "uploadIds": [
    "upload-id-1",
    "upload-id-2",
    "upload-id-3"
  ]
}
```

**Via Multipart** (direct file upload):
```http
POST /api/convert-to-pdf/{tool}
Content-Type: multipart/form-data

file: [Document or image file]
```

**Via Multipart** (multiple images):
```http
POST /api/convert-to-pdf/image-to-pdf
Content-Type: multipart/form-data

files: [image1.jpg]
files: [image2.jpg]
files: [image3.png]
```

**Response** (200 OK):
```json
{
  "id": "job-uuid",
  "userId": "user-uuid",
  "toolType": "word-to-pdf",
  "status": "queued",
  "progress": 0,
  "fileName": "document.docx",
  "fileSize": "123.45 KB",
  "createdAt": "2024-01-15T10:30:00Z"
}
```

---

### List Jobs by Tool

```http
GET /api/convert-to-pdf/{tool}
```

Returns all jobs for the specified tool, filtered by user/guest token.

---

### Get Job Status

```http
GET /api/convert-to-pdf/{tool}/{jobId}
```

Returns current job status and progress.

---

### Download Result

```http
GET /api/convert-to-pdf/{tool}/{jobId}/download
```

Downloads the converted PDF file. Only available when `status = "completed"`.

---

### Delete Job

```http
DELETE /api/convert-to-pdf/{tool}/{jobId}
```

Deletes the job and its associated files.

---

## Tool Details

### word-to-pdf

Converts Microsoft Word documents to PDF.

**Input**: `.doc`, `.docx`
**Output**: `.pdf`
**Implementation**: LibreOffice Writer

**Features**:
- Preserves formatting and styles
- Embeds fonts
- Maintains page layout
- Includes images and tables

**Limitations**:
- Some advanced Word features may not render perfectly
- Custom fonts may be substituted
- Macros are not executed

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/word-to-pdf \
  -F "file=@document.docx"
```

---

### excel-to-pdf

Converts Microsoft Excel spreadsheets to PDF.

**Input**: `.xls`, `.xlsx`
**Output**: `.pdf`
**Implementation**: LibreOffice Calc

**Features**:
- Preserves cell formatting
- Maintains column widths
- Includes charts and images
- Multiple sheets converted to multi-page PDF

**Limitations**:
- Very wide spreadsheets may be scaled to fit page
- Print area settings from Excel not preserved
- Formulas are converted to values

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/excel-to-pdf \
  -F "file=@spreadsheet.xlsx"
```

---

### ppt-to-pdf

Converts Microsoft PowerPoint presentations to PDF.

**Input**: `.ppt`, `.pptx`
**Output**: `.pdf`
**Implementation**: LibreOffice Impress

**Features**:
- Each slide becomes a PDF page
- Preserves layout and design
- Includes images and shapes
- Maintains text formatting

**Limitations**:
- Animations not preserved (static output)
- Transitions not included
- Some effects may be simplified

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/ppt-to-pdf \
  -F "file=@presentation.pptx"
```

---

### html-to-pdf

Converts HTML documents to PDF format.

**Input**: `.html`, `.htm`
**Output**: `.pdf`
**Implementation**: LibreOffice Writer

**Features**:
- Renders HTML with CSS styling
- Supports embedded images (base64)
- Maintains document structure
- Handles tables and lists

**Limitations**:
- External CSS files may not be loaded (use inline styles)
- External images via URL may not render (use base64 or local paths)
- JavaScript is not executed
- Complex CSS3 features may not render perfectly

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/html-to-pdf \
  -F "file=@document.html"
```

---

### image-to-pdf / img-to-pdf

Converts images to PDF format.

**Input**: `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`, `.bmp`
**Output**: `.pdf`
**Implementation**: pdfcpu (pure Go, no external dependencies)

**Features**:
- Multiple images combined into single PDF (one image per page)
- Maintains image quality (no re-compression)
- Automatic page sizing to fit image dimensions
- Fast processing

**Single Image Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/image-to-pdf \
  -F "file=@photo.jpg"
```

**Multiple Images Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/image-to-pdf \
  -F "files=@page1.jpg" \
  -F "files=@page2.jpg" \
  -F "files=@page3.png"
```

**Output**: Single PDF with 3 pages

---

### odt-to-pdf

Converts LibreOffice Writer documents to PDF.

**Input**: `.odt`
**Output**: `.pdf`
**Implementation**: LibreOffice Writer (via unoconvert/fallback)

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/odt-to-pdf \
  -F "file=@document.odt"
```

---

### ods-to-pdf

Converts LibreOffice Calc spreadsheets to PDF.

**Input**: `.ods`
**Output**: `.pdf`
**Implementation**: LibreOffice Calc (via unoconvert/fallback)

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/ods-to-pdf \
  -F "file=@spreadsheet.ods"
```

---

### odp-to-pdf

Converts LibreOffice Impress presentations to PDF.

**Input**: `.odp`
**Output**: `.pdf`
**Implementation**: LibreOffice Impress (via unoconvert/fallback)

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/odp-to-pdf \
  -F "file=@presentation.odp"
```

---

### word-to-odt

Converts Microsoft Word documents to LibreOffice Writer format.

**Input**: `.doc`, `.docx`
**Output**: `.odt`
**Implementation**: LibreOffice Writer (via `officeToOffice()` using unoconvert/fallback)

**Use Cases**:
- Migrating from Microsoft Office to LibreOffice
- Open-source document workflows
- Cross-platform compatibility

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/word-to-odt \
  -F "file=@document.docx"
```

---

### excel-to-ods

Converts Microsoft Excel spreadsheets to LibreOffice Calc format.

**Input**: `.xls`, `.xlsx`
**Output**: `.ods`
**Implementation**: LibreOffice Calc (via `officeToOffice()` using unoconvert/fallback)

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/excel-to-ods \
  -F "file=@spreadsheet.xlsx"
```

---

### powerpoint-to-odp

Converts Microsoft PowerPoint presentations to LibreOffice Impress format.

**Input**: `.ppt`, `.pptx`
**Output**: `.odp`
**Implementation**: LibreOffice Impress (via `officeToOffice()` using unoconvert/fallback)

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/powerpoint-to-odp \
  -F "file=@presentation.pptx"
```

---

### watermark-pdf

Adds text or image watermarks to PDF documents.

**Input**: `.pdf`
**Output**: `.pdf`
**Implementation**: pdfcpu text/image watermarks

**Options**:
| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `type` | `"text"` \| `"image"` | `"text"` | Watermark type |
| `text` | string | `"CONFIDENTIAL"` | Watermark text (when type=text) |
| `imageData` | string | — | Base64 data URL of watermark image (when type=image) |
| `position` | `"center"` \| `"diagonal"` \| `"tiled"` | `"diagonal"` | Watermark placement |
| `opacity` | number (10-100) | `50` | Watermark opacity percentage |
| `fontSize` | number (12-120) | `48` | Font size in points (when type=text) |
| `color` | string (hex) | `"#6366f1"` | Text color (when type=text) |

**Features**:
- Text watermarks with configurable font size, color, and opacity
- Image watermarks from PNG, JPEG, or WebP (sent as base64 data URL)
- Three position modes: center, diagonal (45-degree rotation), tiled
- Applied to all pages

**Example** (text watermark):
```bash
curl -X POST http://localhost:8080/api/convert-to-pdf/watermark-pdf \
  -H "Content-Type: application/json" \
  -d '{"uploadId": "upload-uuid", "options": {"type": "text", "text": "DRAFT", "position": "diagonal", "opacity": 30}}'
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8083` | HTTP server port (internal) |
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `UPLOAD_DIR` | `/app/uploads` | Input files directory |
| `OUTPUT_DIR` | `/app/outputs` | Output files directory |
| `WORKER_CONCURRENCY` | `2` | Max concurrent jobs processed in parallel |
| `UNOSERVER_HOST` | `127.0.0.1` | unoserver daemon host |
| `UNOSERVER_PORT` | `2002` | unoserver daemon port |
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |
| `MAX_RETRIES` | `3` | Max retry attempts for failed jobs |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing |

## Dependencies

### LibreOffice + unoserver

Used for Office and HTML document conversions. LibreOffice runs as a persistent daemon via `unoserver`, and conversions are sent via `unoconvert`.

**Installed Packages**:
- `libreoffice` (full suite for all conversions)
- `ttf-liberation` - Font support
- `python3`, `py3-pip` - Python runtime for unoserver
- `unoserver` (pip) - Persistent LibreOffice daemon + `unoconvert` CLI

**Fast path (via daemon)**:
```bash
unoconvert --host 127.0.0.1 --port 2002 --convert-to pdf input.docx output.pdf
```

**Fallback (direct invocation)**:
```bash
libreoffice --headless --convert-to pdf --outdir /output /input/file.docx
```

### pdfcpu

Pure Go PDF library for image to PDF conversion.

**Features**:
- Fast image embedding
- No external dependencies
- No re-compression (lossless)
- Automatic page sizing

## Deployment

### Docker Compose

```yaml
convert-to-pdf:
  build:
    context: ./convert-to-pdf
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    UPLOAD_DIR: /app/uploads
    OUTPUT_DIR: /app/outputs
    MAX_RETRIES: "3"
    PROCESSING_TIMEOUT: 30m
  volumes:
    - uploads_data:/app/uploads
    - outputs_data:/app/outputs
  depends_on:
    - db
    - redis
```

### Local Development

1. Install LibreOffice:
   ```bash
   # Ubuntu/Debian
   sudo apt-get install libreoffice

   # macOS
   brew install libreoffice

   # Alpine
   apk add libreoffice ttf-liberation
   ```

2. Start dependencies:
   ```bash
   docker compose up -d db redis
   ```

3. Run service:
   ```bash
   cd convert-to-pdf
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
   export REDIS_ADDR="localhost:6379"
   go run main.go
   ```

## Performance

### Processing Times (Typical, with unoserver daemon)

| Tool | Small File | Medium File | Large File |
|------|-----------|-------------|------------|
| word-to-pdf | <1s | 1-3s | 5-15s |
| excel-to-pdf | <1s | 2-4s | 8-20s |
| ppt-to-pdf | 1-2s | 3-5s | 10-25s |
| html-to-pdf | <1s | 1-2s | 3-8s |
| image-to-pdf | <1s | 1-2s | 3-5s |

Note: Without the unoserver daemon (fallback mode), office conversions add 2-5s overhead per request due to LibreOffice cold start.

**File Sizes**:
- Small: < 1 MB, < 10 pages
- Medium: 1-5 MB, 10-50 pages
- Large: 5-50 MB, 50-100 pages

## Troubleshooting

### LibreOffice Issues

**Test LibreOffice**:
```bash
# Check version
docker compose exec convert-to-pdf libreoffice --version

# Manual conversion test
docker compose exec convert-to-pdf \
  libreoffice --headless --convert-to pdf \
  --outdir /app/outputs /app/uploads/test.docx

# Check output
docker compose exec convert-to-pdf ls -lh /app/outputs/
```

### Memory Issues

**Symptoms**: Worker crashes or containers restart

**Solutions**:

1. **Increase Memory Limit**:
   ```yaml
   convert-to-pdf:
     deploy:
       resources:
         limits:
           memory: 2G
         reservations:
           memory: 1G
   ```

2. **Monitor Memory Usage**:
   ```bash
   docker stats convert-to-pdf
   ```

### Image Conversion Issues

**Symptoms**: Image-to-PDF jobs fail

**Common Causes**:
1. Image file corrupted
2. Unsupported image format
3. Image too large

**Solutions**:
```bash
# Verify image file
docker compose exec convert-to-pdf file /app/uploads/image.jpg
```

## Sequence Diagrams

### Job Processing Flow (NATS Worker)

```mermaid
sequenceDiagram
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as convert-to-pdf Worker
    participant DB as PostgreSQL
    participant FS as File System
    participant LO as LibreOffice/pdfcpu

    JS->>NATS: Publish JobCreated to dispatch.convert-to-pdf
    NATS->>W: Deliver JobCreated event

    W->>W: Validate toolType in AllowedTools
    W->>DB: UPDATE progress=20% (processing started)

    alt Looped pdfcpu operation (split, page numbers, edit)
        Note over W: Real page-by-page progress via ProgressFunc callback
        W->>LO: pdfcpu process page by page
        loop Each page/annotation
            LO-->>W: Page i of N done
            W->>DB: UPDATE progress = 20% + (i/N × 70%)
        end
        LO-->>FS: output file
    else Office document (word/excel/ppt/html)
        W->>W: Start time-based progressReporter
        Note over W: Estimates time from file size,<br/>smoothly increments 20%→90% with ease-out curve
        W->>LO: unoconvert --host 127.0.0.1 --port 2002 --convert-to pdf input.docx output.pdf
        Note over W,LO: Falls back to libreoffice --headless if unoserver is unavailable
        LO-->>FS: output.pdf
        W->>W: Stop progressReporter
    else Single-pass pdfcpu (merge, compress, encrypt, etc.)
        W->>W: Start time-based progressReporter
        W->>LO: pdfcpu single operation
        LO-->>FS: output file
        W->>W: Stop progressReporter
    end

    alt Conversion succeeds
        W->>FS: Verify output file exists
        W->>DB: INSERT file_metadata (kind='output')
        W->>DB: UPDATE progress=100%, status='completed'
        W->>NATS: Ack message
    else Conversion fails
        W->>DB: UPDATE processing_jobs SET status='failed', failure_reason=error
        W->>NATS: Ack message
    end
```

### Image-to-PDF Multi-File Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as convert-to-pdf Worker
    participant FS as File System

    C->>JS: POST /api/convert-to-pdf/image-to-pdf {uploadIds: [id1, id2, id3]}
    JS->>JS: Consume each upload, move files to job directory
    JS->>NATS: Publish JobCreated {inputPaths: [img1.jpg, img2.jpg, img3.png]}

    NATS->>W: Deliver JobCreated
    W->>FS: Read all image files
    W->>W: pdfcpu: create PDF with one image per page
    W->>FS: Write combined output.pdf
    W-->>W: Job completed (3 pages)
```

### Health Check Flow

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant W as convert-to-pdf :8083
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

`/readyz` -- Readiness check (PostgreSQL + Redis + NATS + unoserver), returns 200/503 with individual check results. Unlike `/healthz` (liveness), `/readyz` verifies all dependencies are connected. The unoserver check is informational only — it does not affect readiness status since the fallback to direct LibreOffice invocation is always available.

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
| pdfcpu image import failure | Set status=failed | No |
| Unsupported image format | Set status=failed | No |
| Output file not produced | Set status=failed | No |
| Database update failure | Log error, message not acked | NATS redelivery |
| Disk full | Set status=failed | No |

### NATS Redelivery

NATS JetStream handles retries via `AckWait` and `MaxDeliver` settings:
- If a worker crashes mid-processing, the message is redelivered after `AckWait` timeout
- Messages are redelivered up to `MaxDeliver` times before being moved to a dead letter queue
- Permanent failures (invalid input, unsupported format) are acked immediately to prevent infinite retry

When retries are exhausted (MaxDeliver reached), the failed job payload is published to `jobs.dlq.convert-to-pdf` on the `JOBS_DLQ` stream (7-day retention) before the original message is acknowledged. This preserves failed jobs for debugging and replay.

## Related Documentation

- [Convert From PDF](./CONVERT_FROM_PDF.md) - Convert PDF to other formats
- [Organize PDF](./ORGANIZE_PDF.md) - PDF manipulation (merge, split, etc.)
- [Optimize PDF](./OPTIMIZE_PDF.md) - PDF compression, repair, OCR
- [Job Service](./JOB_SERVICE.md) - Job creation and management
- [Auth Service](./AUTH_SERVICE.md) - Authentication and user management
- [API Gateway](./API_GATEWAY.md) - Request routing

## Support

For issues:
- Check logs: `docker compose logs -f convert-to-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Monitor queues: `docker compose exec redis redis-cli keys "queue:*"`
- Test LibreOffice: `docker compose exec convert-to-pdf libreoffice --version`
