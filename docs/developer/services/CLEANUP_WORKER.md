# Cleanup Worker Service

## Overview

The Cleanup Worker is a background process that maintains system hygiene by cleaning up expired jobs, expired upload sessions, and their associated objects in MinIO object storage. It runs on a scheduled interval and ensures that expired data does not accumulate over time.

It is **owned by job-service**: the binary lives at `job-service/cmd/cleanup/main.go` with the sweep logic in `job-service/internal/cleanup/cleanup.go`, and it uses job-service's real GORM models (`job-service/internal/models`) directly — there are no duplicated `ProcessingJob`/`FileMetadata` structs, so job-service can evolve its schema without silently breaking cleanup. It still runs as its own container (service name `cleanup-worker`, built from `job-service/Dockerfile.cleanup`) with the same port and behavior as before.

**Port**: 8088
**Type**: Background Worker with HTTP health/metrics server (second binary of job-service)
**Framework**: Go (Gin)

## Responsibilities

1. **Expired Job Cleanup** - Delete jobs past their expiration time and remove their input/output objects from MinIO
2. **Expired Upload Session Cleanup** - Delete stale `upload:*` Redis state (older than **2 × `UPLOAD_TTL`**), abort the associated S3 multipart upload, and remove never-consumed upload objects
3. **Stale Multipart Abort** - Abort incomplete multipart uploads older than `MULTIPART_ABORT_TTL` (default 24h). This is the sole mechanism — there is no bucket lifecycle rule.
4. **Expiry Backfill** - Backfill `expires_at` on legacy authenticated jobs

All TTL fallbacks come from the canonical helpers in `shared/config/defaults.go` (see [Environment Variables](#environment-variables)) — the same values job-service uses, so the two can no longer drift.

The worker no longer touches any filesystem — there is no shared volume. All file bytes live in MinIO (see [Object Storage](../architecture/object-storage.md)).

## Architecture

```
Cleanup Binary (job-service/cmd/cleanup — background loop)
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

**Criteria**: Jobs where `expires_at IS NOT NULL` and `expires_at <= NOW()` — applies to any user type. Deleting a job removes **both its input and output** objects. `expires_at` is always finite (set at job-creation time by `job-service`) and governs the retention of **both input and output files**. The retention windows are env-driven, with the canonical fallbacks defined once in `shared/config/defaults.go` and read by both job-service and this binary:

- **Guest Jobs** (no user): `GUEST_JOB_TTL` (`config.GuestJobTTL`) — default **30 min**
- **Free Plan Users**: `FREE_JOB_TTL` (`config.FreeJobTTL`) — default **7 days (168h)**
- **Pro Plan Users**: `PRO_JOB_TTL` (`config.ProJobTTL`) — default **30 days (720h)** (finite, no longer NULL)

> **Drift bug fixed**: the old standalone cleanup-worker carried its own `FREE_JOB_TTL` fallback of **24h** while job-service used **7d** — with the variable unset, cleanup would have deleted free users' jobs 6 days early. Both now call the same `shared/config` helper, so this class of divergence is gone.

> The plan's `RetentionDays` field is descriptive only; these `*_JOB_TTL` env vars are authoritative. There is **no lifecycle rule at all** on the buckets — input/output retention is fully DB-driven here, and incomplete multipart uploads are aborted by Phase 3 (`MULTIPART_ABORT_TTL`).

**Process**:
1. Query jobs table for expired jobs
   ```sql
   SELECT * FROM processing_jobs
   WHERE expires_at IS NOT NULL AND expires_at <= NOW()
   LIMIT 100
   ```
2. Batch-fetch all `file_metadata` rows for the batch (single query)
3. For each row, delete the object: `RemoveObject(bucketFor(kind), path)`
   - `kind = "input"` → uploads bucket (`uploads`)
   - `kind = "output"` → outputs bucket (`outputs`)
   - Rows whose `path` starts with `/` are **legacy filesystem paths** from before the migration — they are skipped (logged once per batch) and handled by the one-off [`scripts/migrate-files-to-minio.sh`](../../../scripts/migrate-files-to-minio.sh)
   - A missing object is treated as success (idempotent across retries)
4. Batch-delete `file_metadata` records from database
5. Batch-delete `processing_jobs` records from database

**Objects Cleaned**:
- Input objects: `uploads/uploads/{uploadId}/{fileName}`
- Output objects: `outputs/jobs/{jobId}/{outputName}`

---

### 2. Expired Upload Session Cleanup

**Criteria**: Redis `upload:*` hashes older than **2 × `UPLOAD_TTL`** (`config.UploadTTL`, default 30m → reap at 60m). The reap age is derived from the same `UPLOAD_TTL` job-service uses for session expiry — twice the session lifetime, replacing the old independent 2h fallback — so an in-flight session is never reaped while its presigned URLs could still be valid.

**Process**:
1. `SCAN` Redis for `upload:*` keys (skipping `:chunks` suffixes)
2. `HGETALL` each hash; parse `createdAt`
3. If older than 2 × `UPLOAD_TTL`:
   - `DEL` the Redis state keys
   - If the hash carries an `s3UploadId`: `AbortMultipart(uploads, key, s3UploadId)` — frees orphaned parts immediately
   - If the object key is **not referenced** by any `file_metadata` row (`WHERE path = <key>`), `RemoveObject(uploads, key)`. A consumed upload's key *is* referenced by a job, so consumed objects are left for Phase 1 to clean with the job. On a DB error the object is kept (never delete what cannot be proven unreferenced).

---

### 3. Stale Multipart Abort

**Criteria**: Incomplete multipart uploads in the uploads bucket initiated more than **24 hours** ago

**Process**:
1. `ListIncompleteUploads(uploads, MULTIPART_ABORT_TTL)` (default 24h)
2. `AbortMultipart` each result

**When this triggers**: Catches multipart uploads whose Redis session vanished without an abort (crash, manual Redis flush). Since there is no bucket lifecycle rule, this phase is the sole mechanism that reclaims their space; the age threshold is `MULTIPART_ABORT_TTL` (default 24h).

---

### 4. Expiry Backfill for Legacy Jobs

**Criteria**: Jobs where `user_id IS NOT NULL` and `expires_at IS NULL` (created before plan-based expiration was added)

**Process**:
1. Query for authenticated-user jobs missing `expires_at`
2. Set `expires_at = created_at + FREE_JOB_TTL` (default 7d)
3. These jobs will then be cleaned up by the normal expired job cleanup in the next cycle

**When this triggers**: One-time for any legacy jobs. Since new jobs (including pro) now always get a finite `expires_at`, a `NULL` only ever means a legacy row — so this no longer risks clobbering pro's expiry. Once old jobs are backfilled, it's a no-op.

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
| `S3_BUCKET_UPLOADS` | `uploads` | Bucket holding raw uploads |
| `S3_BUCKET_OUTPUTS` | `outputs` | Bucket holding processed outputs |
| `UPLOAD_TTL` | `30m` | Upload-session lifetime (same variable job-service uses); Phase 2 reaps `upload:<id>` state at **2 × `UPLOAD_TTL`** (state + multipart + object) |
| `MULTIPART_ABORT_TTL` | `24h` | How old an **incomplete** multipart upload must be before Phase 3 aborts it and frees its parts (Go duration) |
| `FREE_JOB_TTL` | `168h` (7d) | TTL applied by Phase 4 backfill to legacy authenticated jobs that were created before plan-based expiration was wired up |
| `CLEANUP_INTERVAL` | `15m` | How often the ticker fires |

All duration fallbacks are the canonical helpers in `shared/config/defaults.go`: `GuestJobTTL` (30m), `FreeJobTTL` (7d), `ProJobTTL` (30d), `UploadTTL` (30m), `CleanupInterval` (15m), `StaleMultipartAge` (24h), `MaxUploadBytes` (50MB). job-service reads the same helpers, so the fallbacks cannot diverge again.

### Redis Keys

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `cleanup-worker:lock` | String (SETNX) | 10 minutes | Distributed lock ensuring only one replica runs cleanup per cycle |

## Cleanup Schedule

### Default Schedule

```
Every 15 minutes (under Redis SETNX lock):
  ├─ Phase 1: Delete expired jobs (any user, expires_at <= NOW), RemoveObject per file_metadata row
  ├─ Phase 2: Reap Redis upload:* sessions older than 2 × UPLOAD_TTL (DEL state, AbortMultipart, RemoveObject if unconsumed)
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

The worker is the **single** mechanism that keeps object storage bounded — there
are no MinIO bucket lifecycle rules. All deletion is DB/Redis-driven here:
job input+output objects at job expiry (Phase 1), orphaned/expired upload
sessions (Phase 2), and incomplete multipart uploads (Phase 3,
`MULTIPART_ABORT_TTL`).

**File Retention** — the same TTL governs a job's **input and output** files (set at job creation by `job-service`; env vars are the source of truth, fallbacks from `shared/config/defaults.go`):
- Active uploads (not yet a job): until consumed, or until the Redis session expires (`UPLOAD_TTL`) and Phase 2 reaps the object at 2 × `UPLOAD_TTL` (no bucket object-expiry lifecycle anymore)
- Completed jobs (guest): `GUEST_JOB_TTL` (default **30m**)
- Completed jobs (free user): `FREE_JOB_TTL` (default **7d**); legacy `NULL` rows backfilled by Phase 4
- Completed jobs (pro user): `PRO_JOB_TTL` (default **30d**) — finite, no longer `NULL`
- Failed jobs: same TTL as completed (still rows in `processing_jobs`)

## Deployment

### Docker Compose

The service name stays `cleanup-worker`; the image is built from job-service's cleanup Dockerfile:

```yaml
cleanup-worker:
  build:
    context: ..
    dockerfile: job-service/Dockerfile.cleanup
  environment:
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    S3_ENDPOINT: minio:9000
    S3_ACCESS_KEY: ${S3_ACCESS_KEY}
    S3_SECRET_KEY: ${S3_SECRET_KEY}
    S3_USE_SSL: "false"
    S3_BUCKET_UPLOADS: uploads
    S3_BUCKET_OUTPUTS: outputs
    UPLOAD_TTL: 30m
    CLEANUP_INTERVAL: 5m
    FREE_JOB_TTL: 168h
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

2. Run the cleanup binary (from the job-service module):
   ```bash
   cd job-service
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs"
   export REDIS_ADDR="localhost:6379"
   export S3_ENDPOINT="localhost:9000"   # publish 9000 locally or port-forward
   export S3_ACCESS_KEY=... S3_SECRET_KEY=...
   go run ./cmd/cleanup
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
mc du fyredocs/uploads
mc du fyredocs/outputs

# Incomplete multipart uploads
mc ls --incomplete fyredocs/uploads

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
        alt age > 2 × UPLOAD_TTL
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
- Inspect buckets: `mc du fyredocs/uploads`, `mc ls --incomplete fyredocs/uploads`
- Inspect database: Query `processing_jobs` and `file_metadata` tables

## Bucket rename migration (2026-07)

The buckets were renamed `fyredocs-uploads` → `uploads` and `fyredocs-outputs`
→ `outputs` (the bucket name is the public URL path segment, so this is what
made browser URLs `/uploads/...`). `minio-init` creates the new buckets
automatically on the next `deploy.sh` run; objects in the old buckets are NOT
migrated — files of jobs created before the rename 404 until reprocessed.

To keep old outputs, mirror before removing; otherwise just delete:

```sh
docker compose exec -it minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" \
  && mc mirror local/fyredocs-outputs local/outputs \
  && mc rb --force local/fyredocs-uploads local/fyredocs-outputs'
```
