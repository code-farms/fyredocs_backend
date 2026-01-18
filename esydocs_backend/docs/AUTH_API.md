# Authentication API

Base URL: `http://localhost:8080`

---

## POST /auth/signup

Create a new user account.

**Rate Limit:** 3 requests per 60 seconds

### Request

```
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
| email | string | Yes | User email address |
| password | string | Yes | User password |
| fullName | string | Yes | User's full name |
| country | string | Yes | Country code |
| phone | string | No | Phone number |
| image | string | No | Profile image URL |

### Response

**201 Created**
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "phone": "+1234567890",
    "image": "https://example.com/avatar.jpg",
    "role": "user"
  }
}
```

**Response Headers:**
```
Set-Cookie: access_token=<JWT>; Path=/; Secure; HttpOnly; SameSite=Lax
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing required fields |
| 409 | USER_ALREADY_EXISTS | Email already registered |

---

## POST /auth/login

Authenticate an existing user.

**Rate Limit:** 5 requests per 60 seconds

### Request

```
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

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| email | string | Yes | User email address |
| password | string | Yes | User password |

### Response

**200 OK**
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "phone": "+1234567890",
    "image": "https://example.com/avatar.jpg",
    "role": "user"
  }
}
```

**Response Headers:**
```
Set-Cookie: access_token=<JWT>; Path=/; Secure; HttpOnly; SameSite=Lax
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | INVALID_CREDENTIALS | Wrong email or password |

---

## GET /auth/me

Get the currently authenticated user.

**Authentication:** Required

### Request

```
GET /auth/me
Cookie: access_token=<JWT>
```

Or with Authorization header:
```
GET /auth/me
Authorization: Bearer <JWT>
```

### Response

**200 OK**
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "phone": "+1234567890",
    "image": "https://example.com/avatar.jpg",
    "role": "user"
  }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## GET /auth/profile

Get the user's profile information.

**Authentication:** Required

### Request

```
GET /auth/profile
Cookie: access_token=<JWT>
```

### Response

**200 OK**
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "phone": "+1234567890",
    "image": "https://example.com/avatar.jpg",
    "role": "user"
  }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## POST /auth/logout

Log out the current user.

**Authentication:** Required

### Request

```
POST /auth/logout
Cookie: access_token=<JWT>
```

### Response

**204 No Content**

**Response Headers:**
```
Set-Cookie: access_token=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=-1
```

### Behavior

- Revokes access token via Redis denylist
- Clears access token cookie

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## POST /auth/refresh (Deprecated)

This endpoint is deprecated.

**Rate Limit:** 10 requests per 60 seconds

### Response

**410 Gone**
```json
{
  "code": "ENDPOINT_DEPRECATED",
  "message": "Refresh tokens are no longer supported. Please login again to get a new access token."
}
```

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
| role | string | User role (`user`) |
