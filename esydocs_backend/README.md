# EsyDocs Backend

A microservices-based document conversion platform built with Go, featuring PDF conversion, Office document processing, and secure user authentication.

## Quick Start

Deploy everything with one command:

```bash
./deploy.sh
```

That's it! The script will:
- ✅ Generate a secure JWT secret (or reuse existing)
- ✅ Start all services with Docker Compose
- ✅ Wait for services to be healthy
- ✅ Display service endpoints

### Access Points

| Service | URL | Purpose |
|---------|-----|---------|
| API Gateway | http://localhost:8080 | Main API endpoint |
| Upload Service | http://localhost:8081 | Direct access (if needed) |
| PostgreSQL | localhost:5432 | Database |
| Redis | localhost:6379 | Cache & Queue |

## Architecture

### Services Overview

| Service | Port | Description | Documentation |
|---------|------|-------------|---------------|
| **API Gateway** | 8080 | Request routing, CORS, auth middleware | [API_GATEWAY.md](api-gateway/API_GATEWAY.md) |
| **Upload Service** | 8081 | File uploads, job management, authentication | [UPLOAD_SERVICE.md](upload-service/UPLOAD_SERVICE.md) |
| **Convert From PDF** | 8082 | PDF → Word/Excel/PPT/Image/HTML/Text conversions | [CONVERT_FROM_PDF.md](convert-from-pdf/CONVERT_FROM_PDF.md) |
| **Convert To PDF** | 8083 | Word/Excel/PPT/HTML/Image → PDF conversions | [CONVERT_TO_PDF.md](convert-to-pdf/CONVERT_TO_PDF.md) |
| **Organize PDF** | 8084 | Merge, split, reorder, extract pages | [ORGANIZE_PDF.md](organize-pdf/ORGANIZE_PDF.md) |
| **Optimize PDF** | 8085 | Compress, repair, OCR for PDFs | [OPTIMIZE_PDF.md](optimize-pdf/OPTIMIZE_PDF.md) |
| **Cleanup Worker** | - | Background cleanup of expired files/jobs | [CLEANUP_WORKER.md](cleanup-worker/CLEANUP_WORKER.md) |
| **PostgreSQL** | 5432 | Primary database | - |
| **Redis** | 6379 | Cache, queue, session storage | - |

### Request Flow

```
Client
  ↓
API Gateway :8080
  ├─ CORS & Rate Limiting
  ├─ Authentication (Cookie/Bearer)
  └─ Route to Service
      ↓
Upload Service :8081
  ├─ /auth/* → Authentication
  ├─ /api/upload/* → Chunked Uploads
  ├─ /api/jobs/* → Job Management
  └─ /api/{service}/* → Create Processing Jobs
      ↓
Redis Queue
  ├─ queue:pdf-to-word
  ├─ queue:word-to-pdf
  ├─ queue:merge-pdf
  ├─ queue:compress-pdf
  └─ queue:{tool-type}
      ↓
Worker Services
  ├─ Convert From PDF :8082 (PDF → Other formats)
  ├─ Convert To PDF :8083 (Other formats → PDF)
  ├─ Organize PDF :8084 (Merge, split, reorder)
  └─ Optimize PDF :8085 (Compress, repair, OCR)
      ↓
Job Completed
  ├─ Output stored in /app/outputs
  └─ Status updated in PostgreSQL
```

## Authentication

EsyDocs uses a modern **cookie-based authentication system** with HTTP-only cookies and 8-hour access tokens.

**Key Features**:
- 🔒 HTTP-only, Secure cookies (XSS protection)
- ⏱️ 8-hour token lifetime (no refresh tokens)
- 🚫 Immediate token revocation on logout (Redis denylist)
- 🌐 CORS-compatible with credentials
- 🔄 Backward compatible with Bearer tokens

**Complete Documentation**: [AUTHENTICATION.md](upload-service/AUTHENTICATION.md)

### Quick Examples

**Signup**:
```bash
curl -X POST http://localhost:8080/api/auth/signup \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "SecurePass123!",
    "fullName": "John Doe",
    "country": "US"
  }'
```

**Login**:
```bash
curl -c cookies.txt -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"SecurePass123!"}'
```

**Authenticated Request**:
```bash
curl -b cookies.txt http://localhost:8080/api/auth/me
```

**Frontend Integration**:
```javascript
// Always include credentials for cookies
fetch('http://localhost:8080/api/jobs', {
  credentials: 'include'
});
```

## Features

### Document Conversions

#### Convert From PDF (Port 8082)
- PDF → Word (.docx)
- PDF → Excel (.xlsx)
- PDF → PowerPoint (.pptx)
- PDF → Images (.zip with PNGs)
- PDF → HTML (with images)
- PDF → Plain Text (.txt)
- PDF → PDF/A (archival format)

#### Convert To PDF (Port 8083)
- Word (.doc, .docx) → PDF
- Excel (.xls, .xlsx) → PDF
- PowerPoint (.ppt, .pptx) → PDF
- HTML (.html, .htm) → PDF
- Images (.jpg, .png, .gif, .webp, .bmp) → PDF

#### Organize PDF (Port 8084)
- **Merge PDF** - Combine multiple PDFs into one
- **Split PDF** - Split by page ranges
- **Remove Pages** - Delete specific pages
- **Extract Pages** - Extract pages into new PDF
- **Organize PDF** - Reorder pages
- **Scan to PDF** - Convert images to PDF with optional OCR

#### Optimize PDF (Port 8085)
- **Compress PDF** - Reduce file size (quality options: screen/ebook/printer/prepress)
- **Repair PDF** - Fix corrupted PDFs using Ghostscript
- **OCR PDF** - Add searchable text layer to scanned PDFs (Tesseract)

### File Upload

**Chunked Upload System**:
- Large file support (up to 50 MB)
- Resume interrupted uploads
- Parallel chunk uploads
- Progress tracking

### Job Management

- Real-time status updates
- Job history and filtering
- Automatic cleanup of expired jobs
- Guest access for unauthenticated users

## Environment Configuration

### Required Variables

```bash
JWT_HS256_SECRET="<64-char-hex-secret>"  # Generate with: openssl rand -hex 32
DATABASE_URL="postgresql://user:password@db:5432/esydocs"
REDIS_ADDR="redis:6379"
```

### Optional Configuration

```bash
# JWT Settings
JWT_ACCESS_TTL="8h"              # Token lifetime
JWT_ISSUER="esydocs"
JWT_AUDIENCE="esydocs-api"

# Cookie Settings (Production)
AUTH_COOKIE_SECURE="true"        # HTTPS only
AUTH_COOKIE_DOMAIN="yourdomain.com"
AUTH_COOKIE_SAMESITE="lax"

# File Limits
MAX_UPLOAD_MB="50"               # Max file size
UPLOAD_TTL="2h"                  # Upload expiration
GUEST_JOB_TTL="2h"               # Guest job retention

# CORS (Production)
CORS_ALLOW_ORIGINS="https://yourdomain.com"
CORS_ALLOW_CREDENTIALS="true"

# Rate Limiting
RATE_LIMIT_LOGIN="5"             # Per minute
RATE_LIMIT_SIGNUP="3"            # Per minute

# Processing
MAX_RETRIES="3"                  # Failed job retries
PROCESSING_TIMEOUT="30m"         # Job timeout
CLEANUP_INTERVAL="5m"            # Cleanup frequency
```

## Development

### Prerequisites

- Docker & Docker Compose
- OpenSSL (for JWT secret generation)
- Git

### Local Setup

1. **Clone and navigate**:
   ```bash
   cd esydocs_backend
   ```

2. **Deploy**:
   ```bash
   ./deploy.sh
   ```

3. **View logs**:
   ```bash
   docker compose logs -f
   ```

4. **Stop services**:
   ```bash
   docker compose down
   ```

### Manual Service Development

Run individual services locally for development:

```bash
# Start infrastructure
docker compose up -d db redis

# Run upload-service
cd upload-service
export DATABASE_URL="postgresql://user:password@localhost:5432/esydocs"
export JWT_HS256_SECRET=$(openssl rand -hex 32)
go run main.go

# Run API gateway
cd api-gateway
export UPLOAD_SERVICE_URL="http://localhost:8081"
export JWT_HS256_SECRET="<same-as-upload-service>"
go run main.go
```

## Production Deployment

### Pre-Deployment Checklist

- [ ] Generate strong JWT secret (`openssl rand -hex 32`)
- [ ] Set `AUTH_COOKIE_SECURE=true`
- [ ] Configure specific CORS origins (no wildcards)
- [ ] Use HTTPS for all traffic
- [ ] Change default database credentials
- [ ] Set up secret management (AWS Secrets Manager, etc.)
- [ ] Configure backup strategy for PostgreSQL
- [ ] Set up monitoring and logging
- [ ] Review rate limits for expected traffic
- [ ] Plan for Redis persistence/backup

### Docker Compose Production

```bash
# Set environment variables
export JWT_HS256_SECRET="<production-secret>"

# Deploy with production settings
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### Security Considerations

1. **JWT Secret**: Use cryptographically secure random string (32+ characters)
2. **HTTPS Only**: Enforce HTTPS in production (`AUTH_COOKIE_SECURE=true`)
3. **CORS**: Whitelist specific origins, never use `*` with credentials
4. **Database**: Use strong passwords, enable SSL connections
5. **Redis**: Configure password authentication
6. **Rate Limiting**: Adjust limits based on traffic patterns
7. **Monitoring**: Set up alerts for failed logins, high error rates

## API Documentation

### Base URL

**Development**: `http://localhost:8080`
**Production**: `https://api.yourdomain.com`

### Authentication Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/auth/signup` | Create new user account |
| POST | `/api/auth/login` | Authenticate user |
| POST | `/api/auth/logout` | Logout and revoke token |
| GET | `/api/auth/me` | Get current user profile |

See [AUTHENTICATION.md](upload-service/AUTHENTICATION.md) for detailed documentation.

### File Upload Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/upload/init` | Initialize chunked upload |
| PUT | `/api/upload/{id}/chunk` | Upload file chunk |
| GET | `/api/upload/{id}/status` | Get upload status |
| POST | `/api/upload/{id}/complete` | Finalize upload |

### Processing Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/convert-from-pdf/{tool}` | Convert PDF to other format |
| POST | `/api/convert-to-pdf/{tool}` | Convert other format to PDF |
| POST | `/api/organize-pdf/{tool}` | Organize PDF operations |
| POST | `/api/optimize-pdf/{tool}` | Optimize PDF operations |
| GET | `/api/{service}/{tool}` | List jobs for tool |
| GET | `/api/{service}/{tool}/{id}` | Get job status |
| GET | `/api/{service}/{tool}/{id}/download` | Download result |
| DELETE | `/api/{service}/{tool}/{id}` | Delete job |

**Services**: `convert-from-pdf`, `convert-to-pdf`, `organize-pdf`, `optimize-pdf`

### Job Management Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/jobs` | List all user jobs |
| GET | `/api/jobs/{id}` | Get specific job |
| DELETE | `/api/jobs/{id}` | Delete job |

## Common Tasks

### View Logs

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f api-gateway
docker compose logs -f upload-service

# Last 100 lines
docker compose logs --tail=100 -f
```

### Restart Services

```bash
# All services
docker compose restart

# Specific service
docker compose restart upload-service
```

### Database Access

```bash
# PostgreSQL shell
docker compose exec db psql -U user -d esydocs

# Example queries
SELECT COUNT(*) FROM users;
SELECT status, COUNT(*) FROM processing_jobs GROUP BY status;
```

### Redis Access

```bash
# Redis CLI
docker compose exec redis redis-cli

# Example commands
KEYS queue:*           # List queues
LLEN queue:word-to-pdf # Queue length
KEYS denylist:jwt:*    # Denied tokens
```

### Manual Cleanup

```bash
# Clear all data (⚠️ DESTRUCTIVE)
docker compose down -v

# Clean old files
docker compose exec upload-service find /app/uploads -type f -mtime +7 -delete
docker compose exec upload-service find /app/outputs -type f -mtime +7 -delete
```

## Troubleshooting

### Services Won't Start

```bash
# Check logs for errors
docker compose logs api-gateway upload-service

# Common issues:
# - JWT secret not set
# - Port conflicts (8080, 8081 already in use)
# - Database not ready
```

### Authentication Errors (401)

```bash
# Check JWT secret consistency
docker compose exec api-gateway env | grep JWT_HS256_SECRET
docker compose exec upload-service env | grep JWT_HS256_SECRET
# Must be identical

# Check token denylist
docker compose exec redis redis-cli keys "denylist:jwt:*"
```

### Jobs Not Processing

```bash
# Check worker status
docker compose ps convert-from-pdf convert-to-pdf organize-pdf optimize-pdf

# Check queue depths
docker compose exec redis redis-cli llen "queue:word-to-pdf"
docker compose exec redis redis-cli llen "queue:merge-pdf"
docker compose exec redis redis-cli llen "queue:compress-pdf"

# View worker logs
docker compose logs -f convert-from-pdf
docker compose logs -f organize-pdf
docker compose logs -f optimize-pdf
```

### CORS Issues

```bash
# Verify CORS configuration
docker compose exec api-gateway env | grep CORS

# Ensure credentials enabled
CORS_ALLOW_CREDENTIALS=true

# Frontend must use
credentials: 'include'
```

## Testing

### Health Check

```bash
curl http://localhost:8080/healthz
# Response: "ok"
```

### Full Workflow Test

```bash
# 1. Signup
curl -c cookies.txt -X POST http://localhost:8080/api/auth/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"test@test.com","password":"pass123","fullName":"Test","country":"US"}'

# 2. Upload file
curl -b cookies.txt -X POST http://localhost:8080/api/convert-to-pdf/word-to-pdf \
  -F "file=@test.docx"
# Save jobID from response

# 3. Check status
curl -b cookies.txt http://localhost:8080/api/jobs/{jobID}

# 4. Download (when completed)
curl -b cookies.txt http://localhost:8080/api/convert-to-pdf/word-to-pdf/{jobID}/download \
  -o output.pdf
```

## Monitoring

### Key Metrics

- Request rate and latency
- Job processing time
- Queue depths
- Database connection pool usage
- Disk space usage
- Failed job rate
- Authentication success/failure rate

### Recommended Tools

- **Prometheus**: Metrics collection
- **Grafana**: Visualization
- **ELK Stack**: Log aggregation
- **Datadog/New Relic**: APM

## Technology Stack

- **Language**: Go 1.21+
- **Web Framework**: Gin (HTTP), net/http (Gateway)
- **Database**: PostgreSQL 15
- **Cache/Queue**: Redis 7
- **Document Processing**:
  - LibreOffice (Office documents, PDF to Office)
  - pdfcpu (Pure Go PDF manipulation)
  - Poppler (PDF rendering, pdftoppm, pdftotext, pdftohtml)
  - Ghostscript (PDF repair, PDF/A conversion)
  - Tesseract OCR (Searchable PDF text layer)
- **Authentication**: JWT (HS256)
- **Containerization**: Docker, Docker Compose

## Project Structure

```
esydocs_backend/
├── api-gateway/              # API Gateway service
│   ├── auth/                 # Auth middleware
│   ├── main.go
│   ├── Dockerfile
│   └── API_GATEWAY.md        # Service docs
├── upload-service/           # Upload & auth service
│   ├── auth/                 # Auth logic
│   ├── handlers/             # HTTP handlers
│   ├── database/             # DB layer
│   ├── main.go
│   ├── Dockerfile
│   ├── UPLOAD_SERVICE.md     # Service docs
│   └── AUTHENTICATION.md     # Auth system docs ⭐
├── convert-from-pdf/         # PDF conversion worker
│   ├── processing/           # Conversion logic
│   ├── worker/               # Queue worker
│   ├── main.go
│   ├── Dockerfile
│   └── CONVERT_FROM_PDF.md   # Service docs
├── convert-to-pdf/           # Document conversion worker
│   ├── processing/           # Conversion logic
│   ├── worker/               # Queue worker
│   ├── main.go
│   ├── Dockerfile
│   └── CONVERT_TO_PDF.md     # Service docs
├── organize-pdf/             # PDF organization worker
│   ├── processing/           # PDF manipulation logic
│   ├── worker/               # Queue worker
│   ├── main.go
│   ├── Dockerfile
│   └── ORGANIZE_PDF.md       # Service docs
├── optimize-pdf/             # PDF optimization worker
│   ├── processing/           # Compression, repair, OCR
│   ├── worker/               # Queue worker
│   ├── main.go
│   ├── Dockerfile
│   └── OPTIMIZE_PDF.md       # Service docs
├── cleanup-worker/           # Cleanup service
│   ├── main.go
│   ├── Dockerfile
│   └── CLEANUP_WORKER.md     # Service docs
├── docker/                   # Docker configs
│   └── BASE_IMAGE_SETUP.md
├── docker-compose.yml        # Service orchestration
├── deploy.sh                 # One-command deploy
└── README.md                 # This file

```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## License

[Your License Here]

## Support

For issues or questions:
- **Documentation**: See service-specific .md files in each directory
- **Logs**: `docker compose logs -f [service]`
- **Database**: `docker compose exec db psql -U user -d esydocs`
- **Redis**: `docker compose exec redis redis-cli`

---

**Built with ❤️ using Go and open-source tools**
