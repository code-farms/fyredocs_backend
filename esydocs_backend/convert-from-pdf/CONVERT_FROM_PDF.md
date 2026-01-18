# Convert From PDF Service

## Overview

The Convert From PDF service converts PDF files to other formats including images, Office documents (Word, Excel, PowerPoint), HTML, plain text, and PDF/A archival format.

**Port**: 8082 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: LibreOffice, Ghostscript, Poppler (pdftotext, pdftohtml, pdftoppm)

## Responsibilities

1. **PDF to Image** - Convert PDF pages to PNG images
2. **PDF to Office** - Convert PDF to Word/Excel/PowerPoint formats
3. **PDF to HTML** - Convert PDF to HTML format
4. **PDF to Text** - Extract plain text from PDF
5. **PDF to PDF/A** - Convert PDF to archival PDF/A format
6. **Job Processing** - Pick jobs from Redis queue and process them
7. **Status Updates** - Update job status and progress in database

## Architecture

```
Redis Queue (queue:pdf-to-image, queue:pdf-to-word, etc.)
  ↓
Convert-From-PDF Worker
  ├─ Poll Queue
  ├─ Download Input File
  ├─ Process with LibreOffice/Ghostscript/Poppler
  ├─ Upload Output File
  └─ Update Job Status (PostgreSQL)
```

## Supported Tools

| Tool | Input | Output | Implementation | Status |
|------|-------|--------|----------------|--------|
| `pdf-to-image` | .pdf | .zip (PNGs) | pdftoppm (Poppler) | ✅ Implemented |
| `pdf-to-img` | .pdf | .zip (PNGs) | pdftoppm (Poppler) | ✅ Alias |
| `pdf-to-word` | .pdf | .docx | LibreOffice Writer | ✅ Implemented |
| `pdf-to-docx` | .pdf | .docx | LibreOffice Writer | ✅ Alias |
| `pdf-to-excel` | .pdf | .xlsx | LibreOffice Calc | ✅ Implemented |
| `pdf-to-xlsx` | .pdf | .xlsx | LibreOffice Calc | ✅ Alias |
| `pdf-to-ppt` | .pdf | .pptx | LibreOffice Impress | ✅ Implemented |
| `pdf-to-powerpoint` | .pdf | .pptx | LibreOffice Impress | ✅ Alias |
| `pdf-to-pptx` | .pdf | .pptx | LibreOffice Impress | ✅ Alias |
| `pdf-to-html` | .pdf | .zip (HTML+images) | pdftohtml (Poppler) | ✅ Implemented |
| `pdf-to-text` | .pdf | .txt | pdftotext (Poppler) | ✅ Implemented |
| `pdf-to-txt` | .pdf | .txt | pdftotext (Poppler) | ✅ Alias |
| `pdf-to-pdfa` | .pdf | .pdf (PDF/A-2b) | Ghostscript | ✅ Implemented |

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
**Output**: `.zip` containing PNG files (one per page)
**Implementation**: Poppler (pdftoppm)

**Output Format**:
```
output.zip
├── page_001-1.png
├── page_002-1.png
└── page_003-1.png
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
**Implementation**: LibreOffice Writer (PDF import filter)

**Limitations**:
- Complex layouts may not convert perfectly
- Scanned PDFs will contain images, not editable text
- Fonts may be substituted

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
**Implementation**: LibreOffice Impress

**Behavior**: Each PDF page becomes a slide

**Limitations**:
- Content is placed as images/shapes
- Original animations not preserved

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
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs"
   export REDIS_ADDR="localhost:6379"
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

## Related Documentation

- [Convert To PDF](../convert-to-pdf/CONVERT_TO_PDF.md) - Convert files TO PDF
- [Organize PDF](../organize-pdf/ORGANIZE_PDF.md) - PDF manipulation (merge, split, etc.)
- [Optimize PDF](../optimize-pdf/OPTIMIZE_PDF.md) - PDF compression, repair, OCR
- [Upload Service](../upload-service/UPLOAD_SERVICE.md) - Job creation and management
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing

## Support

For issues:
- Check logs: `docker compose logs -f convert-from-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Check queues: `docker compose exec redis redis-cli keys "queue:*"`
