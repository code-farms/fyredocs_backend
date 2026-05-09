# Authentication API

Base URL (via gateway): `http://localhost:8080`

The gateway forwards `/auth/*` to `auth-service:8086`. Client requests should always go to the gateway, not directly to auth-service.

Cookies returned by signup, login, and refresh:
- `access_token` — Path `/`, HttpOnly, Secure, SameSite=Lax, Max-Age 8h (default)
- `refresh_token` — Path `/auth`, HttpOnly, Secure, SameSite=Lax, Max-Age 7d (default)

---

## POST /auth/signup

Create a new user account. Returns access + refresh cookies.

**Rate Limit:** 3 requests per 60 seconds per IP

### Request

```http
POST /auth/signup
Content-Type: application/json
```

**Body:**
```json
{
  "email": "user@example.com",
  "password": "password123",
  "fullName": "John Doe",
  "country": "US",
  "phone": "+1234567890",
  "image": "https://example.com/avatar.jpg"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| email | string | Yes | User email address (lowercased / trimmed server-side) |
| password | string | Yes | 8–128 characters |
| fullName | string | Yes | User's full name |
| country | string | Yes | Country code |
| phone | string | No | Phone number |
| image | string | No | Profile image URL |

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Welcome back!",
  "data": {
    "user": {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "fullName": "John Doe",
      "country": "US",
      "phone": "+1234567890",
      "image": "https://example.com/avatar.jpg",
      "role": "user",
      "planName": "free"
    },
    "accessExpiresAt": 1705324800000
  }
}
```

**Response Headers:**
```
Set-Cookie: access_token=<JWT>; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=28800
Set-Cookie: refresh_token=<JWT>; Path=/auth; Secure; HttpOnly; SameSite=Lax; Max-Age=604800
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing required fields or unparsable body |
| 400 | WEAK_PASSWORD | Password less than 8 characters |
| 400 | INVALID_INPUT | Password longer than 128 characters |
| 409 | USER_ALREADY_EXISTS | Email already registered |
| 429 | RATE_LIMIT_EXCEEDED | Too many signups from this IP |
| 500 | SERVER_ERROR | DB / hashing / token-issuance failure |

---

## POST /auth/login

Authenticate an existing user. Returns access + refresh cookies.

**Rate Limit:** 5 requests per 60 seconds per IP

### Request

```http
POST /auth/login
Content-Type: application/json
```

**Body:**
```json
{
  "email": "user@example.com",
  "password": "password123"
}
```

### Response

**200 OK** — same envelope as `/auth/signup` (`{user, accessExpiresAt}`).

**Response Headers:** same `access_token` + `refresh_token` cookies as signup.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | INVALID_CREDENTIALS | Wrong email or password |
| 429 | RATE_LIMIT_EXCEEDED | Too many login attempts from this IP |
| 500 | SERVER_ERROR | DB / token-issuance failure |

---

## POST /auth/refresh

Issue a new access token from a valid refresh-token cookie. The refresh row in `user_sessions` is updated in place — the refresh cookie itself is not rotated until logout / 7-day expiry.

**Rate Limit:** 10 requests per 60 seconds per IP

### Request

```http
POST /auth/refresh
Cookie: refresh_token=<JWT>
```

(no body)

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Token refreshed",
  "data": {
    "user": { "id": "...", "email": "...", "planName": "free", "role": "user", ... },
    "accessExpiresAt": 1705324800000
  }
}
```

**Response Headers:**
```
Set-Cookie: access_token=<new JWT>; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=28800
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | INVALID_REFRESH_TOKEN | Missing, expired, malformed, or revoked refresh token |
| 429 | RATE_LIMIT_EXCEEDED | Too many refresh attempts from this IP |
| 500 | SERVER_ERROR | DB / token-issuance failure |

---

## GET /auth/me

Get the currently authenticated user.

**Authentication:** Required (access_token cookie or `Authorization: Bearer <jwt>`).

### Request

```http
GET /auth/me
Cookie: access_token=<JWT>
```

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Profile loaded",
  "data": {
    "user": {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "fullName": "John Doe",
      "country": "US",
      "phone": "+1234567890",
      "image": "https://example.com/avatar.jpg",
      "role": "user",
      "planName": "free"
    }
  }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## GET /auth/profile

Same payload as `/auth/me`, returned under `data.profile` instead of `data.user`. Kept for backwards compatibility.

---

## POST /auth/logout

Log out the current user. Deletes the matching `user_sessions` row, denies the access-token hash in Redis, drops the plan cache, and clears both cookies.

**Authentication:** Required.

### Request

```http
POST /auth/logout
Cookie: access_token=<JWT>; refresh_token=<JWT>
```

### Response

**204 No Content**

**Response Headers:**
```
Set-Cookie: access_token=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=-1
Set-Cookie: refresh_token=; Path=/auth; Secure; HttpOnly; SameSite=Lax; Max-Age=-1
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## PUT /auth/plan

Change the authenticated user's subscription plan. Refreshes the Redis plan cache and publishes `analytics.events.plan.changed` to NATS.

**Authentication:** Required.

### Request

```http
PUT /auth/plan
Cookie: access_token=<JWT>
Content-Type: application/json

{ "planName": "pro" }
```

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Plan updated successfully",
  "data": {
    "user": { "id": "...", "planName": "pro", ... }
  }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing or empty `planName` |
| 400 | INVALID_PLAN | Plan does not exist |
| 400 | SAME_PLAN | User is already on the requested plan |
| 401 | UNAUTHORIZED | Not authenticated |
| 500 | SERVER_ERROR | DB failure |

---

## GET /auth/plans

List all non-anonymous subscription plans (no auth required).

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Plans retrieved",
  "data": {
    "plans": [
      { "name": "free", "maxFileSizeMb": 25,  "maxFilesPerJob": 10, "retentionDays": 7  },
      { "name": "pro",  "maxFileSizeMb": 500, "maxFilesPerJob": 50, "retentionDays": 30 }
    ]
  }
}
```

---

## Internal endpoints (not exposed via gateway)

These are reachable from inside the cluster only. They are mounted under `/internal/...` and are not proxied by api-gateway.

### GET /internal/users/:id/plan

Returns `{userId, plan: {name, maxFileSizeMb, maxFilesPerJob, retentionDays}}`. Falls back to the `free` plan defaults if `subscription_plans` lookup fails.

### POST /internal/users/:id/revoke-sessions

Revokes every active session for the user (deletes rows from `user_sessions` and adds each access-token hash to the Redis denylist). Returns `{revokedCount}`.

### DELETE /internal/sessions/:id

Revokes a single session by its `user_sessions.id` (= JWT `jti`). Returns the standard envelope with no payload on success.

---

## User Object

| Field | Type | Description |
|-------|------|-------------|
| id | string (UUID) | Unique user identifier |
| email | string | User email address |
| fullName | string | User's full name |
| country | string | Country code |
| phone | string | Phone number (optional) |
| image | string | Profile image URL (optional) |
| role | string | User role (`user` or `super-admin`) |
| planName | string | Current plan name (`free`, `pro`, ...) |
