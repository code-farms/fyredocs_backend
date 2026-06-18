# API Gateway Service

## Overview

The API Gateway is the central entry point for all client requests to the Fyredocs backend. It handles request routing, CORS, rate limiting, and authentication middleware before forwarding requests to the appropriate backend services.

**Port**: 8080
**Type**: HTTP Reverse Proxy
**Framework**: Go net/http

## Architecture

The API Gateway acts as a reverse proxy that:
- Routes incoming HTTP requests to backend services
- Enforces CORS policies for browser clients
- Validates JWT tokens from cookies or Authorization headers
- Issues + validates guest cookies for unauthenticated users
- Resolves plan info from Redis on every request and forwards it as headers
- Adds security response headers (X-Content-Type-Options, X-Frame-Options, X-XSS-Protection, Referrer-Policy, Permissions-Policy) via `withSecurityHeaders()` middleware
- Enforces request body size limit (1 MiB on **all** service routes — `/api/upload/*` is JSON-only now that file bytes flow through the presigned MinIO proxy) via `withMaxBodySize()` middleware
- Relays presigned object-storage traffic (`/fyredocs-uploads/*`, `/fyredocs-outputs/*`) verbatim to MinIO — no auth, no body limit, original `Host` preserved for SigV4
- Optionally serves the SPA bundle from `SPA_DIR` so the frontend ships with first-party cookies (no cross-origin)
- Performs graceful shutdown with 30-second drain on SIGTERM/SIGINT

### Request Flow

```
Client Request
    ↓
[API Gateway :8080]
    ↓
Telemetry → Metrics → Request-ID → SecurityHeaders
    ↓
    ├─ /fyredocs-uploads/*   ─┐  Presigned MinIO proxy (MINIO_URL)
    ├─ /fyredocs-outputs/*   ─┘  no auth · no body limit · path verbatim · Host preserved (SigV4)
    │
    └─ everything else → CORS → Auth middleware
            ├─ Auth: Bearer header > access_token cookie > guest_token cookie (issues new if absent)
            └─ ResolvePlan: read user:plan:<userId> from Redis (defaults to free)
            ↓
        Body-size limit (1 MiB on all service routes — uploads are JSON-only)
            ↓
        Reverse-proxy to backend (FlushInterval=-1 for streaming)
            ├─ /auth/*                                      → AUTH_SERVICE_URL  (default: JOB_SERVICE_URL fallback)
            ├─ /api/upload/*                                → JOB_SERVICE_URL   (rewritten to /api/uploads/*)
            ├─ /api/jobs/*                                  → JOB_SERVICE_URL
            ├─ /api/{convert-from,convert-to,organize,optimize}-pdf/* → JOB_SERVICE_URL
            ├─ /admin/*                                     → ANALYTICS_SERVICE_URL
            ├─ /api/dashboard                               → ANALYTICS_SERVICE_URL  (role-aware, any authenticated user)
            └─ /                                            → SPA_DIR static files (when set)
```

## Routing Configuration

### Service Routing Map

All `/api/*` traffic is proxied to **job-service**. Workers are not exposed publicly — `job-service` publishes jobs to NATS and the workers consume from there.

| Path Prefix | Target Service (env override) | Purpose |
|-------------|------------------------------|---------|
| `/auth/*` | `AUTH_SERVICE_URL` (default = `JOB_SERVICE_URL` fallback for backward compat) | Auth, plan management, sessions |
| `/fyredocs-uploads/*` | `MINIO_URL` (path verbatim, Host preserved, no auth/body limit) | Presigned upload PUTs / multipart parts |
| `/fyredocs-outputs/*` | `MINIO_URL` (path verbatim, Host preserved, no auth/body limit) | Presigned output downloads |
| `/api/upload/*` | `JOB_SERVICE_URL` (rewritten to `/api/uploads/*`) | Upload orchestration (JSON init/complete — file bytes go via the MinIO proxy) |
| `/api/jobs/*` | `JOB_SERVICE_URL` | History + SSE event stream |
| `/api/convert-from-pdf/*` | `JOB_SERVICE_URL` | PDF → Word/Excel/PPTX/Image/HTML/Text/ODF |
| `/api/convert-to-pdf/*` | `JOB_SERVICE_URL` | Office/Image → PDF |
| `/api/organize-pdf/*` | `JOB_SERVICE_URL` | Merge, split, rotate, watermark, sign, etc. |
| `/api/optimize-pdf/*` | `JOB_SERVICE_URL` | Compress, repair, OCR |
| `/admin/*` | `ANALYTICS_SERVICE_URL` | Admin / analytics dashboards (super-admin only, enforced by the service) |
| `/api/dashboard` | `ANALYTICS_SERVICE_URL` | Unified role-aware dashboard summary; the service filters by `X-User-Role` (admin/super-admin → system KPIs, user → personal KPIs, guest → 403) |
| `/healthz` | api-gateway (local) | Liveness — pings Redis |
| `/metrics` | api-gateway (local) | Prometheus metrics |
| `/` (catch-all) | static SPA from `SPA_DIR` (when set) | Frontend bundle, with `/index.html` fallback for client-side routing |

**Job dispatch is NATS, not Redis.** The gateway is HTTP-only — it never touches the JOBS_DISPATCH stream directly. `job-service` is the only publisher.

### Proxy Transport Configuration

The reverse proxy uses a custom `http.Transport` tuned for long-running conversions:

| Setting | Value | Rationale |
|---------|-------|-----------|
| `ResponseHeaderTimeout` | 5 minutes | Allows long PDF conversions to complete |
| `IdleConnTimeout` | 90 seconds | Reclaim idle backend connections |
| `MaxIdleConnsPerHost` | 20 | Connection pool per backend service |
| `MaxIdleConns` | 100 | Global idle connection pool limit |

The MinIO bucket proxy uses its own `minioTransport` with `MaxIdleConnsPerHost: 50` because browsers send multipart upload parts in parallel to a single upstream host.

### Presigned Object-Storage Proxy

`/fyredocs-uploads/*` and `/fyredocs-outputs/*` (prefixes configurable via `S3_BUCKET_UPLOADS`/`S3_BUCKET_OUTPUTS`) are relayed to `MINIO_URL` with semantics that differ from the service routes:

- **Path forwarded verbatim** — no prefix stripping; the SigV4 signature covers the canonical `/{bucket}/{key}` path.
- **Original `Host` header preserved** — presigned URLs are signed by job-service against `S3_PUBLIC_ENDPOINT` (this gateway's public origin), and SigV4 includes `Host` in the signed canonical request. Service routes rewrite `req.Host` to the upstream; the MinIO proxy must NOT, or MinIO's signature check fails.
- **No auth middleware** — the presigned signature (with its expiry) is the credential. Client-supplied identity headers (`X-User-ID` etc.) are stripped before forwarding.
- **No body-size limit** — file bytes flow through these routes.
- **`FlushInterval = -1`** — streams immediately in both directions.

See [Object Storage](../architecture/object-storage.md) for the full presigned flow and the multi-host variant (publishing port 9000 and flipping `S3_PUBLIC_ENDPOINT`).

### Response Streaming

The reverse proxy sets `FlushInterval = -1` to stream responses immediately to the client without buffering. This is critical for file download performance — without it, the proxy buffers the entire upstream response before forwarding, which can add significant latency for multi-megabyte files.

## Environment Variables

### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `JWT_HS256_SECRET` | **REQUIRED** - JWT signing secret (min 32 chars) | `your-64-char-hex-secret` |
| `PORT` | Gateway listening port | `8080` |
| `JOB_SERVICE_URL` | Job-service base URL (handles uploads, jobs, tool routes) | `http://job-service:8081` |
| `AUTH_SERVICE_URL` | Auth-service base URL (defaults to `JOB_SERVICE_URL` for backward compat) | `http://auth-service:8086` |
| `ANALYTICS_SERVICE_URL` | Analytics-service base URL | `http://analytics-service:8087` |
| `REDIS_ADDR` | Redis server address (used for denylist, guest cookie store, plan cache) | `redis:6379` |

#### Object Storage Proxy
| Variable | Description | Default |
|----------|-------------|---------|
| `MINIO_URL` | Upstream for the presigned bucket proxy routes | `http://minio:9000` |
| `S3_BUCKET_UPLOADS` | Uploads bucket name (= proxied path prefix) | `fyredocs-uploads` |
| `S3_BUCKET_OUTPUTS` | Outputs bucket name (= proxied path prefix) | `fyredocs-outputs` |

### Optional (with defaults)

#### JWT Configuration
| Variable | Description | Default |
|----------|-------------|---------|
| `JWT_ALLOWED_ALGS` | Allowed JWT algorithms | `HS256` |
| `JWT_ISSUER` | Expected token issuer | `fyredocs` |
| `JWT_AUDIENCE` | Expected token audience | `fyredocs-api` |
| `JWT_CLOCK_SKEW` | Allowed clock skew for token validation | `60s` |

#### Authentication
| Variable | Description | Default |
|----------|-------------|---------|
| `AUTH_GUEST_PREFIX` | Guest token Redis key prefix | `guest` |
| `AUTH_GUEST_SUFFIX` | Guest token Redis key suffix | `jobs` |
| `AUTH_DENYLIST_ENABLED` | Enable token denylist (logout) | `true` |
| `AUTH_DENYLIST_PREFIX` | Denylist Redis key prefix | `denylist:jwt` |
| `SPA_DIR` | If set, serve static files from this directory at `/` (with index.html fallback). Disabled by default. | `""` |

#### Redis
| Variable | Description | Default |
|----------|-------------|---------|
| `REDIS_PASSWORD` | Redis password (if required) | `""` (none) |
| `REDIS_DB` | Redis database number | `0` |

#### CORS Configuration
| Variable | Description | Default |
|----------|-------------|---------|
| `CORS_ALLOW_ORIGINS` | Allowed origins (comma-separated) | `http://localhost:5173,http://localhost:3000` |
| `CORS_ALLOW_METHODS` | Allowed HTTP methods | `GET,POST,PUT,PATCH,DELETE,OPTIONS` |
| `CORS_ALLOW_HEADERS` | Allowed request headers | `Authorization,Content-Type,X-User-ID,X-Guest-Token` |
| `CORS_ALLOW_CREDENTIALS` | Allow credentials (cookies) | `true` |

## Authentication Middleware

The API Gateway validates authentication for all proxied requests using middleware that checks tokens in this priority order:

### 1. Authorization Header (Highest Priority)
```http
Authorization: Bearer eyJhbGc...
```
Used by API clients, mobile apps, and testing tools.

### 2. HTTP-Only Cookie
```http
Cookie: access_token=eyJhbGc...
```
Primary method for browser clients. Automatically sent by browsers.

### 3. Guest Cookie (Lowest Priority)
```http
Cookie: guest_token=<uuid>
```
For unauthenticated users accessing guest features. The gateway issues this cookie automatically on first contact when no auth header/cookie is present and validates it against `guest:<token>:jobs` in Redis (the suffix is configurable via `AUTH_GUEST_SUFFIX`). Same-origin SPA hosting (via `SPA_DIR`) keeps this cookie HttpOnly + Secure.

### Token Validation

The middleware:
1. Extracts the token from the request
2. Validates JWT signature using `JWT_HS256_SECRET`
3. Checks token expiration
4. Verifies issuer and audience (if configured)
5. Checks token denylist (if logout was called)
6. Sets `X-User-ID`, `X-User-Plan`, `X-User-Plan-Max-File-MB`, and `X-User-Plan-Max-Files` headers for downstream services

### Plan Headers Forwarded to Downstream Services

For authenticated requests the gateway reads the user's plan info from **Redis** (key `user:plan:{userID}`, written by auth-service) and forwards:

| Header | Source | Example |
|--------|--------|---------|
| `X-User-Plan` | Redis cache `plan` | `free` |
| `X-User-Plan-Max-File-MB` | Redis cache `max_file_mb` | `25` |
| `X-User-Plan-Max-Files` | Redis cache `max_files` | `10` |

If the Redis key is missing (e.g., cache expired), defaults to the free plan (25 MB, 10 files).

For anonymous requests (no valid token, no guest token), the gateway forwards anonymous-plan defaults:

| Header | Default Value |
|--------|---------------|
| `X-User-Plan` | `anonymous` |
| `X-User-Plan-Max-File-MB` | `10` |
| `X-User-Plan-Max-Files` | `5` |

These headers are cleared from incoming client requests before proxying (`ClearUserHeaders`) to prevent spoofing.

### Bypass Paths

The following paths skip authentication (`PublicPaths` in [main.go:179](../../../api-gateway/main.go#L179)):
- `/healthz` - Health check endpoint
- `/auth/login` - User login
- `/auth/signup` - User registration
- `/auth/refresh` - Refresh-token rotation (the cookie itself is the credential)
- `/auth/plans` - Public plan listing
- OPTIONS requests (CORS preflight)

## CORS Configuration

The gateway enforces CORS policies to allow browser-based frontends to make requests.

### Production Configuration

For production, set specific origins:

```yaml
environment:
  CORS_ALLOW_ORIGINS: "https://yourdomain.com"
  CORS_ALLOW_CREDENTIALS: "true"
```

### Development Configuration

For local development with multiple frontend ports:

```yaml
environment:
  CORS_ALLOW_ORIGINS: "http://localhost:3000,http://localhost:5173"
  CORS_ALLOW_CREDENTIALS: "true"
```

### Important Notes

- **Credentials Required**: `CORS_ALLOW_CREDENTIALS` must be `true` for cookie-based authentication
- **No Wildcards**: When credentials are enabled, origins cannot be `*`
- **Exact Match**: Origins must exactly match (including protocol and port)
- **Startup Warning**: A startup warning is logged if `CORS_ALLOW_ORIGINS=*` is used with `CORS_ALLOW_CREDENTIALS=true`, as this effectively disables CORS protection.

## Deployment

### Docker Compose

The API Gateway is configured in [docker-compose.yml](../docker-compose.yml):

```yaml
api-gateway:
  build:
    context: ./api-gateway
    dockerfile: Dockerfile
  ports:
    - "8080:8080"
  environment:
    PORT: "8080"
    JOB_SERVICE_URL: http://job-service:8081
    AUTH_SERVICE_URL: http://auth-service:8086
    ANALYTICS_SERVICE_URL: http://analytics-service:8087
    JWT_HS256_SECRET: ${JWT_HS256_SECRET}
    # SPA_DIR: /app/spa  # uncomment to serve the frontend bundle
    # ... other env vars
  depends_on:
    - redis
    - job-service
    - auth-service
```

### Local Development

1. Ensure dependencies are running:
   ```bash
   docker compose up -d redis job-service auth-service
   ```

2. Set environment variables:
   ```bash
   export JWT_HS256_SECRET=$(openssl rand -hex 32)
   export JOB_SERVICE_URL=http://localhost:8081
   export AUTH_SERVICE_URL=http://localhost:8086
   export ANALYTICS_SERVICE_URL=http://localhost:8087
   export REDIS_ADDR=localhost:6379
   ```

3. Run the gateway:
   ```bash
   cd api-gateway
   go run main.go
   ```

### Production Deployment

#### Security Checklist

- [ ] Generate a strong JWT secret (`openssl rand -hex 32`)
- [ ] Set specific CORS origins (no wildcards)
- [ ] Use HTTPS in production
- [ ] Update `AUTH_COOKIE_SECURE=true` (enforced by job-service)
- [ ] Configure proper DNS/load balancer
- [ ] Enable request logging and monitoring
- [ ] Set up rate limiting at load balancer level

## Health Check

The API Gateway exposes two health endpoints:

- `/healthz` = **liveness** (is the process alive?)
- `/readyz` = **readiness** (can it serve traffic? checks all dependencies)

### Liveness: `/healthz`

```http
GET /healthz
```

**Response**: `OK` (200)

### Readiness: `/readyz`

```http
GET /readyz
```

**Response** (200 when all checks pass, 503 when any check fails):
```json
{
  "status": "ready",
  "checks": {
    "redis": "ok"
  }
}
```

Use these endpoints for:
- Docker health checks (`/healthz` for liveness probe)
- Kubernetes / load balancer readiness probes (`/readyz`)
- Monitoring system checks

## Troubleshooting

### Common Issues

#### 1. 401 Unauthorized on Valid Requests

**Symptoms**: Requests with valid tokens return 401

**Possible Causes**:
- JWT secret mismatch between services
- Token expired (check expiry time)
- Token in denylist (user logged out)
- Clock skew between services

**Solutions**:
```bash
# Check if JWT secret is set
docker compose exec api-gateway env | grep JWT_HS256_SECRET

# Verify Redis connection
docker compose exec api-gateway redis-cli -h redis ping

# Check denylist
docker compose exec redis redis-cli keys "denylist:jwt:*"
```

#### 2. CORS Errors

**Symptoms**: Browser shows CORS error in console

**Possible Causes**:
- Origin not in `CORS_ALLOW_ORIGINS`
- Credentials enabled but origin is wildcard
- Missing `credentials: 'include'` in frontend

**Solutions**:
```bash
# Check CORS configuration
docker compose exec api-gateway env | grep CORS

# Update allowed origins
docker compose up -d api-gateway
```

**Frontend fix**:
```javascript
fetch('http://localhost:8080/api/jobs', {
  credentials: 'include'  // Required!
});
```

#### 3. Service Unavailable (502/503)

**Symptoms**: Gateway returns 502 Bad Gateway or 503 Service Unavailable

**Possible Causes**:
- Upload service not running
- Upload service not healthy
- Network issues between containers

**Solutions**:
```bash
# Check service status
docker compose ps

# Check job-service logs
docker compose logs job-service

# Restart services
docker compose restart api-gateway job-service
```

#### 4. Cookie Not Being Sent

**Symptoms**: Authenticated requests fail, cookie exists in browser

**Possible Causes**:
- CORS credentials not enabled
- Cookie domain mismatch
- SameSite restriction

**Solutions**:
```bash
# Verify CORS credentials
docker compose exec api-gateway env | grep CORS_ALLOW_CREDENTIALS

# Check cookie settings in job-service
docker compose exec job-service env | grep AUTH_COOKIE
```

### Debug Logging

To enable debug logging, add to environment:

```yaml
environment:
  LOG_LEVEL: "debug"
```

Then check logs:
```bash
docker compose logs -f api-gateway
```

## Monitoring

### Key Metrics to Monitor

- **Request Rate**: Requests per second through gateway
- **Response Times**: P50, P95, P99 latencies
- **Error Rate**: 4xx and 5xx responses
- **Service Health**: Backend service availability
- **Redis Connection**: Connection pool metrics

### Recommended Tools

- **Prometheus**: Metrics collection
- **Grafana**: Visualization
- **ELK Stack**: Log aggregation
- **Datadog/New Relic**: APM

## Sequence Diagrams

### Authenticated Request Flow

```mermaid
sequenceDiagram
    participant C as Client (Browser)
    participant GW as API Gateway :8080
    participant R as Redis
    participant JS as Job Service :8081

    C->>GW: GET /api/convert-from-pdf/pdf-to-word (Cookie: access_token=<jwt>)

    GW->>GW: CORS middleware (check Origin, set headers)
    GW->>GW: Extract token (Authorization header > Cookie > Guest header)
    GW->>GW: Validate JWT signature (HS256)
    GW->>GW: Check expiration, issuer, audience

    GW->>R: GET denylist:jwt:<token>
    alt Token is denied
        GW-->>C: 401 Unauthorized
    else Token is valid
        GW->>GW: Set X-User-ID from JWT, resolve plan from Redis cache, set X-User-Plan headers
        GW->>JS: Proxy request with X-User-ID and plan headers
        JS-->>GW: 200 {jobs data}
        GW-->>C: 200 {jobs data}
    end
```

### Guest User Request Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway :8080
    participant R as Redis
    participant JS as Job Service :8081

    C->>GW: POST /api/convert-to-pdf/word-to-pdf (X-Guest-Token: <uuid>)

    GW->>GW: CORS middleware
    GW->>GW: No JWT token found
    GW->>R: Check guest token validity
    R-->>GW: Guest token exists

    GW->>GW: Set guest context in request
    GW->>JS: Proxy request with guest context headers
    JS-->>GW: 201 {job created}
    GW-->>C: 201 {job created}
```

### CORS Preflight Flow

```mermaid
sequenceDiagram
    participant C as Browser
    participant GW as API Gateway :8080

    C->>GW: OPTIONS /api/jobs (Origin: http://localhost:5173)
    GW->>GW: Check Origin against CORS_ALLOW_ORIGINS
    alt Origin allowed
        GW-->>C: 204 No Content + CORS headers
        Note over C,GW: Access-Control-Allow-Origin: http://localhost:5173<br/>Access-Control-Allow-Credentials: true<br/>Access-Control-Allow-Methods: GET,POST,...
    else Origin not allowed
        GW-->>C: 204 No Content (no CORS headers)
        Note over C: Browser blocks the actual request
    end

    C->>GW: GET /api/jobs (actual request)
    GW->>GW: Process normally with CORS headers
```

### Presigned Object Upload/Download Flow

```mermaid
sequenceDiagram
    participant B as Browser
    participant GW as API Gateway :8080
    participant JS as Job Service :8081
    participant M as MinIO :9000 (internal)

    B->>GW: POST /api/upload/init (JSON, 1 MiB limit)
    GW->>JS: proxy (auth + plan headers)
    JS->>M: CreateMultipart / presign part URLs (signed for gateway origin)
    JS-->>B: {uploadId, presigned part URLs https://<origin>/fyredocs-uploads/...}

    par parts in parallel
        B->>GW: PUT /fyredocs-uploads/uploads/<id>/<file>?partNumber=N&X-Amz-...
        Note over GW: bucket proxy — no auth, no body limit,<br/>path verbatim, Host preserved (SigV4)
        GW->>M: relay bytes (FlushInterval=-1)
        M-->>B: 200 + ETag
    end

    B->>GW: POST /api/upload/complete (JSON: part ETags)
    GW->>JS: proxy
    JS->>M: CompleteMultipart

    Note over B,M: download is the same shape:
    B->>GW: GET /fyredocs-outputs/jobs/<jobId>/<file>?X-Amz-...
    GW->>M: relay
    M-->>B: object bytes (streamed)
```

### Health Check Flow

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant GW as API Gateway :8080
    participant R as Redis

    LB->>GW: GET /healthz
    GW->>R: PING (with 2s timeout)
    alt Redis healthy
        R-->>GW: PONG
        GW-->>LB: 200 {"status": "healthy"}
    else Redis unreachable
        GW-->>LB: 503 {"status": "unhealthy", "redis": "error message"}
    end
```

## Error Flows

### Gateway Error Responses

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `401 Unauthorized` | 401 | Invalid, expired, or revoked JWT token |
| `401 Unauthorized` | 401 | Missing authentication on protected route |
| `502 Bad Gateway` | 502 | Backend service unreachable |
| `503 Service Unavailable` | 503 | Health check failed (Redis down) |
| `204 No Content` | 204 | CORS preflight response |

### Authentication Bypass Paths

The following paths skip JWT authentication:
- `/healthz` -- Health check endpoint
- `/readyz` -- Readiness check endpoint
- `/auth/signup` -- User registration
- `/auth/login` -- User login
- `/auth/plans` -- Public plan listing
- `OPTIONS` requests -- CORS preflight

### Backend Service Failure Handling

When a backend service is unreachable:
1. The reverse proxy returns 502 Bad Gateway
2. No retry is attempted at the gateway level
3. The client should retry with exponential backoff

## Related Documentation

- [Auth Service](./AUTH_SERVICE.md) - Detailed authentication documentation
- [Job Service](./JOB_SERVICE.md) - Backend service documentation
- [Object Storage](../architecture/object-storage.md) - MinIO topology and presigned flow
- [Main README](../README.md) - Overall architecture and deployment

## Support

For issues or questions:
- Check service logs: `docker compose logs -f api-gateway`
- Review environment variables: `docker compose exec api-gateway env`
- Verify service connectivity: `docker compose exec api-gateway ping job-service`
