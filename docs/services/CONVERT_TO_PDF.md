# Convert To PDF Service

## Overview

The Convert To PDF service converts various document and image formats to PDF. It handles Word, Excel, PowerPoint, HTML, and image file conversions using LibreOffice and pdfcpu.

**Port**: 8083 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: LibreOffice, pdfcpu

## Responsibilities

1. **Office to PDF** - Convert Word/Excel/PowerPoint documents to PDF
2. **HTML to PDF** - Convert HTML documents to PDF
3. **Image to PDF** - Convert images (JPG, PNG, etc.) to PDF
4. **Job Processing** - Pick jobs from Redis queue and process them
5. **Status Updates** - Update job status and progress in database

## Architecture

```
Redis Queue (queue:word-to-pdf, queue:image-to-pdf, etc.)
  ↓
Convert-To-PDF Worker
  ├─ Poll Queue
  ├─ Download Input File(s)
  ├─ Process with LibreOffice/pdfcpu
  ├─ Upload Output PDF
  └─ Update Job Status (PostgreSQL)
```

## Supported Tools

| Tool | Input Formats | Output | Implementation | Status |
|------|--------------|--------|----------------|--------|
| `word-to-pdf` | .doc, .docx | .pdf | LibreOffice Writer | ✅ Implemented |
| `excel-to-pdf` | .xls, .xlsx | .pdf | LibreOffice Calc | ✅ Implemented |
| `ppt-to-pdf` | .ppt, .pptx | .pdf | LibreOffice Impress | ✅ Implemented |
| `html-to-pdf` | .html, .htm | .pdf | LibreOffice Writer | ✅ Implemented |
| `image-to-pdf` | .jpg, .png, .gif, .webp, .bmp | .pdf | pdfcpu | ✅ Implemented |
| `img-to-pdf` | .jpg, .png, .gif, .webp, .bmp | .pdf | pdfcpu | ✅ Alias |

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
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |
| `MAX_RETRIES` | `3` | Max retry attempts for failed jobs |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing |

## Dependencies

### LibreOffice

Used for Office and HTML document conversions.

**Installed Packages**:
- `libreoffice` (full suite for all conversions)
- `ttf-liberation` - Font support

**Command Example**:
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
    DATABASE_URL: postgresql://user:password@db:5432/esydocs
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
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs"
   export REDIS_ADDR="localhost:6379"
   go run main.go
   ```

## Performance

### Processing Times (Typical)

| Tool | Small File | Medium File | Large File |
|------|-----------|-------------|------------|
| word-to-pdf | 2-3s | 5-8s | 15-30s |
| excel-to-pdf | 2-4s | 6-10s | 20-40s |
| ppt-to-pdf | 3-5s | 8-12s | 25-45s |
| html-to-pdf | 1-2s | 3-5s | 8-15s |
| image-to-pdf | <1s | 1-2s | 3-5s |

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
    W->>DB: UPDATE processing_jobs SET status='processing'

    W->>FS: Read input file(s) from inputPaths

    alt Office document (word/excel/ppt/html)
        W->>LO: libreoffice --headless --convert-to pdf --outdir /output input.docx
        LO-->>FS: output.pdf
    else Image to PDF
        W->>LO: pdfcpu import images to PDF
        LO-->>FS: output.pdf
    end

    alt Conversion succeeds
        W->>FS: Verify output file exists
        W->>DB: INSERT file_metadata (kind='output')
        W->>DB: UPDATE processing_jobs SET status='completed', progress=100
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

## Error Flows

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

## Related Documentation

- [Convert From PDF](../convert-from-pdf/CONVERT_FROM_PDF.md) - Convert PDF to other formats
- [Organize PDF](../organize-pdf/ORGANIZE_PDF.md) - PDF manipulation (merge, split, etc.)
- [Optimize PDF](../optimize-pdf/OPTIMIZE_PDF.md) - PDF compression, repair, OCR
- [Upload Service](../upload-service/UPLOAD_SERVICE.md) - Job creation and management
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing

## Support

For issues:
- Check logs: `docker compose logs -f convert-to-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Monitor queues: `docker compose exec redis redis-cli keys "queue:*"`
- Test LibreOffice: `docker compose exec convert-to-pdf libreoffice --version`
