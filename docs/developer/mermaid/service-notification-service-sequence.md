# Notification Service -- Sequence Diagrams

Request flows through the `notification-service` (port 8091).

## Job Event → Notification → Live Bell

```mermaid
sequenceDiagram
    participant W as Worker
    participant JS as NATS JetStream (JOBS_EVENTS)
    participant SUB as notification subscriber
    participant PG as PostgreSQL
    participant CORE as NATS core (notify.<uid>)
    participant STREAM as SSE handler
    participant Client

    W->>JS: publish jobs.events.<jobId>.completed {userId, jobId, toolType}
    JS->>SUB: deliver (durable notification-job-events)
    SUB->>SUB: skip guests / non-terminal (ack)
    SUB->>PG: COUNT notifications WHERE (user_id, source_job_id)
    alt already exists
        SUB->>JS: Ack (idempotent)
    else new
        SUB->>PG: INSERT notification (type job.completed/job.failed, title, link)
        SUB->>CORE: publish notify.<userId> {notification}
        SUB->>JS: Ack
    end

    Note over Client,STREAM: browser has an open SSE stream
    CORE->>STREAM: notify.<userId> message
    STREAM-->>Client: event: notification\ndata: {...}
```

## List Notifications (bell open)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant NS as notification-service :8091
    participant PG as PostgreSQL

    Client->>GW: GET /api/notifications
    GW->>NS: Proxy (X-User-ID)
    par concurrent (one round-trip)
        NS->>PG: SELECT recent notifications (max 50) ORDER BY created_at DESC
    and
        NS->>PG: COUNT WHERE read_at IS NULL
    end
    NS-->>Client: 200 {notifications:[...], unreadCount}
```

## Open Live Stream (SSE)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant NS as notification-service :8091
    participant CORE as NATS core

    Client->>GW: GET /api/notifications/stream (Accept: text/event-stream)
    GW->>NS: Proxy (X-User-ID)
    alt NATS unavailable
        NS-->>Client: 500 NATS_UNAVAILABLE
    else ok
        NS->>CORE: SUBSCRIBE notify.<userId> (ephemeral)
        NS-->>Client: event: connected
        loop until disconnect
            CORE->>NS: notify.<userId> message
            NS-->>Client: event: notification\ndata: {...}
        end
    end
```

## Internal Create (mesh call, e.g. export.ready)

```mermaid
sequenceDiagram
    participant Svc as document-service (or other)
    participant NS as notification-service :8091
    participant PG as PostgreSQL
    participant CORE as NATS core

    Svc->>NS: POST /internal/notifications {userId, title, type, body?, link?, sourceId?}
    alt sourceId present and duplicate
        NS->>PG: exists WHERE (user_id, source_id)?
        NS-->>Svc: 200 "Notification already exists" (idempotent)
    else create
        NS->>PG: INSERT notification
        NS->>CORE: publish notify.<userId>
        NS-->>Svc: 201 "Notification created"
    end
```
