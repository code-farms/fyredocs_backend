# System Overview

High-level architecture of the Fyredocs PDF processing platform.

## Service Topology

```mermaid
graph TB
    subgraph Clients
        WebApp["Web Application<br/>(React / SPA)"]
        CLI["CLI / API Consumer"]
    end

    subgraph Edge["Caddy Edge :80/:443"]
        CADDY["caddy<br/>TLS (auto-HTTPS via PUBLIC_DOMAIN)<br/>SPA static files · object-byte routing"]
    end

    subgraph Gateway["API Gateway :8080 (internal-only)"]
        GW["api-gateway<br/>net/http reverse proxy<br/>JWT + guest verify, plan resolve"]
    end

    subgraph Core["Core Services"]
        AUTH["auth-service :8086<br/>Gin · DB-backed sessions"]
        JOB["job-service :8081<br/>Gin · uploads, jobs, SSE<br/>+ in-process cleanup loop<br/>(jobs · upload sessions · stale multiparts · backfill)"]
    end

    subgraph Workers["Worker Services (NATS consumers)"]
        CFP["convert-from-pdf :8082<br/>pdf2docx + LibreOffice + poppler"]
        CTP["convert-to-pdf :8083<br/>LibreOffice via unoserver (concurrent)"]
        ORG["organize-pdf :8084<br/>pdfcpu + Tesseract"]
        OPT["optimize-pdf :8085<br/>Ghostscript + Tesseract"]
    end

    subgraph Analytics["Analytics"]
        AN["analytics-service :8087<br/>Gin + NATS subscriber"]
    end

    subgraph Platform["Platform Services"]
        DOC["document-service :8089<br/>Gin · library + NATS finalize subscriber"]
        USR["user-service :8090<br/>Gin · orgs, memberships, RBAC"]
        NOT["notification-service :8091<br/>Gin · feed + SSE bell (NATS subscriber)"]
    end

    subgraph Infrastructure
        PG[(PostgreSQL)]
        RD[(Redis)]
        NATS["NATS JetStream<br/>JOBS_DISPATCH · JOBS_EVENTS · JOBS_DLQ · ANALYTICS<br/>(payloads = object keys; MaxMsgSize/MaxBytes capped)"]
        S3[("MinIO :9000 (internal)<br/>uploads · outputs<br/>bootstrap: minio-init (buckets · lifecycle · app user)")]
    end

    WebApp -->|HTTPS| CADDY
    CLI -->|HTTPS| CADDY
    WebApp -.->|SPA assets| CADDY

    CADDY -->|"/api/* · /auth/* · /admin/* · /healthz"| GW
    CADDY -->|"/uploads/* · /outputs/*<br/>presigned, direct (Host preserved)"| S3

    GW -->|/auth/*| AUTH
    GW -->|/api/upload/*| JOB
    GW -->|/api/jobs/*| JOB
    GW -->|/api/{convert,organize,optimize}-pdf/*| JOB
    GW -->|/api/{documents,folders,tags,exports}/*| DOC
    GW -->|/api/orgs/*| USR
    GW -->|/api/notifications/*| NOT
    GW -->|/admin/* · /api/dashboard| AN
    GW -->|plan info| RD

    JOB -->|jobs.dispatch.*| NATS
    NATS -->|jobs.dispatch.convert-from-pdf| CFP
    NATS -->|jobs.dispatch.convert-to-pdf| CTP
    NATS -->|jobs.dispatch.organize-pdf| ORG
    NATS -->|jobs.dispatch.optimize-pdf| OPT

    CFP -->|jobs.events.<jobId>.*| NATS
    CTP -->|jobs.events.<jobId>.*| NATS
    ORG -->|jobs.events.<jobId>.*| NATS
    OPT -->|jobs.events.<jobId>.*| NATS
    NATS -->|SSE filter consumer| JOB

    AUTH --> PG
    AUTH --> RD
    JOB --> PG
    JOB --> RD
    JOB --> NATS
    CFP --> PG
    CFP --> RD
    CTP --> PG
    CTP --> RD
    ORG --> PG
    ORG --> RD
    OPT --> PG
    OPT --> RD
    AN --> PG
    AN --> NATS
    AUTH -->|analytics.events.*| NATS
    JOB -->|analytics.events.*| NATS
    JOB -->|presign · stat · multipart| S3
    CFP -->|input download · output upload| S3
    CTP -->|input download · output upload| S3
    ORG -->|input download · output upload| S3
    OPT -->|input download · output upload| S3
    JOB -->|cleanup: RemoveObject · AbortMultipart| S3
    GW --> RD

    DOC --> PG
    USR --> PG
    NOT --> PG
    NATS -->|jobs.events.> finalize| DOC
    NATS -->|jobs.events.> notify| NOT
    DOC -->|GET /api/orgs/:id RBAC| USR
    DOC -.->|POST /internal/notifications| NOT
```

## Observability (opt-in `observability` profile)

Every service already emits OpenTelemetry traces (`shared/telemetry`) and
Prometheus metrics at `/metrics` (`shared/metrics`). The backing stack below is
**not** started by a plain `docker compose up` — bring it up with
`docker compose --profile observability up -d`. When it is down, the services'
OTLP endpoint probe fails and tracing self-disables cleanly.

```mermaid
graph LR
    subgraph Services["All 11 services"]
        SVC["api-gateway · auth · job · workers ·<br/>analytics · document · user · notification<br/>(OTLP traces + /metrics)"]
    end

    subgraph Obs["Observability stack (profile: observability)"]
        COL["otel-collector<br/>OTLP/HTTP :4318 · gRPC :4317<br/>self-metrics :8888"]
        TEMPO["tempo<br/>trace store · HTTP API :3200"]
        PROM["prometheus :9090<br/>scrapes /metrics (loopback UI)"]
        GRAF["grafana :3000<br/>dashboards + trace explorer (loopback UI)"]
    end

    SVC -->|OTLP/HTTP :4318| COL
    COL -->|OTLP/gRPC :4317| TEMPO
    PROM -->|scrape /metrics| SVC
    PROM -->|scrape :8888| COL
    GRAF -->|PromQL| PROM
    GRAF -->|traces| TEMPO
```

## Data Flow Overview

```mermaid
flowchart LR
    subgraph Upload["Upload (presigned, same-origin via the Caddy edge)"]
        A[Client] -->|1. Init upload JSON| B[job-service]
        B -->|Store state incl. key + s3UploadId| Redis[(Redis: upload:*)]
        B -->|presign part URLs for edge origin| S3[("MinIO<br/>uploads")]
        A -->|"2. PUT parts via Caddy<br/>/uploads/*?X-Amz-..."| S3
        A -->|3. Complete upload JSON + ETags| B
        B -->|CompleteMultipart| S3
    end

    subgraph Processing
        A -->|4. POST /api/<group>/:tool| B
        B -->|5. Save ProcessingJob<br/>UUIDv7 + idempotency| PG[(PostgreSQL)]
        B -->|6. Publish JobMessage with object key| NATS["NATS JetStream<br/>JOBS_DISPATCH"]
        NATS -->|7. Pull-consumer| W["Worker Service"]
        W -->|8. download input to tmpfs scratch| S3
        W -->|9. upload output jobs/<jobId>/...| S3O[("MinIO<br/>outputs")]
        W -->|10. Update status, output key| PG
        W -->|11. Publish progress / completed / failed| EV["jobs.events.<jobId>.*"]
        W -->|on max-retry exhaustion| DLQ["jobs.dlq.<service>"]
    end

    subgraph Retrieval
        A -->|12. SSE /api/jobs/:id/events| B
        EV --> B
        B -->|stream events| A
        A -->|13. GET download → presigned URL| B
        A -->|"14. GET via Caddy<br/>/outputs/*?X-Amz-..."| S3O
    end
```

## NATS JetStream Streams

```mermaid
graph LR
    subgraph JOBS_DISPATCH["JOBS_DISPATCH (WorkQueue · 24h · 1 GiB · 64 KiB/msg)"]
        D1["jobs.dispatch.convert-from-pdf"]
        D2["jobs.dispatch.convert-to-pdf"]
        D3["jobs.dispatch.organize-pdf"]
        D4["jobs.dispatch.optimize-pdf"]
    end

    subgraph JOBS_EVENTS["JOBS_EVENTS (Interest · 1h · 256 MiB)"]
        E1["jobs.events.&lt;jobId&gt;.progress"]
        E2["jobs.events.&lt;jobId&gt;.completed"]
        E3["jobs.events.&lt;jobId&gt;.failed"]
    end

    subgraph JOBS_DLQ["JOBS_DLQ (Limits · 7d · 256 MiB)"]
        Q1["jobs.dlq.&lt;service&gt;"]
    end

    subgraph ANALYTICS_STREAM["ANALYTICS (Interest · 24h · 256 MiB)"]
        A1["analytics.events.user.*"]
        A2["analytics.events.job.*"]
        A3["analytics.events.plan.*"]
    end

    JS[job-service] -->|Publish| D1
    JS -->|Publish| D2
    JS -->|Publish| D3
    JS -->|Publish| D4

    D1 -->|Consume| CFP[convert-from-pdf]
    D2 -->|Consume| CTP[convert-to-pdf]
    D3 -->|Consume| ORG[organize-pdf]
    D4 -->|Consume| OPT[optimize-pdf]

    CFP -->|Publish events| E1
    CTP -->|Publish events| E1
    ORG -->|Publish events| E1
    OPT -->|Publish events| E1
    E1 -->|filter by jobId| JS
    E2 -->|filter by jobId| JS
    E3 -->|filter by jobId| JS
    E2 -->|finalize completed → document| DOC2[document-service]
    E2 -->|notify user| NOT2[notification-service]
    E3 -->|notify user| NOT2

    CFP -.->|on max-retry| Q1
    CTP -.->|on max-retry| Q1
    ORG -.->|on max-retry| Q1
    OPT -.->|on max-retry| Q1

    AUTH2[auth-service] -->|Publish| A1
    JS -->|Publish| A2
    JS -->|Publish| A3
    A1 -->|Consume| AN[analytics-service]
    A2 -->|Consume| AN
    A3 -->|Consume| AN
    E1 -->|Consume| AN
    E2 -->|Consume| AN
    E3 -->|Consume| AN
```

## Authentication Flow

```mermaid
flowchart TD
    Client -->|Request with auth cookie or Bearer token| Caddy[Caddy edge]
    Caddy -->|/api/* · /auth/* · /admin/*| GW[api-gateway]
    GW -->|Verify JWT via HS256 secret| GW
    GW -->|Check token denylist| Redis[(Redis)]
    GW -->|No token? Issue/load guest_token cookie| Redis
    GW -->|ResolvePlan: read plan info| Redis
    GW -->|Set X-User-ID, X-Role, X-Plan headers| Backend[Backend Service]
    Backend -->|Trust gateway headers OR re-verify| Backend
```

## Refresh Token Rotation

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as api-gateway
    participant AUTH as auth-service
    participant DB as Postgres (user_sessions)

    C->>GW: POST /auth/refresh (refresh cookie)
    GW->>AUTH: forward
    AUTH->>DB: lookup session by refresh_token_hash
    alt Session valid & not revoked
        AUTH->>AUTH: issue new access + refresh pair
        AUTH->>DB: revoke old session row, insert new session row
        AUTH-->>C: Set-Cookie: access + refresh (new pair)
    else Session missing / reused / revoked
        AUTH->>DB: revoke ALL sessions for user (defense in depth)
        AUTH-->>C: 401 Unauthorized
    end
```
