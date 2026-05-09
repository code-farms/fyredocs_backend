# Fyredocs Backend

A microservices-based document conversion platform built with Go, featuring PDF conversion, Office document processing, and secure user authentication.

## Quick Start

```bash
./deploy.sh
```

The script generates a JWT secret, starts all services with Docker Compose, and displays endpoints once healthy.

| Service | URL |
|---------|-----|
| API Gateway | http://localhost:8080 |
| Job Service | http://localhost:8081 |

## Services

| Service | Port | Description |
|---------|------|-------------|
| **API Gateway** | 8080 | Reverse proxy, CORS, JWT/guest verification, plan resolution, SPA hosting |
| **Auth Service** | 8086 | Signup, login, refresh-token rotation with DB-backed sessions, plan management |
| **Job Service** | 8081 | Chunked uploads, job creation, NATS publish, SSE streaming, history |
| **Convert From PDF** | 8082 | PDF → DOCX (pdf2docx + LibreOffice fallback) / XLSX / PPTX (image-based) / Image / HTML / Text / ODF |
| **Convert To PDF** | 8083 | Word / Excel / PowerPoint / HTML / Image → PDF (LibreOffice via unoserver) |
| **Organize PDF** | 8084 | Merge, split, rotate, extract, remove, watermark, protect, unlock, sign, edit, page numbers (pdfcpu) |
| **Optimize PDF** | 8085 | Compress, repair, OCR (Ghostscript / Tesseract) |
| **Analytics Service** | 8087 | Business / engagement / reliability metrics, NATS subscriber |
| **Cleanup Worker** | 8088 | Background TTL cleanup of jobs, uploads, orphaned dirs (health/metrics endpoints only) |

## Request Flow

```
Client → API Gateway (:8080)
            │  • CORS, security headers, body-size limit (1MB except uploads)
            │  • JWT/guest token verification
            │  • Plan info resolution from Redis cache
            │
            ├─ /auth/*               → Auth Service (:8086)
            ├─ /api/upload/*         → Job Service (:8081, rewritten to /api/uploads/*)
            ├─ /api/jobs/*           → Job Service (:8081)
            ├─ /api/{convert-from,convert-to,organize,optimize}-pdf/* → Job Service (:8081)
            ├─ /admin/*              → Analytics Service (:8087)
            └─ /                     → SPA static files (when SPA_DIR is set)
                          │
                          ▼
                    NATS JetStream  (jobs.dispatch.<service-name>)
                          │
        ┌─────────────────┼──────────────────┬──────────────────┐
        ▼                 ▼                  ▼                  ▼
  convert-from-pdf  convert-to-pdf     organize-pdf      optimize-pdf
        │                 │                  │                  │
        └────────┬────────┴────────┬─────────┴────────┬─────────┘
                 ▼                 ▼                  ▼
            PostgreSQL (status update)   Filesystem (output)   NATS jobs.events.<jobId>.* (progress / completed / failed)
                                                                       │
                                                                       ▼
                                                              SSE stream → client
```

## Technology Stack

- **Language**: Go 1.25
- **Web Framework**: Gin (services) + net/http (api-gateway)
- **Database**: PostgreSQL 15 (per-service schema, UUIDv7 IDs, pooled DSN)
- **Cache / Sessions**: Redis 7 (token denylist, upload state, rate limiting, plan cache, cleanup lock)
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

```bash
# Start infrastructure only (Postgres, Redis, NATS)
docker compose -f deployment/docker-compose.essentials.yml up -d

# Run a service locally
cd job-service && go run main.go
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

Available services: `shared`, `api-gateway`, `auth-service`, `job-service`, `convert-to-pdf`, `convert-from-pdf`, `organize-pdf`, `optimize-pdf`, `cleanup-worker`, `analytics-service`

## License

[Your License Here]
