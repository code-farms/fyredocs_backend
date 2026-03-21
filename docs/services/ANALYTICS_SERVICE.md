# Analytics Service

## Service Responsibility
The analytics-service collects, stores, and aggregates business metrics for the EsyDocs platform. It provides admin API endpoints for dashboards covering user growth, tool usage, plan distribution, and real-time operational metrics.

## Design Constraints
- Fully independent microservice with its own database tables
- Receives events via NATS JetStream (no direct DB access to other services)
- Admin endpoints protected by JWT role-based auth (`super-admin` role required, verified via `X-User-Role` header set by API gateway)
- Lightweight: designed for low overhead on the main request path (fire-and-forget NATS publish from other services)

## Internal Architecture

### Event Ingestion
The service subscribes to two NATS streams:
1. **ANALYTICS stream** (`analytics.events.>`) — Custom analytics events published by auth-service and job-service
2. **JOBS_EVENTS stream** (`jobs.events.>`) — Job lifecycle events (completed, failed) from worker services

Events are persisted to the `analytics_events` PostgreSQL table for querying.

### Event Types
| Event Type | Source | Description |
|-----------|--------|-------------|
| `user.signup` | auth-service | New user registration |
| `user.login` | auth-service | User login |
| `plan.changed` | auth-service | User changed subscription plan (metadata: oldPlan, newPlan) |
| `job.created` | job-service | New processing job created |
| `job.completed` | worker services (via JOBS_EVENTS) | Job finished successfully (includes UserID) |
| `job.failed` | worker services (via JOBS_EVENTS) | Job processing failed (includes UserID) |
| `plan.limit_hit` | job-service | User hit plan limit (file size or file count) |

## Routes

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
| GET | `/admin/metrics/reliability?days=30` | Reliability metrics (success/failure rates, processing time p50/p95, tool errors, plan limit hits) |
| GET | `/admin/metrics/system` | System health (ingestion rate, active users, processing lag, event breakdown) |
| GET | `/admin/metrics/server-performance` | Server performance (CPU, memory, storage, uptime, service availability, Go runtime per service) |
| GET | `/admin/metrics/api-performance` | API performance (per-endpoint latency p50/p95/p99, throughput, error rates, slowest/most-erroring endpoints) |

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
| TRUSTED_PROXIES | 127.0.0.1,::1 | Trusted proxy addresses |
| SERVICE_URLS | api-gateway=http://api-gateway:8080,auth-service=http://auth-service:8086,job-service=http://job-service:8081,convert-from-pdf=http://convert-from-pdf:8082,convert-to-pdf=http://convert-to-pdf:8083,organize-pdf=http://organize-pdf:8084,optimize-pdf=http://optimize-pdf:8085,cleanup-worker=http://cleanup-worker:8088 | Service name=URL pairs for performance scraping |
| API_GATEWAY_METRICS_URL | http://api-gateway:8080/metrics | API gateway Prometheus metrics URL |
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
