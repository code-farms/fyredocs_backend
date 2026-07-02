# Cleanup Worker -- Architecture

Internal structure and component diagram of the `cleanup-worker` container — **job-service's cleanup binary** (`job-service/cmd/cleanup`, sweep logic in `job-service/internal/cleanup`, built from `job-service/Dockerfile.cleanup`). It uses job-service's own GORM models directly and reads TTL fallbacks from `shared/config/defaults.go`.

## Component Diagram

```mermaid
graph TB
    subgraph cleanup-worker[" cleanup-worker :8088 (job-service/cmd/cleanup) "]
        direction TB

        subgraph Bootstrap["Bootstrap (main)"]
            INIT["Initialize<br/>Config · Logger · Telemetry"]
            CONNECT["Connect<br/>Postgres · Redis · storage.NewFromEnv()<br/>(exit(1) if S3_* missing)"]
            HTTP["Gin HTTP server :8088<br/>/healthz · /readyz · /metrics"]
            TICKER["time.Ticker<br/>CLEANUP_INTERVAL (default 15m)"]
            LOOP["Run loop<br/>runCleanup(ctx, store) each tick"]
        end

        subgraph Cleanup["runCleanup() — under SETNX lock cleanup-worker:lock (10m TTL)"]
            P1["Phase 1: cleanupExpiredJobs()"]
            P2["Phase 2: cleanupUploadState()"]
            P3["Phase 3: abortStaleMultipartUploads()"]
            P4["Phase 4: backfillExpiry()"]
        end

        subgraph P1Detail["Phase 1: Expired jobs (batch=100)"]
            P1A["SELECT processing_jobs<br/>WHERE expires_at IS NOT NULL<br/>AND expires_at <= NOW()"]
            P1B["Batch SELECT file_metadata<br/>WHERE job_id IN (...)"]
            P1C["removeJobObjects():<br/>RemoveObject(bucketFor(kind), path)<br/>kind input→uploads · output→outputs<br/>legacy '/...' paths skipped (log once)"]
            P1D["Batch DELETE file_metadata + processing_jobs"]
        end

        subgraph P2Detail["Phase 2: Upload sessions"]
            P2A["Redis SCAN upload:* (skip :chunks)"]
            P2B["HGETALL upload:&lt;id&gt;<br/>(createdAt · key · s3UploadId)"]
            P2C["age > 2 × UPLOAD_TTL<br/>(config.UploadTTL, default 30m → reap at 60m) ?"]
            P2D["DEL upload:&lt;id&gt; keys<br/>AbortMultipart(uploads, key, s3UploadId)<br/>RemoveObject(uploads, key) if no<br/>file_metadata row references key"]
        end

        subgraph P3Detail["Phase 3: Stale multipart abort"]
            P3A["ListIncompleteUploads(uploads, 24h)"]
            P3B["AbortMultipart each<br/>(lifecycle rule 1d is the backstop)"]
        end

        subgraph P4Detail["Phase 4: Backfill expiry"]
            P4A["UPDATE processing_jobs<br/>SET expires_at = created_at + FREE_JOB_TTL<br/>WHERE user_id IS NOT NULL<br/>AND expires_at IS NULL"]
        end

        subgraph Models["job-service/internal/models (GORM — job-service's own models, no duplicated structs)"]
            JM["ProcessingJob"]
            FM["FileMetadata"]
        end

        subgraph Store["objectStore (narrow interface over fyredocs/shared/storage)"]
            ST["BucketUploads/BucketOutputs<br/>RemoveObject · AbortMultipart<br/>ListIncompleteUploads"]
        end
    end

    INIT --> CONNECT --> HTTP --> TICKER --> LOOP
    LOOP --> P1 --> P2 --> P3 --> P4

    P1 --> P1A --> P1B --> P1C --> P1D
    P2 --> P2A --> P2B --> P2C --> P2D
    P3 --> P3A --> P3B
    P4 --> P4A

    P1A & P1B & P1D & P2D & P4A --> PG[(PostgreSQL)]
    P2A & P2B --> RD[(Redis)]
    P1C & P2D & P3A & P3B --> ST
    ST --> S3[(MinIO<br/>fyredocs-uploads · fyredocs-outputs)]
```

## Cleanup Targets

```mermaid
graph LR
    subgraph Targets["What gets cleaned per cycle"]
        A["Any expired job<br/>(guest, free, pro-with-explicit-expires_at)<br/>processing_jobs.expires_at <= NOW()<br/>→ RemoveObject per file_metadata row"]
        B["Stale upload sessions<br/>upload:* keys older than UPLOAD_TTL<br/>→ DEL + AbortMultipart + RemoveObject (if unconsumed)"]
        C["Stale multipart uploads<br/>incomplete > 24h in uploads bucket<br/>→ AbortMultipart"]
        D["Legacy authenticated jobs<br/>user_id IS NOT NULL AND expires_at IS NULL<br/>→ backfilled to created_at + FREE_JOB_TTL"]
    end

    subgraph NotCleaned["Out of scope"]
        E["Pro user jobs (expires_at = NULL)"]
        F["Legacy '/...' filesystem paths in file_metadata<br/>(migrated by scripts/migrate-files-to-minio.sh)"]
        G["Uploads-bucket lifecycle expiry (2d) — MinIO-side backstop"]
        H["NATS message redelivery (handled by JetStream AckWait)"]
        I["Refresh-token sessions (handled by auth-service hourly purge)"]
    end
```

## Configuration

```mermaid
graph TD
    subgraph EnvVars["Environment Variables (fallbacks from shared/config/defaults.go — same helpers job-service uses)"]
        A["CLEANUP_INTERVAL<br/>Default: 15m (config.CleanupInterval)"]
        B["UPLOAD_TTL<br/>Default: 30m (config.UploadTTL)<br/>Phase 2 reaps at 2 × UPLOAD_TTL"]
        C["FREE_JOB_TTL<br/>Default: 7d/168h (config.FreeJobTTL)<br/>(used by Phase 4 backfill only —<br/>old 24h-vs-7d drift bug fixed)"]
        D["S3_ENDPOINT · S3_ACCESS_KEY · S3_SECRET_KEY<br/>Required (fail-fast)"]
        E["S3_BUCKET_UPLOADS / S3_BUCKET_OUTPUTS<br/>Default: fyredocs-uploads / fyredocs-outputs"]
        F["PORT<br/>Default: 8088"]
    end

    subgraph Locking["Distributed lock"]
        L["Redis SETNX cleanup-worker:lock<br/>10-minute TTL<br/>One replica per tick · others skip"]
    end
```

## HTTP Surface

```mermaid
graph LR
    Probe[Liveness/Readiness probe] -->|GET /healthz| HZ[Ping Redis]
    Probe -->|GET /readyz| RZ[Ping Redis + Postgres]
    Prom[Prometheus] -->|GET /metrics| MX[Prometheus collector]
```
