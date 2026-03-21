# EsyDocs API Documentation

## Overview

EsyDocs is a document conversion and PDF manipulation platform built with a microservices architecture. This documentation provides a complete API reference for frontend integration.

## Architecture

| Service | Internal Port | Description |
|---------|---------------|-------------|
| API Gateway | 8080 | Main entry point, authentication, routing |
| Job Service | 8081 | Job creation, file uploads, user management, job tracking |
| Convert To PDF | 8083 | Document to PDF conversions |
| Convert From PDF | 8082 | PDF to document conversions |
| Organize PDF | 8084 | PDF manipulation (merge, split, etc.) |
| Optimize PDF | 8085 | PDF optimization (compress, OCR, repair) |
| Auth Service | 8086 | User registration, login, token management |
| Cleanup Worker | - | Background cleanup of expired files/jobs |

## Base URL

```
http://localhost:8080
```

All requests go through the API Gateway which routes to appropriate services.

---

## Authentication

### Methods

**1. HTTP-only Cookie (Recommended)**
- Cookie name: `access_token`
- Automatically set on login/signup
- Sent automatically with requests when `credentials: 'include'`

**2. Authorization Header**
```
Authorization: Bearer <JWT_TOKEN>
```

**3. Guest Mode**
- For unauthenticated users
- Use `X-Guest-Token` header or cookie
- Jobs expire after 2 hours

### JWT Claims Structure
```json
{
  "sub": "user_id",
  "role": "user",
  "scope": ["scope1", "scope2"],
  "plan": "plan_name",
  "is_guest": false,
  "jti": "uuid-v4",
  "iat": 1234567890,
  "exp": 1234567890
}
```

---

## CORS Configuration

**Allowed Origins:** `http://localhost:5173` (configurable via `CORS_ALLOW_ORIGINS`)

**Allowed Methods:** GET, POST, PUT, PATCH, DELETE, OPTIONS

**Allowed Headers:** Authorization, Content-Type, X-User-ID, X-Guest-Token

**Credentials:** Enabled (cookies allowed)

---

## Standard API Response Format

All API endpoints use a unified response envelope:

### Success Response
```json
{
  "success": true,
  "message": "Operation completed successfully",
  "data": { ... },
  "error": null,
  "meta": {
    "requestId": "uuid"
  }
}
```

### Success Response with Pagination
```json
{
  "success": true,
  "message": "Jobs retrieved",
  "data": [ ... ],
  "error": null,
  "meta": {
    "page": 1,
    "limit": 25,
    "total": 100,
    "requestId": "uuid"
  }
}
```

### Error Response
```json
{
  "success": false,
  "message": "Human readable error message",
  "data": null,
  "error": {
    "code": "ERROR_CODE",
    "message": "Human readable error message"
  },
  "meta": {
    "requestId": "uuid"
  }
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `INVALID_INPUT` | 400 | Missing or invalid required fields |
| `USER_ALREADY_EXISTS` | 409 | Email already registered |
| `INVALID_CREDENTIALS` | 401 | Wrong email or password |
| `UNAUTHORIZED` | 401 | Authentication required |
| `AUTH_UNAUTHORIZED` | 401 | Invalid or expired token |
| `AUTH_FORBIDDEN` | 403 | Insufficient permissions |
| `NOT_FOUND` | 404 | Resource not found |
| `FILE_TOO_LARGE` | 400 | Uploaded file exceeds size limit |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many requests |
| `SERVER_ERROR` | 500 | Internal server error |

---

## Rate Limiting

Applied to authentication endpoints via Redis sliding window:

| Endpoint | Limit | Window |
|----------|-------|--------|
| Signup | 3 requests | 60 seconds |
| Login | 5 requests | 60 seconds |
| Refresh | 10 requests | 60 seconds |

### Rate Limit Headers
```
X-RateLimit-Limit: 5
X-RateLimit-Remaining: 2
X-RateLimit-Reset: 1642610400
Retry-After: 45
```

### Rate Limit Error Response (429)
```json
{
  "success": false,
  "message": "Too many requests. Please try again in 45 seconds.",
  "data": null,
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Too many requests. Please try again in 45 seconds."
  }
}
```

---

## Request Tracing

All API responses include an `X-Request-ID` header. You can also pass your own `X-Request-ID` header in requests, and the same ID will be echoed back and included in `meta.requestId` of the response.

---

## File Constraints

| Constraint | Value |
|------------|-------|
| Max File Size | 50 MB (configurable via `MAX_UPLOAD_MB`) |
| Upload TTL | 30 minutes (configurable via `UPLOAD_TTL`) |
| Guest Job TTL | 30 minutes (configurable via `GUEST_JOB_TTL`) |

---

## Job Status Lifecycle

```
pending → processing → completed
                    ↘ failed
```

| Status | Description |
|--------|-------------|
| `pending` | Job created, waiting in queue |
| `processing` | Job is being processed |
| `completed` | Job finished successfully |
| `failed` | Job failed (see `failureReason`) |

Jobs that fail permanently after all retries are preserved in a NATS Dead Letter Queue (`JOBS_DLQ`) for debugging and replay.

---

## API Documentation Files

| File | Description |
|------|-------------|
| [AUTH_API.md](./api/AUTH_API.md) | Authentication endpoints |
| [UPLOAD_API.md](./api/UPLOAD_API.md) | File upload endpoints |
| [CONVERT_TO_PDF_API.md](./api/CONVERT_TO_PDF_API.md) | Convert documents to PDF |
| [CONVERT_FROM_PDF_API.md](./api/CONVERT_FROM_PDF_API.md) | Convert PDF to documents |
| [ORGANIZE_PDF_API.md](./api/ORGANIZE_PDF_API.md) | PDF organization tools |
| [OPTIMIZE_PDF_API.md](./api/OPTIMIZE_PDF_API.md) | PDF optimization tools |
| [JOBS_API.md](./api/JOBS_API.md) | Job management endpoints |

## Service Documentation

| File | Description |
|------|-------------|
| [API_GATEWAY.md](./services/API_GATEWAY.md) | API Gateway service |
| [UPLOAD_SERVICE.md](./services/UPLOAD_SERVICE.md) | Upload service (deprecated — see [JOB_SERVICE.md](./services/JOB_SERVICE.md)) |
| [AUTH_SERVICE.md](./services/AUTH_SERVICE.md) | Authentication system |
| [CONVERT_TO_PDF.md](./services/CONVERT_TO_PDF.md) | Convert to PDF service |
| [CONVERT_FROM_PDF.md](./services/CONVERT_FROM_PDF.md) | Convert from PDF service |
| [ORGANIZE_PDF.md](./services/ORGANIZE_PDF.md) | Organize PDF service |
| [OPTIMIZE_PDF.md](./services/OPTIMIZE_PDF.md) | Optimize PDF service |
| [CLEANUP_WORKER.md](./services/CLEANUP_WORKER.md) | Cleanup worker |
| [BASE_IMAGE_SETUP.md](./services/BASE_IMAGE_SETUP.md) | Docker base image setup |

## Architecture Documentation

| File | Description |
|------|-------------|
| [REDIS_ARCHITECTURE.md](./architecture/REDIS_ARCHITECTURE.md) | Redis data structures and caching |

## OpenAPI / Swagger

| File | Description |
|------|-------------|
| [openapi.yaml](./swagger/openapi.yaml) | OpenAPI 3.0 specification |

---

## Typical Integration Flow

1. **Authenticate** → POST `/auth/login` or `/auth/signup`
2. **Upload File** → Initialize upload, send chunks, complete upload
3. **Create Job** → POST to appropriate service endpoint with `uploadId`
   - Include an optional `Idempotency-Key` header to prevent duplicate job creation on retry.
4. **Poll Status** → GET job status until `completed` or `failed`
   - **SSE alternative:** Clients can subscribe to `GET /api/jobs/:id/events` for real-time status updates via Server-Sent Events instead of polling.
5. **Download Result** → GET download endpoint to retrieve converted file

---

## Health Checks

- `/healthz` — Liveness check (is the process running?)
- `/readyz` — Readiness check (are all dependencies connected? Returns 200/503 with check details)

```
GET /healthz
```

**Response:** `{"status":"healthy"}` (200)

```
GET /readyz
```

**Response (200):** `{"status":"ready","checks":{"postgres":"ok","redis":"ok","nats":"ok"}}`
**Response (503):** `{"status":"not_ready","checks":{"postgres":"ok","redis":"fail","nats":"ok"}}`

---

## Logging

All services use structured logging via Go's `log/slog`:

- **Development mode** (`LOG_MODE=dev`): Human-readable text output with source info
- **Production mode** (`LOG_MODE=prod` or unset): JSON structured logs

Set `LOG_LEVEL` environment variable to control verbosity: `debug`, `info` (default), `warn`, `error`.

---

## Testing

Run tests using the provided scripts from the `esydocs_backend/` directory:

```bash
# All services
bash test.sh

# Single service
bash test.sh api-gateway

# Multiple services
bash test.sh shared auth-service

# Verbose
bash test.sh -v

# Windows CMD
test.bat
test.bat -v api-gateway
```

Available services: `shared`, `api-gateway`, `auth-service`, `job-service`, `convert-to-pdf`, `convert-from-pdf`, `organize-pdf`, `optimize-pdf`, `cleanup-worker`
