# Cleanup Worker Service

## Overview

The Cleanup Worker is a background service that maintains system hygiene by cleaning up expired jobs, expired upload sessions, and their associated objects in MinIO object storage. It runs on a scheduled interval and ensures that expired data does not accumulate over time.

**Port**: 8088
**Type**: Background Worker with HTTP health/metrics server
**Framework**: Go (Gin)

## Responsibilities

1. **Expired Job Cleanup** - Delete jobs past their expiration time and remove their input/output objects from MinIO
2. **Expired Upload Session Cleanup** - Delete stale `upload:*` Redis state, abort the associated S3 multipart upload, and remove never-consumed upload objects
3. **Stale Multipart Abort** - Abort incomplete multipart uploads older than 24h (belt-and-braces ahead of the bucket lifecycle rule)
4. **Expiry Backfill** - Backfill `expires_at` on legacy authenticated jobs

The worker no longer touches any filesystem — there is no shared volume. All file bytes live in MinIO (see [Object Storage](../architecture/object-storage.md)).

## Architecture

```
Cleanup Worker (Background Loop)
  ↓
┌─────────────────────────────────────┐
│  Every CLEANUP_INTERVAL (15 min)    │
└────────────┬────────────────────────┘
             ↓
   Acquire Redis SETNX lock
   `cleanup-worker:lock` (10-min TTL)
             ↓
   ┌─────────┴─────────────┐
   ▼                       ▼
   ├ Phase 1: cleanupExpiredJobs           (DB-driven, batch=100, RemoveObject per file)
   ├ Phase 2: cleanupUploadState           (Redis SCAN upload:* → AbortMultipart + RemoveObject)
   ├ Phase 3: abortStaleMultipartUploads   (ListIncompleteUploads > 24h → AbortMultipart)
   └ Phase 4: backfillExpiry               (UPDATE legacy authenticated jobs)
             ↓
   Release lock (DEL)
```

The worker exposes `/healthz`, `/readyz`, and `/metrics` on port `8088` but does **not** consume from NATS — it operates directly on PostgreSQL, Redis, and MinIO via `fyredocs/shared/storage`. The storage client is initialized fail-fast at startup (`storage.NewFromEnv()`); the process exits if `S3_*` configuration is missing.

When scaled to multiple replicas, the Redis SETNX lock ensures only one instance runs cleanup per tick. Other replicas skip the cycle if the lock is already held.

## Cleanup Operations

### 1. Expired Job Cleanup

**Criteria**: Jobs where `expires_at IS NOT NULL` and `expires_at <= NOW()` — applies to any user type. The `expires_at` value is set at job-creation time by `job-service` based on the user's plan:

- **Guest Jobs** (no user): TTL set by `job-service` (`GUEST_JOB_TTL`, configured there — not in cleanup-worker)
- **Free Plan Users**: TTL set by `job-service` from plan info, also enforced by Phase 4 backfill below for legacy rows (`FREE_JOB_TTL`, default `24h`)
- **Pro Plan Users**: Never expire (`expires_at = NULL`)

**Process**:
1. Query jobs table for expired jobs
   ```sql
   SELECT * FROM processing_jobs
   WHERE expires_at IS NOT NULL AND expires_at <= NOW()
   LIMIT 100
   ```
2. Batch-fetch all `file_metadata` rows for the batch (single query)
3. For each row, delete the object: `RemoveObject(bucketFor(kind), path)`
   - `kind = "input"` → uploads bucket (`fyredocs-uploads`)
   - `kind = "output"` → outputs bucket (`fyredocs-outputs`)
   - Rows whose `path` starts with `/` are **legacy filesystem paths** from before the migration — they are skipped (logged once per batch) and handled by the one-off [`scripts/migrate-files-to-minio.sh`](../../../scripts/migrate-files-to-minio.sh)
   - A missing object is treated as success (idempotent across retries)
4. Batch-delete `file_metadata` records from database
5. Batch-delete `processing_jobs` records from database

**Objects Cleaned**:
- Input objects: `fyredocs-uploads/uploads/{uploadId}/{fileName}`
- Output objects: `fyredocs-outputs/jobs/{jobId}/{outputName}`

---

### 2. Expired Upload Session Cleanup

**Criteria**: Redis `upload:*` hashes older than `UPLOAD_TTL` (default: 2 hours, deployment sets 30m)

**Process**:
1. `SCAN` Redis for `upload:*` keys (skipping `:chunks` suffixes)
2. `HGETALL` each hash; parse `createdAt`
3. If older than `UPLOAD_TTL`:
   - `DEL` the Redis state keys
   - If the hash carries an `s3UploadId`: `AbortMultipart(uploads, key, s3UploadId)` — frees orphaned parts immediately
   - If the object key is **not referenced** by any `file_metadata` row (`WHERE path = <key>`), `RemoveObject(uploads, key)`. A consumed upload's key *is* referenced by a job, so consumed objects are left for Phase 1 to clean with the job. On a DB error the object is kept (never delete what cannot be proven unreferenced).

---

### 3. Stale Multipart Abort

**Criteria**: Incomplete multipart uploads in the uploads bucket initiated more than **24 hours** ago

**Process**:
1. `ListIncompleteUploads(fyredocs-uploads, 24h)`
2. `AbortMultipart` each result

**When this triggers**: Catches multipart uploads whose Redis session vanished without an abort (crash, manual Redis flush). The bucket lifecycle rule (`AbortIncompleteMultipartUpload` after 1 day) is the final backstop; this phase reclaims space sooner and emits logs/metrics.

---

### 4. Expiry Backfill for Legacy Jobs

**Criteria**: Jobs where `user_id IS NOT NULL` and `expires_at IS NULL` (created before plan-based expiration was added)

**Process**:
1. Query for authenticated-user jobs missing `expires_at`
2. Set `expires_at = created_at + FREE_JOB_TTL` (default 24h)
3. These jobs will then be cleaned up by the normal expired job cleanup in the next cycle

**When this triggers**: One-time for any legacy jobs. Once all old jobs have been backfilled, this is a no-op.

---

## HTTP Endpoints

The cleanup worker exposes a lightweight HTTP server for health checks, readiness probes, and Prometheus metrics scraping.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check (checks Redis connectivity) |
| GET | `/readyz` | Readiness check (checks Redis + PostgreSQL) |
| GET | `/metrics` | Prometheus metrics endpoint |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8088` | HTTP server port for health checks and metrics |
| `DATABASE_URL` | **Required** | PostgreSQL connection string |
| `REDIS_ADDR` | **Required** | Redis server address |
| `REDIS_PASSWORD` | `""` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `S3_ENDPOINT` | **Required** | MinIO/S3 endpoint, e.g. `minio:9000` |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | **Required** | Scoped app credentials (created by `minio-init`) |
| `S3_USE_SSL` | `false` | TLS to the S3 endpoint |
| `S3_BUCKET_UPLOADS` | `fyredocs-uploads` | Bucket holding raw uploads |
| `S3_BUCKET_OUTPUTS` | `fyredocs-outputs` | Bucket holding processed outputs |
| `UPLOAD_TTL` | `2h` | How long an upload session in Redis (`upload:<id>`) can live before Phase 2 reaps it (state + multipart + object) |
| `FREE_JOB_TTL` | `24h` | TTL applied by Phase 4 backfill to legacy authenticated jobs that were created before plan-based expiration was wired up |
| `CLEANUP_INTERVAL` | `15m` | How often the ticker fires |

### Redis Keys

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `cleanup-worker:lock` | String (SETNX) | 10 minutes | Distributed lock ensuring only one replica runs cleanup per cycle |

## Cleanup Schedule

### Default Schedule

```
Every 15 minutes (under Redis SETNX lock):
  ├─ Phase 1: Delete expired jobs (any user, expires_at <= NOW), RemoveObject per file_metadata row
  ├─ Phase 2: Reap Redis upload:* sessions older than UPLOAD_TTL (DEL state, AbortMultipart, RemoveObject if unconsumed)
  ├─ Phase 3: Abort incomplete multipart uploads older than 24h
  └─ Phase 4: Backfill expires_at on legacy authenticated-user jobs (user_id IS NOT NULL AND expires_at IS NULL)
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

The worker is one of three layers that keep object storage bounded:

1. **Cleanup worker** (this service) — DB/Redis-driven deletion, the primary mechanism
2. **Bucket lifecycle rules** (applied by `minio-init`, uploads bucket only) — expire objects after 2 days, abort incomplete multipart uploads after 1 day
3. **No lifecycle on the outputs bucket** — pro-plan outputs never expire; output deletion is exclusively DB-driven by Phase 1

**File Retention** (TTLs are set at job creation time by `job-service`, except where noted):
- Active uploads: until consumed by a job, until `UPLOAD_TTL` expires the Redis session, or at most 2 days (bucket lifecycle)
- Completed jobs (pro user): no expiration (`expires_at = NULL`)
- Completed jobs (free user): TTL set from plan info; legacy rows backfilled to `FREE_JOB_TTL` (default `24h`) by Phase 4
- Completed jobs (guest): TTL set by job-service (guest TTL is not configured in cleanup-worker)
- Failed jobs: same TTL as completed (still rows in `processing_jobs`)

## Deployment

### Docker Compose

```yaml
cleanup-worker:
  build:
    context: ..
    dockerfile: cleanup-worker/Dockerfile
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    S3_ENDPOINT: minio:9000
    S3_ACCESS_KEY: ${S3_ACCESS_KEY}
    S3_SECRET_KEY: ${S3_SECRET_KEY}
    S3_USE_SSL: "false"
    S3_BUCKET_UPLOADS: fyredocs-uploads
    S3_BUCKET_OUTPUTS: fyredocs-outputs
    UPLOAD_TTL: 30m
    CLEANUP_INTERVAL: 5m
    FREE_JOB_TTL: 24h
  depends_on:
    redis:
      condition: service_healthy
    minio-init:
      condition: service_completed_successfully
```

No volumes — the worker is stateless.

### Local Development

1. Start dependencies:
   ```bash
   docker compose -f deployment/docker-compose.yml up -d redis minio minio-init
   ```

2. Run worker:
   ```bash
   cd cleanup-worker
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
   export REDIS_ADDR="localhost:6379"
   export S3_ENDPOINT="localhost:9000"   # publish 9000 locally or port-forward
   export S3_ACCESS_KEY=... S3_SECRET_KEY=...
   go run main.go
   ```

### Production Deployment

**Best Practices**:

1. **Multiple replicas supported**: A Redis distributed lock ensures only one instance runs cleanup at a time. Additional replicas provide high availability.
2. **Resource Limits**: Minimal CPU/memory requirements (256MB sufficient)
3. **Credentials**: Use the scoped app user (`minio-init` provisions it), never the MinIO root credentials
4. **Logging**: Enable structured logging for audit trail
5. **Monitoring**: Track cleanup metrics (objects deleted, multiparts aborted)

## Logging

### Log Levels

- **INFO**: Cleanup cycles started/completed, stale multiparts aborted
- **WARN**: Object/multipart deletion failures, legacy paths skipped
- **ERROR**: Database errors, ListIncompleteUploads failures, critical issues

### Sample Logs

```
INFO  [cleanup-worker] cleanup-worker started interval=5m
WARN  [cleanup-worker] skipping legacy filesystem path(s); run scripts/migrate-files-to-minio.sh path=/app/uploads/... jobId=...
INFO  [cleanup-worker] aborted stale multipart upload key=uploads/abc/file.pdf initiated=...
INFO  [cleanup-worker] backfilled expires_at for old jobs count=12
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
2. **Objects Deleted per Cycle**: Indicates cleanup load
3. **Stale Multiparts Aborted**: Should trend to zero in steady state
4. **Error Rate**: Object deletion failures
5. **Database Query Performance**: Cleanup queries should be fast

### Health Indicators

**Healthy**:
- Regular cleanup cycles every `CLEANUP_INTERVAL`
- Low error rate (< 1%)
- Stable bucket usage

**Unhealthy**:
- Cleanup cycles taking > 30 seconds
- High error rate (> 5%)
- Growing bucket usage despite cleanup

### Monitoring Commands

```bash
# Check if worker is running
docker compose ps cleanup-worker

# Bucket usage (mc alias configured against the MinIO console/API)
mc du fyredocs/fyredocs-uploads
mc du fyredocs/fyredocs-outputs

# Incomplete multipart uploads
mc ls --incomplete fyredocs/fyredocs-uploads

# Check database for expired records
psql "$DATABASE_URL" -c \
  "SELECT COUNT(*) FROM processing_jobs WHERE expires_at < NOW();"
```

## Troubleshooting

### Cleanup Not Running

**Symptoms**: Bucket usage growing, expired jobs lingering

**Solutions**:
```bash
# Check if worker is running
docker compose ps cleanup-worker

# Check worker logs for errors
docker compose logs cleanup-worker | tail -50

# Restart worker
docker compose restart cleanup-worker

# Verify environment variables
docker compose exec cleanup-worker env | grep -E "(DATABASE_URL|CLEANUP_INTERVAL|S3_)"
```

### Object Deletion Failures

**Symptoms**: Warnings in logs about `failed to remove object`

**Possible Causes**:
1. Wrong/expired `S3_ACCESS_KEY`/`S3_SECRET_KEY` (re-run `minio-init`)
2. App policy missing `s3:DeleteObject` on the bucket
3. MinIO unreachable from the worker network

**Solutions**:
```bash
# Verify credentials work
docker compose logs minio-init

# Verify MinIO health
docker compose exec cleanup-worker wget -qO- http://minio:9000/minio/health/live
```

### Legacy Path Warnings

**Symptoms**: `skipping legacy filesystem path(s)` warnings every cycle

**Cause**: `file_metadata` rows created before the object-storage migration still carry absolute filesystem paths.

**Solution**: Run the one-off migration (dry-run first):
```bash
DATABASE_URL=... MC_ALIAS=fyredocs ./scripts/migrate-files-to-minio.sh
DATABASE_URL=... MC_ALIAS=fyredocs ./scripts/migrate-files-to-minio.sh --execute
```

### High Bucket Usage Despite Cleanup

**Possible Causes**:
1. `UPLOAD_TTL` (or job-service-side `GUEST_JOB_TTL` / plan TTLs) too long
2. Pro user jobs not expiring (by design)
3. Incomplete multipart uploads piling up (check Phase 3 logs and `mc ls --incomplete`)

## Performance Optimization

### Reducing Cleanup Time

1. **Index Database Columns**:
   ```sql
   CREATE INDEX IF NOT EXISTS idx_jobs_expires_at
   ON processing_jobs(expires_at);

   CREATE INDEX IF NOT EXISTS idx_file_metadata_path
   ON file_metadata(path);
   ```

2. **Batch Deletions**: Jobs are processed in batches of 100; DB deletes are batched per cycle

### Reducing Load

1. **Longer Intervals**: Increase `CLEANUP_INTERVAL` to reduce frequency
2. **Off-Peak Cleanup**: Schedule cleanup during low-traffic periods

## Sequence Diagrams

### Cleanup Cycle Flow

```mermaid
sequenceDiagram
    participant T as Ticker (every CLEANUP_INTERVAL)
    participant CW as Cleanup Worker
    participant DB as PostgreSQL
    participant R as Redis
    participant S3 as MinIO

    T->>CW: Tick (trigger cleanup cycle)
    CW->>R: SETNX cleanup-worker:lock (TTL 10m)

    Note over CW: Phase 1: Expired Job Cleanup
    CW->>DB: SELECT * FROM processing_jobs WHERE expires_at IS NOT NULL AND expires_at <= NOW() LIMIT 100
    CW->>DB: SELECT * FROM file_metadata WHERE job_id IN (...)
    loop For each file_metadata row
        alt path starts with "/" (legacy)
            CW->>CW: skip (log once, migrate via script)
        else object key
            CW->>S3: RemoveObject(bucketFor(kind), path)
        end
    end
    CW->>DB: DELETE FROM file_metadata WHERE job_id IN (...)
    CW->>DB: DELETE FROM processing_jobs WHERE id IN (...)

    Note over CW: Phase 2: Expired Upload State Cleanup
    CW->>R: SCAN 0 MATCH upload:* COUNT 100
    loop For each upload key
        CW->>R: HGETALL <key>
        alt age > UPLOAD_TTL
            CW->>R: DEL <key> <key>:chunks
            opt hash has s3UploadId
                CW->>S3: AbortMultipart(uploads, key, s3UploadId)
            end
            CW->>DB: SELECT count(*) FROM file_metadata WHERE path = <key>
            alt not consumed by a job
                CW->>S3: RemoveObject(uploads, key)
            end
        end
    end

    Note over CW: Phase 3: Stale Multipart Abort
    CW->>S3: ListIncompleteUploads(uploads, olderThan=24h)
    loop For each stale upload
        CW->>S3: AbortMultipart(uploads, key, uploadId)
    end

    Note over CW: Phase 4: Backfill Expiry for Legacy Jobs
    CW->>DB: UPDATE processing_jobs SET expires_at = created_at + FREE_JOB_TTL WHERE user_id IS NOT NULL AND expires_at IS NULL

    CW->>R: DEL cleanup-worker:lock
    Note over CW: Cycle complete, wait for next tick
```

### Startup and Lifecycle

```mermaid
sequenceDiagram
    participant OS as Operating System
    participant CW as Cleanup Worker
    participant DB as PostgreSQL
    participant R as Redis
    participant S3 as MinIO

    OS->>CW: Start process
    CW->>CW: LoadConfig()
    CW->>CW: logger.Init("cleanup-worker")
    CW->>CW: telemetry.Init("cleanup-worker")
    CW->>DB: Connect (PostgreSQL)
    CW->>DB: Migrate (ensure tables exist)
    CW->>R: Connect (Redis)
    CW->>S3: storage.NewFromEnv() — exit(1) if S3_* missing

    CW->>CW: Create ticker (CLEANUP_INTERVAL)

    loop Forever
        CW->>CW: runCleanup(ctx, store)
        CW->>CW: Wait for next tick
    end
```

## Error Flows

### Cleanup Error Handling

| Error Type | Impact | Handling |
|------------|--------|----------|
| Database query failure | Jobs not cleaned up | Log error, skip to next phase |
| RemoveObject failure | Orphaned object in bucket | Log warning, continue; lifecycle rule is the backstop for uploads |
| Object already missing | No impact | `RemoveObject` treats missing as success; DB record deleted anyway |
| AbortMultipart on unknown upload | No impact | Treated as success (idempotent) |
| `file_metadata WHERE path = ?` query failure | Upload object kept | Fail safe: never delete what cannot be proven unreferenced |
| Redis SCAN failure | Upload state not cleaned | Log error, retry next cycle |
| ListIncompleteUploads failure | Stale multiparts remain | Log error; bucket lifecycle aborts them after 1 day |
| Database DELETE failure | Stale DB records remain | Log error, will retry next cycle |

### Failure Recovery

The cleanup worker is designed for resilience:
1. **Idempotent operations**: Removing a missing object or aborting an unknown multipart upload is success, not an error
2. **Batched processing**: Jobs are processed in batches of 100 to limit memory usage
3. **Independent phases**: Upload cleanup runs even if job cleanup fails
4. **Automatic retry**: Any items missed in one cycle will be caught in the next cycle
5. **Defense in depth**: Bucket lifecycle rules (uploads: expire 2d, abort incomplete multipart 1d) back up the worker
6. **No NATS dependency**: The cleanup worker does not use NATS — it operates directly on the database, Redis, and MinIO

## Related Documentation

- [Object Storage](../architecture/object-storage.md) - MinIO topology, buckets, lifecycle
- [Job Service](./JOB_SERVICE.md) - Job orchestration and file management
- [Main README](../../README.md) - Overall architecture

## Support

For issues:
- Check logs: `docker compose logs -f cleanup-worker`
- Inspect buckets: `mc du fyredocs/fyredocs-uploads`, `mc ls --incomplete fyredocs/fyredocs-uploads`
- Inspect database: Query `processing_jobs` and `file_metadata` tables
