# Organize-PDF Service -- Architecture

Internal structure and component diagram of the `organize-pdf` service (port 8084).

## Component Diagram

```mermaid
graph TB
    subgraph organize-pdf[" organize-pdf :8084 "]
        direction TB

        subgraph HTTP["HTTP Server (Gin)"]
            TRACE["OpenTelemetry Middleware"]
            METRICS["Metrics Middleware"]
            REQID["Request ID Middleware"]
            LOGGER["Request Logger"]
            RECOVERY["Recovery Middleware"]
            HEALTHZ["/healthz"]
            METRICSEP["/metrics"]
            DETECTEP["POST /internal/v1/detect-edges<br/>(internal/httpapi · optional X-Internal-Token)"]
        end

        subgraph Imaging["internal/imaging (pure Go)"]
            DECODE["decode (jpeg/png/webp · ≤50MP)"]
            HOMOG["homography<br/>(perspective warp)"]
            ENHANCE["enhance<br/>(grayscale/bw/color-boost)"]
            DETECTPIPE["detect<br/>(downscale→500px · blur · Sobel ·<br/>threshold · Hough · quad)"]
        end

        subgraph Worker["NATS Worker (goroutine)"]
            CONSUMER["JetStream Pull Consumer<br/>Durable: organize-pdf<br/>Filter: jobs.dispatch.organize-pdf"]
            MSGLOOP["Message Loop"]
            DISPATCH["Tool Dispatcher"]
            STAGE["fetchInputs()<br/>(uploads bucket → scratch/in)"]
            STORE["storeOutput()<br/>(scratch/out → outputs bucket jobs/&lt;jobId&gt;/...)"]
        end

        subgraph Processing["processing package (pdfcpu + Tesseract)"]
            PROC["ProcessFile()"]
            MERGE["merge-pdf"]
            SPLIT["split-pdf (→ ZIP)"]
            REMOVE["remove-pages"]
            EXTRACT["extract-pages (CollectFile)"]
            ORGANIZE["organize-pdf (reorder)"]
            ROTATE["rotate-pdf<br/>(90/180/270 · all/odd/even)"]
            SCAN["scan-to-pdf<br/>(warp/rotate/enhance preprocessing ·<br/>page-size import · optional Tesseract OCR)"]
            WATERMARK["watermark-pdf<br/>(text or image)"]
            PROTECT["protect-pdf"]
            UNLOCK["unlock-pdf"]
            SIGN["sign-pdf<br/>(image stamp)"]
            EDIT["edit-pdf<br/>(text stamp)"]
            PAGENO["add-page-numbers"]
        end

        subgraph Models["internal/models"]
            DB_CONN["Database Connection<br/>(GORM)"]
            JOB_MODEL["ProcessingJob"]
            FILE_MODEL["FileMetadata"]
        end
    end

    NATS["NATS JetStream<br/>JOBS_DISPATCH"] -->|jobs.dispatch.organize-pdf| CONSUMER
    CONSUMER --> MSGLOOP --> DISPATCH

    DISPATCH --> STAGE --> PROC
    PROC --> STORE
    PROC --> MERGE
    PROC --> SPLIT
    PROC --> REMOVE
    PROC --> EXTRACT
    PROC --> ORGANIZE
    PROC --> ROTATE
    PROC --> SCAN
    PROC --> WATERMARK
    PROC --> PROTECT
    PROC --> UNLOCK
    PROC --> SIGN
    PROC --> EDIT
    PROC --> PAGENO

    DISPATCH -->|Update status| DB_CONN
    DISPATCH -->|"jobs.events.&lt;jobId&gt;.{processing,completed,failed}"| EVENTS["JOBS_EVENTS"]
    DISPATCH -.->|on MaxDeliver| DLQ["jobs.dlq.organize-pdf · JOBS_DLQ · 7d"]
    DB_CONN --> PG[(PostgreSQL)]
    HEALTHZ -->|Ping| Redis[(Redis)]
    HEALTHZ -->|Check connected| NATS
    MINIO[("MinIO / S3<br/>uploads + outputs buckets")]
    STAGE -->|DownloadToFile| MINIO
    STORE -->|UploadFromFile| MINIO
    PROC --> Scratch[(container-local scratch dir<br/>job-&lt;jobId&gt;-* · removed after job)]

    JOBSVC["job-service<br/>(POST /api/organize-pdf/detect-edges relay)"] -->|"POST {bucket?, key}"| DETECTEP
    DETECTEP -->|GetObject ≤30MiB| MINIO
    DETECTEP --> DECODE --> DETECTPIPE
    SCAN --> DECODE
    SCAN --> HOMOG
    SCAN --> ENHANCE
```

## Allowed Tool Types

The whitelist in `main.go:59` is the authoritative source — 13 tools.

```mermaid
graph LR
    subgraph Structural["Structural"]
        A["merge-pdf"]
        B["split-pdf"]
        C["remove-pages"]
        D["extract-pages"]
        E["organize-pdf<br/>(reorder)"]
        F["rotate-pdf"]
        G["scan-to-pdf"]
    end

    subgraph Annotation["Annotation / stamping"]
        H["watermark-pdf"]
        I["sign-pdf"]
        J["edit-pdf"]
        K["add-page-numbers"]
    end

    subgraph Encryption["Encryption"]
        L["protect-pdf"]
        M["unlock-pdf"]
    end
```

## Worker Configuration

```mermaid
graph TD
    subgraph ConsumerConfig["NATS Consumer Configuration"]
        A["Durable: organize-pdf"]
        B["FilterSubject: jobs.dispatch.organize-pdf"]
        C["AckPolicy: Explicit"]
        D["MaxDeliver: 4"]
        E["AckWait: 30 minutes"]
        F["BackOff: 10s, 30s, 2m"]
    end

    subgraph Dependencies
        PG[(PostgreSQL)]
        Redis[(Redis)]
        NATS["NATS JetStream"]
        S3[("MinIO / S3<br/>uploads + outputs buckets")]
    end

    ConsumerConfig --> NATS
```
