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

## API Endpoints

All endpoints follow RESTful conventions:

### Job Creation
```
POST /api/organize-pdf/:tool
```
Creates a new processing job for the specified tool.

**Parameters:**
- `:tool` - One of: merge-pdf, split-pdf, rotate-pdf, remove-pages, extract-pages, organize-pdf, scan-to-pdf
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

## Sequence Diagrams

### Job Processing Flow (NATS Worker)

```mermaid
sequenceDiagram
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant DB as PostgreSQL
    participant FS as File System
    participant PC as pdfcpu

    JS->>NATS: Publish JobCreated to dispatch.organize-pdf
    NATS->>W: Deliver JobCreated event

    W->>W: Validate toolType in AllowedTools
    W->>DB: UPDATE processing_jobs SET status='processing'

    W->>FS: Read input PDF file(s) from inputPaths

    alt merge-pdf
        W->>PC: pdfcpu merge [file1.pdf, file2.pdf, ...] -> merged.pdf
    else split-pdf
        W->>PC: pdfcpu split input.pdf by page range
        W->>FS: Create ZIP archive of split pages
    else remove-pages
        W->>PC: pdfcpu remove pages [2,4,6] from input.pdf
    else extract-pages
        W->>PC: pdfcpu extract pages [1,3,5-7] from input.pdf
    else rotate-pdf
        W->>PC: pdfcpu rotate pages by 90/180/270 degrees
    else organize-pdf (reorder)
        W->>PC: pdfcpu reorder pages [3,1,2,4] in input.pdf
    else scan-to-pdf
        W->>PC: pdfcpu import images to PDF
    end

    alt Processing succeeds
        PC-->>W: Output file produced
        W->>FS: Write output to output directory
        W->>DB: INSERT file_metadata (kind='output')
        W->>DB: UPDATE processing_jobs SET status='completed', progress=100
        W->>NATS: Ack message
    else Processing fails
        W->>DB: UPDATE processing_jobs SET status='failed', failure_reason=error
        W->>NATS: Ack message
    end
```

### Merge PDF Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant FS as File System

    C->>JS: POST /api/organize-pdf/merge-pdf (files: [doc1.pdf, doc2.pdf, doc3.pdf])
    JS->>JS: Save all files to uploads/<jobId>/
    JS->>NATS: Publish JobCreated {inputPaths: [doc1.pdf, doc2.pdf, doc3.pdf]}

    NATS->>W: Deliver JobCreated
    W->>FS: Read all input PDFs
    W->>W: pdfcpu.MergeCreateFile(inputPaths, outputPath)
    W->>FS: Write merged.pdf
    W-->>W: Job completed
```

### Split PDF Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant JS as Job Service
    participant NATS as NATS JetStream
    participant W as organize-pdf Worker
    participant FS as File System

    C->>JS: POST /api/organize-pdf/split-pdf {files: [doc.pdf], options: {range: "1-3,5"}}
    JS->>NATS: Publish JobCreated {inputPaths: [doc.pdf], options: {range: "1-3,5"}}

    NATS->>W: Deliver JobCreated
    W->>FS: Read input PDF
    W->>W: Parse page range "1-3,5"
    W->>W: Extract pages 1, 2, 3, 5 into individual PDFs
    W->>FS: Create ZIP archive of extracted pages
    W->>FS: Write output.zip
    W-->>W: Job completed
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

Part of the EsyDocs project.

## Support

For issues and questions:
- Check logs: `docker logs organize-pdf`
- Review queue status: `redis-cli`
- Verify database: Check processing_jobs table
