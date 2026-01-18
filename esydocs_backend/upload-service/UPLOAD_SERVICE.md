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
3. **User Authentication** - Handle user signup, login, logout (see [AUTHENTICATION.md](./AUTHENTICATION.md))
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
  ├─ Auth Handlers (see AUTHENTICATION.md)
  ├─ PostgreSQL (job metadata)
  └─ Redis (job queue & cache)
       ↓
  Worker Services
  (convert-from-pdf, convert-to-pdf)
```

## API Endpoints

For authentication endpoints, see [AUTHENTICATION.md](./AUTHENTICATION.md).

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

Creates a new upload session for chunked file upload.

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

Assembles all uploaded chunks into a single file. The upload is now ready to be used for conversion jobs.

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

- Uploads expire after `UPLOAD_TTL` (default: 2 hours)
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
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://user:password@db:5432/esydocs?sslmode=disable` |
| `REDIS_ADDR` | Redis server address | `redis:6379` |
| `JWT_HS256_SECRET` | JWT signing secret (32+ chars) | See [AUTHENTICATION.md](./AUTHENTICATION.md) |

### File Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `UPLOAD_DIR` | `/app/uploads` | Directory for uploaded files |
| `OUTPUT_DIR` | `/app/outputs` | Directory for conversion outputs |
| `MAX_UPLOAD_MB` | `50` | Maximum file size in MB |
| `UPLOAD_TTL` | `2h` | Upload expiration time |

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

For authentication-related environment variables, see [AUTHENTICATION.md](./AUTHENTICATION.md).

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
    DATABASE_URL: postgresql://user:password@db:5432/esydocs
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
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs?sslmode=disable"
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
docker compose exec upload-service pg_isready -h db -U user -d esydocs

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
docker compose exec db pg_isready -U user -d esydocs

# Redis health
docker compose exec redis redis-cli ping
```

## Related Documentation

- [AUTHENTICATION.md](./AUTHENTICATION.md) - Complete authentication system documentation
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing and CORS
- [Convert From PDF](../convert-from-pdf/CONVERT_FROM_PDF.md) - PDF conversion worker
- [Convert To PDF](../convert-to-pdf/CONVERT_TO_PDF.md) - Document conversion worker
- [Cleanup Worker](../cleanup-worker/CLEANUP_WORKER.md) - Job cleanup service
- [Main README](../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f upload-service`
- Inspect database: `docker compose exec db psql -U user -d esydocs`
- Check Redis: `docker compose exec redis redis-cli`
