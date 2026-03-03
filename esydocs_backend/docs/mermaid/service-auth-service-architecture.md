# Auth Service -- Architecture

Internal structure and component diagram of the `auth-service` (port 8086).

## Component Diagram

```mermaid
graph TB
    subgraph auth-service[" auth-service :8086 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry<br/>GinTraceMiddleware"]
            METRICS["Prometheus<br/>GinMetricsMiddleware"]
            REQID["Request ID"]
            LOGGER["Request Logger"]
            AUTHMW["Auth Middleware<br/>(JWT + Guest)"]
        end

        subgraph Routes["Route Groups"]
            subgraph AuthRoutes["/auth"]
                SIGNUP["POST /signup"]
                LOGIN["POST /login"]
                REFRESH["POST /refresh<br/>(deprecated)"]
                ME["GET /me"]
                PROFILE["GET /profile"]
                LOGOUT["POST /logout"]
            end

            subgraph InternalRoutes["/internal"]
                USER_PLAN["GET /users/:id/plan"]
            end

            HEALTHZ["/healthz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            AH["AuthEndpoints<br/>(Signup, Login, Logout, Me, Profile)"]
            IH["Internal API<br/>(GetUserPlan)"]
        end

        subgraph Internal
            ISSUER["Token Issuer<br/>(HS256 JWT)"]
            VERIFIER["Auth Verifier<br/>(JWT validation)"]
            DENYLIST["Token Denylist<br/>(Redis-backed)"]
            GUESTSTORE["Guest Store<br/>(Redis-backed)"]
        end

        subgraph RateLimiting["Rate Limiting (Redis)"]
            RL_LOGIN["login: 5 req/min"]
            RL_SIGNUP["signup: 3 req/min"]
            RL_REFRESH["refresh: 10 req/min"]
        end

        subgraph Models["internal/models"]
            USER_MODEL["User model<br/>(id, email, password_hash,<br/>full_name, phone, country, image_url)"]
            DB_CONN["Database Connection<br/>(GORM + PostgreSQL)"]
        end
    end

    Client["api-gateway"] --> TRACE

    AuthRoutes --> AH
    InternalRoutes --> IH

    AH --> ISSUER
    AH --> DENYLIST
    AH --> DB_CONN
    IH --> DB_CONN

    DB_CONN --> PG[(PostgreSQL)]
    DENYLIST --> Redis[(Redis)]
    GUESTSTORE --> Redis
    RateLimiting --> Redis
```

## Token Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Issued: POST /signup or POST /login
    Issued --> Active: Set in HttpOnly cookie
    Active --> Verified: api-gateway validates on each request
    Active --> Denied: POST /logout<br/>(added to denylist)
    Active --> Expired: TTL exceeded (8h default)
    Denied --> [*]: Rejected on next request
    Expired --> [*]: Rejected on next request
```

## Password Security Flow

```mermaid
flowchart TD
    A["User submits password"] --> B{"Length check"}
    B -->|< 8 chars| C["400: WEAK_PASSWORD"]
    B -->|> 128 chars| D["400: INVALID_INPUT"]
    B -->|8-128 chars| E["bcrypt.GenerateFromPassword<br/>(DefaultCost)"]
    E --> F["Store password_hash in PostgreSQL"]

    G["User logs in"] --> H["bcrypt.CompareHashAndPassword"]
    H -->|Match| I["Issue JWT access token"]
    H -->|No match| J["401: INVALID_CREDENTIALS"]
```

## Dependency Graph

```mermaid
graph LR
    AS[auth-service] --> |shared/config| Config
    AS --> |shared/logger| Logger
    AS --> |shared/metrics| Metrics
    AS --> |shared/telemetry| Telemetry
    AS --> |shared/response| Response

    AS --> |internal/authverify| AuthVerify
    AS --> |internal/models| Models
    AS --> |internal/token| TokenIssuer

    Models --> |gorm| PG[(PostgreSQL)]
    AuthVerify --> |go-redis/v9| Redis[(Redis)]
    AuthVerify --> |golang-jwt/jwt/v5| JWT
    AS --> |golang.org/x/crypto/bcrypt| BCrypt
```
