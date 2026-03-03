# Cleanup Worker -- Sequence Diagrams

Execution flows for the `cleanup-worker` background service.

## Main Loop

```mermaid
sequenceDiagram
    participant Main as main()
    participant Config as shared/config
    participant DB as PostgreSQL
    participant Redis
    participant Cleanup as runCleanup()

    Main->>Config: LoadConfig()
    Main->>Main: logger.Init("cleanup-worker")
    Main->>Main: telemetry.Init("cleanup-worker")
    Main->>DB: models.Connect() + Migrate()
    Main->>Redis: redisstore.Connect()

    Main->>Main: Start ticker (15 min interval)

    loop Every 15 minutes (forever)
        Main->>Cleanup: runCleanup(ctx)

        Cleanup->>Cleanup: cleanupExpiredJobs(ctx)
        Cleanup->>Cleanup: cleanupUploadState(ctx)

        Note over Main: Wait for next tick
    end
```

## Cleanup Expired Guest Jobs

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant PG as PostgreSQL
    participant Disk as File System

    CW->>PG: SELECT * FROM processing_jobs<br/>WHERE user_id IS NULL<br/>AND expires_at IS NOT NULL<br/>AND expires_at <= now()<br/>LIMIT 100

    PG-->>CW: [job1, job2, ..., jobN]

    loop For each expired job
        CW->>PG: SELECT * FROM file_metadata<br/>WHERE job_id = <job.id>
        PG-->>CW: [file1, file2]

        loop For each file
            CW->>Disk: os.Remove(file.Path)
            Note over Disk: Remove input/output files
        end

        CW->>PG: DELETE FROM file_metadata<br/>WHERE job_id = <job.id>

        CW->>PG: DELETE FROM processing_jobs<br/>WHERE id = <job.id>
    end

    alt More than 100 jobs found
        Note over CW: Loop again (paginated cleanup)
        CW->>PG: SELECT next batch...
    else Fewer than 100 jobs
        Note over CW: Done with expired jobs
    end
```

## Cleanup Stale Upload State

```mermaid
sequenceDiagram
    participant CW as cleanup-worker
    participant Redis
    participant Disk as File System

    CW->>Redis: SCAN 0 MATCH upload:* COUNT 100
    Redis-->>CW: [cursor, keys]

    loop For each key (skip :chunks keys)
        CW->>Redis: HGET upload:<id> createdAt
        Redis-->>CW: "2024-01-15T10:30:00Z"

        CW->>CW: Parse timestamp<br/>Check: time.Since(createdAt) > 2h

        alt Upload is stale (> 2h old)
            CW->>Redis: DEL upload:<id>
            CW->>Redis: DEL upload:<id>:chunks

            CW->>CW: Parse upload ID as UUID

            alt Valid UUID
                CW->>Disk: os.RemoveAll(uploads/tmp/<id>/)
                Note over Disk: Remove orphaned chunk directory
            end
        else Upload is recent
            Note over CW: Skip (still valid)
        end
    end

    Note over CW: Continue SCAN until cursor = 0
```

## Cleanup Decision Flow

```mermaid
flowchart TD
    A["Cleanup tick fires"] --> B["cleanupExpiredJobs()"]
    B --> C{"Any expired guest jobs?"}
    C -->|Yes| D["Batch delete (100 at a time)"]
    D --> E["Delete files from disk"]
    E --> F["Delete file_metadata records"]
    F --> G["Delete processing_jobs records"]
    G --> C
    C -->|No more| H["cleanupUploadState()"]

    H --> I["SCAN Redis for upload:* keys"]
    I --> J{"Key is :chunks suffix?"}
    J -->|Yes| K["Skip"]
    J -->|No| L{"createdAt > UPLOAD_TTL?"}
    L -->|No| K
    L -->|Yes| M["DEL Redis keys"]
    M --> N["Remove chunk directory from disk"]
    N --> I
    K --> I
```

## Timing Diagram

```mermaid
gantt
    title Cleanup Worker Execution Timeline
    dateFormat mm:ss
    axisFormat %M:%S

    section Startup
    Load config & connect    :s1, 00:00, 2s
    First cleanup run        :s2, after s1, 5s

    section Periodic (every 15 min)
    Wait for tick            :w1, after s2, 14m
    Expired jobs cleanup     :c1, after w1, 3s
    Upload state cleanup     :c2, after c1, 2s
    Wait for tick            :w2, after c2, 14m
    Expired jobs cleanup     :c3, after w2, 3s
    Upload state cleanup     :c4, after c3, 2s
```
