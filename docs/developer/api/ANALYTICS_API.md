# Analytics & Dashboard API (analytics-service)

Base URL (via gateway): `http://localhost:8080`

The gateway forwards `/api/dashboard` and `/admin/*` to `analytics-service:8087`. All responses use the standard envelope `{success, message, data, error, meta}`. The service computes everything from its own event store (`analytics_events`, `daily_metrics`) populated by the NATS subscribers — there is no separate ingestion endpoint.

**Auth:**
- `/api/dashboard` — any authenticated user (role-aware). Guests are rejected (`403`).
- `/admin/metrics/*` — **super-admin only** (`adminAuth()` reads the gateway-set `X-User-Role`).

---

## GET /api/dashboard
Unified, role-aware landing endpoint. The server filters the payload by role:
- `admin` / `super-admin` → `data.role: "admin"` with system-wide KPIs.
- regular user → `data.role: "user"` with personal KPIs scoped to their `user_id`.
- guest → `403`.

| Query param | Default | Description |
|-------------|---------|-------------|
| `days` | `30` | Look-back window in days |

Cached in Redis for `DASHBOARD_CACHE_TTL` (default 30s), keyed by audience + window (admin payload is shared across all admins). **200 OK** — `data: {role, ...}`.

---

## Admin Metrics (super-admin)

All are `GET` under `/admin/metrics/`, most accept `?days=<n>`. `403 FORBIDDEN` if the caller is not super-admin.

| Path | Description |
|------|-------------|
| `/admin/metrics/overview` | Today's summary |
| `/admin/metrics/daily` | Daily aggregated metrics (`from`/`to`) |
| `/admin/metrics/tools` | Tool usage breakdown |
| `/admin/metrics/users` | User growth over time |
| `/admin/metrics/plans` | Plan distribution |
| `/admin/metrics/realtime` | Last-hour metrics |
| `/admin/metrics/events` | Raw event pagination (`limit`, `page`, `eventType`) |
| `/admin/metrics/business` | Signups, plan changes, churn, conversion |
| `/admin/metrics/growth` | DAU/WAU/MAU, cohorts, funnel (`inactiveDays`) |
| `/admin/metrics/engagement` | Tool trends, file sizes, power users |
| `/admin/metrics/reliability` | Success/failure rates, latency p50/p95/p99 |
| `/admin/metrics/system` | Ingestion rate, active users, event breakdown |
| `/admin/metrics/server-performance` | CPU, memory, uptime, Go runtime per service |
| `/admin/metrics/api-performance` | Per-endpoint latency p50/p95/p99, throughput (`page`, `limit`, `search`, `method`, `sortBy`, `sortDir`) |
| `/admin/metrics/executive` | 8 headline KPIs with sparklines |
| `/admin/metrics/revenue` | Estimated revenue from plan distribution (`PLAN_PRICES`, `PLAN_CURRENCY`) |
| `/admin/metrics/acquisition` | Signup channels (organic/referral/paid) |

---

## Error Codes
`FORBIDDEN` (non-super-admin on `/admin/*`, or guest on `/api/dashboard`), plus standard validation/internal codes.
