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
| Cleanup Worker | — | Background worker for expired file/job cleanup |

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
| Database patterns | [DB_BEST_PRACTICES.md](DB_BEST_PRACTICES.md) |
| Security hardening | [backend-hardening.md](backend-hardening.md) |
| Deployment checklist | [deployment-review.md](deployment-review.md) |
| Project rules & conventions | [../../CLAUDE.md](../../CLAUDE.md) |

---

## Local Development Setup

### Prerequisites
- Go 1.25+
- Docker & Docker Compose
- PostgreSQL 15
- Redis 7
- NATS Server with JetStream
- LibreOffice, Ghostscript, Poppler, Tesseract OCR (for document processing services)

### Quick Start
```bash
# 1. Clone the repo
git clone <repo-url> && cd fyredocs/fyredocs_backend

# 2. Copy environment config
cp .env.example .env  # Edit with your local settings

# 3. Start infrastructure (DB, Redis, NATS)
docker compose -f deployment/docker-compose.essentials.yml up -d

# 4. Run a specific service
cd api-gateway && go run main.go

# 5. Or start everything
./deploy.sh
```

### Running Tests
```bash
# Run tests for a specific service
cd job-service && go test ./...

# Run all tests
for svc in api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf cleanup-worker analytics-service; do
  echo "Testing $svc..." && cd $svc && go test ./... && cd ..
done
```

---

## Request Flow

```
Browser → API Gateway (8080)
           ├─ Auth check (JWT / Guest Token)
           ├─ CORS validation
           └─ Reverse proxy to target service
               │
               ├─ /auth/*          → Auth Service (8086)
               ├─ /api/jobs/*      → Job Service (8081)
               ├─ /api/upload/*    → Job Service (8081)
               └─ /api/analytics/* → Analytics Service (8087)

Job Service receives tool requests:
  1. Accepts chunked file upload → assembles in Redis
  2. Creates ProcessingJob record in PostgreSQL
  3. Publishes event to NATS JetStream (tool-specific queue)
  4. Worker service consumes event → processes file
  5. Client polls for status or connects via SSE
  6. Client downloads result from Job Service
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
