# API Gateway -- Architecture

Internal structure and component diagram of the `api-gateway` service (port 8080).

## Component Diagram

```mermaid
graph TB
    subgraph api-gateway[" api-gateway :8080 "]
        direction TB

        subgraph Middleware["Outer Middleware (all routes)"]
            TRACE["telemetry.HTTPTraceMiddleware"]
            METRICS["metrics.HTTPMetricsMiddleware"]
            REQID["logger.HTTPRequestID"]
            SEC["withSecurityHeaders<br/>(X-Content-Type-Options, X-Frame-Options, ...)"]
        end

        ROOT["root http.ServeMux"]

        subgraph MinioProxy["Presigned object-storage proxy (no CORS/auth/body limit)"]
            PROXY_UP["/fyredocs-uploads/* → MINIO_URL"]
            PROXY_OUT["/fyredocs-outputs/* → MINIO_URL"]
            MPROPS["path verbatim · original Host preserved (SigV4)<br/>identity headers stripped · FlushInterval=-1"]
        end

        subgraph InnerChain["Service-route chain"]
            CORS["withCORS<br/>(allowed origins · credentials · preflight)"]
            AUTHMW["authverify.HTTPAuthMiddleware<br/>+ ResolvePlan callback"]
            RATELIMIT["ratelimit.Middleware<br/>(per-plan sliding window on /api/* · Redis · 429)"]
            BODYLIMIT["withMaxBodySize 1 MiB<br/>(ALL service routes — /api/upload is JSON-only)"]
        end

        subgraph Auth["Auth Verification"]
            VERIFIER["Verifier<br/>(HS256 · iss/aud/exp · clock skew)"]
            DENYLIST["TokenDenylist (Redis)"]
            GUESTSTORE["GuestStore (Redis)<br/>guest:&lt;token&gt;:jobs"]
            PLANCACHE["Plan cache (Redis)<br/>user:plan:&lt;userId&gt;"]
        end

        subgraph Routing["Reverse-proxy routes"]
            MUX["http.ServeMux"]
            PROXY_AUTH["/auth/* → AUTH_SERVICE_URL<br/>(default: JOB_SERVICE_URL fallback)"]
            PROXY_UPLOAD["/api/upload/* → JOB_SERVICE_URL<br/>(rewritten to /api/uploads/*; JSON init/complete only)"]
            PROXY_CFP["/api/convert-from-pdf/* → JOB_SERVICE_URL"]
            PROXY_CTP["/api/convert-to-pdf/* → JOB_SERVICE_URL"]
            PROXY_ORG["/api/organize-pdf/* → JOB_SERVICE_URL"]
            PROXY_OPT["/api/optimize-pdf/* → JOB_SERVICE_URL"]
            PROXY_JOBS["/api/jobs/* → JOB_SERVICE_URL"]
            PROXY_DOCS["/api/documents|folders|tags|exports/* → DOCUMENT_SERVICE_URL"]
            PROXY_USER["/api/orgs/* → USER_SERVICE_URL"]
            PROXY_NOTIF["/api/notifications/* → NOTIFICATION_SERVICE_URL"]
            PROXY_ADMIN["/admin/* → ANALYTICS_SERVICE_URL"]
            PROXY_DASH["/api/dashboard → ANALYTICS_SERVICE_URL<br/>(role-aware)"]
            SPA["/ catch-all → SPA static (when SPA_DIR set)<br/>index.html fallback for client-side routes"]
        end

        subgraph ProxyTransport["http.Transport (service routes)"]
            T1["ResponseHeaderTimeout=5m"]
            T2["IdleConnTimeout=90s"]
            T3["MaxIdleConnsPerHost=20"]
            T4["MaxIdleConns=100"]
            FI["FlushInterval=-1<br/>(stream downloads immediately)"]
        end

        subgraph MinioTransport["minioTransport (bucket proxy)"]
            M1["MaxIdleConnsPerHost=50<br/>(multipart parts arrive in parallel)"]
        end

        HEALTH["/healthz (Redis ping)"]
        METRICEP["/metrics (Prometheus)"]
    end

    Client[Browser/CLI/SPA] --> TRACE --> METRICS --> REQID --> SEC --> ROOT

    ROOT -->|/fyredocs-uploads/* · /fyredocs-outputs/*| PROXY_UP & PROXY_OUT
    ROOT -->|everything else| CORS --> AUTHMW --> RATELIMIT --> BODYLIMIT --> MUX

    AUTHMW --> VERIFIER
    AUTHMW --> DENYLIST
    AUTHMW --> GUESTSTORE
    AUTHMW --> PLANCACHE

    MUX --> PROXY_AUTH & PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS & PROXY_DOCS & PROXY_USER & PROXY_NOTIF & PROXY_ADMIN & PROXY_DASH
    MUX --> SPA
    MUX --> HEALTH
    MUX --> METRICEP

    PROXY_AUTH & PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS & PROXY_DOCS & PROXY_USER & PROXY_NOTIF & PROXY_ADMIN & PROXY_DASH --> ProxyTransport
    PROXY_UP & PROXY_OUT --> MinioTransport

    DENYLIST --> Redis[(Redis)]
    GUESTSTORE --> Redis
    PLANCACHE --> Redis
    HEALTH -->|Ping| Redis

    PROXY_AUTH --> AuthSvc["auth-service :8086"]
    PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS --> JobSvc["job-service :8081"]
    PROXY_DOCS --> DocSvc["document-service :8089"]
    PROXY_USER --> UserSvc["user-service :8090"]
    PROXY_NOTIF --> NotifSvc["notification-service :8091"]
    PROXY_ADMIN & PROXY_DASH --> AnSvc["analytics-service :8087"]
    PROXY_UP & PROXY_OUT --> Minio[("MinIO :9000<br/>fyredocs-uploads · fyredocs-outputs")]
```

## Middleware Execution Order

```mermaid
flowchart LR
    A[Incoming<br/>Request] --> B[telemetry.<br/>HTTPTraceMiddleware]
    B --> C[metrics.<br/>HTTPMetricsMiddleware]
    C --> D[logger.<br/>HTTPRequestID]
    D --> E[withSecurityHeaders]
    E --> R{root mux}
    R -->|"/fyredocs-uploads/*<br/>/fyredocs-outputs/*"| M[MinIO bucket proxy<br/>no CORS · no auth · no body limit<br/>Host preserved · FlushInterval=-1]
    M --> S3[MinIO]
    R -->|other| F[withCORS<br/>preflight short-circuit]
    F --> G[authverify.<br/>HTTPAuthMiddleware<br/>+ ResolvePlan]
    G --> RL[ratelimit.Middleware<br/>per-plan /api/* · 429]
    RL --> H[withMaxBodySize 1 MiB<br/>all service routes]
    H --> I[http.ServeMux<br/>route match]
    I --> J[httputil.<br/>ReverseProxy<br/>FlushInterval=-1]
    J --> K[Backend Service]
```

## Plan Header Injection

```mermaid
flowchart TD
    A[Verified userId] --> B[plancache.GetPlanInfo Redis user:plan:&lt;userId&gt;]
    B --> C{found?}
    C -->|yes| D[Inject X-User-ID, X-Role, X-User-Plan, X-Plan-Max-File-MB, X-Plan-Max-Files]
    C -->|no| E[Default to free plan 25 MB · 10 files]
    E --> D
    D --> F[ClearUserHeaders on incoming req<br/>then ApplyUserHeaders]
    F --> G[Forward to backend]
```

## Dependency Graph

```mermaid
graph LR
    GW[api-gateway] --> |shared/config| Config
    GW --> |shared/logger| Logger
    GW --> |shared/metrics| Metrics
    GW --> |shared/telemetry| Telemetry
    GW --> |internal/authverify| AuthVerify
    GW --> |internal/plancache| PlanCache

    AuthVerify --> |go-redis/v9| Redis[(Redis)]
    AuthVerify --> |golang-jwt/jwt/v5| JWT
    PlanCache --> |go-redis/v9| Redis

    GW --> |net/http/httputil| ReverseProxy
    GW --> |net/http (FileServer)| StaticFS
    ReverseProxy --> |MINIO_URL| Minio[(MinIO)]
```
