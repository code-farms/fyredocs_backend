# Auth Service

## Overview

The Auth Service is a dedicated microservice responsible for user signup, login, profile management, and **DB-backed session management with refresh-token rotation**. It implements a cookie-based authentication system with HTTP-only cookies, an 8-hour access token, and a 7-day refresh token, both backed by a `user_sessions` row in PostgreSQL. Logout revokes the access token via a Redis denylist and deletes the corresponding session row. Plan info is cached in Redis for the API gateway to read on every request.

**Port**: 8086
**Type**: REST API
**Framework**: Gin (Go)
**Database**: PostgreSQL (via GORM)
**Cache**: Redis (token denylist, plan cache, rate limiter)
**Bus**: NATS JetStream (publishes `analytics.events.*`)

## Service Responsibility

1. **User Registration** — Create new user accounts with email/password (bcrypt).
2. **User Login** — Authenticate users and issue an access + refresh JWT pair as HTTP-only cookies, store the session row in `user_sessions`.
3. **Refresh-token Rotation** — Issue a new access token from a valid refresh cookie and update the session row with the new access-token hash. (No refresh-token replay: each refresh re-uses the original refresh row.)
4. **User Profile** — `GET /auth/me`, `GET /auth/profile` return the authenticated user, role, and plan.
5. **Logout** — Revoke the access token via the Redis denylist, delete the session row, clear cookies, and delete the plan cache.
6. **Plan Management** — `PUT /auth/plan` updates the user's `plan_name`, refreshes the Redis plan cache, and publishes `plan.changed` to NATS.
7. **Plans Listing** — `GET /auth/plans` returns all selectable subscription plans.
8. **Internal Admin** — `/internal/users/:id/plan`, `/internal/users/:id/revoke-sessions`, `/internal/sessions/:id` for service-to-service calls.
9. **Rate Limiting** — Login / signup / refresh / forgot-password / reset-password endpoints are IP-throttled via Redis.
10. **Background Cleanup** — A 1-hour ticker calls `models.DeleteExpiredSessions` and `models.DeleteExpiredResetTokens` to prune expired rows.
11. **Password Reset** — `POST /auth/forgot-password` issues a single-use, 1-hour magic-link token and emails it via Resend; `POST /auth/reset-password` consumes the token, updates the bcrypt hash, and revokes every active session for the user.
12. **Proxy Login (Admin Impersonation)** — `POST /auth/proxy-login` lets an `admin`/`super-admin` mint a short-lived access token for another user. The token carries an `impersonated_by` claim, has a short TTL (`PROXY_LOGIN_TTL`), and is **not** paired with a refresh token, so the session cannot be silently renewed and expires on its own. The action is audit-logged (`slog`) and published as a `user.proxy_login` analytics event. Gated by `PROXY_LOGIN_ENABLED`.

> Guest sessions are **not** issued by this service. The api-gateway issues and verifies guest tokens directly via cookies.

## Design Constraints

- **Microservice boundary**: Owns `users`, `auth_metadata`, `subscription_plans`, and `user_sessions`. Does NOT manage jobs, uploads, or file processing.
- **Own database**: Separate PostgreSQL schema. Connection pool managed via `models.PoolConfig` and DSN defaults from `shared/config.ApplyPostgresDSNDefaults`.
- **JWT format**: Self-contained tokens. The `jti` claim is the session UUID — the same UUID is the primary key of the `user_sessions` row.
- **Cookie-first**: Tokens are delivered as HTTP-only Secure cookies (`access_token` on `/`, `refresh_token` scoped to `/auth`). Bearer header tokens are accepted for API clients.
- **Plan info out of band**: Plan limits are stored in Redis (`user:plan:<userId>`), not embedded in the JWT, so plan changes take effect on the next request.

## Internal Architecture

```
Client
  │
API Gateway :8080  (verifies JWT, reads plan cache, sets X-User-* headers)
  │
Auth Service :8086
  │── Gin router (routes/routes.go)
  │── handlers/auth.go             Signup · Login · Refresh · Me · Profile · Logout · ChangePlan
  │── handlers/password_reset.go   ForgotPassword · ResetPassword
  │── handlers/internal_api.go     GetUserPlan
  │── handlers/admin.go            RevokeUserSessions · RevokeSession
  │── handlers/plans.go            GetAllPlans
  │── internal/token/              JWT issuance (HS256), Claims with jti
  │── internal/authverify/         JWT verification middleware, Redis token denylist
  │── internal/email/              Mailer interface · ResendMailer · NoopMailer
  │── internal/models/             GORM models: User · SubscriptionPlan · UserSession · PasswordResetToken
  │── middleware/ratelimit         Redis-backed rate limiter (per IP)
  │── PostgreSQL                   users · auth_metadata · subscription_plans · user_sessions · password_reset_tokens
  │── Redis                        denylist:jwt:* · ratelimit:* · user:plan:*
  └── NATS                         analytics.events.user.signup / user.login / plan.changed / user.password_reset_completed
```

### Key Internal Packages

| Package | Purpose |
|---------|---------|
| `handlers/auth.go` | Signup, Login, Refresh, Me, Profile, Logout, ChangePlan endpoints; plan cache helpers |
| `handlers/password_reset.go` | ForgotPassword, ResetPassword endpoints; magic-link token issuance |
| `handlers/admin.go` | RevokeUserSessions, RevokeSession (internal API) |
| `handlers/internal_api.go` | GetUserPlan (internal API) |
| `handlers/plans.go` | GetAllPlans (public, lists non-anonymous plans) |
| `internal/token/` | JWT issuance with HS256; AccessToken (8h, includes role) and RefreshToken (7d) |
| `internal/authverify/` | JWT verification, denylist, gin middleware |
| `internal/email/` | `Mailer` interface, `ResendMailer` (HTTPS API), `NoopMailer` (dev/test) |
| `internal/models/user.go` | `User`, `SubscriptionPlan`, `AuthMetadata` |
| `internal/models/token.go` | `UserSession` + `HashToken`, `StoreSession`, `FindSessionByRefreshHash`, `RevokeSessionByAccessHash`, `RevokeAllUserSessions`, `DeleteExpiredSessions` |
| `internal/models/password_reset.go` | `PasswordResetToken` + `CreatePasswordResetToken`, `FindValidResetTokenByHash`, `DeleteResetTokensForUser`, `DeleteExpiredResetTokens` |
| `routes/routes.go` | Gin route registration with rate limiting |

## Routes

### Public Auth Endpoints

| Method | Path | Handler | Rate Limit | Description |
|--------|------|---------|------------|-------------|
| POST | `/auth/signup` | `Signup` | 3/min per IP | Create a new user account; returns access+refresh cookies |
| POST | `/auth/login` | `Login` | 5/min per IP | Authenticate; returns access+refresh cookies |
| POST | `/auth/refresh` | `Refresh` | 10/min per IP | Issue a new access token from a valid refresh cookie (DB-backed rotation) |
| POST | `/auth/forgot-password` | `ForgotPassword` | 3/min per IP | Issue a single-use reset token and email it; always responds 200 regardless of whether the email is registered |
| POST | `/auth/reset-password` | `ResetPassword` | 5/min per IP | Consume a reset token, update password hash, revoke every active session for the user |
| GET | `/auth/plans` | `GetAllPlans` | none | List all non-anonymous subscription plans with limits |

### Authenticated Endpoints

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/auth/me` | `Me` | Get current user profile |
| GET | `/auth/profile` | `Profile` | Get current user profile (alternate envelope) |
| POST | `/auth/logout` | `Logout` | Deny access token, delete session row, clear cookies, drop plan cache |
| PUT | `/auth/plan` | `ChangePlan` | Change user's `plan_name`; refresh Redis plan cache; publish `plan.changed` to NATS |
| POST | `/auth/proxy-login` | `ProxyLogin` | **Admin impersonation.** Caller must be `admin`/`super-admin`. Mints a short-lived, refresh-less access token for the target user (`{"userId": "<uuid>"}`), sets the access cookie, and audit-logs the action. Requires `PROXY_LOGIN_ENABLED=true`. |

### Internal Endpoints (not exposed via gateway)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/internal/users/:id/plan` | `GetUserPlan` | Get a user's subscription plan (used by analytics-service / job-service if needed) |
| POST | `/internal/users/:id/revoke-sessions` | `RevokeUserSessions` | Revoke all active sessions for a user; deny each access-token hash in Redis |
| DELETE | `/internal/sessions/:id` | `RevokeSession` | Revoke a single session by `user_sessions.id` |

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
    id                UUID PRIMARY KEY,
    name              TEXT UNIQUE NOT NULL,
    max_file_size_mb  INT NOT NULL,
    max_files_per_job INT NOT NULL,
    retention_days    INT NOT NULL,
    created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

Three rows are seeded at startup:

| name | max_file_size_mb | max_files_per_job | retention_days |
|------|------------------|-------------------|----------------|
| `anonymous` | 10 | 5 | 0 |
| `free` | 25 | 10 | 7 |
| `pro` | 500 | 50 | 30 |

### user_sessions (refresh-token rotation)

```sql
CREATE TABLE user_sessions (
    id                  UUID PRIMARY KEY,           -- == JWT 'jti'
    user_id             UUID NOT NULL,              -- FK users(id) ON DELETE CASCADE
    access_token_hash   TEXT NOT NULL,              -- SHA-256 hex
    refresh_token_hash  TEXT,                       -- SHA-256 hex
    access_expires_at   TIMESTAMP NOT NULL,
    refresh_expires_at  TIMESTAMP,
    created_at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_user_sessions_access_hash  ON user_sessions(access_token_hash);
CREATE UNIQUE INDEX idx_user_sessions_refresh_hash ON user_sessions(refresh_token_hash);
CREATE INDEX        idx_user_sessions_user_id     ON user_sessions(user_id);
```

- **One row per login** — created by `StoreSession` during `Login` / `Signup`.
- **Refresh** updates the row's `access_token_hash` + `access_expires_at` (the refresh hash stays put for its 7-day lifetime).
- **Logout** deletes the row by access-token hash.
- **Background ticker** (every 1h) deletes rows where both access and refresh tokens have expired.

### password_reset_tokens

```sql
CREATE TABLE password_reset_tokens (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL,              -- FK users(id) ON DELETE CASCADE
    token_hash  TEXT NOT NULL,              -- SHA-256 hex of the raw token sent in the email
    expires_at  TIMESTAMP NOT NULL,
    request_ip  TEXT,                       -- client IP that requested the reset (audit)
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_password_reset_tokens_token_hash ON password_reset_tokens(token_hash);
CREATE INDEX        idx_password_reset_tokens_user_id    ON password_reset_tokens(user_id);
CREATE INDEX        idx_password_reset_tokens_expires_at ON password_reset_tokens(expires_at);
```

- **One outstanding token per user** — `ForgotPassword` calls `DeleteResetTokensForUser` before inserting, so a new request invalidates any prior link.
- **Single-use** — `ResetPassword` deletes every token row for the user inside the same transaction that updates the password hash.
- **Background ticker** (every 1h) also prunes expired rows via `DeleteExpiredResetTokens`.
- The raw token is never persisted; only its SHA-256 hash. Reuse of `HashToken` from [`internal/models/token.go`](../../../auth-service/internal/models/token.go) keeps the lookup format identical to session tokens.

### Redis Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `denylist:jwt:<token-hash>` | String | Remaining token TTL | Revoked access tokens (cross-service) |
| `user:plan:<userId>` | String (JSON) | Access-token TTL (8h) | Cached plan info for the gateway: `{plan, max_file_mb, max_files}` |
| `ratelimit:login:<ip>` | String | Rate limit window | Login attempt counter |
| `ratelimit:signup:<ip>` | String | Rate limit window | Signup attempt counter |
| `ratelimit:refresh:<ip>` | String | Rate limit window | Refresh attempt counter |
| `ratelimit:forgot_password:<ip>` | String | Rate limit window | Forgot-password attempt counter |
| `ratelimit:reset_password:<ip>` | String | Rate limit window | Reset-password attempt counter |

## Sequence Diagrams

### Signup Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis
    participant NATS as NATS

    C->>GW: POST /auth/signup {email, password, fullName, country}
    GW->>AS: Proxy
    AS->>AS: Validate (email, password >= 8, fullName, country)
    AS->>DB: SELECT users WHERE email=?
    alt User exists
        AS-->>C: 409 USER_ALREADY_EXISTS
    else New user
        AS->>AS: bcrypt.GenerateFromPassword
        AS->>DB: INSERT users (...)
        AS->>NATS: Publish analytics.events.user.signup
        AS->>DB: SELECT subscription_plans WHERE name='free'
        AS->>AS: Issuer.IssueAccessToken (8h) + IssueRefreshToken (7d)
        AS->>DB: INSERT user_sessions (id=jti, hashes, expiries)
        AS->>R: SET user:plan:<id> {free, 25, 10} EX=accessTTL
        AS-->>C: 200 + Set-Cookie access_token (Path=/) + Set-Cookie refresh_token (Path=/auth)
    end
```

### Login Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis
    participant NATS as NATS

    C->>GW: POST /auth/login {email, password}
    GW->>AS: Proxy
    AS->>R: Rate-limit ratelimit:login:<ip>
    alt over limit
        AS-->>C: 429 RATE_LIMIT_EXCEEDED
    else allowed
        AS->>DB: SELECT users WHERE email=?
        alt not found / password mismatch
            AS-->>C: 401 INVALID_CREDENTIALS
        else valid
            AS->>DB: SELECT subscription_plans WHERE name=user.plan_name (fallback to free)
            AS->>AS: IssueAccessToken (jti=sessionId) + IssueRefreshToken
            AS->>DB: INSERT user_sessions
            AS->>NATS: Publish analytics.events.user.login
            AS->>R: SET user:plan:<id> EX=accessTTL
            AS-->>C: 200 + Set-Cookie access_token & refresh_token
        end
    end
```

### Refresh-Token Rotation

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL (user_sessions)
    participant R as Redis

    C->>GW: POST /auth/refresh (Cookie: refresh_token)
    GW->>AS: Proxy
    AS->>AS: Read refresh cookie; reject if missing
    AS->>AS: Issuer.VerifyRefreshToken → userId

    AS->>DB: FindSessionByRefreshHash(SHA256(refresh)) WHERE refresh_expires_at > NOW
    alt not found / expired
        AS-->>C: 401 INVALID_REFRESH_TOKEN
    else found
        AS->>DB: SELECT users WHERE id=userId
        AS->>DB: SELECT subscription_plans WHERE name=user.plan_name
        AS->>R: SET user:plan:<id> EX=accessTTL
        AS->>AS: IssueAccessToken (new) — refresh stays the same row
        AS->>DB: UPDATE user_sessions SET access_token_hash, access_expires_at WHERE id=session.id
        AS-->>C: 200 + Set-Cookie access_token (refresh cookie unchanged)
    end
```

### Logout Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis

    C->>GW: POST /auth/logout (Cookie: access_token)
    GW->>AS: Proxy (auth-required)
    AS->>AS: Extract access token from Authorization header / cookie
    AS->>DB: DELETE FROM user_sessions WHERE access_token_hash = SHA256(access)
    AS->>R: SET denylist:jwt:<hash> EX=remaining-ttl
    AS->>R: DEL user:plan:<userId>
    AS->>AS: Clear access_token (Path=/) and refresh_token (Path=/auth) cookies
    AS-->>C: 204 No Content
```

### Token Validation (Gateway-side)

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant R as Redis

    C->>GW: Any /api/* request
    GW->>GW: Extract token (Authorization > access_token cookie > guest_token cookie)
    GW->>GW: Verify JWT (HS256 + iss/aud/exp + clock skew)
    GW->>R: GET denylist:jwt:<hash>
    alt denied or invalid
        GW-->>C: 401
    else valid
        GW->>R: GET user:plan:<userId>  (ResolvePlan callback)
        GW->>GW: Inject X-User-ID, X-Role, X-User-Plan, X-Plan-Max-File-MB, X-Plan-Max-Files
        GW->>Backend: Forward
    end
```

### Forgot Password — Issue Reset Token

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis
    participant M as Resend (or NoopMailer)

    C->>GW: POST /auth/forgot-password {email}
    GW->>AS: Proxy
    AS->>R: Rate-limit ratelimit:forgot_password:<ip>
    AS->>DB: SELECT users WHERE email=?
    alt User not found / DB error
        Note over AS: STILL respond 200 (no enumeration)
    else User found
        AS->>DB: DELETE password_reset_tokens WHERE user_id=?
        AS->>AS: Generate 32 random bytes -> base64url (raw)
        AS->>DB: INSERT password_reset_tokens (token_hash=SHA256(raw), expires_at=NOW()+1h, request_ip)
        AS-->>M: SendPasswordReset(to, ${APP_BASE_URL}/reset-password?token=raw, ip, ttl)  [async]
    end
    AS-->>C: 200 "If an account exists for this email, a reset link has been sent."
```

### Reset Password — Consume Token

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis
    participant NATS as NATS

    C->>GW: POST /auth/reset-password {token, newPassword}
    GW->>AS: Proxy
    AS->>R: Rate-limit ratelimit:reset_password:<ip>
    AS->>AS: Validate token non-empty + password length [8,128]
    AS->>DB: SELECT password_reset_tokens WHERE token_hash=SHA256(token) AND expires_at>NOW
    alt not found / expired
        AS-->>C: 400 INVALID_OR_EXPIRED_TOKEN
    else valid
        AS->>AS: bcrypt.GenerateFromPassword(newPassword)
        AS->>DB: BEGIN
        AS->>DB: UPDATE users SET password_hash=? WHERE id=user_id
        AS->>DB: DELETE FROM password_reset_tokens WHERE user_id=user_id
        AS->>DB: DELETE FROM user_sessions WHERE user_id=user_id RETURNING * (active only)
        AS->>DB: COMMIT
        loop For each revoked session
            AS->>R: SET denylist:jwt:<access_token_hash> EX=remaining
        end
        AS->>R: DEL user:plan:<user_id>
        AS->>NATS: Publish analytics.events.user.password_reset_completed
        AS-->>C: 200 "Password updated. Please sign in."
    end
```

### Internal Session Revocation (admin)

```mermaid
sequenceDiagram
    participant Admin as Internal Caller
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis

    Admin->>AS: POST /internal/users/:id/revoke-sessions
    AS->>DB: DELETE FROM user_sessions WHERE user_id=:id RETURNING * (active only)
    loop For each revoked session
        AS->>R: SET denylist:jwt:<hash> EX=remaining
    end
    AS-->>Admin: 200 {revokedCount}
```

### Proxy Login (Admin Impersonation)

```mermaid
sequenceDiagram
    participant Admin as Admin (browser)
    participant GW as API Gateway
    participant AS as Auth Service
    participant DB as PostgreSQL
    participant R as Redis

    Admin->>GW: POST /auth/proxy-login {userId} (admin token)
    GW->>GW: Verify admin token (protected route)
    GW->>AS: forward request
    AS->>AS: PROXY_LOGIN_ENABLED? caller role in {admin, super-admin}?
    alt disabled or not allowed
        AS-->>Admin: 403 PROXY_LOGIN_DISABLED / FORBIDDEN
    else
        AS->>DB: SELECT target user by id
        alt target missing
            AS-->>Admin: 404 USER_NOT_FOUND
        else
            AS->>AS: IssueImpersonationToken(target, impersonated_by=admin, short TTL)
            AS->>DB: StoreAccessOnlySession (no refresh hash)
            AS->>R: cache target plan info (TTL)
            AS->>AS: audit log + publish user.proxy_login
            AS-->>Admin: 200 {user, accessExpiresAt, impersonatedBy} + access cookie
        end
    end
```

## Error Flows

### Authentication Errors

| Error Code | HTTP Status | Condition |
|------------|-------------|-----------|
| `INVALID_INPUT` | 400 | Missing required fields or invalid payload |
| `WEAK_PASSWORD` | 400 | Password less than 8 characters |
| `USER_ALREADY_EXISTS` | 409 | Email already registered |
| `INVALID_CREDENTIALS` | 401 | Wrong email or password |
| `INVALID_REFRESH_TOKEN` | 401 | Missing, expired, or unknown refresh token |
| `INVALID_OR_EXPIRED_TOKEN` | 400 | Reset token does not match any active row or has expired |
| `UNAUTHORIZED` | 401 | Not authenticated or token expired/revoked |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many attempts |
| `INVALID_PLAN` | 400 | `ChangePlan` with unknown plan name |
| `SAME_PLAN` | 400 | `ChangePlan` to current plan |
| `PROXY_LOGIN_DISABLED` | 403 | `ProxyLogin` while `PROXY_LOGIN_ENABLED=false` |
| `FORBIDDEN` | 403 | `ProxyLogin` caller is not `admin`/`super-admin` |
| `INVALID_TARGET` | 400 | `ProxyLogin` target equals the caller (self-impersonation) |
| `USER_NOT_FOUND` | 404 | `ProxyLogin` target user does not exist |
| `SERVER_ERROR` | 500 | Database / token issuance failure |

### Error Response Format

All errors follow the standard response format:
```json
{
  "success": false,
  "message": "human readable message",
  "error": { "code": "ERROR_CODE", "details": "detailed description" }
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
  "jti": "<sessionId — primary key of user_sessions row>",
  "role": "user"
}
```

Plan info (`plan`, `max_file_mb`, `max_files`) is **not** embedded in the JWT. It is cached in Redis and read by the API gateway, so plan changes take effect immediately without waiting for token expiry.

### Plan Cache (Redis)

- **Key**: `user:plan:<userId>` → JSON `{"plan":"free","max_file_mb":25,"max_files":10}`
- **TTL**: Same as access-token TTL (default 8h)
- **Written by**: auth-service on signup, login, refresh, and `PUT /auth/plan`
- **Deleted on**: logout
- **Read by**: api-gateway via the `ResolvePlan` callback in the auth middleware
- **Fallback**: If the key is missing, the gateway treats the user as the `free` plan defaults

### JWT Configuration

- **Algorithm**: HS256 (HMAC-SHA256)
- **Secret**: `JWT_HS256_SECRET` (min 32 characters)
- **Access TTL**: 8 hours (`JWT_ACCESS_TTL`)
- **Refresh TTL**: 7 days (`JWT_REFRESH_TTL`)
- **Clock Skew**: 60s tolerance (`JWT_CLOCK_SKEW`)

### Token Validation Priority

The middleware checks for authentication tokens in this order:

1. **Authorization Header** (`Bearer <token>`) — API clients, mobile apps, testing
2. **`access_token` Cookie** — primary for browsers (HTTP-only, Secure)
3. **`guest_token` Cookie** — anonymous users (issued + verified at the gateway)

### Security Features

| Feature | Protection Against |
|---------|-------------------|
| HTTP-only Cookies | XSS |
| Secure Flag | MITM |
| SameSite=Lax | CSRF |
| Token Denylist | Immediate access-token revocation on logout |
| `user_sessions` table | Refresh-token rotation, server-side session control, admin revocation |
| bcrypt Password Hashing | Database breach |
| Rate Limiting | Brute force attacks |

### Rate Limits

| Endpoint | Default | Window |
|----------|---------|--------|
| POST /auth/login | `RATE_LIMIT_LOGIN` (5) | `RATE_LIMIT_WINDOW` (60s) |
| POST /auth/signup | `RATE_LIMIT_SIGNUP` (3) | `RATE_LIMIT_WINDOW` |
| POST /auth/refresh | `RATE_LIMIT_REFRESH` (10) | `RATE_LIMIT_WINDOW` |

Rate limits are per IP and enforced via Redis-backed middleware.

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_ADDR` | Redis server address |
| `JWT_HS256_SECRET` | JWT signing secret (min 32 characters) |
| `JWT_ISSUER` | Token issuer claim |
| `JWT_AUDIENCE` | Token audience claim |

### JWT Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_ACCESS_TTL` | `8h` | Access token lifetime |
| `JWT_REFRESH_TTL` | `168h` (7d) | Refresh token lifetime |
| `JWT_CLOCK_SKEW` | `60s` | Allowed clock skew for validation |
| `JWT_ALLOWED_ALGS` | `HS256` | Allowed JWT algorithms |

### Cookie Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_ACCESS_COOKIE` | `access_token` | Cookie name for access token (Path=`/`) |
| `AUTH_REFRESH_COOKIE` | `refresh_token` | Cookie name for refresh token (Path=`/auth`) |
| `AUTH_COOKIE_DOMAIN` | `""` | Cookie domain (empty = current domain) |
| `AUTH_COOKIE_SECURE` | `true` | Require HTTPS (must be `true` in production) |
| `AUTH_COOKIE_SAMESITE` | `lax` | SameSite policy (`lax`, `strict`, or `none`) |

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
| `RATE_LIMIT_FORGOT_PASSWORD` | `3` | Max forgot-password requests per window |
| `RATE_LIMIT_RESET_PASSWORD` | `5` | Max reset-password attempts per window |
| `RATE_LIMIT_PROXY_LOGIN` | `5` | Max proxy-login attempts per window |
| `RATE_LIMIT_WINDOW` | `60s` | Rate-limit time window |

### Password Reset / Email

| Variable | Default | Description |
|----------|---------|-------------|
| `RESEND_API_KEY` | `""` | Resend API key. When empty, the service uses `NoopMailer` and the reset link is logged instead of emailed (intended for local dev). |
| `RESET_EMAIL_FROM` | `no-reply@fyredocs.com` | From address. Must be on a domain verified in the Resend dashboard, otherwise Resend will only deliver to the account owner. |
| `APP_BASE_URL` | `http://localhost:5173` | Used to build the magic link: `${APP_BASE_URL}/reset-password?token=<raw>`. |
| `PASSWORD_RESET_TOKEN_TTL` | `1h` | Lifetime of a password reset token. |

### Proxy Login (Admin Impersonation)

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXY_LOGIN_ENABLED` | `true` | When `false`, `POST /auth/proxy-login` returns `403 PROXY_LOGIN_DISABLED`. |
| `PROXY_LOGIN_TTL` | `30m` | Lifetime of an impersonation access token (no refresh token is issued). |

### Other

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8086` | HTTP server port |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | Trusted proxy IP addresses |
| `AUTH_TRUST_GATEWAY_HEADERS` | `false` | Trust X-User-ID from gateway (only enable when gateway is upstream) |
| `LOG_MODE` | `""` | Logging mode |

## Background Workers

### Expired Session Cleanup
A goroutine started at boot runs `models.DeleteExpiredSessions` once per hour. It is driven by a `time.Ticker` and selects on `cleanupCtx.Done()` so it exits on SIGTERM. The main goroutine cancels `cleanupCtx` and waits on a `sync.WaitGroup` after `srv.Shutdown` returns, so an in-flight delete is allowed to finish instead of being killed mid-statement when the DB connection pool is closed.

The same goroutine also runs `models.DeleteExpiredResetTokens` each tick so the `password_reset_tokens` table does not accumulate dead rows.

## Scaling Constraints

1. **Horizontal scaling**: Stateless beyond the DB/Redis dependencies. Multiple instances can run behind a load balancer.
2. **DB connection pool**: `MaxOpenConns=10`, `MaxIdleConns=5`, `ConnMaxLifetime=5m`, `ConnMaxIdleTime=2m`. The idle bound is shorter than typical managed-Postgres idle-close windows so the pool never holds a half-dead socket. `Connect()` enriches `DATABASE_URL` via `shared/config.ApplyPostgresDSNDefaults` (`statement_timeout=15s`, `idle_in_transaction_session_timeout=30s`, libpq TCP keepalives).
3. **Redis dependency**: The token denylist, plan cache, and rate limiter depend on Redis. Without Redis, logout revocation, plan caching, and rate limiting are degraded. Use Redis Sentinel/Cluster for HA.
4. **JWT secret sync**: All services that validate JWT tokens (api-gateway, job-service, auth-service) must use the same `JWT_HS256_SECRET`. Secret rotation requires coordinated deployment.
5. **bcrypt cost**: `bcrypt.DefaultCost`. High traffic may benefit from tuning this value.

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
    JWT_ISSUER: fyredocs
    JWT_AUDIENCE: fyredocs-api
    AUTH_COOKIE_SECURE: "true"
    AUTH_DENYLIST_ENABLED: "true"
  depends_on:
    - db
    - redis
    - nats
```

### Local Development

```bash
docker compose up -d db redis nats

export DATABASE_URL="postgresql://user:password@localhost:5432/fyredocs?sslmode=disable"
export REDIS_ADDR="localhost:6379"
export JWT_HS256_SECRET=$(openssl rand -hex 32)
export JWT_ISSUER=fyredocs
export JWT_AUDIENCE=fyredocs-api
export AUTH_COOKIE_SECURE="false"  # Only for local dev!

cd auth-service && go run main.go
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
Set-Cookie: refresh_token=eyJhbGc...; HttpOnly; Secure; SameSite=Lax; Max-Age=604800; Path=/auth

{
  "success": true,
  "message": "Welcome back!",
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
    },
    "accessExpiresAt": 1705324800000
  }
}
```

### POST /auth/login

Same response shape as signup. Pre-flight rate limit: `RATE_LIMIT_LOGIN` per `RATE_LIMIT_WINDOW`.

### POST /auth/refresh

**Request**: requires `refresh_token` cookie (no body).

**Response** (200 OK): new `access_token` cookie + JSON `{user, accessExpiresAt}`. The `refresh_token` cookie is left unchanged (the underlying `user_sessions` row's refresh hash is not rotated until logout / 7-day expiry).

### GET /auth/me

Returns `{user}` derived from the verified token + `users` row + `subscription_plans`.

### POST /auth/logout

Returns 204. Side effects: deletes the matching `user_sessions` row, denies the access-token hash in Redis, deletes the plan cache, clears both cookies.

### PUT /auth/plan

**Request**: `{"planName": "pro"}`. Side effects: updates `users.plan_name`, refreshes Redis plan cache, publishes `analytics.events.plan.changed` to NATS.

### GET /auth/plans

Returns all non-anonymous plans (`free`, `pro` by default).

### POST /auth/forgot-password

**Request**:
```http
POST /auth/forgot-password
Content-Type: application/json

{
  "email": "user@example.com"
}
```

**Response** (always 200):
```json
{
  "success": true,
  "message": "If an account exists for this email, a reset link has been sent.",
  "data": null
}
```

Notes:
- The response shape is identical whether or not the email is registered, to prevent account enumeration.
- The email send is fire-and-forget on a background goroutine with a 15-second timeout; mailer failures do not change the response.
- A new request invalidates any previously-issued reset token for that user.

### POST /auth/reset-password

**Request**:
```http
POST /auth/reset-password
Content-Type: application/json

{
  "token": "<raw token from the magic link>",
  "newPassword": "NewSecurePass123!"
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "Password updated. Please sign in.",
  "data": null
}
```

**Error responses**:

| Status | Code | Cause |
|--------|------|-------|
| 400 | `INVALID_INPUT` | Missing token or empty/whitespace-only token |
| 400 | `WEAK_PASSWORD` | New password shorter than 8 characters |
| 400 | `INVALID_INPUT` | New password longer than 128 characters |
| 400 | `INVALID_OR_EXPIRED_TOKEN` | Token does not match any active row, or has expired |
| 500 | `SERVER_ERROR` | bcrypt failure or transaction failure |

Side effects on success:
- `users.password_hash` updated.
- Every row in `password_reset_tokens` for the user deleted (single-use).
- Every row in `user_sessions` for the user deleted; each access-token hash is added to the Redis denylist for the remainder of its original TTL.
- The plan cache (`user:plan:<id>`) is dropped.
- `analytics.events.user.password_reset_completed` published to NATS.

### Internal — GET /internal/users/:id/plan

Returns `{userId, plan: {name, maxFileSizeMb, maxFilesPerJob, retentionDays}}`. Used by other services that need plan info without going through the gateway / Redis cache.

### Internal — POST /internal/users/:id/revoke-sessions

Revokes every active session for the user and adds each access-token hash to the Redis denylist.

### Internal — DELETE /internal/sessions/:id

Revokes a single session by its UUID (= `jti`).

## Error Logging

All 5xx response sites in `Signup`, `Login`, `Refresh`, `respondWithTokens`, `ChangePlan`, `RevokeUserSessions`, `RevokeSession`, and `GetAllPlans` use `response.InternalErrorf` to log the underlying err with `op` and identifiers before returning the standard envelope. Authentication paths (`Login`, `Refresh`, `loadUserFromAuth`) emit `slog.Warn` lines via `logger.LogWarn` for observability without altering the user-visible response. See [Error Logging](../architecture/ERROR_LOGGING.md) for the convention.

To debug a failed signup/login, take `meta.requestId` from the response and grep auth-service stdout — the matching log line names the failing `op` (e.g. `db.users.create`, `bcrypt.generate_password_hash`, `issue_access_token.login`).

## Performance

- **Subscription-plan lookup is cached in-process** (`handlers/plancache.go`,
  `lookupPlan`). Plans are a tiny, rarely-changing set but were previously read
  by name from the database on every login, refresh, `/me`, `/profile`, and the
  internal `GetUserPlan` call. With a remote database each of those reads is a
  full network round-trip, so the lookup is cached by plan name for
  `PLAN_CACHE_TTL` (default 5m). This removes one round-trip from each of those
  endpoints — e.g. `/auth/me` dropped from ~480 ms to ~280 ms (one round-trip),
  and login lost its plan round-trip.
- Trade-off: a change to a plan's *definition* (e.g. raising its max file size)
  takes up to `PLAN_CACHE_TTL` to propagate. A user *switching* plans is
  unaffected — that changes their `user.PlanName`, which keys a different cache
  entry. Set `PLAN_CACHE_TTL=0` to disable caching.
- Login latency is otherwise bound by bcrypt (cost-10 password verification,
  intentional) plus the mandatory session-row insert; both are inherent and not
  cached.

## Related Documentation

- [API Gateway](./API_GATEWAY.md) — Request routing, JWT verification, plan resolution
- [Job Service](./JOB_SERVICE.md) — Job orchestration and file management
- [Main README](../../../README.md) — Overall architecture
- [Error Logging](../architecture/ERROR_LOGGING.md) — Backend-wide error logging convention
