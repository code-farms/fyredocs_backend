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
| Upload Service | http://localhost:8081 |

## Services

| Service | Port | Description |
|---------|------|-------------|
| **API Gateway** | 8080 | Request routing, CORS, auth middleware |
| **Upload Service** | 8081 | File uploads, job management, authentication |
| **Auth Service** | - | User registration, login, token management |
| **Job Service** | - | Job creation, status tracking, routing |
| **Convert From PDF** | 8082 | PDF to Word/Excel/PPT/Image/HTML/Text |
| **Convert To PDF** | 8083 | Word/Excel/PPT/HTML/Image to PDF |
| **Organize PDF** | 8084 | Merge, split, reorder, extract pages |
| **Optimize PDF** | 8085 | Compress, repair, OCR |
| **Cleanup Worker** | - | Background cleanup of expired files/jobs |

## Request Flow

```
Client -> API Gateway (:8080) -> Upload Service (:8081)
                                       |
                                 Redis Queue (queue:{tool-type})
                                       |
                          Worker Services (convert/organize/optimize)
                                       |
                          Output stored + status updated in PostgreSQL
```

## Technology Stack

- **Language**: Go 1.25
- **Web Framework**: Gin
- **Database**: PostgreSQL 15
- **Cache/Queue**: Redis 7 + NATS JetStream
- **Document Processing**: LibreOffice, pdfcpu, Poppler, Ghostscript, Tesseract OCR
- **Auth**: JWT (HS256) with HTTP-only cookies
- **Containerization**: Docker Compose

## Documentation

All detailed documentation lives under [`docs/`](docs/):

- **Service docs**: [`docs/services/`](docs/services/) — architecture, endpoints, and configuration for each service
- **Architecture**: [`docs/architecture/`](docs/architecture/) — system-level design and infrastructure
- **API spec**: [`docs/swagger/openapi.yaml`](docs/swagger/openapi.yaml) — OpenAPI specification
- **Diagrams**: [`docs/mermaid/`](docs/mermaid/) — architecture and sequence diagrams
- **Postman**: [`Fyredocs_API.postman_collection.json`](../Fyredocs_API.postman_collection.json) — importable API collection

## Development

```bash
# Start infrastructure only
docker compose up -d db redis

# Run a service locally
cd upload-service && go run main.go
```

See individual service docs in [`docs/services/`](docs/services/) for environment variables and configuration details.

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

Available services: `shared`, `api-gateway`, `auth-service`, `job-service`, `convert-to-pdf`, `convert-from-pdf`, `organize-pdf`, `optimize-pdf`, `cleanup-worker`

## License

[Your License Here]
