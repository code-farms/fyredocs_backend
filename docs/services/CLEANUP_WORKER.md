# Cleanup Worker Service

## Overview

The Cleanup Worker is a background service that maintains system hygiene by cleaning up expired uploads, jobs, and their associated files. It runs on a scheduled interval and ensures that temporary files and expired data don't accumulate over time.

**Port**: None (background service only, no HTTP server)
**Type**: Background Worker
**Framework**: Go

## Responsibilities

1. **Expired Upload Cleanup** - Delete upload sessions and chunks that have expired
2. **Expired Job Cleanup** - Delete jobs and output files past their expiration time
3. **Orphaned File Cleanup** - Remove files without associated database records
4. **Queue Management** - Clean up stale queue entries
5. **Storage Management** - Prevent disk space exhaustion

## Architecture

```
Cleanup Worker (Background Loop)
  ↓
┌─────────────────────────────────┐
│  Every CLEANUP_INTERVAL (5min)  │
└────────────┬────────────────────┘
             ↓
      ┌──────────────┐
      │ Query Database│
      │ for expired   │
      │ uploads/jobs  │
      └──────┬───────┘
             ↓
      ┌──────────────┐
      │ Delete Files │
      │ from disk    │
      └──────┬───────┘
             ↓
      ┌──────────────┐
      │ Delete DB    │
      │ records      │
      └──────┬───────┘
             ↓
      ┌──────────────┐
      │ Log Results  │
      └──────────────┘
```

When scaled to multiple replicas, a Redis distributed lock (`cleanup-worker:lock`, 10-minute TTL via SETNX) ensures only one instance runs cleanup at a time. If the lock is already held, the instance skips the cycle.

## Cleanup Operations

### 1. Expired Upload Cleanup

**Criteria**: Uploads older than `UPLOAD_TTL` (default: 30 minutes)

**Process**:
1. Query uploads table for expired uploads
   ```sql
   SELECT id, file_path FROM uploads
   WHERE expires_at < NOW() AND NOT consumed
   ```
2. Delete upload directory and chunks
   ```
   /app/uploads/{upload_id}/
   ├── chunks/
   │   ├── chunk_0
   │   ├── chunk_1
   │   └── chunk_2
   └── {filename}
   ```
3. Delete upload record from database

**Files Cleaned**:
- Individual chunks in `chunks/` directory
- Assembled file (if upload was completed)
- Upload session metadata

---

### 2. Expired Job Cleanup

**Criteria**: Jobs where `expires_at` has passed (any user type)

- **Guest Jobs** (no user): `GUEST_JOB_TTL` (default: 30 minutes)
- **Free Plan Users**: `FREE_JOB_TTL` (default: 24 hours)
- **Pro Plan Users**: Never expire (`expires_at = NULL`)

**Process**:
1. Query jobs table for expired jobs
   ```sql
   SELECT * FROM processing_jobs
   WHERE expires_at IS NOT NULL AND expires_at <= NOW()
   LIMIT 100
   ```
2. Delete each file referenced in `file_metadata`
3. Remove the empty job upload directory (`/app/uploads/{job_id}/`)
4. Remove the empty job output directory (`/app/outputs/{job_id}/`)
5. Delete `file_metadata` records from database
6. Delete `processing_jobs` record from database

**Files Cleaned**:
- Input files: `/app/uploads/{job_id}/{filename}`
- Output files: `/app/outputs/{job_id}/{filename}`
- Empty job directories: `/app/uploads/{job_id}/`, `/app/outputs/{job_id}/`

---

### 3. Orphaned File/Directory Cleanup

**Criteria**: Upload directories or output files on disk with no matching `processing_jobs` record

**Process**:
1. Scan `/app/uploads/` for UUID-named directories (skip `tmp`, `.gitkeep`)
2. For each directory, check if a matching `processing_jobs` record exists
3. If no record exists → `os.RemoveAll(dir)` (removes directory and any leftover files)
4. Scan `/app/outputs/` for files matching `{prefix}_{jobID}_{timestamp}.{ext}` pattern
5. Extract job UUID from filename, check if matching record exists
6. If no record exists → `os.Remove(file)`

**When this triggers**: Catches directories/files left behind when the old cleanup code deleted DB records but not directories, or when files are left after incomplete processing.

---

### 4. Expiry Backfill for Legacy Jobs

**Criteria**: Jobs where `user_id IS NOT NULL` and `expires_at IS NULL` (created before plan-based expiration was added)

**Process**:
1. Query for authenticated-user jobs missing `expires_at`
2. Set `expires_at = created_at + FREE_JOB_TTL` (default 24h)
3. These jobs will then be cleaned up by the normal expired job cleanup in the next cycle

**When this triggers**: One-time for any legacy jobs. Once all old jobs have been backfilled, this is a no-op.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `UPLOAD_DIR` | `/app/uploads` | Directory for uploaded files |
| `OUTPUT_DIR` | `/app/outputs` | Directory for output files |
| `UPLOAD_TTL` | `30m` | Upload expiration time |
| `GUEST_JOB_TTL` | `30m` | Guest job expiration time |
| `FREE_JOB_TTL` | `24h` | Free plan user job expiration time (set in job-service) |
| `CLEANUP_INTERVAL` | `5m` | How often to run cleanup |
| `MAX_RETRIES` | `3` | Max retries for failed jobs before cleanup |
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |

### Redis Keys

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `cleanup-worker:lock` | String (SETNX) | 10 minutes | Distributed lock ensuring only one replica runs cleanup per cycle |

## Cleanup Schedule

### Default Schedule

```
Every 5 minutes:
  ├─ Phase 1: Delete expired jobs (guest + free user) and their files/directories
  ├─ Phase 2: Clean up expired upload sessions from Redis and temp chunks
  ├─ Phase 3: Remove orphaned upload dirs and output files (no matching DB record)
  └─ Phase 4: Backfill expires_at on legacy authenticated-user jobs
```

### Customizing Interval

```yaml
environment:
  CLEANUP_INTERVAL: "10m"  # Run every 10 minutes
```

**Recommended Values**:
- Development: `5m` (frequent cleanup)
- Production: `10m` to `30m` (balance frequency vs load)
- High Traffic: `5m` (prevent accumulation)

## Storage Management

### Disk Space Monitoring

The cleanup worker helps prevent disk space exhaustion by:

1. **Regularly removing expired data**
2. **Cleaning up failed job artifacts**
3. **Removing incomplete uploads**

### Estimated Cleanup Impact

**Typical Scenario** (100 jobs/hour):
- Without cleanup: ~5 GB/day accumulation
- With cleanup (2h TTL): ~500 MB average usage

**File Retention**:
- Active uploads: Until expiration or consumption
- Completed jobs (pro user): No expiration
- Completed jobs (free user): 24 hours (configurable via `FREE_JOB_TTL`)
- Completed jobs (guest): 30 minutes (configurable via `GUEST_JOB_TTL`)
- Failed jobs: 24 hours

## Deployment

### Docker Compose

```yaml
cleanup-worker:
  build:
    context: ./cleanup-worker
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/esydocs
    REDIS_ADDR: redis:6379
    UPLOAD_DIR: /app/uploads
    OUTPUT_DIR: /app/outputs
    UPLOAD_TTL: 30m
    CLEANUP_INTERVAL: 5m
    GUEST_JOB_TTL: 30m
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

2. Run worker:
   ```bash
   cd cleanup-worker
   export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs"
   export REDIS_ADDR="localhost:6379"
   export UPLOAD_DIR="./uploads"
   export OUTPUT_DIR="./outputs"
   go run main.go
   ```

### Production Deployment

**Best Practices**:

1. **Multiple replicas supported**: A Redis distributed lock ensures only one instance runs cleanup at a time. Additional replicas provide high availability.
2. **Resource Limits**: Minimal CPU/memory requirements (256MB sufficient)
3. **Volume Access**: Must have read/write access to uploads and outputs volumes
4. **Logging**: Enable structured logging for audit trail
5. **Monitoring**: Track cleanup metrics (files deleted, space freed)

## Logging

### Log Levels

- **INFO**: Cleanup cycles started/completed
- **WARN**: File deletion failures
- **ERROR**: Database errors, critical issues

### Sample Logs

```
INFO  [cleanup-worker] Starting cleanup cycle
INFO  [cleanup-worker] Found 5 expired uploads
INFO  [cleanup-worker] Deleted 5 upload directories (15.3 MB)
INFO  [cleanup-worker] Found 12 expired jobs
INFO  [cleanup-worker] Deleted 12 job files (45.7 MB)
INFO  [cleanup-worker] Cleanup cycle completed (61.0 MB freed)
```

### Viewing Logs

```bash
# Real-time logs
docker compose logs -f cleanup-worker

# Last 100 lines
docker compose logs --tail=100 cleanup-worker

# Search for errors
docker compose logs cleanup-worker | grep ERROR
```

## Monitoring

### Key Metrics to Track

1. **Cleanup Cycle Duration**: Should complete within seconds
2. **Files Deleted per Cycle**: Indicates cleanup load
3. **Disk Space Freed**: Total MB/GB freed
4. **Error Rate**: File deletion failures
5. **Database Query Performance**: Cleanup queries should be fast

### Health Indicators

**Healthy**:
- Regular cleanup cycles every `CLEANUP_INTERVAL`
- Low error rate (< 1%)
- Stable disk usage

**Unhealthy**:
- Cleanup cycles taking > 30 seconds
- High error rate (> 5%)
- Growing disk usage despite cleanup

### Monitoring Commands

```bash
# Check if worker is running
docker compose ps cleanup-worker

# Monitor disk usage
docker compose exec cleanup-worker df -h /app/uploads /app/outputs

# Count files in directories
docker compose exec cleanup-worker find /app/uploads -type f | wc -l
docker compose exec cleanup-worker find /app/outputs -type f | wc -l

# Check database for expired records
docker compose exec db psql -U user -d esydocs -c \
  "SELECT COUNT(*) FROM uploads WHERE expires_at < NOW();"

docker compose exec db psql -U user -d esydocs -c \
  "SELECT COUNT(*) FROM processing_jobs WHERE expires_at < NOW();"
```

## Troubleshooting

### Cleanup Not Running

**Symptoms**: Files accumulating, disk usage growing

**Solutions**:
```bash
# Check if worker is running
docker compose ps cleanup-worker

# Check worker logs for errors
docker compose logs cleanup-worker | tail -50

# Restart worker
docker compose restart cleanup-worker

# Verify environment variables
docker compose exec cleanup-worker env | grep -E "(DATABASE_URL|CLEANUP_INTERVAL)"
```

### File Deletion Failures

**Symptoms**: Warnings in logs about file deletion failures

**Possible Causes**:
1. Permission issues
2. Files locked by other processes
3. Disk I/O errors

**Solutions**:
```bash
# Check file permissions
docker compose exec cleanup-worker ls -la /app/uploads/

# Check volume mounts
docker compose exec cleanup-worker df -h

# Manual cleanup (if needed)
docker compose exec cleanup-worker rm -rf /app/uploads/{expired-id}
```

### Database Connection Issues

**Symptoms**: Errors connecting to database

**Solutions**:
```bash
# Test database connection
docker compose exec cleanup-worker pg_isready -h db -U user -d esydocs

# Check database logs
docker compose logs db | tail -50

# Restart database and worker
docker compose restart db cleanup-worker
```

### High Disk Usage Despite Cleanup

**Symptoms**: Disk usage growing even with cleanup running

**Possible Causes**:
1. `UPLOAD_TTL` or `GUEST_JOB_TTL` too long
2. Orphaned files (no database records)
3. User jobs not expiring (by design)

**Solutions**:
```bash
# Check for orphaned files
docker compose exec cleanup-worker find /app/uploads -type f -mtime +1

# Reduce TTL values
# In docker-compose.yml:
environment:
  UPLOAD_TTL: "1h"      # Shorter expiration
  GUEST_JOB_TTL: "1h"

# Manual cleanup of old files
docker compose exec cleanup-worker \
  find /app/uploads -type f -mtime +7 -delete

# Check for large files
docker compose exec cleanup-worker \
  find /app/outputs -type f -size +100M -ls
```

### Worker Crashes or Restarts

**Symptoms**: Worker keeps restarting

**Solutions**:
```bash
# Check crash logs
docker compose logs cleanup-worker --since 1h

# Check memory usage
docker stats cleanup-worker

# Increase memory limit if needed
# In docker-compose.yml:
cleanup-worker:
  deploy:
    resources:
      limits:
        memory: 512M
```

## Performance Optimization

### Reducing Cleanup Time

1. **Index Database Columns**:
   ```sql
   CREATE INDEX IF NOT EXISTS idx_uploads_expires_at
   ON uploads(expires_at);

   CREATE INDEX IF NOT EXISTS idx_jobs_expires_at
   ON processing_jobs(expires_at);
   ```

2. **Batch Deletions**: Delete files in batches instead of one-by-one

3. **Parallel Processing**: Delete files from multiple directories concurrently

### Reducing Disk I/O

1. **Longer Intervals**: Increase `CLEANUP_INTERVAL` to reduce frequency
2. **Off-Peak Cleanup**: Schedule cleanup during low-traffic periods
3. **Incremental Deletion**: Delete a limited number of files per cycle

## Sequence Diagrams

### Cleanup Cycle Flow

```mermaid
sequenceDiagram
    participant T as Ticker (every CLEANUP_INTERVAL)
    participant CW as Cleanup Worker
    participant DB as PostgreSQL
    participant R as Redis
    participant FS as File System

    T->>CW: Tick (trigger cleanup cycle)

    Note over CW: Phase 1: Expired Job Cleanup
    CW->>DB: SELECT * FROM processing_jobs WHERE expires_at IS NOT NULL AND expires_at <= NOW() LIMIT 100

    loop For each expired job (batched)
        CW->>DB: SELECT * FROM file_metadata WHERE job_id = <id>
        loop For each file
            CW->>FS: os.Remove(file.Path)
        end
        CW->>DB: DELETE FROM file_metadata WHERE job_id = <id>
        CW->>DB: DELETE FROM processing_jobs WHERE id = <id>
    end

    Note over CW: Phase 2: Expired Upload State Cleanup
    CW->>R: SCAN 0 MATCH upload:* COUNT 100

    loop For each upload key
        CW->>R: HGET <key> createdAt
        CW->>CW: Check if age > UPLOAD_TTL
        alt Upload expired
            CW->>R: DEL <key> <key>:chunks
            CW->>FS: os.RemoveAll(uploads/tmp/<uploadId>/)
        end
    end

    Note over CW: Phase 3: Orphaned Directory/File Cleanup
    CW->>FS: ReadDir(uploads/)
    loop For each UUID directory
        CW->>DB: SELECT count(*) FROM processing_jobs WHERE id = <dirName>
        alt No matching job
            CW->>FS: os.RemoveAll(uploads/<dirName>/)
        end
    end
    CW->>FS: ReadDir(outputs/)
    loop For each output file
        CW->>CW: Extract jobID from filename regex
        CW->>DB: SELECT count(*) FROM processing_jobs WHERE id = <jobID>
        alt No matching job
            CW->>FS: os.Remove(outputs/<filename>)
        end
    end

    Note over CW: Phase 4: Backfill Expiry for Legacy Jobs
    CW->>DB: UPDATE processing_jobs SET expires_at = created_at + FREE_JOB_TTL WHERE user_id IS NOT NULL AND expires_at IS NULL

    Note over CW: Cycle complete, wait for next tick
```

### Expired Job Cleanup Detail

```mermaid
sequenceDiagram
    participant CW as Cleanup Worker
    participant DB as PostgreSQL
    participant FS as File System

    CW->>DB: SELECT * FROM processing_jobs WHERE expires_at IS NOT NULL AND expires_at <= NOW() LIMIT 100
    DB-->>CW: [job1, job2, ..., jobN]

    loop For each job
        CW->>DB: SELECT * FROM file_metadata WHERE job_id = job.ID
        DB-->>CW: [file1, file2]

        CW->>FS: os.Remove(file1.Path)
        alt File exists
            FS-->>CW: OK
        else File already deleted
            CW->>CW: Log warning, continue
        end

        CW->>FS: os.Remove(file2.Path)
        CW->>FS: os.Remove(uploads/{job.ID}/) [remove empty dir]
        CW->>FS: os.Remove(outputs/{job.ID}/) [remove empty dir]
        CW->>DB: DELETE FROM file_metadata WHERE job_id = job.ID
        CW->>DB: DELETE FROM processing_jobs WHERE id = job.ID
    end

    Note over CW: If 100 jobs found, loop again (pagination)
    alt len(jobs) == 100
        CW->>DB: SELECT next batch...
    else len(jobs) < 100
        CW->>CW: Done with job cleanup
    end
```

### Upload State Cleanup Detail

```mermaid
sequenceDiagram
    participant CW as Cleanup Worker
    participant R as Redis
    participant FS as File System

    CW->>R: SCAN 0 MATCH upload:* COUNT 100
    R-->>CW: [upload:abc123, upload:abc123:chunks, upload:def456]

    loop For each key (skip :chunks keys)
        CW->>R: HGET upload:abc123 createdAt
        R-->>CW: "2024-01-15T08:00:00Z"

        CW->>CW: time.Since(createdAt) > UPLOAD_TTL?

        alt Upload expired
            CW->>R: DEL upload:abc123 upload:abc123:chunks
            CW->>CW: Parse uploadId from key
            CW->>FS: os.RemoveAll(uploads/tmp/abc123/)
        else Upload still valid
            CW->>CW: Skip
        end
    end
```

### Startup and Lifecycle

```mermaid
sequenceDiagram
    participant OS as Operating System
    participant CW as Cleanup Worker
    participant DB as PostgreSQL
    participant R as Redis

    OS->>CW: Start process
    CW->>CW: LoadConfig()
    CW->>CW: logger.Init("cleanup-worker")
    CW->>CW: telemetry.Init("cleanup-worker")
    CW->>DB: Connect (PostgreSQL)
    CW->>DB: Migrate (ensure tables exist)
    CW->>R: Connect (Redis)

    CW->>CW: Create ticker (CLEANUP_INTERVAL)

    loop Forever
        CW->>CW: runCleanup()
        CW->>CW: Wait for next tick
    end
```

## Error Flows

### Cleanup Error Handling

| Error Type | Impact | Handling |
|------------|--------|----------|
| Database query failure | Jobs not cleaned up | Log error, skip to next phase |
| File deletion failure (permission) | Orphaned files on disk | Log warning, continue with other files |
| File not found on disk | No impact (already cleaned) | Log warning, delete DB record anyway |
| Redis SCAN failure | Upload state not cleaned | Log error, skip upload cleanup |
| Redis DEL failure | Stale upload keys remain | Log warning, will retry next cycle |
| Database DELETE failure | Stale DB records remain | Log error, will retry next cycle |

### Failure Recovery

The cleanup worker is designed for resilience:
1. **Idempotent operations**: Deleting a file or record that does not exist is treated as a warning, not an error
2. **Batched processing**: Jobs are processed in batches of 100 to limit memory usage
3. **Independent phases**: Upload cleanup runs even if job cleanup fails
4. **Automatic retry**: Any items missed in one cycle will be caught in the next cycle
5. **No NATS dependency**: The cleanup worker does not use NATS -- it operates directly on the database and filesystem

## Related Documentation

- [Job Service](./JOB_SERVICE.md) - Job orchestration and file management
- [Convert From PDF](./CONVERT_FROM_PDF.md) - PDF conversion worker
- [Convert To PDF](./CONVERT_TO_PDF.md) - Document conversion worker
- [Main README](../../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f cleanup-worker`
- Monitor disk: `docker compose exec cleanup-worker df -h /app`
- Inspect database: Query uploads and processing_jobs tables
- Manual cleanup: Use `find` and `rm` commands in container
