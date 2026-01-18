# EsyDocs API Documentation

## Overview

EsyDocs is a document conversion and PDF manipulation platform built with a microservices architecture. This documentation provides a complete API reference for frontend integration.

## Architecture

| Service | Internal Port | Description |
|---------|---------------|-------------|
| API Gateway | 8080 | Main entry point, authentication, routing |
| Upload Service | 8081 | File uploads, user management, job tracking |
| Convert To PDF | 8083 | Document to PDF conversions |
| Convert From PDF | 8082 | PDF to document conversions |
| Organize PDF | 8084 | PDF manipulation (merge, split, etc.) |
| Optimize PDF | 8085 | PDF optimization (compress, OCR, repair) |

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

## Common Response Formats

### Success Response
```json
{
  "data": { ... }
}
```

### Error Response
```json
{
  "code": "ERROR_CODE",
  "message": "Human readable error message"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `INVALID_INPUT` | 400 | Missing or invalid required fields |
| `USER_ALREADY_EXISTS` | 409 | Email already registered |
| `INVALID_CREDENTIALS` | 401 | Wrong email or password |
| `UNAUTHORIZED` | 401 | Authentication required |
| `NOT_FOUND` | 404 | Resource not found |
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
  "code": "RATE_LIMIT_EXCEEDED",
  "message": "Too many requests. Please try again in 45 seconds."
}
```

---

## File Constraints

| Constraint | Value |
|------------|-------|
| Max File Size | 50 MB (configurable via `MAX_UPLOAD_MB`) |
| Upload TTL | 2 hours (configurable via `UPLOAD_TTL`) |
| Guest Job TTL | 2 hours (configurable via `GUEST_JOB_TTL`) |

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

---

## API Documentation Files

| File | Description |
|------|-------------|
| [AUTH_API.md](./AUTH_API.md) | Authentication endpoints |
| [UPLOAD_API.md](./UPLOAD_API.md) | File upload endpoints |
| [CONVERT_TO_PDF_API.md](./CONVERT_TO_PDF_API.md) | Convert documents to PDF |
| [CONVERT_FROM_PDF_API.md](./CONVERT_FROM_PDF_API.md) | Convert PDF to documents |
| [ORGANIZE_PDF_API.md](./ORGANIZE_PDF_API.md) | PDF organization tools |
| [OPTIMIZE_PDF_API.md](./OPTIMIZE_PDF_API.md) | PDF optimization tools |
| [JOBS_API.md](./JOBS_API.md) | Job management endpoints |

---

## Typical Integration Flow

1. **Authenticate** → POST `/auth/login` or `/auth/signup`
2. **Upload File** → Initialize upload, send chunks, complete upload
3. **Create Job** → POST to appropriate service endpoint with `uploadId`
4. **Poll Status** → GET job status until `completed` or `failed`
5. **Download Result** → GET download endpoint to retrieve converted file

---

## Health Check

```
GET /healthz
```

**Response:** `ok` (200)
