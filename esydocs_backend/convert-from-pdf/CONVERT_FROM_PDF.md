# Convert From PDF Service

## Overview

The Convert From PDF service is a worker service that processes PDF conversion jobs. It converts PDF files to other formats (Word, Excel, PowerPoint, images) and provides PDF utility operations (merge, split, compress, protect, etc.).

**Port**: 8082 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: LibreOffice, pdfcpu, Poppler

## Responsibilities

1. **PDF Conversion** - Convert PDF to Word/Excel/PowerPoint/Images
2. **PDF Utilities** - Merge, split, compress, protect, unlock, watermark PDFs
3. **Job Processing** - Pick jobs from Redis queue and process them
4. **Status Updates** - Update job status and progress in database

## Architecture

```
Redis Queue (queue:pdf-to-word, queue:merge-pdf, etc.)
  ↓
Convert-From-PDF Worker
  ├─ Poll Queue
  ├─ Download Input File
  ├─ Process with LibreOffice/pdfcpu
  ├─ Upload Output File
  └─ Update Job Status (PostgreSQL)
```

## Supported Tools

### Conversion Tools

| Tool | Input | Output | Status |
|------|-------|--------|--------|
| `pdf-to-word` | .pdf | .docx | ✅ Implemented |
| `pdf-to-excel` | .pdf | .xlsx | ✅ Implemented |
| `pdf-to-powerpoint` | .pdf | .pptx | ✅ Implemented |
| `pdf-to-ppt` | .pdf | .pptx | ✅ Alias for pdf-to-powerpoint |
| `pdf-to-image` | .pdf | .zip (JPEGs) | ✅ Implemented |
| `pdf-to-img` | .pdf | .zip (JPEGs) | ✅ Alias for pdf-to-image |
| `ocr` | .pdf | .pdf (searchable) | ❌ Not implemented |

### PDF Utility Tools

| Tool | Description | Status |
|------|-------------|--------|
| `merge-pdf` | Merge multiple PDFs into one | ✅ Implemented |
| `split-pdf` | Split PDF by page ranges | ✅ Implemented |
| `compress-pdf` | Reduce PDF file size | ✅ Implemented |
| `protect-pdf` | Add password protection | ✅ Implemented |
| `unlock-pdf` | Remove password protection | ✅ Implemented |
| `watermark-pdf` | Add text watermark | ✅ Implemented |
| `sign-pdf` | Digitally sign PDF | ⚠️ Stub (copies input) |
| `edit-pdf` | Edit PDF content | ⚠️ Stub (copies input) |

## API Endpoints

All endpoints are routed through the API Gateway and Upload Service.

### Create Conversion Job

**Via JSON** (using pre-uploaded file):
```http
POST /api/convert-from-pdf/{tool}
Content-Type: application/json

{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "options": {
    "range": "1-3,5"  // For split-pdf
  }
}
```

**Via Multipart** (direct file upload):
```http
POST /api/convert-from-pdf/{tool}
Content-Type: multipart/form-data

file: [PDF file]
options: {"range": "1-3,5"}  // Optional JSON string
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

### pdf-to-word

Converts PDF to Microsoft Word (.docx).

**Input**: `.pdf`
**Output**: `.docx`
**Implementation**: LibreOffice (Writer)

**Limitations**:
- Complex layouts may not convert perfectly
- Images are embedded but may lose quality
- Fonts may be substituted

**Example**:
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/pdf-to-word \
  -F "file=@document.pdf"
```

---

### pdf-to-excel

Converts PDF to Microsoft Excel (.xlsx).

**Input**: `.pdf`
**Output**: `.xlsx`
**Implementation**: LibreOffice (Calc)

**Best For**: PDFs containing tables or structured data

**Limitations**:
- Works best with simple table structures
- Complex layouts may require manual cleanup

---

### pdf-to-powerpoint / pdf-to-ppt

Converts PDF to Microsoft PowerPoint (.pptx).

**Input**: `.pdf`
**Output**: `.pptx`
**Implementation**: LibreOffice (Impress)

**Behavior**: Each PDF page becomes a slide

**Limitations**:
- Animations not preserved
- Some formatting may change

---

### pdf-to-image / pdf-to-img

Converts PDF pages to JPEG images.

**Input**: `.pdf`
**Output**: `.zip` containing JPEG files (one per page)
**Implementation**: Poppler (pdftoppm)

**Output Format**:
```
output.zip
├── page-001.jpg
├── page-002.jpg
└── page-003.jpg
```

**Resolution**: 150 DPI (configurable in processing code)

---

### merge-pdf

Merges multiple PDF files into a single PDF.

**Input**: Multiple `.pdf` files
**Output**: Single `.pdf` file
**Implementation**: pdfcpu

**Minimum**: 2 PDF files required

**Example** (multipart):
```bash
curl -X POST http://localhost:8080/api/convert-from-pdf/merge-pdf \
  -F "files=@file1.pdf" \
  -F "files=@file2.pdf" \
  -F "files=@file3.pdf"
```

**Example** (JSON with uploadIds):
```json
{
  "uploadIds": [
    "upload-id-1",
    "upload-id-2",
    "upload-id-3"
  ]
}
```

---

### split-pdf

Splits PDF into separate pages or page ranges.

**Input**: `.pdf` file
**Output**: `.zip` containing split PDF files
**Implementation**: pdfcpu

**Options**:
- `range`: Page range specification (e.g., "1-3,5,7-9")

**Range Format**:
- Single page: `"5"`
- Range: `"1-10"`
- Multiple ranges: `"1-3,5,7-9"`
- All pages: `"all"` or omit

**Example**:
```json
{
  "uploadId": "file-upload-id",
  "options": {
    "range": "1-5,10"
  }
}
```

---

### compress-pdf

Reduces PDF file size.

**Input**: `.pdf`
**Output**: `.pdf` (compressed)
**Implementation**: pdfcpu

**Compression Methods**:
- Image optimization
- Duplicate object removal
- Stream compression

**Typical Savings**: 20-50% file size reduction

---

### protect-pdf

Adds password protection to PDF.

**Input**: `.pdf`
**Output**: `.pdf` (password-protected)
**Implementation**: pdfcpu

**Required Options**:
- `password`: Password to protect the PDF

**Example**:
```json
{
  "uploadId": "file-upload-id",
  "options": {
    "password": "SecurePassword123"
  }
}
```

**Permissions**: Full restrictions (no copying, printing, or editing)

---

### unlock-pdf

Removes password protection from PDF.

**Input**: `.pdf` (password-protected)
**Output**: `.pdf` (unlocked)
**Implementation**: pdfcpu

**Required Options**:
- `password`: Current password of the PDF

**Example**:
```json
{
  "uploadId": "file-upload-id",
  "options": {
    "password": "CurrentPassword123"
  }
}
```

---

### watermark-pdf

Adds text watermark to all pages of a PDF.

**Input**: `.pdf`
**Output**: `.pdf` (watermarked)
**Implementation**: pdfcpu

**Options**:
- `text`: Watermark text (default: "CONFIDENTIAL")

**Example**:
```json
{
  "uploadId": "file-upload-id",
  "options": {
    "text": "DRAFT - DO NOT DISTRIBUTE"
  }
}
```

**Watermark Position**: Diagonal across center of each page

---

### sign-pdf (Stub)

**Status**: ⚠️ Not fully implemented (copies input to output)

Digital signature functionality requires:
- X.509 certificates
- Private key management
- Signature appearance configuration

**Planned Enhancement**: Full digital signature support

---

### edit-pdf (Stub)

**Status**: ⚠️ Not fully implemented (copies input to output)

PDF editing functionality requires:
- Advanced PDF manipulation library
- Content stream parsing
- Object modification

**Planned Enhancement**: Basic text/image editing

---

## Processing Workflow

### Worker Loop

```
1. Poll Redis Queue
   ↓
2. Pop Job from Queue
   ↓
3. Mark Job as "processing"
   ↓
4. Download Input File(s)
   ↓
5. Execute Tool Logic
   ├─ LibreOffice conversion
   ├─ pdfcpu operations
   └─ Poppler rendering
   ↓
6. Upload Output File
   ↓
7. Mark Job as "completed"
   ↓
8. Return to Step 1
```

### Error Handling

If processing fails:
1. Increment retry count
2. If retries < `MAX_RETRIES`:
   - Re-queue job
   - Exponential backoff
3. If retries >= `MAX_RETRIES`:
   - Mark job as "failed"
   - Set `failureReason` in database

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8082` | HTTP server port (internal) |
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `UPLOAD_DIR` | `/app/uploads` | Input files directory |
| `OUTPUT_DIR` | `/app/outputs` | Output files directory |
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |
| `MAX_RETRIES` | `3` | Max retry attempts for failed jobs |
| `PROCESSING_TIMEOUT` | `30m` | Maximum time for job processing |

## File Size Limits

- **Maximum Input Size**: 50 MB (enforced by upload-service)
- **Maximum Output Size**: No explicit limit (depends on conversion)
- **Temporary Storage**: Cleaned up after job completion

## Supported Input Formats

- **PDF**: `.pdf` (any version)
- **Multiple PDFs**: For merge operations

## Dependencies

### LibreOffice

Used for PDF to Office format conversions.

**Installed Packages**:
- `libreoffice-writer` - PDF to Word
- `libreoffice-calc` - PDF to Excel
- `libreoffice-impress` - PDF to PowerPoint

**Command Line**:
```bash
libreoffice --headless --convert-to docx --outdir /output /input/file.pdf
```

### pdfcpu

Pure Go PDF processor for utility operations.

**Operations**:
- Merge, split, compress
- Encrypt/decrypt (protect/unlock)
- Watermark
- Optimize

**Installation**: Compiled into service binary

### Poppler

PDF rendering library for image conversion.

**Tools Used**:
- `pdftoppm` - PDF to PPM/JPEG conversion

**Installed Packages**:
- `poppler-utils`

## Deployment

### Docker Compose

```yaml
convert-from-pdf:
  build:
    context: ./convert-from-pdf
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

1. Install dependencies:
   ```bash
   # Ubuntu/Debian
   sudo apt-get install libreoffice poppler-utils

   # macOS
   brew install libreoffice poppler
   ```

2. Start dependencies:
   ```bash
   docker compose up -d db redis
   ```

3. Run service:
   ```bash
   cd convert-from-pdf
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs"
   export REDIS_ADDR="localhost:6379"
   go run main.go
   ```

## Troubleshooting

### Jobs Stuck in Queue

**Symptoms**: Jobs remain in "queued" status

**Possible Causes**:
- Worker not running
- Redis connection issue
- Queue key mismatch

**Solutions**:
```bash
# Check worker status
docker compose ps convert-from-pdf

# Check Redis queues
docker compose exec redis redis-cli keys "queue:*"

# Check queue length
docker compose exec redis redis-cli llen "queue:pdf-to-word"

# Restart worker
docker compose restart convert-from-pdf
```

### Conversion Failures

**Symptoms**: Jobs fail with error

**Common Errors**:

1. **LibreOffice timeout**:
   - Increase `PROCESSING_TIMEOUT`
   - Check CPU/memory resources

2. **File not found**:
   - Verify file paths in job metadata
   - Check volume mounts

3. **Corrupted PDF**:
   - Input PDF may be damaged
   - Try opening PDF in a viewer first

**Check Logs**:
```bash
docker compose logs -f convert-from-pdf
```

### LibreOffice Issues

**Symptoms**: PDF to Office conversions fail

**Solutions**:
```bash
# Test LibreOffice in container
docker compose exec convert-from-pdf libreoffice --version

# Manual conversion test
docker compose exec convert-from-pdf \
  libreoffice --headless --convert-to docx \
  --outdir /app/outputs /app/uploads/test.pdf
```

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

## Performance

### Processing Times (Typical)

| Tool | 1-page PDF | 10-page PDF | 100-page PDF |
|------|-----------|-------------|--------------|
| pdf-to-word | 2-3s | 5-10s | 30-60s |
| pdf-to-image | 1-2s | 3-5s | 20-30s |
| merge-pdf | <1s | 1-2s | 3-5s |
| compress-pdf | 1-2s | 3-5s | 15-25s |

**Note**: Times vary based on PDF complexity and server resources.

### Optimization Tips

1. **Increase Workers**: Run multiple instances
2. **Resource Limits**: Ensure adequate CPU/memory
3. **Queue Management**: Monitor queue depths
4. **Cleanup**: Regular cleanup of old files

## Related Documentation

- [Upload Service](../upload-service/UPLOAD_SERVICE.md) - Job creation and management
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing
- [Main README](../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f convert-from-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Check queues: `docker compose exec redis redis-cli keys "queue:*"`
