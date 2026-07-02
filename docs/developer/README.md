# Fyredocs Developer Documentation

Welcome to the Fyredocs developer docs. This guide helps new developers understand the system architecture, set up a local development environment, and navigate the codebase.

---

## Architecture Overview

Fyredocs uses a true microservices architecture. Each service is independently deployable with its own database, configuration, and API contract.

| Service | Port | Description |
|---------|------|-------------|
| Caddy edge | 80 / 443 | Public entrypoint — TLS termination, serves the SPA, routes object bytes (`/uploads /outputs`) to MinIO, proxies API to the gateway. The only host-exposed service. |
| API Gateway | 8080 (internal) | CORS, auth middleware, plan resolve, reverse proxy to services. Internal-only — not host-exposed; no longer serves the SPA or object bytes. |
| Job Service | 8081 (internal) | Job orchestration, presigned uploads, job lifecycle + in-process TTL cleanup sweep |
| Convert From PDF | 8082 | PDF → Word, Excel, PPT, Image, HTML, Text conversions |
| Convert To PDF | 8083 | Word, Excel, PPT, HTML, Image → PDF conversions |
| Organize PDF | 8084 | Merge, split, rotate, extract, watermark, sign, edit |
| Optimize PDF | 8085 | Compress, repair, OCR operations |
| Auth Service | 8086 | User registration, login, JWT token management |
| Analytics Service | 8087 | Usage metrics and analytics tracking |
| Document Service | 8089 | Persistent document library — documents, folders, tags, exports |
| User Service | 8090 | Organizations, memberships, and RBAC |
| Notification Service | 8091 | In-app notification feed + live SSE bell |
| MinIO | 9000 (internal) | S3-compatible object storage for all file bytes; console on `127.0.0.1:9001` |
| NATS | 4222 (internal) | JetStream work queues + job events |
| PostgreSQL / Redis | internal | Co-located Postgres; Redis (auth denylist, upload session state, rate limits, plan cache) |

All service ports except the Caddy edge (80/443) are internal-only — reachable on the `fyredocs_net` Docker network, not published to the host.

### Communication
- **Client → stack**: All traffic enters at the **Caddy edge**, which serves the SPA and proxies `/api /auth /admin` to the API Gateway. Object bytes go **browser ↔ MinIO directly** via presigned URLs (routed at the edge), bypassing the gateway.
- **Service → Service**: NATS JetStream for async job processing
- **Caching/State**: Redis for upload **session state**, rate limiting, guest tokens, plan cache
- **Persistence**: PostgreSQL (each service owns its own schema); file bytes in MinIO

---

## Where to Find What

| What you need | Where to look |
|---------------|---------------|
| API endpoint specs | [api/](api/) |
| Service internals & design | [services/](services/) |
| Architecture diagrams | [mermaid/](mermaid/) |
| OpenAPI spec | [swagger/openapi.yaml](swagger/openapi.yaml) |
| Redis data structures | [architecture/redis-architecture.md](architecture/redis-architecture.md) |
| Docker base image setup | [architecture/base-image-setup.md](architecture/base-image-setup.md) |
| Caddy edge (TLS, SPA, object-byte + API routing) | [architecture/caddy-edge.md](architecture/caddy-edge.md) |
| Compose files layout (common / essentials / per-service) | [architecture/compose-files.md](architecture/compose-files.md) |
| Database (perf, optimization, locality) | [architecture/database.md](architecture/database.md) |
| Security hardening | [architecture/backend-hardening.md](architecture/backend-hardening.md) |
| Load testing (k6) | [architecture/load-testing.md](architecture/load-testing.md) |
| Production readiness (audit + deployment review) | [architecture/production-readiness.md](architecture/production-readiness.md) |
| Backups & restore | [architecture/backup-and-restore.md](architecture/backup-and-restore.md) |
| Object storage (MinIO) | [architecture/object-storage.md](architecture/object-storage.md) |
| Error logging convention | [architecture/error-logging.md](architecture/error-logging.md) |
| Project rules & conventions | [../../CLAUDE.md](../../CLAUDE.md) |

---

## Local Development Setup

### Prerequisites
- Go 1.25+
- Docker & Docker Compose
- PostgreSQL 18
- Redis 7
- NATS Server with JetStream
- LibreOffice, Ghostscript, Poppler, Tesseract OCR (for document processing services)

### Quick Start
```bash
# 1. Clone the repo
git clone <repo-url> && cd fyredocs/fyredocs_backend

# 2. Copy environment config
cp .env.example .env  # Edit with your local settings

# 3. Start infrastructure (DB, Redis, NATS, MinIO)
docker compose -f deployment/docker-compose.essentials.yml --env-file .env up -d

# 4. Run a specific service on the host...
cd api-gateway && go run main.go

# ...or (re)deploy a single service as a container
docker compose -f deployment/docker-compose-api-gateway.yml --env-file .env up -d --build

# 5. Or start everything
./deployment/deploy.sh
```

### Running Tests
```bash
# Run tests for a specific service
cd job-service && go test ./...

# Run all tests
for svc in api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf analytics-service document-service user-service notification-service; do
  echo "Testing $svc..." && cd $svc && go test ./... && cd ..
done
```

---

## Request Flow

```
Browser → Caddy edge (:80/:443)
           ├─ TLS termination (automatic HTTPS when PUBLIC_DOMAIN is set)
           ├─ /                           → SPA static files (/srv/spa)
           ├─ /uploads/* , /outputs/*     → MinIO (:9000) directly (presigned object bytes)
           └─ /api/* /auth/* /admin/* /healthz → api-gateway (:8080, internal)
                       │
                       ├─ CORS, security headers
                       ├─ Auth check (JWT / Guest cookie) + plan resolution from Redis
                       ├─ Body size limit (1 MB on non-upload routes)
                       └─ Reverse proxy to target service
                           │
                           ├─ /auth/*                     → Auth Service (8086)
                           ├─ /api/upload/*               → Job Service (8081)  (rewritten to /api/uploads/*)
                           ├─ /api/jobs/history|:id/events → Job Service (8081)
                           ├─ /api/{convert-from-pdf,convert-to-pdf,organize-pdf,optimize-pdf}/:tool → Job Service (8081)
                           ├─ /api/{documents,folders,tags,exports}/* → Document Service (8089)
                           ├─ /api/orgs/*                 → User Service (8090)
                           ├─ /api/notifications/*        → Notification Service (8091)
                           └─ /admin/* , /api/dashboard   → Analytics Service (8087)

Job Service receives tool requests:
  1. Client presigns an upload via /api/uploads/* (session state in Redis) and
     PUTs the file's parts straight to MinIO through the edge — bytes never
     touch job-service or a shared disk
  2. Client POSTs /api/<group>/:tool with the uploadId(s) — job-service creates a
     ProcessingJob in PostgreSQL (UUIDv7), guarded by idempotency-key + a 10-minute
     uploadId-dedupe window
  3. Publishes a JobMessage to NATS subject `jobs.dispatch.<service-name>`
  4. Worker pulls from JOBS_DISPATCH (WorkQueue), runs processing, updates status,
     publishes `jobs.events.<jobId>.{progress,completed,failed}` events
  5. Client subscribes via SSE on /api/jobs/:id/events — job-service uses an
     ephemeral NATS consumer with FilterSubject scoped to the jobId
  6. Client downloads the result from /api/<group>/:tool/:id/download
  7. On max-retry exhaustion, the worker publishes to `jobs.dlq.<service>` (JOBS_DLQ stream)
```

---

## Service Documentation

Each service has a dedicated architecture document:

- [API Gateway](services/api-gateway.md)
- [Auth Service](services/auth-service.md)
- [Job Service](services/job-service.md)
- [Convert From PDF](services/convert-from-pdf.md)
- [Convert To PDF](services/convert-to-pdf.md)
- [Organize PDF](services/organize-pdf.md)
- [Optimize PDF](services/optimize-pdf.md)
- [Analytics Service](services/analytics-service.md)
- [Document Service](services/document-service.md)
- [User Service](services/user-service.md)
- [Notification Service](services/notification-service.md)
