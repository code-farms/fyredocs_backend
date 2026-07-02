# Fyredocs Developer Documentation

Welcome to the Fyredocs developer docs. This guide helps new developers understand the system architecture, set up a local development environment, and navigate the codebase.

---

## Architecture Overview

Fyredocs uses a true microservices architecture. Each service is independently deployable with its own database, configuration, and API contract.

| Service | Port | Description |
|---------|------|-------------|
| API Gateway | 8080 | Central entry point — CORS, auth middleware, reverse proxy |
| Job Service | 8081 | Job orchestration, file uploads, job lifecycle management |
| Convert From PDF | 8082 | PDF → Word, Excel, PPT, Image, HTML, Text conversions |
| Convert To PDF | 8083 | Word, Excel, PPT, HTML, Image → PDF conversions |
| Organize PDF | 8084 | Merge, split, rotate, extract, watermark, sign, edit |
| Optimize PDF | 8085 | Compress, repair, OCR operations |
| Auth Service | 8086 | User registration, login, JWT token management |
| Analytics Service | 8087 | Usage metrics and analytics tracking |
| Cleanup Worker | 8088 | Background worker for expired file/job cleanup (health/metrics only) |
| Document Service | 8089 | Persistent document library — documents, folders, tags, exports |
| User Service | 8090 | Organizations, memberships, and RBAC |
| Notification Service | 8091 | In-app notification feed + live SSE bell |

### Communication
- **Client → Services**: All traffic flows through the API Gateway via REST
- **Service → Service**: NATS JetStream for async job processing
- **Caching/State**: Redis for upload chunks, rate limiting, guest tokens
- **Persistence**: PostgreSQL (each service owns its own schema)

---

## Where to Find What

| What you need | Where to look |
|---------------|---------------|
| API endpoint specs | [api/](api/) |
| Service internals & design | [services/](services/) |
| Architecture diagrams | [mermaid/](mermaid/) |
| OpenAPI spec | [swagger/openapi.yaml](swagger/openapi.yaml) |
| Redis data structures | [architecture/REDIS_ARCHITECTURE.md](architecture/REDIS_ARCHITECTURE.md) |
| Docker base image setup | [architecture/BASE_IMAGE_SETUP.md](architecture/BASE_IMAGE_SETUP.md) |
| Compose files layout (common / essentials / per-service) | [architecture/COMPOSE_FILES.md](architecture/COMPOSE_FILES.md) |
| Database patterns | [DB_BEST_PRACTICES.md](DB_BEST_PRACTICES.md) |
| Security hardening | [backend-hardening.md](backend-hardening.md) |
| Deployment checklist | [deployment-review.md](deployment-review.md) |
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
for svc in api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf cleanup-worker analytics-service document-service user-service notification-service; do
  echo "Testing $svc..." && cd $svc && go test ./... && cd ..
done
```

---

## Request Flow

```
Browser → API Gateway (8080)
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
               ├─ /admin/* , /api/dashboard   → Analytics Service (8087)
               └─ /                           → SPA static files (when SPA_DIR is set)

Job Service receives tool requests:
  1. Client uploads chunks via /api/uploads/* (state in Redis, bytes on disk)
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

- [API Gateway](services/API_GATEWAY.md)
- [Auth Service](services/AUTH_SERVICE.md)
- [Job Service](services/JOB_SERVICE.md)
- [Convert From PDF](services/CONVERT_FROM_PDF.md)
- [Convert To PDF](services/CONVERT_TO_PDF.md)
- [Organize PDF](services/ORGANIZE_PDF.md)
- [Optimize PDF](services/OPTIMIZE_PDF.md)
- [Cleanup Worker](services/CLEANUP_WORKER.md)
- [Analytics Service](services/ANALYTICS_SERVICE.md)
- [Document Service](services/DOCUMENT_SERVICE.md)
- [User Service](services/USER_SERVICE.md)
- [Notification Service](services/NOTIFICATION_SERVICE.md)
