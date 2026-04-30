# Auth Service

## Overview

The Auth Service is a dedicated microservice responsible for user authentication, registration, and profile management in Fyredocs. It implements a modern **cookie-based authentication system** with HTTP-only cookies, 8-hour access tokens, and immediate token revocation on logout via a Redis denylist. It also exposes internal service-to-service APIs for user plan lookups.

**Port**: 8086
**Type**: REST API
**Framework**: Gin (Go)
**Database**: PostgreSQL (via GORM)
**Cache**: Redis (token denylist, guest store)

## Service Responsibility

1. **User Registration** -- Create new user accounts with email/password
2. **User Login** -- Authenticate users and issue JWT access tokens as HTTP-only cookies
3. **User Profile** -- Return authenticated user profile data
4. **Logout** -- Revoke access tokens via Redis denylist and clear cookies
5. **Guest Sessions** -- Issue temporary guest tokens so users can use tools without signing up
6. **Token Issuance** -- Generate HS256 JWT tokens with configurable TTL
7. **Token Denylist** -- Maintain a Redis-backed denylist for revoked tokens
8. **Rate Limiting** -- Protect auth endpoints from brute force attacks
9. **Internal API** -- Expose `/internal/users/:id/plan` for service-to-service calls

## Design Constraints

- **Microservice boundary**: The auth-service owns user records, password hashes, and token issuance. It does NOT manage jobs, uploads, or file processing.
- **Own database**: Separate PostgreSQL schema with `users`, `auth_metadata`, and `subscription_plans` tables.
- **Stateless tokens**: JWT tokens are self-contained. The denylist only stores revoked tokens, not active sessions.
- **Cookie-first**: Tokens are delivered as HTTP-only Secure cookies. Bearer header tokens are supported for backward compatibility.
- **No refresh tokens**: The `/auth/refresh` endpoint returns 410 Gone. Clients must re-login after token expiration.

## Internal Architecture

```
Client
  |
API Gateway :8080
  |
Auth Service :8086
  |--- Auth Handlers (signup, login, me, profile, logout, refresh)
  |--- Internal API (user plan lookup)
  |--- Auth Middleware (JWT / Guest Token)
  |--- Token Issuer (HS256 JWT)
  |--- Token Denylist (Redis)
  |--- Rate Limiter (Redis)
  |--- PostgreSQL (users, auth_metadata, subscription_plans)
  |--- Redis (denylist, guest store, rate limits)
```

### Key Internal Packages

| Package | Purpose |
|---------|---------|
| `handlers/auth.go` | Signup, Login, Me, Profile, Logout, Refresh, ChangePlan endpoints |
| `handlers/guest.go` | CreateGuestSession -- issues temporary Redis-backed guest tokens |
| `handlers/internal_api.go` | GetUserPlan -- internal service-to-service endpoint |
| `internal/token/` | JWT token issuance (HS256) |
| `internal/authverify/` | JWT verification, denylist, guest store |
| `internal/models/` | GORM models for User, AuthMetadata, SubscriptionPlan |
| `routes/routes.go` | Gin route registration with rate limiting |
| `middleware/` | Rate limiting middleware |

## Routes

### Public Auth Endpoints

| Method | Path | Handler | Rate Limit | Description |
|--------|------|---------|------------|-------------|
| POST | `/auth/signup` | `Signup` | 3/min per IP | Create a new user account |
| POST | `/auth/login` | `Login` | 5/min per IP | Authenticate and get access token |
| POST | `/auth/refresh` | `Refresh` | 10/min per IP | DEPRECATED (returns 410 Gone) |
| POST | `/auth/guest` | `CreateGuestSession` | 20/min per IP | Create a temporary guest session token |
| GET | `/auth/plans` | `GetPlans` | none | List all non-anonymous subscription plans with limits |

### Authenticated Endpoints

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/auth/me` | `Me` | Get current user profile |
| GET | `/auth/profile` | `Profile` | Alias for /auth/me |
| PUT | `/auth/plan` | `ChangePlan` | Change user's subscription plan (publishes `plan.changed` analytics event) |
| POST | `/auth/logout` | `Logout` | Revoke token and clear cookie |

### Internal Endpoints (not exposed via gateway)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/internal/users/:id/plan` | `GetUserPlan` | Get user subscription plan |

### Infrastructure Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check (returns "ok") |
| GET | `/readyz` | Readiness check (PostgreSQL + Redis), returns 200/503 with individual check results |
| GET | `/metrics` | Prometheus metrics |

## DB Schema

### users

```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    full_name     TEXT,
    phone         TEXT,
    country       TEXT,
    image_url     TEXT,
    password_hash TEXT NOT NULL,
    plan_name     TEXT NOT NULL DEFAULT 'free',
    role          TEXT NOT NULL DEFAULT 'user',  -- 'user' or 'super-admin'
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### auth_metadata

```sql
CREATE TABLE auth_metadata (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL,
    provider      TEXT NOT NULL,       -- e.g., 'local', 'google', 'github'
    subject       TEXT NOT NULL,       -- provider-specific user ID
    last_login_at TIMESTAMP,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_auth_metadata_user_id ON auth_metadata(user_id);
```

### subscription_plans

```sql
CREATE TABLE subscription_plans (
    id               UUID PRIMARY KEY,
    name             TEXT UNIQUE NOT NULL,
    max_file_size_mb INT NOT NULL,
    max_files_per_job INT NOT NULL,
    retention_days   INT NOT NULL,
    created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

Three rows are seeded at startup:

| name | max_file_size_mb | max_files_per_job | retention_days |
|------|-----------------|-------------------|----------------|
| `anonymous` | 10 | 5 | 0 |
| `free` | 25 | 10 | 7 |
| `pro` | 500 | 50 | 30 |

### Redis Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `denylist:jwt:<token>` | String | Remaining token TTL | Revoked access tokens |
| `ratelimit:login:<ip>` | String | Rate limit window | Login attempt counter |
| `ratelimit:signup:<ip>` | String | Rate limit window | Signup attempt counter |
| `ratelimit:refresh:<ip>` | String | Rate limit window | Refresh attempt counter |

## Sequence Diagrams

### Signup Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL

    C->>GW: POST /auth/signup {email, password, fullName, country}
    GW->>AS: Proxy request

    AS->>AS: Validate input (email, password >= 8 chars, fullName, country)
    AS->>DB: SELECT * FROM users WHERE email = ?
    alt User exists
        AS-->>GW: 409 {code: USER_ALREADY_EXISTS}
        GW-->>C: 409 Conflict
    else User does not exist
        AS->>AS: bcrypt.GenerateFromPassword(password)
        AS->>DB: INSERT INTO users (email, full_name, phone, country, image_url, password_hash, plan_name='free')
        AS->>DB: SELECT * FROM subscription_plans WHERE name = 'free'
        AS->>AS: Issuer.IssueAccessToken(userId, "user", PlanInfo{name, maxFileMB, maxFiles})
        AS-->>GW: 200 {user, planName} + Set-Cookie: access_token=<jwt>
        GW-->>C: 200 {user, planName} + Set-Cookie
    end
```

### Login Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant RL as Rate Limiter (Redis)

    C->>GW: POST /auth/login {email, password}
    GW->>AS: Proxy request

    AS->>RL: Check rate limit for IP
    alt Rate limit exceeded
        AS-->>GW: 429 {code: RATE_LIMIT_EXCEEDED}
        GW-->>C: 429 Too Many Requests
    end

    AS->>DB: SELECT * FROM users WHERE email = ?
    alt User not found
        AS-->>GW: 401 {code: INVALID_CREDENTIALS}
        GW-->>C: 401 Unauthorized
    else User found
        AS->>AS: bcrypt.CompareHashAndPassword(hash, password)
        alt Password mismatch
            AS-->>GW: 401 {code: INVALID_CREDENTIALS}
            GW-->>C: 401 Unauthorized
        else Password matches
            AS->>DB: SELECT * FROM subscription_plans WHERE name = user.plan_name
            AS->>AS: Issuer.IssueAccessToken(userId, "user", PlanInfo{name, maxFileMB, maxFiles})
            AS-->>GW: 200 {user, planName} + Set-Cookie: access_token=<jwt>
            GW-->>C: 200 {user, planName} + Set-Cookie
        end
    end
```

### Logout Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant R as Redis

    C->>GW: POST /auth/logout (Cookie: access_token=<jwt>)
    GW->>AS: Proxy request

    AS->>AS: Verify auth context (must be authenticated)
    AS->>AS: Extract access token from Authorization header or context
    AS->>AS: Parse token to get remaining TTL

    AS->>R: SET denylist:jwt:<token> WITH TTL=remaining_ttl
    AS->>AS: Clear access_token cookie (Max-Age=-1)
    AS-->>GW: 204 No Content + Set-Cookie: access_token=; Max-Age=-1
    GW-->>C: 204 No Content
```

### Token Validation Flow (Middleware)

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant R as Redis

    C->>GW: GET /auth/me (Cookie: access_token=<jwt>)
    GW->>AS: Proxy request

    AS->>AS: Extract token (Authorization header > Cookie > Guest header)
    AS->>AS: Validate JWT signature (HS256)
    AS->>AS: Check expiration, issuer, audience
    AS->>R: GET denylist:jwt:<token>
    alt Token is denied
        AS-->>GW: 401 {code: UNAUTHORIZED}
        GW-->>C: 401 Unauthorized
    else Token is valid
        AS->>AS: Set auth context (userId, role)
        AS->>AS: Continue to handler
    end
```

## Error Flows

### Authentication Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing required fields (email, password, fullName, country) |
| `WEAK_PASSWORD` | 400 | Password less than 8 characters |
| `INVALID_INPUT` | 400 | Password exceeds 128 characters |
| `USER_ALREADY_EXISTS` | 409 | Email already registered |
| `INVALID_CREDENTIALS` | 401 | Wrong email or password |
| `UNAUTHORIZED` | 401 | Not authenticated or token expired/revoked |
| `ENDPOINT_DEPRECATED` | 410 | Refresh endpoint called |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many attempts |
| `SERVER_ERROR` | 500 | Database or token issuance failure |

### Error Response Format

All errors follow the standard response format:
```json
{
  "success": false,
  "message": "human readable message",
  "error": {
    "code": "ERROR_CODE",
    "details": "detailed description"
  }
}
```

## Authentication System Overview

### JWT Token Structure

```json
{
  "sub": "550e8400-e29b-41d4-a716-446655440000",
  "iss": "fyredocs",
  "aud": "fyredocs-api",
  "exp": 1705324800,
  "iat": 1705296000,
  "jti": "unique-token-id",
  "role": "user"
}
```

Every token includes a `jti` claim -- a UUID v4 generated per token. It uniquely identifies each token for denylist matching and audit purposes.

Plan info (`plan`, `max_file_mb`, `max_files`) is **not** embedded in the JWT. Instead, it is cached in Redis on login/refresh and read by the API gateway to forward as HTTP headers. This ensures plan changes take effect immediately without waiting for token expiry.

### Plan Cache (Redis)

- **Key**: `user:plan:{userID}` → JSON `{"plan":"free","max_file_mb":25,"max_files":10}`
- **TTL**: Same as access token TTL (8 hours), refreshed on login/refresh
- **Written by**: Auth-service on login, signup, refresh, and plan change
- **Deleted on**: Logout
- **Read by**: API gateway (populates `X-User-Plan`, `X-User-Plan-Max-File-MB`, `X-User-Plan-Max-Files` headers)
- **Fallback**: If Redis key is missing, defaults to free plan (25 MB, 10 files)

- **Algorithm**: HS256 (HMAC-SHA256)
- **Secret**: `JWT_HS256_SECRET` environment variable (min 32 characters)
- **Access Token TTL**: 8 hours (configurable via `JWT_ACCESS_TTL`)
- **Clock Skew**: 60 seconds tolerance (configurable via `JWT_CLOCK_SKEW`)

### Token Validation Priority

The middleware checks for authentication tokens in this order:

1. **Authorization Header** (`Bearer <token>`) -- API clients, mobile apps, testing
2. **access_token Cookie** -- Primary for browsers (HTTP-only, Secure)
3. **Guest Token Header** (`X-Guest-Token`) -- Anonymous users

### Security Features

| Feature | Protection Against |
|---------|-------------------|
| HTTP-only Cookies | XSS (Cross-Site Scripting) |
| Secure Flag | MITM (Man-in-the-Middle) |
| SameSite=Lax | CSRF (Cross-Site Request Forgery) |
| Token Denylist | Immediate token revocation on logout |
| bcrypt Password Hashing | Database breach |
| Rate Limiting | Brute force attacks |

### Rate Limits

| Endpoint | Limit | Window |
|----------|-------|--------|
| POST /auth/login | 5 requests | 60 seconds |
| POST /auth/signup | 3 requests | 60 seconds |
| POST /auth/refresh | 10 requests | 60 seconds |

Rate limits are per IP address and enforced via Redis-backed middleware.

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_ADDR` | Redis server address |
| `JWT_HS256_SECRET` | JWT signing secret (min 32 characters) |

### JWT Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_ACCESS_TTL` | `8h` | Access token lifetime |
| `JWT_ISSUER` | **Required** | Token issuer claim |
| `JWT_AUDIENCE` | **Required** | Token audience claim |
| `JWT_CLOCK_SKEW` | `60s` | Allowed clock skew for validation |
| `JWT_ALLOWED_ALGS` | `HS256` | Allowed JWT algorithms |

> **Note**: `JWT_ISSUER` and `JWT_AUDIENCE` are required. The service will fail to start if these are not set.

### Cookie Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_ACCESS_COOKIE` | `access_token` | Cookie name for access token |
| `AUTH_COOKIE_DOMAIN` | `""` | Cookie domain (empty = current domain) |
| `AUTH_COOKIE_SECURE` | `true` | Require HTTPS (must be `true` in production) |
| `AUTH_COOKIE_SAMESITE` | `lax` | SameSite policy (`lax` or `strict`) |

### Token Denylist

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_DENYLIST_ENABLED` | `true` | Enable logout token revocation |
| `AUTH_DENYLIST_PREFIX` | `denylist:jwt` | Redis key prefix for denylist |

### Rate Limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `RATE_LIMIT_LOGIN` | `5` | Max login attempts per window |
| `RATE_LIMIT_SIGNUP` | `3` | Max signup attempts per window |
| `RATE_LIMIT_REFRESH` | `10` | Max refresh attempts per window |
| `RATE_LIMIT_WINDOW` | `60s` | Rate limit time window |

### Other

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8086` | HTTP server port |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | Trusted proxy IP addresses |
| `AUTH_GUEST_PREFIX` | `guest` | Guest token Redis key prefix |
| `AUTH_GUEST_SUFFIX` | `jobs` | Guest token Redis key suffix |
| `AUTH_TRUST_GATEWAY_HEADERS` | `false` | Trust X-User-ID from gateway |
| `LOG_MODE` | `""` | Logging mode |

## Scaling Constraints

1. **Horizontal scaling**: The auth-service is stateless (JWT tokens are self-contained). Multiple instances can run behind a load balancer.
2. **Database connection pool**: Configured with `MaxOpenConns=10`, `MaxIdleConns=5`. Appropriate for auth workloads.
3. **Redis dependency**: The token denylist and rate limiter depend on Redis. If Redis is unavailable, logout revocation and rate limiting will not function. Consider Redis Sentinel or Cluster for HA.
4. **JWT secret sync**: All services that validate JWT tokens (api-gateway, job-service, auth-service) must use the same `JWT_HS256_SECRET`. Secret rotation requires coordinated deployment.
5. **bcrypt cost**: Password hashing uses `bcrypt.DefaultCost`. High traffic may benefit from tuning this value.
6. **Single write path**: User creation uses PostgreSQL unique constraints on email to prevent duplicates under concurrent writes.

## Deployment

### Docker Compose

```yaml
auth-service:
  build:
    context: ./auth-service
  ports:
    - "8086:8086"
  environment:
    PORT: "8086"
    DATABASE_URL: postgresql://user:password@db:5432/fyredocs
    REDIS_ADDR: redis:6379
    JWT_HS256_SECRET: ${JWT_HS256_SECRET}
    AUTH_COOKIE_SECURE: "true"
    AUTH_DENYLIST_ENABLED: "true"
  depends_on:
    - db
    - redis
```

### Local Development

1. Start dependencies:
   ```bash
   docker compose up -d db redis
   ```

2. Set environment variables:
   ```bash
   export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs?sslmode=disable"
   export REDIS_ADDR="localhost:6379"
   export JWT_HS256_SECRET=$(openssl rand -hex 32)
   export AUTH_COOKIE_SECURE="false"  # Only for local dev!
   ```

3. Run the service:
   ```bash
   cd auth-service
   go run main.go
   ```

## API Endpoint Details

### POST /auth/signup

**Request**:
```http
POST /auth/signup
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "SecurePass123!",
  "fullName": "John Doe",
  "country": "US",
  "phone": "+1234567890",
  "image": "https://..."
}
```

**Response** (200 OK):
```http
Set-Cookie: access_token=eyJhbGc...; HttpOnly; Secure; SameSite=Lax; Max-Age=28800; Path=/

{
  "success": true,
  "message": "Authentication successful",
  "data": {
    "user": {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "fullName": "John Doe",
      "phone": "+1234567890",
      "country": "US",
      "image": "https://...",
      "role": "user",
      "planName": "free"
    }
  }
}
```

### POST /auth/login

**Request**:
```http
POST /auth/login
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "SecurePass123!"
}
```

**Response** (200 OK): Same format as signup (includes `planName` in the `user` object).

### GET /auth/me

**Request**:
```http
GET /auth/me
Cookie: access_token=eyJhbGc...
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "User profile retrieved",
  "data": {
    "user": {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "fullName": "John Doe",
      "country": "US",
      "role": "user",
      "planName": "free"
    }
  }
}
```

### POST /auth/logout

**Request**:
```http
POST /auth/logout
Cookie: access_token=eyJhbGc...
```

**Response** (204 No Content):
```http
Set-Cookie: access_token=; Max-Age=-1; Path=/
```

### PUT /auth/plan

**Request** (authenticated):
```http
PUT /auth/plan
Cookie: access_token=eyJhbGc...
Content-Type: application/json

{
  "planName": "pro"
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "Plan updated successfully",
  "data": {
    "user": {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "fullName": "John Doe",
      "country": "US",
      "role": "user",
      "planName": "pro"
    }
  }
}
```

**Error Responses**:
- `400 INVALID_INPUT` — Missing or empty planName
- `400 INVALID_PLAN` — Plan does not exist
- `400 SAME_PLAN` — User is already on the requested plan
- `401 UNAUTHORIZED` — Not authenticated
- `500 SERVER_ERROR` — Database failure

**Side Effect**: Publishes a `plan.changed` analytics event via NATS with metadata `{"oldPlan": "free", "newPlan": "pro"}`.

### GET /internal/users/:id/plan

**Request** (service-to-service, not via gateway):
```http
GET /internal/users/550e8400-e29b-41d4-a716-446655440000/plan
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "User plan retrieved",
  "data": {
    "userId": "550e8400-e29b-41d4-a716-446655440000",
    "plan": {
      "name": "free",
      "maxFileSizeMb": 25,
      "maxFilesPerJob": 10,
      "retentionDays": 7
    }
  }
}
```

The response is populated from a real DB lookup of `subscription_plans` using the user's `plan_name` field.

### GET /auth/plans

**Request** (no authentication required):
```http
GET /auth/plans
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "Plans retrieved",
  "data": {
    "plans": [
      {
        "name": "free",
        "maxFileSizeMb": 25,
        "maxFilesPerJob": 10,
        "retentionDays": 7
      },
      {
        "name": "pro",
        "maxFileSizeMb": 500,
        "maxFilesPerJob": 50,
        "retentionDays": 30
      }
    ]
  }
}
```

Returns all non-anonymous plans. The `anonymous` plan is excluded because it is an internal default used when no token is present, not a user-selectable tier.

## Related Documentation

- [API Gateway](./API_GATEWAY.md) -- Request routing and CORS
- [Job Service](./JOB_SERVICE.md) -- Job orchestration and file management
- [Upload Service](./UPLOAD_SERVICE.md) -- Legacy upload service
- [Main README](../../README.md) -- Overall architecture
