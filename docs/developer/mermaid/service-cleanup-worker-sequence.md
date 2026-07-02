# Cleanup Worker -- Sequence Diagrams

Execution flows for the `cleanup-worker` container — job-service's cleanup binary (`job-service/cmd/cleanup`, logic in `job-service/internal/cleanup`, models from `job-service/internal/models`, TTL fallbacks from `shared/config/defaults.go`).

## Startup and Main Loop

```mermaid
sequenceDiagram
    participant Main as main() (job-service/cmd/cleanup)
    participant Cfg as shared/config
    participant DB as PostgreSQL
    participant R as Redis
    participant S3 as MinIO (shared/storage)
    participant HTTP as Gin :8088
    participant Cleanup as runCleanup()

    Main->>Cfg: LoadConfig()
    Main->>Main: logger.Init("cleanup-worker")
    Main->>Main: telemetry.Init("cleanup-worker")
    Main->>DB: models.Connect() + Migrate()
    Main->>R: redisstore.Connect()
    Main->>S3: storage.NewFromEnv()
    alt S3_* config missing
        Main->>Main: log error + os.Exit(1) (fail-fast)
    end
    Main->>HTTP: ListenAndServe :8088 (/healthz, /readyz, /metrics)
    Main->>Main: Start ticker (CLEANUP_INTERVAL, default 15m)

    loop Forever
        Main->>Cleanup: runCleanup(ctx, store)
        Cleanup->>R: SETNX cleanup-worker:lock TTL=10m
        alt Lock acquired
            Cleanup->>Cleanup: cleanupExpiredJobs(ctx, store)
            Cleanup->>Cleanup: cleanupUploadState(ctx, store)
            Cleanup->>Cleanup: abortStaleMultipartUploads(ctx, store)
            Cleanup->>Cleanup: backfillExpiry(ctx)
            Cleanup->>R: DEL cleanup-worker:lock
        else Lock held by another replica
            Cleanup->>Cleanup: log("skipping") and return
        end
        Note over Main: Wait for next tick
    end
```

## Phase 1 — cleanupExpiredJobs

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant PG as PostgreSQL
    participant S3 as MinIO

    loop Until batch < 100
        CW->>PG: SELECT * FROM processing_jobs<br/>WHERE expires_at IS NOT NULL<br/>AND expires_at <= NOW() LIMIT 100
        PG-->>CW: jobs[]

        alt no rows
            CW->>CW: return
        end

        CW->>PG: SELECT * FROM file_metadata WHERE job_id IN (jobIds[])
        PG-->>CW: files[]

        Note over CW: removeJobObjects(files)
        loop For each file
            alt path starts with "/" (legacy filesystem path)
                CW->>CW: skip — log once,<br/>migrate via scripts/migrate-files-to-minio.sh
            else kind == "input"
                CW->>S3: RemoveObject(fyredocs-uploads, path)
            else kind == "output"
                CW->>S3: RemoveObject(fyredocs-outputs, path)
            end
            Note over S3: missing object == success (idempotent)
        end

        CW->>PG: DELETE FROM file_metadata WHERE job_id IN (jobIds[])
        CW->>PG: DELETE FROM processing_jobs WHERE id IN (jobIds[])
    end
```

## Phase 2 — cleanupUploadState

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant R as Redis
    participant PG as PostgreSQL
    participant S3 as MinIO

    CW->>R: SCAN 0 MATCH upload:* COUNT 100
    R-->>CW: keys

    loop For each key (skip :chunks)
        CW->>R: HGETALL upload:&lt;id&gt;
        R-->>CW: {createdAt, key, s3UploadId, ...}
        CW->>CW: time.Since(createdAt) > 2 × UPLOAD_TTL?<br/>(config.UploadTTL, default 30m → 60m)

        alt Yes — stale
            CW->>R: DEL upload:&lt;id&gt; upload:&lt;id&gt;:chunks
            opt hash has s3UploadId
                CW->>S3: AbortMultipart(fyredocs-uploads, key, s3UploadId)
            end
            CW->>PG: SELECT count(*) FROM file_metadata WHERE path = &lt;key&gt;
            alt count == 0 (never consumed by a job)
                CW->>S3: RemoveObject(fyredocs-uploads, key)
            else consumed — referenced by a job
                CW->>CW: keep object (Phase 1 cleans it with the job)
            end
        else No — keep
            CW->>CW: skip
        end
    end
```

## Phase 3 — abortStaleMultipartUploads

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant S3 as MinIO

    CW->>S3: ListIncompleteUploads(fyredocs-uploads, olderThan=24h)
    S3-->>CW: [{key, uploadId, initiated}, ...]

    loop For each stale incomplete upload
        CW->>S3: AbortMultipart(fyredocs-uploads, key, uploadId)
        Note over S3: unknown upload == success (idempotent)
        CW->>CW: log("aborted stale multipart upload")
    end

    Note over CW,S3: Backstop: bucket lifecycle aborts<br/>incomplete multiparts after 1 day anyway
```

## Phase 4 — backfillExpiry

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant PG as PostgreSQL

    CW->>PG: UPDATE processing_jobs<br/>SET expires_at = created_at + (FREE_JOB_TTL seconds)<br/>WHERE user_id IS NOT NULL AND expires_at IS NULL
    PG-->>CW: rows_affected

    alt rows_affected > 0
        CW->>CW: log("backfilled expires_at", count)
    else
        CW->>CW: no-op (steady state)
    end
```

## Decision Flow (one tick)

```mermaid
flowchart TD
    A["Ticker fires"] --> B{"SETNX cleanup-worker:lock<br/>(TTL 10m)"}
    B -->|Locked elsewhere| Z["Skip cycle"]
    B -->|Acquired| C["Phase 1: cleanupExpiredJobs<br/>(RemoveObject per metadata row)"]
    C --> D["Phase 2: cleanupUploadState<br/>(DEL + AbortMultipart + RemoveObject)"]
    D --> E["Phase 3: abortStaleMultipartUploads"]
    E --> F["Phase 4: backfill legacy expires_at"]
    F --> G["DEL cleanup-worker:lock"]
    G --> Z
```

## Timing Diagram

```mermaid
gantt
    title Cleanup Worker Execution Timeline (default 15-minute interval)
    dateFormat mm:ss
    axisFormat %M:%S

    section Startup
    Load config & connect    :s1, 00:00, 2s
    Start HTTP :8088         :s2, after s1, 1s
    First cleanup run        :s3, after s2, 6s

    section Tick 1 (under SETNX lock)
    Phase 1 expired jobs     :p1, 14:00, 3s
    Phase 2 upload state     :p2, after p1, 2s
    Phase 3 stale multiparts :p3, after p2, 2s
    Phase 4 backfill         :p4, after p3, 1s

    section Tick 2
    Phase 1 expired jobs     :q1, 29:00, 3s
    Phase 2 upload state     :q2, after q1, 2s
    Phase 3 stale multiparts :q3, after q2, 2s
    Phase 4 backfill         :q4, after q3, 1s
```
