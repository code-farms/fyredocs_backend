# Cleanup Worker -- Sequence Diagrams

Execution flows for the `cleanup-worker` background service.

## Startup and Main Loop

```mermaid
sequenceDiagram
    participant Main as main()
    participant Cfg as shared/config
    participant DB as PostgreSQL
    participant R as Redis
    participant HTTP as Gin :8088
    participant Cleanup as runCleanup()

    Main->>Cfg: LoadConfig()
    Main->>Main: logger.Init("cleanup-worker")
    Main->>Main: telemetry.Init("cleanup-worker")
    Main->>DB: models.Connect() + Migrate()
    Main->>R: redisstore.Connect()
    Main->>HTTP: ListenAndServe :8088 (/healthz, /readyz, /metrics)
    Main->>Main: Start ticker (CLEANUP_INTERVAL, default 15m)

    loop Forever
        Main->>Cleanup: runCleanup(ctx)
        Cleanup->>R: SETNX cleanup-worker:lock TTL=10m
        alt Lock acquired
            Cleanup->>Cleanup: cleanupExpiredJobs(ctx)
            Cleanup->>Cleanup: cleanupUploadState(ctx)
            Cleanup->>Cleanup: cleanupOrphanedDirs(ctx)
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
    participant Disk as File System

    loop Until batch < 100
        CW->>PG: SELECT * FROM processing_jobs<br/>WHERE expires_at IS NOT NULL<br/>AND expires_at <= NOW() LIMIT 100
        PG-->>CW: jobs[]

        alt no rows
            CW->>CW: return
        end

        CW->>PG: SELECT * FROM file_metadata WHERE job_id IN (jobIds[])
        PG-->>CW: files[]

        Note over CW: group files by job_id
        loop For each job
            loop For each file
                CW->>Disk: os.Remove(file.Path)
            end
            CW->>Disk: os.Remove uploads/&lt;jobId&gt;/
            CW->>Disk: os.Remove outputs/&lt;jobId&gt;/
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
    participant Disk as File System

    CW->>R: SCAN 0 MATCH upload:* COUNT 100
    R-->>CW: keys

    loop For each key (skip :chunks)
        CW->>R: HGET upload:&lt;id&gt; createdAt
        R-->>CW: RFC3339 timestamp
        CW->>CW: time.Since(createdAt) > UPLOAD_TTL (2h)?

        alt Yes — stale
            CW->>R: DEL upload:&lt;id&gt; upload:&lt;id&gt;:chunks
            alt id parses as UUID
                CW->>Disk: os.RemoveAll(uploads/tmp/&lt;id&gt;/)
            end
        else No — keep
            CW->>CW: skip
        end
    end
```

## Phase 3 — cleanupOrphanedDirs

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant Disk as File System
    participant PG as PostgreSQL
    participant R as Redis

    Note over CW: Phase 3a — uploads/
    CW->>Disk: ReadDir(uploads/)
    Disk-->>CW: entries

    Note over CW: Collect UUID-named dirs (skip 'tmp')
    CW->>PG: SELECT id FROM processing_jobs WHERE id IN (candidates)
    PG-->>CW: existingIds[]

    loop For each candidate not in existingIds
        CW->>R: EXISTS upload:&lt;id&gt;
        alt active upload session
            CW->>CW: skip (a job will consume this soon)
        else no active session
            CW->>Disk: os.RemoveAll(uploads/&lt;id&gt;/)
        end
    end

    Note over CW: Phase 3b — outputs/
    CW->>Disk: ReadDir(outputs/)
    Disk-->>CW: entries

    Note over CW: Match regex ^[a-z]+_&lt;uuid&gt;_
    CW->>PG: SELECT id FROM processing_jobs WHERE id IN (extracted jobIds)
    PG-->>CW: existingIds[]

    loop For each output file with unmatched jobId
        CW->>Disk: os.Remove(outputs/&lt;file&gt;)
    end
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
    B -->|Acquired| C["Phase 1: cleanupExpiredJobs"]
    C --> D["Phase 2: cleanupUploadState"]
    D --> E["Phase 3a: orphan upload dirs"]
    E --> F["Phase 3b: orphan output files"]
    F --> G["Phase 4: backfill legacy expires_at"]
    G --> H["DEL cleanup-worker:lock"]
    H --> Z
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
    Phase 3 orphans          :p3, after p2, 2s
    Phase 4 backfill         :p4, after p3, 1s

    section Tick 2
    Phase 1 expired jobs     :q1, 29:00, 3s
    Phase 2 upload state     :q2, after q1, 2s
    Phase 3 orphans          :q3, after q2, 2s
    Phase 4 backfill         :q4, after q3, 1s
```
