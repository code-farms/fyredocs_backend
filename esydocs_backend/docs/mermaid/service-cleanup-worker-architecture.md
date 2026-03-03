# Cleanup Worker -- Architecture

Internal structure and component diagram of the `cleanup-worker` service.

## Component Diagram

```mermaid
graph TB
    subgraph cleanup-worker[" cleanup-worker "]
        direction TB

        subgraph Main["main() Loop"]
            INIT["Initialize<br/>Config, Logger, Telemetry"]
            CONNECT["Connect DB, Redis"]
            TICKER["time.Ticker<br/>(default: 15 min)"]
            LOOP["Infinite Loop<br/>runCleanup() on each tick"]
        end

        subgraph Cleanup["Cleanup Functions"]
            EXPIRED["cleanupExpiredJobs()"]
            UPLOAD["cleanupUploadState()"]
        end

        subgraph ExpiredJobs["cleanupExpiredJobs()"]
            QUERY["Query expired guest jobs<br/>WHERE user_id IS NULL<br/>AND expires_at <= now()"]
            DELETE_FILES["Delete associated files<br/>from disk"]
            DELETE_META["Delete file_metadata records"]
            DELETE_JOB["Delete processing_job records"]
        end

        subgraph UploadState["cleanupUploadState()"]
            SCAN["Redis SCAN upload:*"]
            CHECK_TTL["Check createdAt > UPLOAD_TTL (2h)"]
            DELETE_REDIS["DEL upload:<id>, upload:<id>:chunks"]
            DELETE_DIR["Remove uploads/tmp/<id>/ directory"]
        end

        subgraph Models["internal/models"]
            DB_CONN["Database Connection<br/>(GORM)"]
            JOB_MODEL["ProcessingJob"]
            FILE_MODEL["FileMetadata"]
        end
    end

    INIT --> CONNECT --> TICKER --> LOOP
    LOOP --> EXPIRED
    LOOP --> UPLOAD

    EXPIRED --> QUERY --> DELETE_FILES --> DELETE_META --> DELETE_JOB
    UPLOAD --> SCAN --> CHECK_TTL --> DELETE_REDIS --> DELETE_DIR

    DB_CONN --> PG[(PostgreSQL)]
    SCAN --> Redis[(Redis)]
    DELETE_REDIS --> Redis
    DELETE_FILES --> Disk[(File System)]
    DELETE_DIR --> Disk
```

## Cleanup Targets

```mermaid
graph LR
    subgraph Targets["What Gets Cleaned Up"]
        A["Expired Guest Jobs<br/>(user_id IS NULL,<br/>expires_at <= now)"]
        B["Stale Upload State<br/>(Redis keys older than<br/>UPLOAD_TTL / 2 hours)"]
        C["Orphaned Chunk Directories<br/>(uploads/tmp/<id>/)"]
    end

    subgraph NotCleaned["Not Cleaned (Handled Elsewhere)"]
        D["Registered User Jobs<br/>(kept indefinitely)"]
        E["NATS Message Redelivery<br/>(handled by JetStream AckWait)"]
    end
```

## Configuration

```mermaid
graph TD
    subgraph EnvVars["Environment Variables"]
        A["CLEANUP_INTERVAL<br/>Default: 15m<br/>How often the ticker fires"]
        B["UPLOAD_TTL<br/>Default: 2h<br/>Max age for upload state in Redis"]
        C["UPLOAD_DIR<br/>Default: uploads<br/>Base directory for uploaded files"]
    end
```
