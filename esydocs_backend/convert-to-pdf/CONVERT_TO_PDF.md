# Convert To PDF Service

## Overview

The Convert To PDF service is a worker service that converts various document and image formats to PDF. It handles Word, Excel, PowerPoint, and image file conversions using LibreOffice and other open-source tools.

**Port**: 8083 (internal, not exposed through API Gateway)
**Type**: Background Worker + REST API
**Framework**: Gin (Go)
**Processing**: LibreOffice, ImageMagick/pdfcpu

## Responsibilities

1. **Office to PDF** - Convert Word/Excel/PowerPoint documents to PDF
2. **Image to PDF** - Convert images (JPG, PNG, GIF, WebP) to PDF
3. **Job Processing** - Pick jobs from Redis queue and process them
4. **Status Updates** - Update job status and progress in database

## Architecture

```
Redis Queue (queue:word-to-pdf, queue:image-to-pdf, etc.)
  ↓
Convert-To-PDF Worker
  ├─ Poll Queue
  ├─ Download Input File(s)
  ├─ Process with LibreOffice/ImageMagick
  ├─ Upload Output PDF
  └─ Update Job Status (PostgreSQL)
```

## Supported Tools

| Tool | Input Formats | Output | Status |
|------|--------------|--------|--------|
| `word-to-pdf` | .doc, .docx | .pdf | ✅ Implemented |
| `excel-to-pdf` | .xls, .xlsx | .pdf | ✅ Implemented |
| `ppt-to-pdf` | .ppt, .pptx | .pdf | ✅ Implemented |
| `powerpoint-to-pdf` | .ppt, .pptx | .pdf | ✅ Alias for ppt-to-pdf |
| `image-to-pdf` | .jpg, .jpeg, .png, .gif, .webp, .bmp | .pdf | ✅ Implemented |
| `img-to-pdf` | .jpg, .jpeg, .png, .gif, .webp, .bmp | .pdf | ✅ Alias for image-to-pdf |

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

### ppt-to-pdf / powerpoint-to-pdf

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

### image-to-pdf / img-to-pdf

Converts images to PDF format.

**Input**: `.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`, `.bmp`
**Output**: `.pdf`
**Implementation**: pdfcpu / ImageMagick

**Features**:
- Multiple images combined into single PDF (one image per page)
- Maintains image quality
- Automatic page sizing to fit image dimensions
- Supports various image formats

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

**Image Quality**:
- Original image quality preserved
- No re-compression (lossless embedding)
- Appropriate for scanning/document digitization

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
5. Execute Conversion
   ├─ LibreOffice for Office docs
   └─ pdfcpu/ImageMagick for images
   ↓
6. Upload Output PDF
   ↓
7. Mark Job as "completed"
   ↓
8. Return to Step 1
```

### Error Handling

If processing fails:
1. Increment retry count
2. If retries < `MAX_RETRIES`:
   - Re-queue job with exponential backoff
3. If retries >= `MAX_RETRIES`:
   - Mark job as "failed"
   - Set `failureReason` in database

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

## File Size Limits

- **Maximum Input Size**: 50 MB (enforced by upload-service)
- **PDF Output Size**: Depends on input content
- **Recommended**: Keep documents under 50 pages for optimal processing

## Supported Input Formats

### Office Documents

| Format | Extension | Versions |
|--------|-----------|----------|
| Word | .doc, .docx | Word 97-2019 |
| Excel | .xls, .xlsx | Excel 97-2019 |
| PowerPoint | .ppt, .pptx | PowerPoint 97-2019 |

### Image Formats

| Format | Extensions | Notes |
|--------|-----------|-------|
| JPEG | .jpg, .jpeg | Most common, lossy compression |
| PNG | .png | Lossless, supports transparency |
| GIF | .gif | Supports animation (first frame used) |
| WebP | .webp | Modern format, good compression |
| BMP | .bmp | Uncompressed, large file size |

## Dependencies

### LibreOffice

Used for Office document conversions.

**Installed Packages**:
- `libreoffice-writer` - Word to PDF
- `libreoffice-calc` - Excel to PDF
- `libreoffice-impress` - PowerPoint to PDF

**Command Example**:
```bash
libreoffice --headless --convert-to pdf --outdir /output /input/file.docx
```

**Conversion Quality**: Production-quality PDF output

### pdfcpu

Pure Go PDF library for image to PDF conversion.

**Features**:
- Fast image embedding
- No re-compression
- Automatic page sizing

### ImageMagick (Optional)

Image processing library for advanced image operations.

**Tools Used**:
- `convert` - Image format conversion
- `identify` - Image metadata extraction

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

## Troubleshooting

### Jobs Stuck in Queue

**Symptoms**: Jobs remain in "queued" status indefinitely

**Solutions**:
```bash
# Check worker status
docker compose ps convert-to-pdf

# Verify Redis queues
docker compose exec redis redis-cli keys "queue:*"

# Check specific queue length
docker compose exec redis redis-cli llen "queue:word-to-pdf"

# Restart worker
docker compose restart convert-to-pdf
```

### Conversion Failures

#### Common Error: "Unsupported File Format"

**Cause**: File extension doesn't match actual file type

**Solution**: Verify file type:
```bash
docker compose exec convert-to-pdf file /app/uploads/file.docx
```

#### Common Error: "LibreOffice Timeout"

**Cause**: Document too large or complex

**Solutions**:
- Increase `PROCESSING_TIMEOUT`
- Simplify document (remove large images, reduce pages)
- Increase container memory limits

#### Common Error: "Corrupted Document"

**Cause**: Input file is damaged

**Solution**: Try opening the file in the native application first to verify it's valid

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

3. **Reduce Concurrent Processing**: Limit to 1 worker per instance

### Image Conversion Issues

**Symptoms**: Image-to-PDF jobs fail

**Common Causes**:
1. Image file corrupted
2. Unsupported image format
3. Image too large

**Solutions**:
```bash
# Verify image file
docker compose exec convert-to-pdf identify /app/uploads/image.jpg

# Check image dimensions
docker compose exec convert-to-pdf \
  identify -format "%wx%h" /app/uploads/image.jpg
```

## Performance

### Processing Times (Typical)

| Tool | Small File | Medium File | Large File |
|------|-----------|-------------|------------|
| word-to-pdf | 2-3s | 5-8s | 15-30s |
| excel-to-pdf | 2-4s | 6-10s | 20-40s |
| ppt-to-pdf | 3-5s | 8-12s | 25-45s |
| image-to-pdf | <1s | 1-2s | 3-5s |

**File Sizes**:
- Small: < 1 MB, < 10 pages
- Medium: 1-5 MB, 10-50 pages
- Large: 5-50 MB, 50-100 pages

### Optimization Tips

1. **Multiple Workers**: Run several instances
2. **Resource Allocation**: Ensure adequate CPU/memory (2 GB recommended)
3. **Queue Monitoring**: Alert on growing queue depths
4. **Input Validation**: Reject oversized files early

## Related Documentation

- [Upload Service](../upload-service/UPLOAD_SERVICE.md) - Job creation and management
- [Convert From PDF](../convert-from-pdf/CONVERT_FROM_PDF.md) - PDF conversion service
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing
- [Main README](../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f convert-to-pdf`
- Inspect jobs: Query `processing_jobs` table in PostgreSQL
- Monitor queues: `docker compose exec redis redis-cli keys "queue:*"`
- Test LibreOffice: `docker compose exec convert-to-pdf libreoffice --version`
