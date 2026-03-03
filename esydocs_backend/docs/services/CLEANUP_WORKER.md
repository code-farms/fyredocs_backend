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

## Cleanup Operations

### 1. Expired Upload Cleanup

**Criteria**: Uploads older than `UPLOAD_TTL` (default: 2 hours)

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

**Criteria**: Jobs older than expiration time

- **Guest Jobs**: `GUEST_JOB_TTL` (default: 2 hours) after completion
- **User Jobs**: Based on `expires_at` field in database

**Process**:
1. Query jobs table for expired jobs
   ```sql
   SELECT id, file_path, output_path FROM processing_jobs
   WHERE expires_at < NOW()
   ```
2. Delete input file (if exists)
3. Delete output file (if exists)
4. Delete job record from database

**Files Cleaned**:
- Input files: `/app/uploads/{job_id}/{filename}`
- Output files: `/app/outputs/{job_id}/{filename}`

---

### 3. Failed Job Cleanup

**Criteria**: Jobs in "failed" state for > 24 hours

**Process**:
1. Query for old failed jobs
2. Delete associated files
3. Archive or delete job record

---

### 4. Orphaned File Cleanup (Future Enhancement)

**Criteria**: Files in uploads/outputs directories without corresponding database records

**Process**:
1. List all files in uploads/outputs directories
2. Check if each file has a database record
3. Delete files without records

**Status**: Not yet implemented

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
| `UPLOAD_TTL` | `2h` | Upload expiration time |
| `GUEST_JOB_TTL` | `2h` | Guest job expiration time (after completion) |
| `CLEANUP_INTERVAL` | `5m` | How often to run cleanup |
| `MAX_RETRIES` | `3` | Max retries for failed jobs before cleanup |
| `QUEUE_PREFIX` | `queue` | Redis queue key prefix |

## Cleanup Schedule

### Default Schedule

```
Every 5 minutes:
  ├─ Check for expired uploads
  ├─ Check for expired jobs
  ├─ Delete associated files
  └─ Update database
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
- Completed jobs (user): Configurable (default: no expiration)
- Completed jobs (guest): 2 hours after completion
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
    UPLOAD_TTL: 2h
    CLEANUP_INTERVAL: 5m
    GUEST_JOB_TTL: 2h
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

1. **Single Instance**: Run only one cleanup worker to avoid race conditions
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

## Related Documentation

- [Upload Service](../upload-service/UPLOAD_SERVICE.md) - Upload and job management
- [Convert From PDF](../convert-from-pdf/CONVERT_FROM_PDF.md) - PDF conversion worker
- [Convert To PDF](../convert-to-pdf/CONVERT_TO_PDF.md) - Document conversion worker
- [Main README](../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f cleanup-worker`
- Monitor disk: `docker compose exec cleanup-worker df -h /app`
- Inspect database: Query uploads and processing_jobs tables
- Manual cleanup: Use `find` and `rm` commands in container
