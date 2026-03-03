# Convert-from-PDF Service -- Architecture

Internal structure and component diagram of the `convert-from-pdf` service (port 8082).

## Component Diagram

```mermaid
graph TB
    subgraph convert-from-pdf[" convert-from-pdf :8082 "]
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
            CONSUMER["JetStream Pull Consumer<br/>Durable: convert-from-pdf<br/>Filter: jobs.dispatch.convert-from-pdf"]
            MSGLOOP["Message Loop<br/>(Fetch 1 at a time, 30s wait)"]
            DISPATCH["Tool Dispatcher"]
        end

        subgraph Processing["processing package"]
            PROC["ProcessFile()"]
            PDF_IMG["pdf-to-image"]
            PDF_WORD["pdf-to-word / pdf-to-docx"]
            PDF_EXCEL["pdf-to-excel / pdf-to-xlsx"]
            PDF_PPT["pdf-to-ppt / pdf-to-pptx"]
            PDF_HTML["pdf-to-html"]
            PDF_TEXT["pdf-to-text / pdf-to-txt"]
            PDF_PDFA["pdf-to-pdfa"]
        end

        subgraph Models["internal/models"]
            DB_CONN["Database Connection<br/>(GORM + PostgreSQL)"]
            JOB_MODEL["ProcessingJob model"]
            FILE_MODEL["FileMetadata model"]
        end
    end

    NATS["NATS JetStream<br/>JOBS_DISPATCH stream"] -->|jobs.dispatch.convert-from-pdf| CONSUMER
    CONSUMER --> MSGLOOP --> DISPATCH

    DISPATCH --> PROC
    PROC --> PDF_IMG
    PROC --> PDF_WORD
    PROC --> PDF_EXCEL
    PROC --> PDF_PPT
    PROC --> PDF_HTML
    PROC --> PDF_TEXT
    PROC --> PDF_PDFA

    DISPATCH -->|Update status| DB_CONN
    DISPATCH -->|Record output| DB_CONN

    DB_CONN --> PG[(PostgreSQL)]
    HEALTHZ -->|Ping| Redis[(Redis)]
    HEALTHZ -->|Check connected| NATS

    PROC --> Disk[(File System<br/>outputs/)]
```

## Allowed Tool Types

```mermaid
graph LR
    subgraph AllowedTools["Allowed Tool Types"]
        A["pdf-to-image<br/>(pdf-to-img)"]
        B["pdf-to-pdfa"]
        C["pdf-to-word<br/>(pdf-to-docx)"]
        D["pdf-to-excel<br/>(pdf-to-xlsx)"]
        E["pdf-to-ppt<br/>(pdf-to-powerpoint, pdf-to-pptx)"]
        F["pdf-to-html"]
        G["pdf-to-text<br/>(pdf-to-txt)"]
    end
```

## Worker Retry Strategy

```mermaid
stateDiagram-v2
    [*] --> Delivered: NATS delivers message
    Delivered --> Processing: Parse payload, validate tool
    Processing --> Completed: Process succeeds
    Processing --> RetryDecision: Process fails

    RetryDecision --> NAK_10s: Delivery 1, recoverable
    RetryDecision --> NAK_30s: Delivery 2, recoverable
    RetryDecision --> NAK_2m: Delivery 3, recoverable
    RetryDecision --> Failed: Delivery 4 OR non-recoverable

    NAK_10s --> Delivered: Redeliver after 10s
    NAK_30s --> Delivered: Redeliver after 30s
    NAK_2m --> Delivered: Redeliver after 2m

    Completed --> [*]: ACK message
    Failed --> [*]: ACK message (stop redelivery)
```
