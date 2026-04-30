> **DEPRECATED:** Upload functionality has been absorbed into the [Job Service](./JOB_SERVICE.md). This document is retained for historical reference. See JOB_SERVICE.md for current upload and job management documentation.

# Upload Service

## Overview

The Upload Service is the core backend service that handles file uploads, job management, and user data. It coordinates file uploads through a chunked upload mechanism and manages the lifecycle of conversion jobs that are processed by worker services.

**Port**: 8081
**Type**: REST API
**Framework**: Gin (Go)
**Database**: PostgreSQL
**Cache/Queue**: Redis

## Responsibilities

1. **File Upload Management** - Chunked file uploads with progress tracking
2. **Job Orchestration** - Create and manage conversion jobs
3. **User Authentication** - Handle user signup, login, logout (see [AUTH_SERVICE.md](./AUTH_SERVICE.md))
4. **Job Queue Management** - Enqueue jobs for worker processing
5. **File Storage** - Temporary storage for uploaded files and conversion outputs

## Architecture

```
Client
  ↓
API Gateway :8080
  ↓
Upload Service :8081
  ├─ Chunked Upload Handler
  ├─ Job Management Handler
  ├─ Auth Handlers (see AUTH_SERVICE.md)
  ├─ PostgreSQL (job metadata)
  └─ Redis (job queue & cache)
       ↓
  Worker Services
  (convert-from-pdf, convert-to-pdf)
```

## API Endpoints

For authentication endpoints, see [AUTH_SERVICE.md](./AUTH_SERVICE.md).

### Upload Endpoints

#### Initialize Upload
```http
POST /api/upload/init
Content-Type: application/json

{
  "fileName": "document.pdf",
  "fileSize": 1048576,
  "totalChunks": 4
}
```

**Response** (201 Created):
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response** (413 Request Entity Too Large):
```json
{
  "success": false,
  "message": "File exceeds plan limit",
  "error": {
    "code": "FILE_TOO_LARGE",
    "details": "file size exceeds your plan limit of 25 MB"
  }
}
```

Creates a new upload session for chunked file upload. The `fileSize` is validated against the caller's plan limit, read from the `X-User-Plan-Max-File-MB` header (forwarded by the API Gateway). If the header is absent, a default of 10 MB (anonymous limit) is applied.

---

#### Upload Chunk
```http
PUT /api/upload/{uploadId}/chunk?index=0
Content-Type: multipart/form-data

chunk: [binary data]
```

**Response** (200 OK):
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "fileName": "document.pdf",
  "fileSize": 1048576,
  "totalChunks": 4,
  "receivedChunks": 1,
  "complete": false
}
```

Uploads a single chunk of the file. Chunks can be uploaded in any order.

---

#### Get Upload Status
```http
GET /api/upload/{uploadId}/status
```

**Response** (200 OK):
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "fileName": "document.pdf",
  "fileSize": 1048576,
  "totalChunks": 4,
  "receivedChunks": 3,
  "complete": false
}
```

---

#### Complete Upload
```http
POST /api/upload/{uploadId}/complete
```

**Response** (200 OK):
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "storedPath": "uploads/550e8400-e29b-41d4-a716-446655440000/document.pdf"
}
```

Assembles all uploaded chunks into a single file. The assembled file size is re-validated against the `X-User-Plan-Max-File-MB` limit (default 10 MB if absent). Returns HTTP 413 with `FILE_TOO_LARGE` if the assembled size exceeds the limit. The upload is now ready to be used for conversion jobs.

**Note**: A completed upload is consumed when used in a conversion job and cannot be reused.

---

### Job Management Endpoints

#### List Jobs
```http
GET /api/jobs?status=completed&limit=50&offset=0
```

**Query Parameters**:
- `status` (optional): Filter by status (`pending`, `processing`, `completed`, `failed`)
- `toolType` (optional): Filter by tool type (e.g., `word-to-pdf`)
- `limit` (optional): Max results (default: 50, max: 100)
- `offset` (optional): Pagination offset (default: 0)

**Response** (200 OK):
```json
{
  "jobs": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "userId": "user-uuid",
      "toolType": "word-to-pdf",
      "status": "completed",
      "progress": 100,
      "fileName": "document.docx",
      "fileSize": "123.45 KB",
      "failureReason": null,
      "metadata": {
        "originalFilename": "document.docx",
        "outputFilename": "document.pdf"
      },
      "createdAt": "2024-01-15T10:30:00Z",
      "updatedAt": "2024-01-15T10:30:15Z",
      "completedAt": "2024-01-15T10:30:15Z",
      "expiresAt": "2024-01-16T10:30:15Z"
    }
  ],
  "total": 42,
  "limit": 50,
  "offset": 0
}
```

---

#### Get Job Details
```http
GET /api/jobs/{jobId}
```

**Response** (200 OK):
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-uuid",
  "toolType": "word-to-pdf",
  "status": "completed",
  "progress": 100,
  "fileName": "document.docx",
  "fileSize": "123.45 KB",
  "failureReason": null,
  "metadata": {
    "originalFilename": "document.docx",
    "outputFilename": "document.pdf",
    "fileSize": 126373
  },
  "createdAt": "2024-01-15T10:30:00Z",
  "updatedAt": "2024-01-15T10:30:15Z",
  "completedAt": "2024-01-15T10:30:15Z",
  "expiresAt": "2024-01-16T10:30:15Z"
}
```

**Job Status Values**:
- `queued` - Job created and waiting in queue
- `processing` - Worker is processing the job
- `completed` - Job finished successfully
- `failed` - Job failed with error

---

#### Delete Job
```http
DELETE /api/jobs/{jobId}
```

**Response**: 204 No Content

Deletes a job and its associated files from the system.

---

## Upload Flow

### Chunked Upload Process

The service uses a chunked upload mechanism to handle large files reliably:

1. **Initialize**: Client calls `/api/upload/init` with file metadata
2. **Upload Chunks**: Client uploads file in chunks using `/api/upload/{id}/chunk?index=N`
   - Chunks can be uploaded in parallel
   - Chunks can be retried independently if they fail
   - Progress is tracked in Redis
3. **Complete**: Client calls `/api/upload/{id}/complete` to assemble chunks
4. **Use**: The completed upload can be used to create a conversion job

### Upload Lifecycle

```
┌──────────────┐
│  Initialize  │
│   (init)     │
└──────┬───────┘
       ↓
┌──────────────┐
│ Upload Chunks│ ← Can retry individual chunks
│   (chunks)   │
└──────┬───────┘
       ↓
┌──────────────┐
│   Complete   │
│  (assemble)  │
└──────┬───────┘
       ↓
┌──────────────┐
│ Create Job   │ ← Upload consumed here
│ (conversion) │
└──────────────┘
```

### Upload Expiration

- Uploads expire after `UPLOAD_TTL` (default: 30 minutes)
- Expired uploads are cleaned up by the cleanup-worker
- Once used in a job, uploads are marked as consumed

## Job Management

### Job Lifecycle

```
┌─────────┐
│ queued  │ ← Job created, waiting in Redis queue
└────┬────┘
     ↓
┌──────────┐
│processing│ ← Worker picked up job
└────┬─────┘
     ↓
┌──────────┐  ┌────────┐
│completed │  │ failed │
└──────────┘  └────────┘
```

### Job Queue

Jobs are queued in Redis for processing by worker services:

**Queue Key Format**: `{QUEUE_PREFIX}:{toolType}`

Examples:
- `queue:word-to-pdf`
- `queue:pdf-to-word`
- `queue:merge-pdf`

Workers subscribe to specific queue keys and process jobs independently.

### Job Metadata

Jobs store metadata in PostgreSQL including:
- User ID (or guest token)
- Tool type (e.g., `word-to-pdf`)
- File paths (input and output)
- Status and progress
- Timestamps (created, updated, completed)
- Expiration time
- Failure reason (if failed)
- Custom metadata (tool-specific options)

## Environment Variables

### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://user:password@db:5432/fyredocs?sslmode=disable` |
| `REDIS_ADDR` | Redis server address | `redis:6379` |
| `JWT_HS256_SECRET` | JWT signing secret (32+ chars) | See [AUTH_SERVICE.md](./AUTH_SERVICE.md) |

### File Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `UPLOAD_DIR` | `/app/uploads` | Directory for uploaded files |
| `OUTPUT_DIR` | `/app/outputs` | Directory for conversion outputs |
| `MAX_UPLOAD_MB` | `50` | Maximum file size in MB (server-side hard cap; per-user plan limits are enforced via `X-User-Plan-Max-File-MB` header from the API Gateway) |
| `UPLOAD_TTL` | `30m` | Upload expiration time |

### Job Processing

| Variable | Default | Description |
|----------|---------|-------------|
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |
| `GUEST_JOB_TTL` | `2h` | Guest job expiration time |
| `MAX_RETRIES` | `3` | Maximum retry attempts for failed jobs |
| `PROCESSING_TIMEOUT` | `30m` | Job processing timeout |

### Database & Redis

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8081` | HTTP server port |
| `TRUSTED_PROXIES` | `172.18.0.0/16,127.0.0.1,::1` | Trusted proxy IP ranges |

For authentication-related environment variables, see [AUTH_SERVICE.md](./AUTH_SERVICE.md).

## Database Schema

### Tables

#### users
Stores user accounts and authentication information.

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    full_name VARCHAR(255) NOT NULL,
    country VARCHAR(2) NOT NULL,
    phone VARCHAR(20),
    image TEXT,
    role VARCHAR(20) DEFAULT 'user',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

#### processing_jobs
Stores all conversion job metadata.

```sql
CREATE TABLE processing_jobs (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    guest_token VARCHAR(255),
    tool_type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL,
    progress INT DEFAULT 0,
    file_path TEXT,
    output_path TEXT,
    file_name VARCHAR(255),
    file_size BIGINT,
    failure_reason TEXT,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP,
    expires_at TIMESTAMP
);
```

#### uploads
Tracks chunked upload sessions.

```sql
CREATE TABLE uploads (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    file_name VARCHAR(255) NOT NULL,
    file_size BIGINT NOT NULL,
    total_chunks INT NOT NULL,
    received_chunks INT DEFAULT 0,
    complete BOOLEAN DEFAULT FALSE,
    consumed BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP
);
```

## Deployment

### Docker Compose

The service is configured in [docker-compose.yml](../docker-compose.yml):

```yaml
upload-service:
  build:
    context: ./upload-service
  ports:
    - "8081:8081"
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    JWT_HS256_SECRET: ${JWT_HS256_SECRET}
    # ... other variables
  volumes:
    - uploads_data:/app/uploads
    - outputs_data:/app/outputs
  depends_on:
    - db
    - redis
```

### Local Development

1. Start dependencies:
   ```bash
   docker compose up -d db redis
   ```

2. Set environment variables:
   ```bash
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs?sslmode=disable"
   export REDIS_ADDR="localhost:6379"
   export JWT_HS256_SECRET=$(openssl rand -hex 32)
   ```

3. Run the service:
   ```bash
   cd upload-service
   go run main.go
   ```

## Troubleshooting

### Upload Issues

#### Chunks Not Being Accepted

**Symptoms**: Chunk uploads return 400 or 404

**Solutions**:
```bash
# Check upload exists
docker compose exec redis redis-cli keys "upload:*"

# Check upload expiration
docker compose logs upload-service | grep "upload expired"

# Verify chunk index
# Index must be 0-based and < totalChunks
```

#### Upload Complete Fails

**Symptoms**: `/upload/{id}/complete` returns error

**Possible Causes**:
- Not all chunks received
- Upload already consumed
- Upload expired

**Solutions**:
```bash
# Check upload status first
curl http://localhost:8081/api/upload/{id}/status

# Verify all chunks present
docker compose exec upload-service ls -la /app/uploads/{uploadId}/chunks/
```

### Job Issues

#### Jobs Stuck in Queue

**Symptoms**: Jobs remain in `queued` status

**Possible Causes**:
- Worker services not running
- Redis connection issues
- Queue key mismatch

**Solutions**:
```bash
# Check worker services
docker compose ps convert-from-pdf convert-to-pdf

# Check Redis queues
docker compose exec redis redis-cli keys "queue:*"

# Check queue lengths
docker compose exec redis redis-cli llen "queue:word-to-pdf"

# Restart workers
docker compose restart convert-from-pdf convert-to-pdf
```

#### Jobs Fail Immediately

**Symptoms**: Jobs go to `failed` status quickly

**Solutions**:
```bash
# Check job failure reason
curl http://localhost:8081/api/jobs/{jobId}

# Check worker logs
docker compose logs convert-from-pdf
docker compose logs convert-to-pdf

# Verify file paths
docker compose exec upload-service ls -la /app/uploads/
docker compose exec upload-service ls -la /app/outputs/
```

### Database Issues

#### Connection Errors

**Symptoms**: Service fails to start or returns 500 errors

**Solutions**:
```bash
# Check database status
docker compose ps db

# Test database connection
docker compose exec upload-service pg_isready -h db -U user -d fyredocs

# Check database logs
docker compose logs db

# Restart database
docker compose restart db upload-service
```

## Monitoring

### Key Metrics

- **Upload Success Rate**: % of uploads that complete successfully
- **Job Processing Time**: Time from queue to completion
- **Job Failure Rate**: % of jobs that fail
- **Queue Depth**: Number of jobs waiting in queue
- **Database Connection Pool**: Active/idle connections
- **Redis Memory Usage**: Memory consumption

### Health Checks

```bash
# Service health
curl http://localhost:8081/healthz

# Database health
docker compose exec db pg_isready -U user -d fyredocs

# Redis health
docker compose exec redis redis-cli ping
```

## Sequence Diagrams

### Chunked Upload Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant US as Upload Service :8081
    participant R as Redis
    participant FS as File System

    C->>GW: POST /api/upload/init {fileName, fileSize, totalChunks}
    GW->>US: Proxy request
    US->>R: HSET upload:<id> {fileName, fileSize, totalChunks, createdAt}
    US->>R: EXPIRE upload:<id> 2h
    US-->>GW: 201 {uploadId}
    GW-->>C: 201 {uploadId}

    loop For each chunk (can be parallel)
        C->>GW: PUT /api/upload/<id>/chunk?index=N [chunk binary]
        GW->>US: Proxy request
        US->>R: HGETALL upload:<id> (verify exists)
        US->>FS: Save chunk to uploads/tmp/<id>/<index>.part
        US->>R: SADD upload:<id>:chunks N
        US-->>GW: 200 {receivedChunks, complete}
        GW-->>C: 200 {receivedChunks, complete}
    end

    C->>GW: POST /api/upload/<id>/complete
    GW->>US: Proxy request
    US->>R: SCARD upload:<id>:chunks
    US->>US: Verify all chunks received
    US->>FS: Assemble chunks into uploads/<id>/<fileName>
    US->>FS: Remove tmp/<id>/ directory
    US-->>GW: 200 {uploadId, storedPath}
    GW-->>C: 200 {uploadId, storedPath}
```

### Job Creation via Upload ID

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant US as Upload Service :8081
    participant R as Redis
    participant DB as PostgreSQL

    C->>GW: POST /api/convert-from-pdf/pdf-to-word {uploadId}
    GW->>US: Proxy (with auth headers)

    US->>R: HGETALL upload:<uploadId>
    R-->>US: {fileName, fileSize, ...}
    US->>US: Validate file type for tool
    US->>US: Move file to job directory

    US->>DB: INSERT processing_job (status=queued)
    US->>R: RPUSH queue:pdf-to-word {jobPayload}
    US->>R: DEL upload:<uploadId> (consume upload)

    US-->>GW: 201 {job object}
    GW-->>C: 201 {job object}
```

### Job Status Polling and Download

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant US as Upload Service :8081
    participant DB as PostgreSQL
    participant FS as File System

    loop Poll until completed
        C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>
        GW->>US: Proxy request
        US->>DB: SELECT * FROM processing_jobs WHERE id=<jobId>
        US-->>GW: 200 {status, progress}
        GW-->>C: 200 {status, progress}
    end

    C->>GW: GET /api/convert-from-pdf/pdf-to-word/<jobId>/download
    GW->>US: Proxy request
    US->>DB: Verify status=completed
    US->>FS: Read output file
    US-->>GW: 200 [binary] Content-Disposition: attachment
    GW-->>C: 200 [binary file download]
```

### Authentication Flow (Signup)

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant US as Upload Service :8081
    participant DB as PostgreSQL

    C->>GW: POST /auth/signup {email, password, fullName, country}
    GW->>US: Proxy request

    US->>US: Validate input
    US->>DB: Check if email exists
    US->>US: bcrypt hash password
    US->>DB: INSERT INTO users

    US->>US: Issue JWT access token
    US-->>GW: 200 {user} + Set-Cookie: access_token=<jwt>
    GW-->>C: 200 {user} + Set-Cookie
```

## Error Flows

### Upload Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing fileName, fileSize, or totalChunks |
| `INVALID_INPUT` | 400 | Invalid or negative chunk index |
| `NOT_FOUND` | 404 | Upload session expired or not found |
| `BAD_REQUEST` | 400 | Attempting to complete an incomplete upload |
| `FILE_TOO_LARGE` | 413 | File size exceeds the caller's plan limit (read from `X-User-Plan-Max-File-MB`; default 10 MB) |
| `SERVER_ERROR` | 500 | Redis unavailable or filesystem error |

### Job Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Unsupported tool type |
| `INVALID_INPUT` | 400 | File type mismatch for tool |
| `NOT_FOUND` | 404 | Job not found or unauthorized |
| `NOT_READY` | 400 | Download requested but job not completed |
| `UNAUTHORIZED` | 401 | Job history without authentication |
| `SERVER_ERROR` | 500 | Database or queue failure |

### Error Response Format

```json
{
  "success": false,
  "message": "human readable message",
  "error": {
    "code": "ERROR_CODE",
    "details": "detailed description"
  }
}
```

## Related Documentation

- [AUTH_SERVICE.md](./AUTH_SERVICE.md) - Complete authentication system documentation
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing and CORS
- [Convert From PDF](../convert-from-pdf/CONVERT_FROM_PDF.md) - PDF conversion worker
- [Convert To PDF](../convert-to-pdf/CONVERT_TO_PDF.md) - Document conversion worker
- [Cleanup Worker](../cleanup-worker/CLEANUP_WORKER.md) - Job cleanup service
- [Main README](../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f upload-service`
- Inspect database: `docker compose exec db psql -U user -d fyredocs`
- Check Redis: `docker compose exec redis redis-cli`
