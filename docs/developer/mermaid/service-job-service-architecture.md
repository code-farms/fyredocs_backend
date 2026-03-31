# Job Service -- Architecture

Internal structure and component diagram of the `job-service` (port 8081).

## Component Diagram

```mermaid
graph TB
    subgraph job-service[" job-service :8081 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry<br/>GinTraceMiddleware"]
            METRICS["Prometheus<br/>GinMetricsMiddleware"]
            REQID["Request ID"]
            LOGGER["Request Logger"]
            AUTHMW["Auth Middleware<br/>(JWT + Guest)"]
        end

        subgraph Routes["Route Groups"]
            subgraph UploadRoutes["/api/uploads"]
                INIT["POST /init"]
                CHUNK["PUT /:uploadId/chunk"]
                STATUS["GET /:uploadId/status"]
                COMPLETE["POST /:uploadId/complete"]
            end

            subgraph ConvertFromRoutes["/api/convert-from-pdf"]
                CF_LIST["GET /:tool"]
                CF_CREATE["POST /:tool"]
                CF_GET["GET /:tool/:id"]
                CF_DELETE["DELETE /:tool/:id"]
                CF_DOWNLOAD["GET /:tool/:id/download"]
            end

            subgraph ConvertToRoutes["/api/convert-to-pdf"]
                CT_LIST["GET /:tool"]
                CT_CREATE["POST /:tool"]
                CT_GET["GET /:tool/:id"]
                CT_DELETE["DELETE /:tool/:id"]
                CT_DOWNLOAD["GET /:tool/:id/download"]
            end

            HISTORY["GET /api/jobs/history"]
            SSE_ROUTE["GET /api/jobs/:id/events"]
            HEALTHZ["/healthz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            UH["Upload Handlers<br/>(chunked upload)"]
            JH["Job Handlers<br/>(CRUD + dispatch)"]
            SSEH["SSE Handler<br/>(real-time job updates)"]
        end

        subgraph Internal
            ROUTING["routing.ServiceForTool()<br/>Tool-to-service mapping"]
            VERIFIER["Auth Verifier"]
            DENYLIST["Token Denylist"]
            GUESTSTORE["Guest Store"]
        end

        subgraph RateLimiting
            RL_UPLOAD["upload: 30 req/min"]
        end
    end

    Client["api-gateway"] --> TRACE
    TRACE --> METRICS --> REQID --> LOGGER --> AUTHMW

    UploadRoutes --> UH
    ConvertFromRoutes --> JH
    ConvertToRoutes --> JH
    HISTORY --> JH
    SSE_ROUTE --> SSEH

    UH --> Redis[(Redis)]
    UH --> Disk[(File System)]
    JH --> PG[(PostgreSQL)]
    JH --> Redis
    JH --> ROUTING
    ROUTING --> NATS["NATS JetStream<br/>(PublishJobEvent)"]
    SSEH --> NATS
    DENYLIST --> Redis
    GUESTSTORE --> Redis
    RateLimiting --> Redis
```

## Job Dispatch Flow

```mermaid
flowchart TD
    A["POST /api/convert-from-pdf/pdf-to-word"] --> B["Normalize tool type"]
    B --> C["Validate tool is supported"]
    C --> D["Save uploaded file(s)"]
    D --> E["Create job record in PostgreSQL<br/>(status: queued)"]
    E --> F["routing.ServiceForTool(toolType)"]
    F --> G{"Service name?"}
    G -->|convert-from-pdf| H["Publish to<br/>jobs.dispatch.convert-from-pdf"]
    G -->|convert-to-pdf| I["Publish to<br/>jobs.dispatch.convert-to-pdf"]
    G -->|organize-pdf| J["Publish to<br/>jobs.dispatch.organize-pdf"]
    G -->|optimize-pdf| K["Publish to<br/>jobs.dispatch.optimize-pdf"]
    H --> L["Return 201 {job}"]
    I --> L
    J --> L
    K --> L
```

## Tool-to-Service Routing Map

```mermaid
graph LR
    subgraph convert-from-pdf
        A1["pdf-to-word"]
        A2["pdf-to-excel"]
        A3["pdf-to-powerpoint"]
        A4["pdf-to-image"]
        A5["ocr"]
    end

    subgraph convert-to-pdf
        B1["word-to-pdf"]
        B2["excel-to-pdf"]
        B3["powerpoint-to-pdf"]
        B4["image-to-pdf"]
        B5["merge-pdf"]
        B6["split-pdf"]
        B7["compress-pdf"]
        B8["protect-pdf / unlock-pdf"]
        B9["watermark-pdf / sign-pdf / edit-pdf"]
    end

    ROUTER["routing.ServiceForTool()"] --> convert-from-pdf
    ROUTER --> convert-to-pdf
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

    JS --> |internal/authverify| AuthVerify
    JS --> |internal/models| Models
    JS --> |internal/routing| Routing

    Models --> |gorm| PG[(PostgreSQL)]
    RedisStore --> |go-redis/v9| Redis[(Redis)]
    NATSConn --> NATS["NATS JetStream"]
    Queue --> |PublishJobEvent| NATS
    AuthVerify --> |golang-jwt/jwt/v5| JWT
```
