# Optimize-PDF Service -- Architecture

Internal structure and component diagram of the `optimize-pdf` service (port 8085).

## Component Diagram

```mermaid
graph TB
    subgraph optimize-pdf[" optimize-pdf :8085 "]
        direction TB

        subgraph HTTP["HTTP Server (Gin)"]
            TRACE["OpenTelemetry Middleware"]
            METRICS["Metrics Middleware"]
            REQID["Request ID Middleware"]
            LOGGER["Request Logger"]
            RECOVERY["Recovery Middleware"]
            HEALTHZ["/healthz"]
            METRICSEP["/metrics"]
        end

        subgraph Worker["NATS Worker (goroutine)"]
            CONSUMER["JetStream Pull Consumer<br/>Durable: optimize-pdf<br/>Filter: jobs.dispatch.optimize-pdf"]
            MSGLOOP["Message Loop<br/>(Fetch 1, 30s wait)"]
            DISPATCH["Tool Dispatcher"]
        end

        subgraph Processing["processing package"]
            PROC["ProcessFile()"]
            COMPRESS["compress-pdf<br/>Reduce file size"]
            REPAIR["repair-pdf<br/>Fix corrupted PDFs"]
            OCR["ocr-pdf<br/>Add text layer via OCR"]
        end

        subgraph Models["internal/models"]
            DB_CONN["Database Connection<br/>(GORM)"]
            JOB_MODEL["ProcessingJob"]
            FILE_MODEL["FileMetadata"]
        end
    end

    NATS["NATS JetStream<br/>JOBS_DISPATCH"] -->|jobs.dispatch.optimize-pdf| CONSUMER
    CONSUMER --> MSGLOOP --> DISPATCH

    DISPATCH --> PROC
    PROC --> COMPRESS
    PROC --> REPAIR
    PROC --> OCR

    DISPATCH -->|Update status| DB_CONN
    DB_CONN --> PG[(PostgreSQL)]
    HEALTHZ -->|Ping| Redis[(Redis)]
    HEALTHZ -->|Check connected| NATS
    PROC --> Disk[(File System<br/>outputs/)]
```

## Allowed Tool Types

```mermaid
graph LR
    subgraph Tools["optimize-pdf Tool Types"]
        A["compress-pdf<br/>Reduce PDF file size<br/>with quality options"]
        B["repair-pdf<br/>Fix corrupted or<br/>malformed PDF files"]
        C["ocr-pdf<br/>Add searchable text layer<br/>to scanned PDFs"]
    end
```

## Service Architecture Pattern

```mermaid
graph TD
    subgraph Pattern["Worker Service Pattern (all worker services follow this)"]
        direction TB
        MAIN["main()"] -->|1| CONFIG["Load config + init logging"]
        CONFIG -->|2| INFRA["Connect DB, Redis, NATS"]
        INFRA -->|3| STREAMS["Ensure NATS JetStream streams"]
        STREAMS -->|4| WORKER["Launch worker goroutine"]
        STREAMS -->|5| HTTP["Start HTTP server<br/>(health + metrics only)"]
        HTTP -->|6| SIGNAL["Wait for shutdown signal"]
        SIGNAL -->|7| CLEANUP["Cancel ctx, drain NATS, shutdown HTTP"]
    end
```
