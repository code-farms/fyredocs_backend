# Notification Service

## Service Responsibility
Owns the user **in-app notification feed**. It consumes job-completion events from NATS and turns them into per-user notifications, and serves the read/mark-read API behind the topbar bell.

## Design Constraints
- Independent microservice: own Postgres schema, own module (`notification-service`), no cross-service DB access or shared models.
- Stateless HTTP; identity from the gateway-injected `X-User-ID` header.
- Standard response envelope (`shared/response`).

## Internal Architecture
- `main.go` — config/logger/telemetry, DB connect+migrate, NATS connect + `EnsureStreams`, the finalize **subscriber**, gin + metrics, graceful shutdown. Port `8091`.
- `subscriber/` — durable consumer `notification-job-events` on `JOBS_EVENTS`; `JobCompleted` → "Processing complete", `JobFailed` → "Processing failed" (authenticated users only; guests skipped). Idempotent on `(user_id, source_job_id)`.
- `handlers/` — `notifications.go` (list + unread count, mark-read, mark-all-read), `health.go`.
- `internal/models/` — `database.go`, `notification.go`.

## Routes
Health: `GET /healthz`, `GET /readyz`. All `/api/notifications/*` require `X-User-ID`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/notifications` | Recent notifications (max 50) + `unreadCount`. |
| POST | `/api/notifications/read-all` | Mark all of the caller's notifications read. |
| POST | `/api/notifications/:id/read` | Mark one notification read. |
| GET | `/api/notifications/stream` | **SSE** stream of new notifications (live bell). |
| POST | `/internal/notifications` | **Mesh-only** (not gateway-proxied): other services raise a notification. Body: `userId`, `title`, `type?`, `body?`, `link?`, `sourceId?` (idempotency). Used by document-service for `export.ready`. |

## DB Schema (own Postgres)
- **notifications**: id (uuid v7), user_id, type (`job.completed`|`job.failed`|…), title, body, link, source_job_id?, read_at?, created_at. Index `(user_id, created_at DESC)`; partial unique `(user_id, source_job_id)` for idempotency.

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8091 | Service port |
| DATABASE_URL | — | PostgreSQL connection string (required) |
| NATS_URL | nats://nats:4222 | NATS for the event subscriber |
| TRUSTED_PROXIES | — | Trusted proxy CIDRs |
| OTEL_EXPORTER_OTLP_ENDPOINT | — | OpenTelemetry collector |

## Gateway / Deployment
- Gateway routes `/api/notifications` → `NOTIFICATION_SERVICE_URL` (`http://notification-service:8091`); `api-gateway` depends on it.
- `docker-compose.yml` has a `notification-service` block; in `go.work` + every service Dockerfile's go.mod copy list.

## Roadmap
- Email + webhook channels (a dedicated `NOTIFY` stream consumed for fan-out).
- More event types: export ready, share received, plan-limit, security events.
- Real-time push to the SPA via the gateway SSE channel (instead of polling).

## Performance
- `GET /api/notifications` returns the recent items and the unread count. These
  are two **independent** queries, dispatched concurrently
  (`handlers/notifications.go`), so the handler costs ~one DB round-trip instead
  of two — meaningful because the database is remote. Each goroutine writes its
  own result variable and builds a fresh statement off `models.DB`.
