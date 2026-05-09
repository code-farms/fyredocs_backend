# API Gateway -- Architecture

Internal structure and component diagram of the `api-gateway` service (port 8080).

## Component Diagram

```mermaid
graph TB
    subgraph api-gateway[" api-gateway :8080 "]
        direction TB

        subgraph Middleware["Middleware Chain (outermost → innermost)"]
            TRACE["telemetry.HTTPTraceMiddleware"]
            METRICS["metrics.HTTPMetricsMiddleware"]
            REQID["logger.HTTPRequestID"]
            SEC["withSecurityHeaders<br/>(X-Content-Type-Options, X-Frame-Options, ...)"]
            CORS["withCORS<br/>(allowed origins · credentials · preflight)"]
            AUTHMW["authverify.HTTPAuthMiddleware<br/>+ ResolvePlan callback"]
            BODYLIMIT["withMaxBodySize 1 MB<br/>(non-upload routes only)"]
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
            PROXY_UPLOAD["/api/upload/* → JOB_SERVICE_URL<br/>(rewritten to /api/uploads/*)"]
            PROXY_CFP["/api/convert-from-pdf/* → JOB_SERVICE_URL"]
            PROXY_CTP["/api/convert-to-pdf/* → JOB_SERVICE_URL"]
            PROXY_ORG["/api/organize-pdf/* → JOB_SERVICE_URL"]
            PROXY_OPT["/api/optimize-pdf/* → JOB_SERVICE_URL"]
            PROXY_JOBS["/api/jobs/* → JOB_SERVICE_URL"]
            PROXY_ADMIN["/admin/* → ANALYTICS_SERVICE_URL"]
            SPA["/ catch-all → SPA static (when SPA_DIR set)<br/>index.html fallback for client-side routes"]
        end

        subgraph ProxyTransport["Shared http.Transport"]
            T1["ResponseHeaderTimeout=5m"]
            T2["IdleConnTimeout=90s"]
            T3["MaxIdleConnsPerHost=20"]
            T4["MaxIdleConns=100"]
            FI["FlushInterval=-1<br/>(stream downloads immediately)"]
        end

        HEALTH["/healthz (Redis ping)"]
        METRICEP["/metrics (Prometheus)"]
    end

    Client[Browser/CLI/SPA] --> TRACE --> METRICS --> REQID --> SEC --> CORS --> AUTHMW --> BODYLIMIT --> MUX

    AUTHMW --> VERIFIER
    AUTHMW --> DENYLIST
    AUTHMW --> GUESTSTORE
    AUTHMW --> PLANCACHE

    MUX --> PROXY_AUTH & PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS & PROXY_ADMIN
    MUX --> SPA
    MUX --> HEALTH
    MUX --> METRICEP

    PROXY_AUTH & PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS & PROXY_ADMIN --> ProxyTransport

    DENYLIST --> Redis[(Redis)]
    GUESTSTORE --> Redis
    PLANCACHE --> Redis
    HEALTH -->|Ping| Redis

    PROXY_AUTH --> AuthSvc["auth-service :8086"]
    PROXY_UPLOAD & PROXY_CFP & PROXY_CTP & PROXY_ORG & PROXY_OPT & PROXY_JOBS --> JobSvc["job-service :8081"]
    PROXY_ADMIN --> AnSvc["analytics-service :8087"]
```

## Middleware Execution Order

```mermaid
flowchart LR
    A[Incoming<br/>Request] --> B[telemetry.<br/>HTTPTraceMiddleware]
    B --> C[metrics.<br/>HTTPMetricsMiddleware]
    C --> D[logger.<br/>HTTPRequestID]
    D --> E[withSecurityHeaders]
    E --> F[withCORS<br/>(preflight short-circuit)]
    F --> G[authverify.<br/>HTTPAuthMiddleware<br/>+ ResolvePlan]
    G --> H[withMaxBodySize 1 MB<br/>(skipped on /api/upload/*)]
    H --> I[http.ServeMux<br/>(route match)]
    I --> J[httputil.<br/>ReverseProxy<br/>(FlushInterval=-1)]
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
```
