# Upload Service -- Architecture

Internal structure and component diagram of the `upload-service` (port 8081).

Note: In the current architecture, `upload-service` and `job-service` share a nearly identical role. The `upload-service` is a standalone deployment option that includes auth endpoints alongside upload and job endpoints.

## Component Diagram

```mermaid
graph TB
    subgraph upload-service[" upload-service :8081 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry<br/>GinTraceMiddleware"]
            METRICS["Prometheus<br/>GinMetricsMiddleware"]
            REQID["Request ID<br/>GinRequestID"]
            LOGGER["Request Logger<br/>GinRequestLogger"]
            AUTHMW["Auth Middleware<br/>(JWT + Guest)"]
        end

        subgraph Routes["Route Groups"]
            subgraph UploadRoutes["/api/uploads"]
                INIT["POST /init"]
                CHUNK["PUT /:uploadId/chunk"]
                STATUS["GET /:uploadId/status"]
                COMPLETE["POST /:uploadId/complete"]
            end

            subgraph JobRoutes["/api/convert-from-pdf & /api/convert-to-pdf"]
                LIST["GET /:tool"]
                CREATE["POST /:tool"]
                GET["GET /:tool/:id"]
                DELETE["DELETE /:tool/:id"]
                DOWNLOAD["GET /:tool/:id/download"]
            end

            subgraph AuthRoutes["/auth"]
                SIGNUP["POST /signup"]
                LOGIN["POST /login"]
                REFRESH["POST /refresh"]
                ME["GET /me"]
                PROFILE["GET /profile"]
                LOGOUT["POST /logout"]
            end

            HISTORY["GET /api/jobs/history"]
        end

        subgraph Handlers
            UH["Upload Handlers"]
            JH["Job Handlers"]
            AH["Auth Endpoints"]
        end

        subgraph Internal
            ISSUER["Token Issuer<br/>(HS256 JWT)"]
            VERIFIER["Auth Verifier"]
            DENYLIST["Token Denylist"]
            GUESTSTORE["Guest Store"]
        end

        subgraph RateLimiting["Rate Limiting"]
            RL_UPLOAD["upload: 30 req/min"]
            RL_LOGIN["login: 5 req/min"]
            RL_SIGNUP["signup: 3 req/min"]
            RL_REFRESH["refresh: 10 req/min"]
        end
    end

    Client["Client"] --> TRACE

    UploadRoutes --> UH
    JobRoutes --> JH
    AuthRoutes --> AH

    UH --> Redis[(Redis)]
    UH --> Disk[(File System)]
    JH --> PG[(PostgreSQL)]
    JH --> Redis
    JH --> Queue["Redis Queue<br/>(job dispatch)"]
    AH --> PG
    AH --> ISSUER
    AH --> DENYLIST
    DENYLIST --> Redis
    GUESTSTORE --> Redis
    RateLimiting --> Redis
```

## Upload State Machine

```mermaid
stateDiagram-v2
    [*] --> Initialized: POST /init
    Initialized --> Uploading: PUT /chunk (first)
    Uploading --> Uploading: PUT /chunk (subsequent)
    Uploading --> Complete: POST /complete<br/>(all chunks received)
    Uploading --> Expired: TTL exceeded (30m)
    Complete --> Consumed: Job created from uploadId
    Initialized --> Expired: TTL exceeded (2h)
    Expired --> [*]: cleanup-worker removes
```

## Dependency Graph

```mermaid
graph LR
    US[upload-service] --> |shared/config| Config
    US --> |shared/logger| Logger
    US --> |shared/metrics| Metrics
    US --> |shared/telemetry| Telemetry
    US --> |shared/redisstore| RedisStore
    US --> |shared/response| Response
    US --> |shared/queue| Queue

    US --> |internal/authverify| AuthVerify
    US --> |internal/models| Models
    US --> |internal/token| TokenIssuer

    Models --> |gorm| PostgreSQL[(PostgreSQL)]
    RedisStore --> |go-redis/v9| Redis[(Redis)]
    Queue --> |go-redis/v9| Redis
    AuthVerify --> |golang-jwt/jwt/v5| JWT
```
