# Notification Service -- Architecture

Internal structure and component diagram of the `notification-service` (port 8091). Owns the in-app notification feed; consumes job events from NATS and pushes live updates over SSE.

## Component Diagram

```mermaid
graph TB
    subgraph notification-service[" notification-service :8091 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry · GinTraceMiddleware"]
            METRICS["Prometheus · GinMetricsMiddleware"]
            REQID["GinRequestID"]
            LOGGER["GinRequestLogger"]
            REQUSER["RequireUser (X-User-ID)"]
        end

        subgraph Routes["Routes"]
            subgraph UserAPI["/api/notifications (auth-required)"]
                NLIST["GET / (recent + unreadCount)"]
                NSTREAM["GET /stream (SSE)"]
                NREADALL["POST /read-all"]
                NREAD["POST /:id/read"]
            end
            INTERNAL["POST /internal/notifications<br/>(mesh-only, not gateway-proxied)"]
            HEALTHZ["/healthz · /readyz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            NH["notifications.go<br/>(List · MarkRead · MarkAllRead · StreamNotifications · CreateInternal)"]
        end

        subgraph Subscriber["subscriber/ (NATS)"]
            SUB["notification-job-events<br/>durable · JOBS_EVENTS · filter jobs.events.><br/>JobCompleted/JobFailed → notification<br/>idempotent on (user_id, source_job_id)"]
        end

        subgraph Models["internal/models (GORM)"]
            NOTIF["notifications (id, user_id, type, title, body,<br/>link, source_job_id?, read_at?, created_at)"]
        end
    end

    Client["api-gateway :8080"] --> TRACE --> METRICS --> REQID --> LOGGER --> REQUSER
    REQUSER --> UserAPI
    Mesh["document-service / other services"] --> INTERNAL
    UserAPI --> NH
    INTERNAL --> NH
    NH --> NOTIF

    SUB --> NOTIF
    SUB -->|"publish notify.&lt;userId&gt; (core NATS)"| CORE["NATS core subject notify.&lt;uid&gt;"]
    CORE -->|"live push"| NSTREAM

    NOTIF --> PG[(PostgreSQL)]
    SUB --> JS["NATS JetStream<br/>JOBS_EVENTS"]
```

## Dependency Graph

```mermaid
graph LR
    NS[notification-service] --> |shared/config| Config
    NS --> |shared/logger| Logger
    NS --> |shared/metrics| Metrics
    NS --> |shared/telemetry| Telemetry
    NS --> |shared/response| Response
    NS --> |shared/natsconn + shared/queue| NATSlib
    NS --> |internal/models| Models
    Models --> |gorm + pgx| PG[(PostgreSQL)]
    NATSlib --> |nats-io/nats.go + jetstream| NATS["NATS (JetStream JOBS_EVENTS + core notify.*)"]
```
