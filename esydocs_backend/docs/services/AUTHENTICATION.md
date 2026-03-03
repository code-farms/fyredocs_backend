# Authentication System

## Overview

EsyDocs uses a modern **cookie-based authentication system** with HTTP-only cookies, 8-hour access tokens, and immediate token revocation on logout. This provides strong security against XSS attacks while offering a simple developer experience.

**Key Features**:
- HTTP-only, Secure cookies (XSS protection)
- 8-hour access token lifetime (no refresh tokens)
- Immediate token revocation via Redis denylist
- CORS-compatible with credentials
- Backward compatible with Bearer tokens

## Architecture

### Authentication Flow

```
┌────────────┐
│   Signup   │
│  /auth/signup
└─────┬──────┘
      ↓
┌─────────────────────────────┐
│ Set-Cookie: access_token    │ (HTTP-only, Secure, 8h expiry)
│ Response: { user }          │
└─────┬───────────────────────┘
      ↓
┌──────────────────────────┐
│ Authenticated Requests   │
│ (Cookie sent automatically)
└────────┬─────────────────┘
         ↓
┌──────────────────────────┐
│    Logout /auth/logout   │
└────────┬─────────────────┘
         ↓
┌──────────────────────────┐
│ Token → Denylist (Redis) │
│ Cookie Cleared           │
└──────────────────────────┘
```

### Token Validation Priority

The middleware checks for authentication tokens in this order:

1. **Authorization Header** (`Bearer <token>`)
   - For API clients, mobile apps, testing
   - Example: `Authorization: Bearer eyJhbGc...`

2. **access_token Cookie** (Primary for browsers)
   - Automatically sent by browser
   - Example: `Cookie: access_token=eyJhbGc...`

3. **Guest Token Header** (For anonymous users)
   - Example: `X-Guest-Token: guest-uuid`

### Middleware Implementation

All services use the same authentication middleware:

- **api-gateway**: [auth/middleware_http.go](../api-gateway/auth/middleware_http.go)
- **upload-service**: [auth/middleware_gin.go](./auth/middleware_gin.go)
- **convert-from-pdf**: [auth/middleware_gin.go](../convert-from-pdf/auth/middleware_gin.go)
- **convert-to-pdf**: [auth/middleware_gin.go](../convert-to-pdf/auth/middleware_gin.go)

## API Endpoints

### POST /auth/signup

Create a new user account.

**Request**:
```http
POST /auth/signup
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "SecurePass123!",
  "fullName": "John Doe",
  "country": "US",
  "phone": "+1234567890",     // optional
  "image": "https://..."      // optional
}
```

**Response** (200 OK):
```http
Set-Cookie: access_token=eyJhbGc...; HttpOnly; Secure; SameSite=Lax; Max-Age=28800; Path=/

{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "phone": "+1234567890",
    "country": "US",
    "image": "https://...",
    "role": "user"
  }
}
```

**Error Responses**:

| Status | Code | Description |
|--------|------|-------------|
| 400 | `INVALID_INPUT` | Missing required fields (email, password, fullName, country) |
| 409 | `USER_ALREADY_EXISTS` | Email already registered |
| 500 | `SERVER_ERROR` | Internal error (database, etc.) |

---

### POST /auth/login

Authenticate an existing user.

**Request**:
```http
POST /auth/login
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "SecurePass123!"
}
```

**Response** (200 OK):
```http
Set-Cookie: access_token=eyJhbGc...; HttpOnly; Secure; SameSite=Lax; Max-Age=28800; Path=/

{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "role": "user"
  }
}
```

**Error Responses**:

| Status | Code | Description |
|--------|------|-------------|
| 401 | `INVALID_CREDENTIALS` | Wrong email or password |
| 429 | `RATE_LIMIT_EXCEEDED` | Too many login attempts (5/min) |
| 500 | `SERVER_ERROR` | Internal error |

**Rate Limiting**: 5 attempts per minute per IP

---

### GET /auth/me

Get the currently authenticated user's profile.

**Request**:
```http
GET /auth/me
Cookie: access_token=eyJhbGc...
```

Or with Bearer token:
```http
GET /auth/me
Authorization: Bearer eyJhbGc...
```

**Response** (200 OK):
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "fullName": "John Doe",
    "country": "US",
    "phone": "+1234567890",
    "image": "https://...",
    "role": "user"
  }
}
```

**Error Responses**:

| Status | Code | Description |
|--------|------|-------------|
| 401 | `UNAUTHORIZED` | Not authenticated or token expired |
| 500 | `SERVER_ERROR` | Internal error |

---

### GET /auth/profile

Alias for `/auth/me`. Returns the same user profile data.

---

### POST /auth/logout

Logout and revoke the access token.

**Request**:
```http
POST /auth/logout
Cookie: access_token=eyJhbGc...
```

**Response** (204 No Content):
```http
Set-Cookie: access_token=; Max-Age=-1; Path=/
```

The token is immediately added to the denylist in Redis, preventing any further use even if stolen.

**Error Responses**:

| Status | Code | Description |
|--------|------|-------------|
| 401 | `UNAUTHORIZED` | Not authenticated |
| 500 | `SERVER_ERROR` | Internal error |

---

### ~~POST /auth/refresh~~ (DEPRECATED)

**Status**: 410 Gone

This endpoint has been removed. Refresh tokens are no longer supported.

**Response** (410 Gone):
```json
{
  "code": "ENDPOINT_DEPRECATED",
  "message": "Refresh tokens are no longer supported. Please login again to get a new access token."
}
```

**Migration**: Update clients to handle token expiration by redirecting to login when receiving 401 responses.

---

## JWT Token Structure

### Token Claims

```json
{
  "sub": "550e8400-e29b-41d4-a716-446655440000",  // User ID
  "iss": "esydocs",                                // Issuer
  "aud": "esydocs-api",                            // Audience
  "exp": 1705324800,                               // Expiration (8 hours)
  "iat": 1705296000,                               // Issued at
  "jti": "unique-token-id"                         // Token ID (for denylist)
}
```

### Token Signing

- **Algorithm**: HS256 (HMAC-SHA256)
- **Secret**: `JWT_HS256_SECRET` environment variable (min 32 characters)
- **Key Strength**: 256-bit minimum

### Token Lifetime

- **Access Token**: 8 hours (configurable via `JWT_ACCESS_TTL`)
- **No Refresh Tokens**: Users must re-login after expiration
- **Clock Skew**: 60 seconds tolerance (configurable via `JWT_CLOCK_SKEW`)

## Security Features

### 1. HTTP-Only Cookies

**Protection**: XSS (Cross-Site Scripting)

```http
Set-Cookie: access_token=eyJhbGc...; HttpOnly; Secure; SameSite=Lax
```

- JavaScript cannot access the cookie via `document.cookie`
- Even if attacker injects malicious script, token cannot be stolen
- Cookie is automatically sent with every request by the browser

### 2. Secure Flag

**Protection**: MITM (Man-in-the-Middle)

```http
Set-Cookie: access_token=eyJhbGc...; Secure
```

- Cookie only sent over HTTPS connections
- Prevents token interception on insecure networks
- **Required in production** (`AUTH_COOKIE_SECURE=true`)

### 3. SameSite Protection

**Protection**: CSRF (Cross-Site Request Forgery)

```http
Set-Cookie: access_token=eyJhbGc...; SameSite=Lax
```

- `SameSite=Lax`: Cookie not sent on cross-site POST requests
- Allows top-level navigation (clicking links)
- Prevents CSRF attacks from malicious websites

### 4. Token Denylist

**Protection**: Immediate token revocation

When a user logs out:
1. Token JWT ID (`jti`) is added to Redis denylist
2. Key format: `denylist:jwt:{jti}`
3. TTL matches remaining token lifetime
4. All subsequent requests with that token are rejected

**Redis Keys**:
```
denylist:jwt:550e8400-e29b-41d4-a716-446655440000
TTL: 3600 seconds (1 hour remaining)
```

### 5. Rate Limiting

**Protection**: Brute force attacks

| Endpoint | Limit | Window |
|----------|-------|--------|
| POST /auth/login | 5 requests | 60 seconds |
| POST /auth/signup | 3 requests | 60 seconds |
| POST /auth/refresh | 10 requests | 60 seconds (deprecated) |

Rate limits are per IP address and enforced by the upload-service.

### 6. Password Hashing

**Protection**: Database breach

- Algorithm: **bcrypt**
- Cost factor: 12 (configurable, balance security vs performance)
- Salted automatically by bcrypt
- Passwords never stored in plaintext

## Configuration

### Environment Variables

#### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `JWT_HS256_SECRET` | **REQUIRED** - JWT signing secret (32+ chars) | `openssl rand -hex 32` |

#### JWT Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_ACCESS_TTL` | `8h` | Access token lifetime |
| `JWT_ISSUER` | `esydocs` | Token issuer claim |
| `JWT_AUDIENCE` | `esydocs-api` | Token audience claim |
| `JWT_CLOCK_SKEW` | `60s` | Allowed clock skew for validation |
| `JWT_ALLOWED_ALGS` | `HS256` | Allowed JWT algorithms |

#### Cookie Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_ACCESS_COOKIE` | `access_token` | Cookie name for access token |
| `AUTH_COOKIE_DOMAIN` | `""` | Cookie domain (empty = current domain) |
| `AUTH_COOKIE_SECURE` | `true` | Require HTTPS (must be `true` in production) |
| `AUTH_COOKIE_SAMESITE` | `lax` | SameSite policy (`lax` or `strict`) |

#### Token Denylist

| Variable | Default | Description |
|----------|---------|-------------|
| `AUTH_DENYLIST_ENABLED` | `true` | Enable logout token revocation |
| `AUTH_DENYLIST_PREFIX` | `denylist:jwt` | Redis key prefix for denylist |

#### Rate Limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `RATE_LIMIT_LOGIN` | `5` | Max login attempts per window |
| `RATE_LIMIT_SIGNUP` | `3` | Max signup attempts per window |
| `RATE_LIMIT_REFRESH` | `10` | Max refresh attempts per window |
| `RATE_LIMIT_WINDOW` | `60s` | Rate limit time window |

### Local Development (HTTP)

For local development without HTTPS:

```yaml
environment:
  AUTH_COOKIE_SECURE: "false"  # ONLY for local development!
  JWT_HS256_SECRET: "dev-secret-min-32-chars-long-12345"
```

**WARNING**: Never set `AUTH_COOKIE_SECURE=false` in production!

### Production Configuration

```yaml
environment:
  AUTH_COOKIE_SECURE: "true"           # REQUIRED
  JWT_HS256_SECRET: ${JWT_SECRET}      # From secret manager
  AUTH_COOKIE_DOMAIN: "yourdomain.com"
  JWT_ACCESS_TTL: "8h"
  CORS_ALLOW_ORIGINS: "https://yourdomain.com"
  CORS_ALLOW_CREDENTIALS: "true"
```

## Frontend Integration

### Browser/SPA Integration

#### Login Example

```javascript
async function login(email, password) {
  const response = await fetch('http://localhost:8080/api/auth/login', {
    method: 'POST',
    credentials: 'include',  // CRITICAL: Required for cookies
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password })
  });

  if (!response.ok) {
    throw new Error('Login failed');
  }

  const { user } = await response.json();
  // Token is automatically stored in cookie
  return user;
}
```

#### Authenticated Requests

```javascript
async function getJobs() {
  const response = await fetch('http://localhost:8080/api/jobs', {
    credentials: 'include'  // Browser sends cookie automatically
  });

  if (response.status === 401) {
    // Token expired - redirect to login
    window.location.href = '/login';
    return;
  }

  return await response.json();
}
```

#### Logout Example

```javascript
async function logout() {
  await fetch('http://localhost:8080/api/auth/logout', {
    method: 'POST',
    credentials: 'include'
  });

  // Redirect to login page
  window.location.href = '/login';
}
```

### React Integration

#### Auth Context Provider

```typescript
import { createContext, useContext, useEffect, useState } from 'react';

interface User {
  id: string;
  email: string;
  fullName: string;
  country: string;
  role: string;
}

interface AuthContextType {
  user: User | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType>(null!);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  // Check authentication on mount
  useEffect(() => {
    fetch('http://localhost:8080/api/auth/me', {
      credentials: 'include'
    })
      .then(res => res.ok ? res.json() : null)
      .then(data => setUser(data?.user || null))
      .finally(() => setLoading(false));
  }, []);

  const login = async (email: string, password: string) => {
    const response = await fetch('http://localhost:8080/api/auth/login', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password })
    });

    if (!response.ok) throw new Error('Login failed');

    const { user } = await response.json();
    setUser(user);
  };

  const logout = async () => {
    await fetch('http://localhost:8080/api/auth/logout', {
      method: 'POST',
      credentials: 'include'
    });
    setUser(null);
  };

  return (
    <AuthContext.Provider value={{ user, loading, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
```

#### Protected Route

```typescript
import { Navigate } from 'react-router-dom';
import { useAuth } from './AuthContext';

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();

  if (loading) return <div>Loading...</div>;
  if (!user) return <Navigate to="/login" />;

  return <>{children}</>;
}
```

### TypeScript Types

```typescript
interface User {
  id: string;
  email: string;
  fullName: string;
  phone?: string;
  country: string;
  image?: string;
  role: string;
}

interface SignupData {
  email: string;
  password: string;
  fullName: string;
  country: string;
  phone?: string;
  image?: string;
}

interface LoginData {
  email: string;
  password: string;
}

interface AuthResponse {
  user: User;
}

interface ErrorResponse {
  code: string;
  message: string;
}
```

### API Client Wrapper

```typescript
const API_BASE = 'http://localhost:8080';

async function apiCall<T>(
  endpoint: string,
  options: RequestInit = {}
): Promise<T> {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  });

  // Handle token expiration
  if (response.status === 401) {
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    const error: ErrorResponse = await response.json();
    throw new Error(error.message || 'Request failed');
  }

  return response.json();
}

// Usage
const { user } = await apiCall<AuthResponse>('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ email, password }),
});
```

## CORS Configuration

### Backend Configuration

For cookie-based authentication to work cross-origin, CORS must allow credentials:

```yaml
# docker-compose.yml
environment:
  CORS_ALLOW_ORIGINS: "http://localhost:5173,http://localhost:3000"
  CORS_ALLOW_CREDENTIALS: "true"
  CORS_ALLOW_HEADERS: "Authorization,Content-Type,X-User-ID,X-Guest-Token"
```

### Frontend Configuration

Always include `credentials: 'include'` in fetch requests:

```javascript
fetch(url, {
  credentials: 'include'  // Required!
});
```

### Important CORS Rules

When `credentials: true`:
- ✅ **Cannot use wildcard** (`*`) for origins
- ✅ **Must specify exact origins**
- ✅ **Origins must include protocol and port**

Examples:
```bash
# ✅ Correct
CORS_ALLOW_ORIGINS="https://app.example.com,https://www.example.com"

# ❌ Wrong - cannot use wildcard with credentials
CORS_ALLOW_ORIGINS="*"

# ❌ Wrong - missing protocol
CORS_ALLOW_ORIGINS="example.com"
```

## Testing

### With curl

```bash
# 1. Login (save cookies to file)
curl -c cookies.txt -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"password123"}'

# 2. View saved cookies
cat cookies.txt

# 3. Authenticated request (load cookies)
curl -b cookies.txt http://localhost:8080/api/auth/me

# 4. Logout (update cookies)
curl -b cookies.txt -c cookies.txt -X POST http://localhost:8080/api/auth/logout

# 5. Verify token is denied
curl -b cookies.txt http://localhost:8080/api/auth/me
# Should return 401 Unauthorized
```

### With Postman

1. **Login**:
   - Send POST to `/api/auth/login`
   - Postman automatically captures cookies from `Set-Cookie` header

2. **View Cookies**:
   - Click "Cookies" button (below Send button)
   - See `access_token` cookie

3. **Authenticated Requests**:
   - Cookies are automatically sent with subsequent requests
   - No need to manually set headers

4. **Logout**:
   - Send POST to `/api/auth/logout`
   - Cookie is automatically cleared

### With Browser DevTools

1. **Login via your app**

2. **Inspect Cookie**:
   - Open DevTools → Application → Cookies
   - Select your domain
   - Find `access_token` cookie

3. **Verify Cookie Attributes**:
   ```
   Name: access_token
   Value: eyJhbGc... (JWT token)
   Domain: localhost
   Path: /
   Expires: (8 hours from now)
   HttpOnly: ✓
   Secure: ✓ (if HTTPS)
   SameSite: Lax
   ```

4. **Test Expiration**:
   - Edit cookie expiration to past date
   - Refresh page
   - Should redirect to login

## Troubleshooting

### Cookie Not Being Set

**Symptoms**: No `access_token` cookie after login

**Possible Causes**:
1. `AUTH_COOKIE_SECURE=true` but using HTTP
2. CORS not configured correctly
3. Response missing `Set-Cookie` header
4. Browser blocking third-party cookies

**Solutions**:

```bash
# Check if cookie is in response
curl -i -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"pass"}'
# Look for: Set-Cookie: access_token=...

# For local HTTP, disable Secure flag
# In docker-compose.yml:
environment:
  AUTH_COOKIE_SECURE: "false"  # Only for local dev!

# Check CORS configuration
docker compose exec upload-service env | grep CORS
```

### Cookie Not Being Sent

**Symptoms**: Authenticated requests return 401, but cookie exists

**Possible Causes**:
1. Missing `credentials: 'include'` in fetch
2. Cookie domain mismatch
3. SameSite restriction
4. Cookie expired

**Solutions**:

```javascript
// ✅ Correct - include credentials
fetch(url, {
  credentials: 'include'
});

// ❌ Wrong - credentials not included
fetch(url);
```

```bash
# Check cookie domain
# In browser DevTools → Application → Cookies
# Domain should match request domain

# Check cookie expiration
# Should be 8 hours from login time
```

### 401 Unauthorized Errors

**Symptoms**: Requests return 401 even with valid cookie

**Possible Causes**:
1. Token expired (8 hours passed)
2. Token in denylist (user logged out)
3. JWT secret mismatch between services
4. Clock skew too large

**Solutions**:

```bash
# Check token expiration
# Decode JWT at https://jwt.io
# Check 'exp' claim

# Check if token is denied
TOKEN_ID="jti-from-jwt"
docker compose exec redis redis-cli get "denylist:jwt:$TOKEN_ID"
# If exists, token was revoked

# Verify JWT secrets match
docker compose exec api-gateway env | grep JWT_HS256_SECRET
docker compose exec upload-service env | grep JWT_HS256_SECRET
# Should be identical

# Check clock skew
docker compose exec api-gateway date
docker compose exec upload-service date
# Should be within 60 seconds
```

### Token Not Clearing on Logout

**Symptoms**: Cookie persists after logout

**Possible Causes**:
1. Cookie domain mismatch in logout
2. Cookie path mismatch
3. Browser not accepting cookie changes

**Solutions**:

```bash
# Check logout response
curl -i -b cookies.txt -c cookies.txt \
  -X POST http://localhost:8080/api/auth/logout
# Look for: Set-Cookie: access_token=; Max-Age=-1

# Verify cookie is cleared
cat cookies.txt
# access_token line should be gone or expired
```

### CORS Errors

**Symptoms**: Browser console shows CORS error

**Possible Causes**:
1. Origin not in `CORS_ALLOW_ORIGINS`
2. Credentials enabled but origin is wildcard
3. Missing CORS headers

**Solutions**:

```bash
# Check CORS configuration
docker compose exec api-gateway env | grep CORS

# Test CORS preflight
curl -i -X OPTIONS http://localhost:8080/api/auth/me \
  -H "Origin: http://localhost:5173" \
  -H "Access-Control-Request-Method: GET"
# Look for: Access-Control-Allow-Origin: http://localhost:5173
# Look for: Access-Control-Allow-Credentials: true
```

## Migration Guide

### From localStorage Tokens to Cookies

**Before** (localStorage):
```javascript
// Login
const { accessToken } = await response.json();
localStorage.setItem('token', accessToken);

// Requests
fetch(url, {
  headers: {
    'Authorization': `Bearer ${localStorage.getItem('token')}`
  }
});

// Logout
localStorage.removeItem('token');
```

**After** (cookies):
```javascript
// Login
const { user } = await response.json();
// Token stored automatically in cookie

// Requests
fetch(url, {
  credentials: 'include'  // Browser handles token
});

// Logout
await fetch('/api/auth/logout', {
  method: 'POST',
  credentials: 'include'
});
```

### Breaking Changes

1. **No token in response body** - Don't look for `accessToken` field
2. **Must use `credentials: 'include'`** - Required for cookies
3. **No refresh endpoint** - Returns 410 Gone
4. **CORS must allow credentials** - Backend config required

## Security Audit Checklist

- [x] HTTP-only cookies prevent XSS
- [x] Secure flag enforces HTTPS in production
- [x] SameSite=Lax prevents CSRF
- [x] 8-hour token expiry limits exposure window
- [x] Token denylist enables immediate revocation
- [x] JWT secret is cryptographically strong (32+ chars)
- [x] JWT secret not in version control
- [x] Password hashing with bcrypt
- [x] Rate limiting on authentication endpoints
- [x] CORS configured correctly for production
- [x] Token validation on all protected routes
- [x] Clock skew handling (60s tolerance)
- [x] Backward compatibility with Bearer tokens

## Related Documentation

- [Upload Service](./UPLOAD_SERVICE.md) - File upload and job management
- [API Gateway](../api-gateway/API_GATEWAY.md) - Request routing and CORS
- [Main README](../README.md) - Overall architecture

## Support

For authentication issues:
- Check logs: `docker compose logs -f upload-service`
- Inspect Redis: `docker compose exec redis redis-cli keys "denylist:jwt:*"`
- Test manually: See [Testing](#testing) section above
