# Analytics Service

## Service Responsibility
The analytics-service collects, stores, and aggregates business metrics for the Fyredocs platform. It provides admin API endpoints for dashboards covering user growth, tool usage, plan distribution, and real-time operational metrics.

## Design Constraints
- Fully independent microservice with its own database tables
- Receives events via NATS JetStream (no direct DB access to other services)
- Admin endpoints protected by JWT role-based auth (`super-admin` role required, verified via `X-User-Role` header set by API gateway)
- Lightweight: designed for low overhead on the main request path (fire-and-forget NATS publish from other services)

## Internal Architecture

### Event Ingestion
The service subscribes to two NATS streams via durable JetStream consumers:
1. **ANALYTICS stream** (`analytics.events.>`) — Custom analytics events published by auth-service and job-service
2. **JOBS_EVENTS stream** (`jobs.events.>`) — Job lifecycle events (progress, completed, failed) from worker services

Both consumers use `DeliverPolicy: DeliverNewPolicy` — only events emitted **after** the analytics-service starts are persisted. Events that flowed through NATS before the service was up are intentionally dropped (no historical replay). This is a deliberate trade-off: the SSE event stream and analytics ingestion share the same `JOBS_EVENTS` interest stream, so a backlog of analytics work would otherwise compete for retention with active SSE consumers.

Events are persisted to the `analytics_events` PostgreSQL table for querying.

### Service start timestamp
`main.go` sets `handlers.ServiceStartTime = time.Now().UTC()` before the subscriber starts. This timestamp is reported by `/admin/metrics/system` so dashboards can compute uptime and detect "events older than ServiceStartTime are missing because they predate boot".

### Subscriber Lifecycle
`subscriber.Start` returns a `*Subscribers` handle that owns the JetStream `ConsumeContext` for both subscriptions. On SIGTERM the service calls `srv.Shutdown(ctx)` to drain in-flight HTTP requests, then `subs.Stop()` to halt the dispatcher goroutines, and finally lets the deferred `natsconn.Close()` drain the NATS connection. This ordering prevents events from being dispatched into DB writes after the connection has begun draining.

### Event Types
| Event Type | Source | Description |
|-----------|--------|-------------|
| `user.signup` | auth-service | New user registration |
| `user.login` | auth-service | User login |
| `plan.changed` | auth-service | User changed subscription plan (metadata: oldPlan, newPlan) |
| `job.created` | job-service | New processing job created (carries `jobId` for latency correlation) |
| `job.completed` | worker services (via JOBS_EVENTS) | Job finished successfully (includes UserID) |
| `job.failed` | worker services (via JOBS_EVENTS) | Job processing failed (includes UserID) |
| `plan.limit_hit` | job-service | User hit plan limit (file size or file count) |

## Routes

### Unified Dashboard (any authenticated user)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/dashboard?days=30` | Role-aware landing summary. Reads the gateway-set `X-User-Role` / `X-User-ID` headers and filters the payload **server-side** by role, so a single endpoint serves everyone and no separate `/admin/dashboard` route is needed. Computed entirely from this service's own `analytics_events` table. |

Behaviour by caller:
- **No `X-User-ID`** → `401 UNAUTHORIZED`. **`X-User-Role: guest`** → `403 FORBIDDEN`.
- **`admin` / `super-admin`** → `data.role: "admin"` with a system summary:
  `period`, `today` (signups, logins, dau, guestSessions, jobsCreated/Completed/Failed),
  `totalUsers`, `toolUsage` (top 10), `planDistribution`.
- **regular user** → `data.role: "user"` with personal KPIs scoped to their
  `user_id`: `jobs` (total/completed/failed), `bytesProcessed`, `toolUsage`,
  `recentActivity` (daily counts), `plan` (from `X-User-Plan`, fallback latest
  event), `memberSince`.

### Admin Endpoints (require `X-User-Role: super-admin` header)
| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin/metrics/overview` | Today's summary (signups, DAU, jobs, errors) |
| GET | `/admin/metrics/daily?from=YYYY-MM-DD&to=YYYY-MM-DD` | Daily aggregated metrics |
| GET | `/admin/metrics/tools?days=30` | Tool usage breakdown |
| GET | `/admin/metrics/users?days=90` | User growth over time |
| GET | `/admin/metrics/plans?days=30` | Plan distribution |
| GET | `/admin/metrics/realtime` | Last hour's metrics |
| GET | `/admin/metrics/events?eventType=&limit=&page=` | Raw events with pagination |
| GET | `/admin/metrics/business?days=30&inactiveDays=30` | Business metrics (signups, plan changes, churn, conversion) |
| GET | `/admin/metrics/growth?days=30` | Growth metrics (DAU/WAU/MAU, stickiness, activation, retention cohorts, funnel) |
| GET | `/admin/metrics/engagement?days=30` | Engagement metrics (tool trends, jobs/user, file sizes, guest vs registered, power users) |
| GET | `/admin/metrics/reliability?days=30` | Reliability metrics (success/failure rates, processing time p50/p95, tool errors, plan limit hits). Also returns `processingTimeTrend` (daily p50/p95/p99 latency, seconds) and `failureCategories` (daily failure counts bucketed into timeout/validation/processing/infrastructure/other from the `[ERROR_CODE]` prefix in `metadata.failureReason`). |
| GET | `/admin/metrics/system` | System health (ingestion rate, active users, processing lag, event breakdown). `eventsByType` is the last-hour distribution (grouped by `event_type`). |
| GET | `/admin/metrics/queues` | Queue status for the System page. NATS snapshot via `natsmon` (`streams[]` name/messages/bytes/consumers, `dispatchConsumers[]` on `JOBS_DISPATCH`, `dlq` `JOBS_DLQ` depth, `analyticsLag` = pending/ackPending on the `ANALYTICS` and `JOBS_EVENTS` consumers) plus `throughput` — hourly `analytics_events` buckets over 24h with `processed` (COUNT) and `failed` (`event_type='job.failed'`). `throughput.queued` is always `0`: queue depth is a live snapshot (see `dlq`/`analyticsLag`), not persisted as a timeseries — a future queue-depth rollup would fill it. Degrades gracefully (empty snapshot sections, non-null `throughput`, still HTTP 200) when NATS is unreachable. |
| GET | `/admin/metrics/nats` | NATS/JetStream health. Scrapes the NATS HTTP monitoring endpoint (`NATS_MONITOR_URL`, default `http://nats:8222`, internal-only): `/varz` for server info and `/jsz?streams=true&consumers=true` for stream/consumer detail. Returns `server` (status, connections, memoryMB, uptime, slowConsumers), `streams[]` (name, messages, bytes, firstSeq, lastSeq, consumerCount), `consumers[]` (stream, name, numPending, numAckPending, numRedelivered, numWaiting), and `summary` (totalStreams, totalConsumers, totalMessages, dlqDepth). Degrades gracefully to `status: "unreachable"` (still HTTP 200) when NATS monitoring is down. |
| GET | `/admin/metrics/server-performance` | Server performance (CPU, memory, storage, uptime, service availability, Go runtime per service). Also returns `servicesList`: a name-sorted, table-friendly array of per-service rows (name, status, uptime, goroutines, heapAllocMB, heapInuseMB, sysMB, goVersion, error?). |
| GET | `/admin/metrics/executive?days=30` | Executive overview: 8 KPIs (totalUsers, activeUsers, revenue, jobsCreated, successRate, apiRequests, apiErrorRate, activeServers) each with `current`, `previous`, and a daily `sparkline`. `revenue` is ESTIMATED (see `/revenue`); `apiRequests`/`apiErrorRate` are null until the metrics sampler is deployed. |
| GET | `/admin/metrics/revenue?days=30` | **Estimated** revenue from the active plan distribution × the configured `PLAN_PRICES` map (no billing integration). Returns `mrr`, `arr`, `previousMrr`, `byPlan`, a daily `trend`, `planChanges` (upgrades/downgrades ranked by price), `prices`, `currency`, and `estimated: true`. |
| GET | `/admin/metrics/acquisition?days=30` | Signup counts grouped by acquisition channel (organic/referral/paid/campaign/direct/unknown), classified from referrer/UTM in `user.signup` metadata. Returns `channels` (with percent), `daily`, `topReferrers`, and a `previous` period comparison. Signups without referrer/UTM metadata bucket as `unknown`. |
| GET | `/admin/metrics/api-performance` | API performance (per-endpoint latency p50/p95/p99, throughput, error rates, slowest/most-erroring endpoints). Supports query params: `page` (default 1), `limit` (default 50, max 200), `search` (partial path match), `method` (exact HTTP method), `sortBy` (requests\|avgLatencyMs\|p50LatencyMs\|p95LatencyMs\|p99LatencyMs\|errorRate\|path\|method), `sortDir` (asc\|desc). Returns paginated endpoints with `meta` containing `page`, `limit`, `total`. |

### Infrastructure
| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Liveness probe |
| GET | `/readyz` | Readiness probe (checks PostgreSQL) |
| GET | `/metrics` | Prometheus metrics endpoint |

## DB Schema

### analytics_events
| Column | Type | Description |
|--------|------|-------------|
| id | UUID | Primary key |
| event_type | TEXT | Event type (indexed) |
| user_id | UUID | Optional user ID (indexed) |
| job_id | UUID | Optional job ID (indexed). Set on `job.created` (and job lifecycle events) so `job.created`↔`job.completed` can be joined to compute processing-duration percentiles. |
| is_guest | BOOLEAN | Whether the user was a guest |
| tool_type | TEXT | Tool used (indexed) |
| plan_name | TEXT | User's plan at time of event |
| file_size | BIGINT | File size in bytes |
| metadata | JSONB | Additional event data |
| created_at | TIMESTAMP | Event timestamp (indexed) |
| persisted_at | TIMESTAMP | When the event was written to DB (for lag measurement) |

### Composite Indexes
| Index | Columns | Purpose |
|-------|---------|---------|
| idx_event_user_type_created | (user_id, event_type, created_at) | Retention cohort and activation queries |
| idx_event_created_user | (created_at, user_id) WHERE user_id IS NOT NULL AND is_guest = false | DAU/WAU/MAU distinct user counts |
| idx_event_metadata_jobid | (metadata->>'jobId') WHERE metadata->>'jobId' IS NOT NULL | Processing time correlation |

### daily_metrics
| Column | Type | Description |
|--------|------|-------------|
| id | UUID | Primary key |
| date | DATE | Metric date (unique with metric_name) |
| metric_name | TEXT | Metric identifier |
| metric_value | FLOAT8 | Aggregated value |
| dimensions | JSONB | Optional breakdown dimensions |
| created_at | TIMESTAMP | Row creation time |
| updated_at | TIMESTAMP | Last update time |

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8087 | Service port |
| DATABASE_URL | — | PostgreSQL connection string |
| NATS_URL | nats://nats:4222 | NATS server URL |
| NATS_MONITOR_URL | http://nats:8222 | NATS HTTP monitoring endpoint (`-m 8222`), internal-only. Scraped by `/admin/metrics/nats`. |
| TRUSTED_PROXIES | 127.0.0.1,::1 | Trusted proxy addresses |
| SERVICE_URLS | api-gateway=http://api-gateway:8080,auth-service=http://auth-service:8086,job-service=http://job-service:8081,convert-from-pdf=http://convert-from-pdf:8082,convert-to-pdf=http://convert-to-pdf:8083,organize-pdf=http://organize-pdf:8084,optimize-pdf=http://optimize-pdf:8085 | Service name=URL pairs for performance scraping |
| API_GATEWAY_METRICS_URL | http://api-gateway:8080/metrics | API gateway Prometheus metrics URL |
| PLAN_PRICES | anonymous=0,free=0,pro=12 | Comma-separated `plan=monthlyPrice` pairs used to compute **estimated** revenue (MRR/ARR). No billing integration. |
| PLAN_CURRENCY | USD | Currency code reported with estimated revenue figures. |
| REDIS_ADDR | redis:6379 | Redis host:port (backs the dashboard response cache) |
| REDIS_PASSWORD | — | Redis password |
| REDIS_DB | 0 | Redis logical DB index |
| DASHBOARD_CACHE_TTL | 30s | TTL for the `/api/dashboard` response cache. `0` disables caching. |
| OTEL_EXPORTER_OTLP_ENDPOINT | http://localhost:4318 | OpenTelemetry collector |

## Authentication
Admin endpoints require the `super-admin` role. The API gateway verifies the JWT and sets `X-User-Role` header on proxied requests. The analytics service checks this header — no API keys or hardcoded credentials. To promote a user to super-admin, run:
```sql
UPDATE users SET role = 'super-admin' WHERE email = 'your@email.com';
```

## Scaling Constraints
- Single consumer per NATS durable subscription (horizontally scalable with consumer groups)
- PostgreSQL queries use indexes on event_type, user_id, tool_type, and created_at
- For high-traffic deployments, consider pre-aggregating into daily_metrics via a background cron job

## Performance
- `GET /api/dashboard` aggregates many independent counts/group-bys over
  `analytics_events`. These are dispatched **concurrently** via `parallelQueries`
  (`handlers/dashboard.go`) rather than sequentially, so the handler's latency is
  ~one DB round-trip instead of the sum of all of them. This matters most when
  the database is remote (a network round-trip dominates each query): the user
  dashboard collapses ~7 sequential round-trips and the admin dashboard ~10 into
  roughly one. Measured: user dashboard ~2000 ms → ~520 ms median.
- The closures in `parallelQueries` must each write only their own result
  variable (no shared mutable state); gorm's root `*DB` is safe for concurrent
  use as long as each query builds a fresh statement off `models.DB`.
- The DB pool (`main.go`) is sized to keep a full dashboard fan-out warm
  (MaxOpenConns 20 / MaxIdleConns 15) so concurrent queries don't pay a fresh
  connection's TLS handshake to a remote Postgres.
- **Dashboard response cache (Redis):** `GET /api/dashboard` responses are cached
  in Redis with a short TTL (`DASHBOARD_CACHE_TTL`, default 30s). Keys are
  `cache:dashboard:v1:user:<userID>:d<days>` (per user) and
  `cache:dashboard:v1:admin:d<days>` (system-wide). A cache hit serves the stored
  JSON without any DB access (~2–3 ms vs ~20–47 ms uncached, scaling with the
  user's event volume). Invalidation is TTL-only — no write-path coupling — so a
  dashboard may be up to TTL stale; this is acceptable for KPI tiles. Any Redis
  error degrades gracefully to a direct DB compute (the endpoint never 5xxes on a
  cache fault). It is the **only** API response cached; other reads were left
  direct because the co-located DB already makes them single-digit-ms.
