# API Gateway -- Sequence Diagrams

Request flows through the `api-gateway` service.

## Authenticated Request to Job Service

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Redis
    participant JobSvc as job-service :8081

    Client->>GW: POST /api/convert-from-pdf/pdf-to-word<br/>(Cookie: access_token=<JWT>)

    Note over GW: Trace + Metrics + RequestID middleware

    Note over GW: CORS check<br/>Validate Origin against CORS_ALLOW_ORIGINS

    GW->>GW: Parse JWT (HS256)<br/>Extract userID, role, exp

    GW->>Redis: Check token denylist<br/>GET deny:<jti>
    Redis-->>GW: nil (not denied)

    Note over GW: AuthContext populated<br/>{UserID, Role, IsGuest: false}

    GW->>GW: Clear downstream auth headers<br/>Set X-User-ID, X-Role, X-Auth-Type

    GW->>JobSvc: POST /api/convert-from-pdf/pdf-to-word<br/>(X-User-ID: <uuid>, X-Role: user)

    JobSvc-->>GW: 201 Created {job}
    GW-->>Client: 201 Created {job}
```

## Guest (Unauthenticated) Request

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Redis
    participant JobSvc as job-service :8081

    Client->>GW: POST /api/uploads/init<br/>(No auth header, X-Guest-Token: <token>)

    Note over GW: No Bearer token found

    GW->>Redis: Validate guest token<br/>EXISTS guest:<token>:jobs
    Redis-->>GW: 1 (valid)

    Note over GW: AuthContext populated<br/>{GuestToken, IsGuest: true}

    GW->>GW: Set X-Guest-Token header

    GW->>JobSvc: POST /api/uploads/init<br/>(X-Guest-Token: <token>)

    JobSvc-->>GW: 201 Created {uploadId}
    GW-->>Client: 201 Created {uploadId}
```

## CORS Preflight

```mermaid
sequenceDiagram
    participant Browser
    participant GW as api-gateway :8080

    Browser->>GW: OPTIONS /api/uploads/init<br/>Origin: https://app.fyredocs.com

    Note over GW: withCORS middleware

    GW->>GW: Check Origin against allowed list
    GW->>GW: Set Access-Control-Allow-Origin
    GW->>GW: Set Access-Control-Allow-Methods
    GW->>GW: Set Access-Control-Allow-Headers
    GW->>GW: Set Access-Control-Allow-Credentials

    GW-->>Browser: 204 No Content<br/>(CORS headers)
```

## Health Check

```mermaid
sequenceDiagram
    participant LB as Load Balancer
    participant GW as api-gateway :8080
    participant Redis

    LB->>GW: GET /healthz

    GW->>Redis: PING (2s timeout)
    Redis-->>GW: PONG

    GW-->>LB: 200 {"status": "healthy"}

    Note over GW: If Redis PING fails:

    GW->>Redis: PING (2s timeout)
    Redis-->>GW: timeout/error

    GW-->>LB: 503 {"status": "unhealthy", "redis": "..."}
```
