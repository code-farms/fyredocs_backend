# Fyredocs Backend

A microservices-based document conversion platform built with Go, featuring PDF conversion, Office document processing, and secure user authentication.

## Quick Start

```bash
./deploy.sh
```

The script generates a JWT secret, starts all services with Docker Compose, and displays endpoints once healthy.

| Service | URL |
|---------|-----|
| Caddy edge (SPA + API) | http://localhost |
| Job Service | http://localhost:8081 |

## Services

| Service | Port | Description |
|---------|------|-------------|
| **Caddy (edge)** | 80/443 | Public edge: TLS termination (automatic HTTPS when `PUBLIC_DOMAIN` is set), SPA static hosting, presigned object-byte routing to MinIO (`/fyredocs-uploads/*`, `/fyredocs-outputs/*`), proxies `/api/*`, `/auth/*`, `/admin/*`, `/healthz` to the API Gateway |
| **API Gateway** | 8080 (internal) | Reverse proxy behind the Caddy edge (not published), CORS, JWT/guest verification, plan resolution |
| **Auth Service** | 8086 | Signup, login, refresh-token rotation with DB-backed sessions, plan management |
| **Job Service** | 8081 | Presigned (multipart) uploads to MinIO, job creation, NATS publish, SSE streaming, history |
| **Convert From PDF** | 8082 | PDF → DOCX (pdf2docx + LibreOffice fallback) / XLSX / PPTX (image-based) / Image / HTML / Text / ODF |
| **Convert To PDF** | 8083 | Word / Excel / PowerPoint / HTML / Image → PDF (LibreOffice via unoserver); ships with 2 replicas |
| **Organize PDF** | 8084 | Merge, split, rotate, extract, remove, watermark, protect, unlock, sign, edit, page numbers (pdfcpu) |
| **Optimize PDF** | 8085 | Compress, repair, OCR (Ghostscript / Tesseract) |
| **Analytics Service** | 8087 | Business / engagement / reliability metrics, NATS subscriber |
| **Cleanup Worker** | 8088 | Background TTL cleanup of jobs, upload sessions, and their MinIO objects; aborts stale multipart uploads (health/metrics endpoints only). Owned by job-service (`job-service/cmd/cleanup`), deployed as its own container |
| **Document Service** | 8089 | Persistent document library — documents, folders, tags, exports; finalizes completed jobs into documents (NATS subscriber) |
| **User Service** | 8090 | Organizations, memberships, and the RBAC role model |
| **Notification Service** | 8091 | In-app notification feed; consumes job events, pushes a live SSE bell |
| **MinIO** | — (internal) | Object storage for all file bytes (`fyredocs-uploads`, `fyredocs-outputs`); bootstrapped by the one-shot `minio-init` container (buckets, lifecycle rules, scoped app user) |

## Request Flow

```
Client → Caddy edge (:80/:443 — TLS, gzip)
            │
            ├─ /fyredocs-uploads/*   → MinIO (presigned PUT/multipart parts — no auth, Host preserved for SigV4)
            ├─ /fyredocs-outputs/*   → MinIO (presigned GET downloads)
            ├─ /  (everything else)  → SPA static files (frontend dist volume)
            │
            └─ /api/* · /auth/* · /admin/* · /healthz
                        ↓
         API Gateway (:8080, internal-only — not published)
            │  • CORS, security headers, body-size limit (1MB on all service routes)
            │  • JWT/guest token verification
            │  • Plan info resolution from Redis cache
            │
            ├─ /auth/*               → Auth Service (:8086)
            ├─ /api/upload/*         → Job Service (:8081, rewritten to /api/uploads/*; JSON init/complete only)
            ├─ /api/jobs/*           → Job Service (:8081)
            ├─ /api/{convert-from,convert-to,organize,optimize}-pdf/* → Job Service (:8081)
            ├─ /api/{documents,folders,tags,exports}/* → Document Service (:8089)
            ├─ /api/orgs/*           → User Service (:8090)
            ├─ /api/notifications/*  → Notification Service (:8091)
            └─ /admin/* , /api/dashboard → Analytics Service (:8087)
                          │
                          ▼
                    NATS JetStream  (jobs.dispatch.<service-name> — payloads carry object keys, not bytes)
                          │
        ┌─────────────────┼──────────────────┬──────────────────┐
        ▼                 ▼                  ▼                  ▼
  convert-from-pdf  convert-to-pdf ×2   organize-pdf      optimize-pdf
        │                 │                  │                  │
        │   download input from MinIO → process in tmpfs → upload output to MinIO
        │                 │                  │                  │
        └────────┬────────┴────────┬─────────┴────────┬─────────┘
                 ▼                 ▼                  ▼
            PostgreSQL (status)   MinIO (jobs/<jobId>/output)   NATS jobs.events.<jobId>.* (progress / completed / failed)
                                                                       │
                                                                       ▼
                                                              SSE stream → client
                                                              (download via presigned URL through the Caddy edge)
```

## Technology Stack

- **Language**: Go 1.25
- **Web Framework**: Gin (services) + net/http (api-gateway)
- **Database**: PostgreSQL 18 (per-service schema, UUIDv7 IDs, pooled DSN)
- **Cache / Sessions**: Redis 7 (token denylist, upload state, rate limiting, plan cache, cleanup lock)
- **Object Storage**: MinIO (S3-compatible) — buckets `fyredocs-uploads` / `fyredocs-outputs`, presigned URLs routed same-origin through the Caddy edge
- **Edge**: Caddy — TLS termination (automatic HTTPS via `PUBLIC_DOMAIN`), SPA static hosting, object-byte routing, API proxy
- **Message Bus**: NATS JetStream — streams: `JOBS_DISPATCH`, `JOBS_EVENTS`, `JOBS_DLQ`, `ANALYTICS`
- **Document Processing**: LibreOffice + unoserver, pdf2docx, pdfcpu, Poppler, Ghostscript, Tesseract OCR
- **Auth**: JWT (HS256) with HTTP-only cookies, refresh-token rotation, DB-backed sessions
- **Containerization**: Docker Compose (per-service compose file under `deployment/`)

## Documentation

All detailed documentation lives under [`docs/`](docs/):

- **Service docs**: [`docs/developer/services/`](docs/developer/services/) — architecture, endpoints, and configuration for each service
- **Architecture**: [`docs/developer/architecture/`](docs/developer/architecture/) — Redis layout, error logging, base image setup
- **API spec**: [`docs/developer/swagger/openapi.yaml`](docs/developer/swagger/openapi.yaml) — OpenAPI specification
- **Diagrams**: [`docs/developer/mermaid/`](docs/developer/mermaid/) — system overview + per-service architecture & sequence diagrams
- **Postman**: [`Fyredocs_API.postman_collection.json`](Fyredocs_API.postman_collection.json) — importable API collection

## Development

Environment lives in a **single** root `.env` (gitignored; `deploy.sh` loads it —
there is no per-service `.env`). Copy `.env.example` → `.env` and fill it in.

### Run a single service (Docker)
Compose reads that one root `.env`; starting a service also starts its
dependencies. Use the Makefile helpers (no long command to type):

```bash
make up   SVC=auth-service        # start a service + its deps, env from root .env
make down SVC=auth-service        # stop just that service
make logs SVC=auth-service        # follow its logs
make ps                           # whole-stack status
make up                           # start the whole stack
```

Equivalent raw command: `docker compose -f deployment/docker-compose.yml --env-file .env up -d auth-service`.

### Run a service locally with `go run`
```bash
# Start infrastructure only (Postgres, Redis, NATS) — publishes localhost ports
docker compose -f deployment/docker-compose.essentials.yml up -d
```
`go run` doesn't read the root `.env` (its docker hostnames like `db:5432` don't
resolve from the host). Provide **localhost** values via inline env or a
service-local `.env` (godotenv loads a `.env` from the working directory), e.g.:

```bash
cd auth-service
DATABASE_URL='postgresql://fyredocs:fyredocs@localhost:5432/fyredocs?sslmode=disable' \
REDIS_ADDR=localhost:6379 NATS_URL=nats://localhost:4222 \
JWT_HS256_SECRET=change-me PORT=8086 go run main.go
```

See individual service docs in [`docs/developer/services/`](docs/developer/services/) for environment variables and configuration details.

## Testing

```bash
# Run all tests across every service
bash test.sh

# Run tests for a specific service
bash test.sh api-gateway

# Run multiple services
bash test.sh shared auth-service job-service

# Verbose output
bash test.sh -v
bash test.sh -v api-gateway

# Windows CMD
test.bat
test.bat -v api-gateway
```

Available services: `shared`, `api-gateway`, `auth-service`, `job-service`, `convert-to-pdf`, `convert-from-pdf`, `organize-pdf`, `optimize-pdf`, `analytics-service`, `document-service`, `user-service`, `notification-service` (the cleanup binary is tested as part of `job-service`)

## License

[Your License Here]
