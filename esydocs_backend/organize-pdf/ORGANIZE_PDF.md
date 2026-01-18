# Organize-PDF Service

A microservice for PDF organization operations including merging, splitting, removing/extracting pages, reordering, and scanning documents to PDF.

## Overview

The Organize-PDF service provides comprehensive PDF manipulation capabilities using free open-source tools. It's part of the EsyDocs microservices architecture and handles all PDF organization operations.

**Port:** 8084
**Queue:** `queue:organize-pdf`

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

### 6. Scan to PDF
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

## API Endpoints

All endpoints follow RESTful conventions:

### Job Creation
```
POST /api/organize-pdf/:tool
```
Creates a new processing job for the specified tool.

**Parameters:**
- `:tool` - One of: merge-pdf, split-pdf, remove-pages, extract-pages, organize-pdf, scan-to-pdf
- Form data: `files` (multipart/form-data)
- Form data: `options` (JSON string, optional)

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
DATABASE_URL="postgresql://user:password@localhost:5432/esydocs?sslmode=disable"
REDIS_ADDR="localhost:6379"
REDIS_PASSWORD=""
REDIS_DB="0"
```

### Service Configuration
```env
PORT="8084"
QUEUE_PREFIX="queue"
PROCESSING_TIMEOUT="30m"
MAX_RETRIES="3"
OUTPUT_DIR="outputs"
```

### JWT Authentication
```env
JWT_ALLOWED_ALGS="HS256"
JWT_HS256_SECRET="your-secret-here-min-32-chars"
JWT_ISSUER="esydocs"
JWT_AUDIENCE="esydocs-api"
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
Client Request → API Handler → Create Job → Redis Queue
                                               ↓
                                            Worker
                                               ↓
                                    Process File (pdfcpu)
                                               ↓
                                    Update Job Status
                                               ↓
                                    Store Output File
```

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
  -v uploads:/app/uploads \
  -v outputs:/app/outputs \
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
  volumes:
    - uploads_data:/app/uploads
    - outputs_data:/app/outputs
```

## Development

### Local Setup
```bash
cd esydocs_backend/organize-pdf

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

### Redis Queue
```bash
# Check queue length
redis-cli LLEN queue:organize-pdf

# View processing jobs
redis-cli LRANGE queue:organize-pdf:processing 0 -1
```

## Troubleshooting

### Service Won't Start
1. Check database connection
2. Verify Redis is running
3. Ensure JWT_HS256_SECRET is set
4. Check port 8084 is available

### Jobs Stuck in Pending
1. Verify worker is running
2. Check Redis queue: `redis-cli LLEN queue:organize-pdf`
3. Review worker logs for errors

### Processing Failures
1. Check file format compatibility
2. Verify page ranges are valid
3. Ensure sufficient disk space
4. Check worker timeout settings

## Future Enhancements

- Page thumbnails for preview
- Batch processing for multiple documents
- Advanced OCR with multi-language support
- PDF metadata editing
- Page rotation during organization
- Watermarking support
- PDF compression options

## License

Part of the EsyDocs project.

## Support

For issues and questions:
- Check logs: `docker logs organize-pdf`
- Review queue status: `redis-cli`
- Verify database: Check processing_jobs table
