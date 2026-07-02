# Job Service -- Architecture

Internal structure and component diagram of the `job-service` (port 8081).

## Component Diagram

```mermaid
graph TB
    subgraph job-service[" job-service :8081 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry · GinTraceMiddleware"]
            METRICS["Prometheus · GinMetricsMiddleware"]
            REQID["GinRequestID"]
            LOGGER["GinRequestLogger"]
            AUTHMW["GinAuthMiddleware<br/>(JWT verifier · denylist · guest)"]
        end

        subgraph Routes["Route Groups"]
            subgraph UploadRoutes["/api/uploads (rate-limited 30/min)"]
                INIT["POST /init (presign multipart)"]
                PARTS["GET /:uploadId/parts (re-presign)"]
                COMPLETE["POST /:uploadId/complete"]
                STATUS["GET /:uploadId/status"]
                ABORT["DELETE /:uploadId"]
                CHUNK["PUT /:uploadId/chunk → 410 stub"]
            end

            subgraph CFRoutes["/api/convert-from-pdf"]
                CF_LIST["GET /:tool"]
                CF_CREATE["POST /:tool"]
                CF_GET["GET /:tool/:id"]
                CF_DELETE["DELETE /:tool/:id"]
                CF_DOWNLOAD["GET /:tool/:id/download"]
            end

            subgraph CTRoutes["/api/convert-to-pdf"]
                CT_LIST["GET /:tool"]
                CT_CREATE["POST /:tool"]
                CT_GET["GET /:tool/:id"]
                CT_DELETE["DELETE /:tool/:id"]
                CT_DOWNLOAD["GET /:tool/:id/download"]
            end

            subgraph ORGRoutes["/api/organize-pdf"]
                ORG_LIST["GET /:tool"]
                ORG_CREATE["POST /:tool"]
                ORG_GET["GET /:tool/:id"]
                ORG_DELETE["DELETE /:tool/:id"]
                ORG_DOWNLOAD["GET /:tool/:id/download"]
            end

            subgraph OPTRoutes["/api/optimize-pdf"]
                OPT_LIST["GET /:tool"]
                OPT_CREATE["POST /:tool"]
                OPT_GET["GET /:tool/:id"]
                OPT_DELETE["DELETE /:tool/:id"]
                OPT_DOWNLOAD["GET /:tool/:id/download"]
            end

            HISTORY["GET /api/jobs/history (auth required)"]
            SSE_ROUTE["GET /api/jobs/:id/events (SSE)"]
            HEALTHZ["/healthz · /readyz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            UH["Upload Handlers<br/>(presigned S3 multipart: init · re-presign · complete · abort)"]
            JH["Job Handlers<br/>(CreateJobFromTool · GetJobsByTool · GetJobByID · DeleteJobByID · DownloadJobFile→302 · GetJobHistory)"]
            SSEH["SSE Handler<br/>(ephemeral NATS consumer · FilterSubject scoped to jobId)"]
            OS["ObjectStore interface<br/>(shared/storage.Client injected at boot)"]
        end

        subgraph Internal
            ROUTING["routing.ServiceForTool()<br/>Tool → service-name"]
            IDEMP["Idempotency-Key cache (Redis)<br/>10-minute TTL"]
            UPLOAD_DEDUP["Upload→Job mapping<br/>(prevents replay confusion)"]
            VERIFIER["Auth Verifier"]
            DENYLIST["Token Denylist"]
            GUESTSTORE["Guest Store<br/>(guest:{token}:jobs)"]
        end

        subgraph RateLimiting
            RL_UPLOAD["ratelimit:upload:&lt;ip&gt; · 30/min"]
            RL_JOBCREATE["ratelimit:jobcreate:&lt;ip&gt; · 20/min (POST /:tool)"]
        end

        subgraph Models["internal/models (GORM)"]
            JOB["processing_jobs (UUIDv7)"]
            FM["file_metadata"]
        end
    end

    Client["api-gateway"] --> TRACE --> METRICS --> REQID --> LOGGER --> AUTHMW
    AUTHMW --> Routes

    UploadRoutes --> UH
    CFRoutes --> JH
    CTRoutes --> JH
    ORGRoutes --> JH
    OPTRoutes --> JH
    HISTORY --> JH
    SSE_ROUTE --> SSEH

    UH --> Redis[(Redis)]
    UH --> OS
    JH --> OS
    OS --> S3[("MinIO / S3<br/>uploads + outputs buckets")]
    Browser["Browser"] -.->|presigned PUT parts / presigned GET download| S3
    JH --> JOB
    JH --> FM
    JH --> IDEMP
    JH --> UPLOAD_DEDUP
    JH --> ROUTING
    JOB & FM --> PG[(PostgreSQL)]
    ROUTING --> NATS["NATS JetStream<br/>JOBS_DISPATCH<br/>(jobs.dispatch.&lt;service&gt;)"]
    JH --> NATS
    SSEH --> NATS
    SSEH -->|filter jobs.events.&lt;jobId&gt;.>| NATS
    DENYLIST --> Redis
    GUESTSTORE --> Redis
    RateLimiting --> Redis
    IDEMP --> Redis
    UPLOAD_DEDUP --> Redis
```

## Job Dispatch Flow

```mermaid
flowchart TD
    A["POST /api/&lt;group&gt;/:tool"] --> B["normalizeToolType"]
    B --> C["routing.ServiceForTool(tool) — reject unknown"]
    C --> D{"Idempotency-Key in Redis?"}
    D -->|Yes, hit| Z["Return original job"]
    D -->|No| E{"Body type?"}
    E -->|JSON uploadIds| E1["Look up by Upload→Job map<br/>(replay safety)"]
    E1 -->|hit| Z
    E1 -->|miss| F["consumeUpload(uploadId): StatObject(uploads, key) for true size<br/>+ ranged-GET MIME sniff (read-only, object stays in place)"]
    E -->|multipart| G["storeDirectUpload: PutObject → uploads/&lt;jobId&gt;/&lt;file&gt;<br/>+ 512-byte tee MIME sniff"]
    F --> H
    G --> H
    H["Build ProcessingJob (UUIDv7) + FileMetadata rows<br/>in single DB transaction"]
    H --> I["Compute expires_at from plan TTL"]
    I --> J["Publish JobEvent to jobs.dispatch.&lt;serviceName&gt;<br/>(JOBS_DISPATCH WorkQueue)"]
    J --> K["Record upload→job mapping<br/>+ release upload Redis state"]
    K --> L["Cache idempotency-key:jobId for 10m"]
    L --> M["publish analytics.events.job.created"]
    M --> N["201 {job + guestToken?}"]
```

## Tool-to-Service Routing Map

```mermaid
graph LR
    subgraph convert-from-pdf
        A1["pdf-to-image / pdf-to-img"]
        A2["pdf-to-pdfa"]
        A3["pdf-to-word / pdf-to-docx"]
        A4["pdf-to-excel / pdf-to-xlsx"]
        A5["pdf-to-ppt / pdf-to-powerpoint / pdf-to-pptx"]
        A6["pdf-to-html"]
        A7["pdf-to-text / pdf-to-txt"]
        A8["pdf-to-odt · pdf-to-ods · pdf-to-odp"]
    end

    subgraph convert-to-pdf
        B1["word-to-pdf"]
        B2["ppt-to-pdf / powerpoint-to-pdf"]
        B3["excel-to-pdf"]
        B4["html-to-pdf"]
        B5["image-to-pdf / img-to-pdf"]
    end

    subgraph organize-pdf
        C1["merge-pdf"]
        C2["split-pdf"]
        C3["remove-pages · extract-pages"]
        C4["organize-pdf · scan-to-pdf"]
        C5["rotate-pdf · watermark-pdf"]
        C6["protect-pdf · unlock-pdf · sign-pdf"]
        C7["edit-pdf · add-page-numbers"]
    end

    subgraph optimize-pdf
        D1["compress-pdf"]
        D2["repair-pdf"]
        D3["ocr-pdf"]
    end

    ROUTER["routing.ServiceForTool()"] --> convert-from-pdf & convert-to-pdf & organize-pdf & optimize-pdf
```

## SSE Consumer Lifecycle

```mermaid
flowchart TD
    A["Client opens GET /api/jobs/:id/events"] --> B["Set SSE headers"]
    B --> C["Create ephemeral consumer on JOBS_EVENTS<br/>FilterSubject jobs.events.&lt;jobId&gt;.&gt;<br/>DeliverPolicy=DeliverNewPolicy<br/>InactiveThreshold=1m"]
    C --> D["Send 'connected' event"]
    D --> E["Loop: Fetch up to 1 msg every 5s"]
    E -->|got msg| F["Forward as 'job-update' event"]
    F --> G{"Status terminal?"}
    G -->|yes (completed/failed)| H["Close stream"]
    G -->|no| E
    E -->|no msg / timeout| I["Send 15s keepalive comment"]
    I --> E
    E -->|ctx done / 5min cap| H
    H --> J["DELETE consumer (best-effort)"]
```

## Dependency Graph

```mermaid
graph LR
    JS[job-service] --> |shared/config| Config
    JS --> |shared/logger| Logger
    JS --> |shared/metrics| Metrics
    JS --> |shared/telemetry| Telemetry
    JS --> |shared/redisstore| RedisStore
    JS --> |shared/natsconn| NATSConn
    JS --> |shared/queue| Queue
    JS --> |shared/response| Response
    JS --> |shared/storage| Storage

    Storage --> |minio-go/v7| S3[("MinIO / S3")]

    JS --> |shared/authverify| AuthVerify
    JS --> |internal/models| Models
    JS --> |internal/routing| Routing

    Models --> |gorm + UUIDv7| PG[(PostgreSQL)]
    RedisStore --> |go-redis/v9| Redis[(Redis)]
    NATSConn --> NATS["NATS JetStream"]
    Queue --> |PublishJobEvent · SubjectForDispatch| NATS
    AuthVerify --> |golang-jwt/jwt/v5| JWT
```
