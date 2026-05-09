# System Overview

High-level architecture of the Fyredocs PDF processing platform.

## Service Topology

```mermaid
graph TB
    subgraph Clients
        WebApp["Web Application<br/>(React / SPA)"]
        CLI["CLI / API Consumer"]
    end

    subgraph Gateway["API Gateway :8080"]
        GW["api-gateway<br/>net/http reverse proxy<br/>JWT + guest verify, plan resolve"]
        SPA["Static SPA<br/>(when SPA_DIR set)"]
    end

    subgraph Core["Core Services"]
        AUTH["auth-service :8086<br/>Gin · DB-backed sessions"]
        JOB["job-service :8081<br/>Gin · uploads, jobs, SSE"]
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

    subgraph Background
        CW["cleanup-worker :8088<br/>Ticker · 4-phase cleanup"]
    end

    subgraph Infrastructure
        PG[(PostgreSQL)]
        RD[(Redis)]
        NATS["NATS JetStream<br/>JOBS_DISPATCH · JOBS_EVENTS · JOBS_DLQ · ANALYTICS"]
    end

    WebApp -->|HTTPS| GW
    CLI -->|HTTPS| GW
    WebApp -.->|SPA assets| SPA

    GW -->|/auth/*| AUTH
    GW -->|/api/upload/*| JOB
    GW -->|/api/jobs/*| JOB
    GW -->|/api/{convert,organize,optimize}-pdf/*| JOB
    GW -->|/admin/*| AN
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
    CW --> PG
    CW --> RD
    GW --> RD
```

## Data Flow Overview

```mermaid
flowchart LR
    subgraph Upload
        A[Client] -->|1. Init upload| B[job-service]
        A -->|2. PUT chunks| B
        A -->|3. Complete upload| B
        B -->|Store state| Redis[(Redis: upload:*)]
        B -->|Save chunks| Disk[(uploads/tmp/<uploadId>)]
    end

    subgraph Processing
        A -->|4. POST /api/<group>/:tool| B
        B -->|5. Save ProcessingJob<br/>UUIDv7 + idempotency| PG[(PostgreSQL)]
        B -->|6. Publish JobMessage| NATS["NATS JetStream<br/>JOBS_DISPATCH"]
        NATS -->|7. Pull-consumer| W["Worker Service"]
        W -->|8. processing/processing.go| W
        W -->|9. Update status, output_path| PG
        W -->|10. Publish progress / completed / failed| EV["jobs.events.<jobId>.*"]
        W -->|on max-retry exhaustion| DLQ["jobs.dlq.<service>"]
    end

    subgraph Retrieval
        A -->|11. SSE /api/jobs/:id/events| B
        EV --> B
        B -->|stream events| A
        A -->|12. GET /:tool/:id/download| B
        B -->|Read file| Disk
    end
```

## NATS JetStream Streams

```mermaid
graph LR
    subgraph JOBS_DISPATCH["JOBS_DISPATCH (WorkQueue · 24h)"]
        D1["jobs.dispatch.convert-from-pdf"]
        D2["jobs.dispatch.convert-to-pdf"]
        D3["jobs.dispatch.organize-pdf"]
        D4["jobs.dispatch.optimize-pdf"]
    end

    subgraph JOBS_EVENTS["JOBS_EVENTS (Interest · 1h)"]
        E1["jobs.events.&lt;jobId&gt;.progress"]
        E2["jobs.events.&lt;jobId&gt;.completed"]
        E3["jobs.events.&lt;jobId&gt;.failed"]
    end

    subgraph JOBS_DLQ["JOBS_DLQ (Limits · 7d)"]
        Q1["jobs.dlq.&lt;service&gt;"]
    end

    subgraph ANALYTICS_STREAM["ANALYTICS (Interest · 24h)"]
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
    Client -->|Request with auth cookie or Bearer token| GW[api-gateway]
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
