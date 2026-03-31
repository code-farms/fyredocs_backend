# Auth Service -- Sequence Diagrams

Request flows through the `auth-service`.

## User Signup

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant Redis
    participant PG as PostgreSQL

    Client->>GW: POST /auth/signup<br/>{"email", "password", "fullName", "country"}

    GW->>GW: CORS check
    GW->>AS: POST /auth/signup<br/>(proxied)

    Note over AS: Rate limit: 3 req/min per IP

    AS->>AS: Validate inputs<br/>email required, password 8-128 chars,<br/>fullName required, country required

    AS->>AS: normalizeEmail(email)<br/>(lowercase + trim)

    AS->>PG: SELECT * FROM users WHERE email = ?
    PG-->>AS: ErrRecordNotFound (user does not exist)

    AS->>AS: bcrypt.GenerateFromPassword(password, DefaultCost)

    AS->>PG: INSERT INTO users<br/>(email, full_name, phone, country, image_url, password_hash)
    PG-->>AS: User created (with generated UUID)

    AS->>AS: Issuer.IssueAccessToken(userId, "user")<br/>Generate HS256 JWT

    AS->>AS: Set access_token cookie<br/>(HttpOnly, Secure, SameSite=Lax, MaxAge=8h)

    AS-->>GW: 200 {user: {id, email, fullName, role: "user"}}
    GW-->>Client: 200 + Set-Cookie: access_token=<jwt>
```

## User Login

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant PG as PostgreSQL

    Client->>GW: POST /auth/login<br/>{"email": "user@example.com", "password": "secret123"}

    GW->>AS: POST /auth/login

    Note over AS: Rate limit: 5 req/min per IP

    AS->>AS: normalizeEmail("user@example.com")
    AS->>AS: Validate: email not empty, password not empty, <= 128 chars

    AS->>PG: SELECT * FROM users WHERE email = ?
    PG-->>AS: User {id, email, password_hash, ...}

    AS->>AS: bcrypt.CompareHashAndPassword(hash, password)
    Note over AS: Password matches

    AS->>AS: Issuer.IssueAccessToken(userId, "user")
    AS->>AS: Set access_token cookie

    AS-->>GW: 200 {user: {id, email, fullName, role}}
    GW-->>Client: 200 + Set-Cookie: access_token=<jwt>
```

## User Logout

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant Redis

    Client->>GW: POST /auth/logout<br/>(Cookie: access_token=<jwt>)

    GW->>GW: Verify JWT, populate auth context
    GW->>AS: POST /auth/logout<br/>(X-User-ID: <uuid>)

    AS->>AS: Extract auth context<br/>Verify user is authenticated

    AS->>AS: Extract access token from<br/>Authorization header or context

    AS->>AS: Parse token expiration (unverified)<br/>Calculate remaining TTL

    AS->>Redis: SET deny:<token_hash> EX <remaining_ttl>
    Note over Redis: Token added to denylist

    AS->>AS: Clear access_token cookie<br/>(Set-Cookie with MaxAge=-1)

    AS-->>GW: 204 No Content
    GW-->>Client: 204 + Set-Cookie: access_token=; Max-Age=-1
```

## Get Current User (Me)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant PG as PostgreSQL

    Client->>GW: GET /auth/me<br/>(Cookie: access_token=<jwt>)

    GW->>GW: Verify JWT
    GW->>Redis: Check denylist
    GW->>AS: GET /auth/me<br/>(X-User-ID: <uuid>, X-Role: user)

    AS->>AS: Auth middleware extracts context
    AS->>AS: Parse user ID from auth context

    AS->>PG: SELECT * FROM users WHERE id = <uuid>
    PG-->>AS: User {id, email, full_name, phone, country, image_url}

    AS-->>GW: 200 {user: {id, email, fullName, phone, country, image, role}}
    GW-->>Client: 200 {user}
```

## Failed Login (Wrong Password)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant PG as PostgreSQL

    Client->>GW: POST /auth/login<br/>{"email": "user@example.com", "password": "wrongpass"}

    GW->>AS: POST /auth/login

    AS->>PG: SELECT * FROM users WHERE email = ?
    PG-->>AS: User found

    AS->>AS: bcrypt.CompareHashAndPassword(hash, "wrongpass")
    Note over AS: Password does NOT match

    AS-->>GW: 401 {code: "INVALID_CREDENTIALS", message: "Invalid credentials"}
    GW-->>Client: 401
```

## Duplicate Signup

```mermaid
sequenceDiagram
    participant Client
    participant AS as auth-service :8086
    participant PG as PostgreSQL

    Client->>AS: POST /auth/signup<br/>{"email": "existing@example.com", ...}

    AS->>PG: SELECT * FROM users WHERE email = ?
    PG-->>AS: User found (already exists)

    AS-->>Client: 409 {code: "USER_ALREADY_EXISTS", message: "User already exists"}
```

## Guest Session Creation

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AS as auth-service :8086
    participant Redis

    Client->>GW: POST /auth/guest

    GW->>GW: CORS check
    GW->>AS: POST /auth/guest<br/>(proxied, no auth required)

    Note over AS: Rate limit: 20 req/min per IP

    AS->>AS: uuid.New() → guest_token

    AS->>Redis: SET guest:{token}:jobs "1" EX 86400
    Redis-->>AS: OK

    AS-->>GW: 200 {guest_token, expires_in: 86400}
    GW-->>Client: 200 {guest_token, expires_in: 86400}

    Note over Client: Store token in localStorage<br/>Send as X-Guest-Token header on API calls

    Client->>GW: POST /api/organize-pdf/merge-pdf<br/>X-Guest-Token: {token}

    GW->>Redis: EXISTS guest:{token}:jobs
    Redis-->>GW: 1 (valid)

    GW->>GW: Set X-User-Role: guest<br/>Forward to job-service
```
