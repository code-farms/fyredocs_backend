# Auth Service -- Architecture

Internal structure and component diagram of the `auth-service` (port 8086).

## Component Diagram

```mermaid
graph TB
    subgraph auth-service[" auth-service :8086 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry · GinTraceMiddleware"]
            METRICS["Prometheus · GinMetricsMiddleware"]
            REQID["GinRequestID"]
            LOGGER["GinRequestLogger"]
            AUTHMW["GinAuthMiddleware<br/>(JWT verifier + token denylist)"]
        end

        subgraph Routes["Route Groups"]
            subgraph PublicAuth["/auth (public, rate-limited)"]
                SIGNUP["POST /signup (3/min)"]
                LOGIN["POST /login (5/min)"]
                REFRESH["POST /refresh (10/min)"]
                PLANS["GET /plans"]
            end

            subgraph ProtectedAuth["/auth (auth required)"]
                ME["GET /me"]
                PROFILE["GET /profile"]
                LOGOUT["POST /logout"]
                CHANGE_PLAN["PUT /plan"]
            end

            subgraph InternalRoutes["/internal (service-to-service)"]
                USER_PLAN["GET /users/:id/plan"]
                REVOKE_USER["POST /users/:id/revoke-sessions"]
                REVOKE_ONE["DELETE /sessions/:id"]
            end

            HEALTHZ["/healthz · /readyz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            AH["AuthEndpoints<br/>(Signup · Login · Refresh · Me · Profile · Logout · ChangePlan)"]
            AdminH["Admin Handlers<br/>(RevokeUserSessions · RevokeSession)"]
            PlanH["Plans Handler<br/>(GetAllPlans · GetUserPlan)"]
        end

        subgraph Internal
            ISSUER["Token Issuer<br/>HS256 · jti==sessionId<br/>access TTL 8h · refresh TTL 7d"]
            VERIFIER["Auth Verifier<br/>(JWT validation + denylist check)"]
            DENYLIST["TokenDenylist<br/>(Redis-backed)"]
        end

        subgraph RateLimiting["Rate Limiting (Redis SETEX)"]
            RL_LOGIN["ratelimit:login:&lt;ip&gt;"]
            RL_SIGNUP["ratelimit:signup:&lt;ip&gt;"]
            RL_REFRESH["ratelimit:refresh:&lt;ip&gt;"]
        end

        subgraph Models["internal/models (GORM)"]
            USER_MODEL["users (id, email, password_hash, plan_name, role, ...)"]
            SESSION_MODEL["user_sessions (id=jti, user_id, access_token_hash, refresh_token_hash, expiries)"]
            PLAN_MODEL["subscription_plans (free · pro · anonymous)"]
            META_MODEL["auth_metadata (provider, subject)"]
        end

        subgraph Background
            CLEANUP["Hourly ticker<br/>DeleteExpiredSessions"]
        end

        subgraph PlanCache["Plan cache (Redis)"]
            PCK["user:plan:&lt;userId&gt;<br/>{plan, max_file_mb, max_files}"]
        end

        subgraph AnalyticsPub["Analytics events (NATS)"]
            EV_SIGNUP["analytics.events.user.signup"]
            EV_LOGIN["analytics.events.user.login"]
            EV_PLAN["analytics.events.plan.changed"]
        end
    end

    Client["api-gateway / internal callers"] --> TRACE --> METRICS --> REQID --> LOGGER --> AUTHMW
    AUTHMW --> Routes

    PublicAuth --> AH
    ProtectedAuth --> AH
    ProtectedAuth --> PlanH
    InternalRoutes --> PlanH
    InternalRoutes --> AdminH

    AH --> ISSUER
    AH --> DENYLIST
    AH --> SESSION_MODEL
    AH --> USER_MODEL
    AH --> PLAN_MODEL
    AH --> PCK
    AH --> EV_SIGNUP
    AH --> EV_LOGIN
    AH --> EV_PLAN

    AdminH --> SESSION_MODEL
    AdminH --> DENYLIST

    PlanH --> USER_MODEL
    PlanH --> PLAN_MODEL

    USER_MODEL & SESSION_MODEL & PLAN_MODEL & META_MODEL --> PG[(PostgreSQL)]
    DENYLIST --> Redis[(Redis)]
    RateLimiting --> Redis
    PCK --> Redis

    EV_SIGNUP & EV_LOGIN & EV_PLAN --> NATS["NATS JetStream<br/>ANALYTICS"]

    CLEANUP --> SESSION_MODEL
```

## Token & Session Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Issued: POST /signup or /login
    Issued --> Stored: INSERT user_sessions (id=jti, hashes)
    Stored --> Active: Set HttpOnly access_token + refresh_token cookies
    Active --> Refreshed: POST /auth/refresh<br/>UPDATE user_sessions.access_token_hash
    Refreshed --> Active
    Active --> Denied: POST /auth/logout<br/>DELETE row + denylist:jwt:<hash>
    Active --> Expired: TTL exceeded (8h)
    Active --> Revoked: POST /internal/users/:id/revoke-sessions<br/>or DELETE /internal/sessions/:id
    Denied --> [*]
    Expired --> [*]
    Revoked --> [*]
    Stored --> Cleaned: hourly DeleteExpiredSessions<br/>(both access & refresh expired)
    Cleaned --> [*]
```

## Password Security Flow

```mermaid
flowchart TD
    A["User submits password"] --> B{"Length check"}
    B -->|< 8 chars| C["400: WEAK_PASSWORD"]
    B -->|> 128 chars| D["400: INVALID_INPUT"]
    B -->|8-128 chars| E["bcrypt.GenerateFromPassword (DefaultCost)"]
    E --> F["INSERT users.password_hash"]

    G["User logs in"] --> H["bcrypt.CompareHashAndPassword"]
    H -->|Match| I["IssueAccessToken + IssueRefreshToken<br/>+ INSERT user_sessions"]
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
    AS --> |shared/natsconn + shared/queue| NATSPub

    AS --> |internal/authverify| AuthVerify
    AS --> |internal/models| Models
    AS --> |internal/token| TokenIssuer

    Models --> |gorm| PG[(PostgreSQL)]
    AuthVerify --> |go-redis/v9| Redis[(Redis)]
    AuthVerify --> |golang-jwt/jwt/v5| JWT
    AS --> |golang.org/x/crypto/bcrypt| BCrypt
    NATSPub --> |nats-io/nats.go + jetstream| NATS["NATS JetStream"]
```
