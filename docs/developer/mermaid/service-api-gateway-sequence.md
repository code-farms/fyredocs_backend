# API Gateway -- Sequence Diagrams

Request flows through the `api-gateway` service (port 8080).

## Authenticated Request — proxied to job-service

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Redis
    participant JobSvc as job-service :8081

    Client->>GW: POST /api/convert-from-pdf/pdf-to-word (Cookie: access_token=<JWT>)
    Note over GW: telemetry · metrics · requestID
    Note over GW: withSecurityHeaders + withCORS (allowed)

    GW->>GW: Parse JWT (HS256) · validate iss/aud/exp/clock-skew
    GW->>Redis: GET denylist:jwt:<hash>
    alt denied
        GW-->>Client: 401 UNAUTHORIZED
    else valid
        GW->>Redis: GET user:plan:<userId>  (ResolvePlan)
        Note over GW: Default to free plan if missing
        GW->>GW: ClearUserHeaders + ApplyUserHeaders<br/>(X-User-ID, X-Role, X-User-Plan, X-Plan-Max-File-MB, X-Plan-Max-Files)
        GW->>GW: withMaxBodySize 1 MiB (all service routes)
        GW->>JobSvc: Proxy with FlushInterval=-1
        JobSvc-->>GW: 201 {job}
        GW-->>Client: 201 {job}
    end
```

## Guest (no auth) → cookie issuance + scoped access

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Redis
    participant JobSvc as job-service :8081

    Client->>GW: POST /api/upload/init (no auth)
    Note over GW: No Authorization header, no access_token cookie
    alt has guest_token cookie
        GW->>Redis: EXISTS guest:<token>:jobs
        alt unknown / expired
            GW->>GW: Issue NEW guest_token cookie (HttpOnly, Secure)
            GW->>Redis: SET guest:<token>:jobs
        end
    else no guest_token
        GW->>GW: Generate new guest UUID; Set-Cookie guest_token=<uuid>
        GW->>Redis: SET guest:<token>:jobs
    end
    Note over GW: AuthContext IsGuest=true · X-Guest-Token forwarded
    GW->>JobSvc: Proxy → /api/uploads/init (path rewritten, JSON ≤ 1 MiB)
    JobSvc-->>GW: 201 {uploadId, presigned URLs}
    GW-->>Client: 201 {uploadId, presigned URLs} + Set-Cookie guest_token (when newly issued)
```

## Presigned Object Traffic — MinIO bucket proxy

```mermaid
sequenceDiagram
    participant Browser
    participant GW as api-gateway :8080
    participant M as MinIO :9000 (internal only)

    Note over Browser: presigned URL from job-service,<br/>signed for the GATEWAY origin (S3_PUBLIC_ENDPOINT)

    Browser->>GW: PUT /fyredocs-uploads/uploads/&lt;id&gt;/&lt;file&gt;?partNumber=N&X-Amz-Signature=...
    Note over GW: root mux matches bucket prefix BEFORE CORS/auth —<br/>signature is the credential
    Note over GW: Director: path verbatim (no strip),<br/>req.Host = original Host (SigV4 signs it),<br/>identity headers stripped
    GW->>M: relay bytes (minioTransport, MaxIdleConnsPerHost=50, FlushInterval=-1)
    M->>M: recompute SigV4 against received Host + path
    M-->>GW: 200 + ETag
    GW-->>Browser: 200 + ETag

    Browser->>GW: GET /fyredocs-outputs/jobs/&lt;jobId&gt;/&lt;file&gt;?X-Amz-Signature=...
    GW->>M: relay
    M-->>Browser: object bytes (streamed, no buffering)
```

## Refresh Token Rotation — passes through to auth-service

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Auth as auth-service :8086

    Client->>GW: POST /auth/refresh (Cookie: refresh_token, Path=/auth)
    Note over GW: /auth/refresh in PublicPaths — auth middleware skipped
    GW->>Auth: Proxy
    Auth-->>GW: 200 + new Set-Cookie access_token
    GW-->>Client: forward
```

## SSE Stream — long-lived proxy

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant Redis
    participant JobSvc as job-service :8081

    Client->>GW: GET /api/jobs/<jobId>/events (Accept: text/event-stream)
    GW->>GW: auth + ResolvePlan (Redis)
    GW->>JobSvc: Proxy with FlushInterval=-1<br/>(events stream immediately, no buffering)
    loop until client disconnect or 5m server cap
        JobSvc-->>GW: SSE events
        GW-->>Client: passthrough
    end
```

## CORS Preflight

```mermaid
sequenceDiagram
    participant Browser
    participant GW as api-gateway :8080

    Browser->>GW: OPTIONS /api/uploads/init · Origin: https://app.fyredocs.com
    Note over GW: withCORS middleware (preflight short-circuit)
    GW->>GW: Match origin against CORS_ALLOW_ORIGINS
    alt allowed
        GW-->>Browser: 204 + Access-Control-Allow-{Origin,Methods,Headers,Credentials}
    else not allowed
        GW-->>Browser: 204 (no CORS headers — browser blocks the actual request)
    end
```

## SPA Static Hosting

```mermaid
sequenceDiagram
    participant Browser
    participant GW as api-gateway :8080
    participant FS as SPA_DIR

    Browser->>GW: GET /pdf-to-word (no /api/* prefix)
    Note over GW: ServeMux falls through to spaFileServer when SPA_DIR is set
    GW->>FS: Open /pdf-to-word
    alt file exists
        FS-->>GW: bytes (with Cache-Control for /assets/*)
        GW-->>Browser: 200
    else missing (SPA route)
        GW->>FS: Serve /index.html
        FS-->>GW: index.html
        GW-->>Browser: 200 (client-side routing takes over)
    end
```

## Health Check

```mermaid
sequenceDiagram
    participant LB as Probe
    participant GW as api-gateway :8080
    participant Redis

    LB->>GW: GET /healthz
    GW->>Redis: PING (2s timeout)
    alt Redis up
        GW-->>LB: 200 {"status":"healthy"}
    else Redis down/timeout
        GW-->>LB: 503 {"status":"unhealthy","redis":"..."}
    end
```
