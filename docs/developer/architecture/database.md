# Database Performance, Optimization & Locality

This document covers database best practices for the Fyredocs backend (completed
optimizations, future production recommendations, general patterns) followed by
the endpoint-latency investigation and the database-locality change that turned
out to be the single biggest lever.

**Stack**: PostgreSQL 18 (self-managed, co-located container — see
[Database Locality](#endpoint-latency--database-locality)) + GORM + Redis.

---

## Table of Contents

1. [Completed Optimizations](#completed-optimizations)
2. [Future Optimizations (Production)](#future-optimizations-production)
3. [General Best Practices](#general-best-practices)
4. [Endpoint Latency & Database Locality](#endpoint-latency--database-locality)

---

## Completed Optimizations

### 1. UUIDv7 Primary Keys

**Problem**: UUIDv4 generates random values that fragment B-tree indexes, causing page splits and degraded insert performance on high-volume tables.

**Solution**: Switched all `BeforeCreate` hooks from `uuid.New()` (v4) to `uuid.Must(uuid.NewV7())`. UUIDv7 embeds a Unix timestamp in the high bits, so new IDs are monotonically increasing — inserts always append to the end of the B-tree.

**Impact**:
- Eliminates random page splits during inserts
- Reduces index size by ~20-30% over time (denser leaf pages)
- Improves cache hit rates for recent data queries
- Natural chronological ordering (can sort by PK instead of `created_at`)

**Files**: All model files across `auth-service`, `job-service`, `analytics-service`

---

### 2. Composite Indexes Matching Query Patterns

**Problem**: Single-column GORM indexes (`index` tag) on individual fields force the query planner to use index intersection or fall back to sequential scans for multi-column queries.

**Solution**: Added composite indexes that exactly match the `WHERE` + `ORDER BY` patterns used in handlers:

| Index | Table | Covers Query Pattern |
|-------|-------|---------------------|
| `idx_job_user_tool_created (user_id, tool_type, created_at DESC)` | `processing_jobs` | `GetJobsByTool` — filter by user + tool, sort by date |
| `idx_filemeta_job_kind (job_id, kind)` | `file_metadata` | Download handler — find output file for a job |
| `idx_session_user_access_exp (user_id, access_expires_at)` | `user_sessions` | `RevokeAllUserSessions` — find active sessions for a user |
| `idx_session_access_refresh_exp (access_expires_at) WHERE refresh_expires_at IS NOT NULL` | `user_sessions` | `DeleteExpiredSessions` — partial index for cleanup |
| `idx_event_user_type_created (user_id, event_type, created_at)` | `analytics_events` | Growth metrics — DAU/WAU/MAU by user activity |
| `idx_event_created_user (created_at, user_id) WHERE ...` | `analytics_events` | Time-range queries filtered to registered users |
| `idx_event_job_type (job_id, event_type) WHERE job_id IS NOT NULL` | `analytics_events` | Processing time JOIN between created/completed events |

**Best practice**: Always create indexes that match your actual query patterns. The column order matters — put equality filters first, range/sort columns last.

**Files**: `auth-service/internal/models/database.go`, `job-service/internal/models/database.go`, `analytics-service/internal/models/database.go`

---

### 3. N+1 Query Elimination in the Cleanup Sweep

**Problem**: The cleanup sweep ran one `SELECT` per expired job to fetch file metadata (100 jobs = 100 queries), and one `SELECT COUNT(*)` per filesystem directory to check job existence.

**Solution**:
- **`cleanupExpiredJobs`**: Batch-fetch all files with `WHERE job_id IN (...)`, group in-memory by job ID, then batch-delete with `WHERE job_id IN (...)` and `WHERE id IN (...)`
- **`cleanupOrphanedDirs`**: Collect all candidate UUIDs from filesystem, single `SELECT id FROM processing_jobs WHERE id IN (...)`, compute orphans in-memory

**Impact**: Reduced from O(N) queries per batch to O(1) — typically 100x fewer database round-trips per cleanup cycle.

**File**: `job-service/internal/cleanup/cleanup.go` (runs in-process inside job-service; see [Job Service](../services/job-service.md#background-cleanup-loop))

---

### 4. Promoted `jobId` from JSONB to Real Column

**Problem**: The `analytics_events` table stored `jobId` inside a JSONB `metadata` column. The reliability handler joined `analytics_events` to itself on `metadata->>'jobId'` — a text extraction from JSONB that can't use standard B-tree indexes efficiently, forcing hash joins on potentially millions of rows.

**Solution**:
- Added `JobID *uuid.UUID` as a proper indexed column on `AnalyticsEvent`
- Subscriber now sets `JobID` directly when persisting job events
- Reliability handler JOINs on `job_id` (UUID) instead of `metadata->>'jobId'` (text)
- Replaced the JSONB expression index with `idx_event_job_type (job_id, event_type)`

**Impact**: Processing time queries (p50/p95 calculations) go from full table scan + hash join to index-only lookups.

**Files**: `analytics-service/internal/models/analytics.go`, `analytics-service/subscriber/subscriber.go`, `analytics-service/handlers/reliability.go`, `analytics-service/internal/models/database.go`

---

### 5. Foreign Key Constraints with CASCADE

**Problem**: No FK constraints existed anywhere, allowing orphaned rows to accumulate silently (e.g., sessions for deleted users, file metadata for deleted jobs).

**Solution**: Added GORM FK tags with `constraint:OnDelete:CASCADE`:

| Child Table | Parent Table | Effect |
|-------------|-------------|--------|
| `auth_metadata.user_id` | `users.id` | Deleting a user cascades to auth metadata |
| `user_sessions.user_id` | `users.id` | Deleting a user cascades to all sessions |
| `file_metadata.job_id` | `processing_jobs.id` | Deleting a job cascades to file metadata |

**Impact**: Database-level integrity guarantee. The cleanup sweep no longer needs to manually delete file metadata before jobs — CASCADE handles it.

**Files**: `auth-service/internal/models/user.go`, `auth-service/internal/models/token.go`, `job-service/internal/models/job.go`

---

### 6. CHECK Constraints on Enum-like Columns

**Problem**: Columns like `status` and `role` use `type:text` but accept a fixed set of values. Without constraints, invalid values can be inserted, and the query planner has no cardinality hints.

**Solution**: Added CHECK constraints:
- `processing_jobs.status`: `CHECK (status IN ('queued','processing','completed','failed'))`
- `users.role`: `CHECK (role IN ('user','admin'))`

**Impact**: Prevents bad data at the database level. The query planner can use the constraint to estimate cardinality more accurately, potentially choosing better execution plans.

**Files**: `job-service/internal/models/database.go`, `auth-service/internal/models/database.go`

---

### 7. Connection Pool Right-Sizing

**Problem**: All services (including workers that only make occasional single-row updates) used 25 max connections / 10 idle. With 8+ services, this meant up to 200 potential connections to one PostgreSQL instance.

**Solution**:
| Service Type | Before | After |
|-------------|--------|-------|
| Worker services (convert-from-pdf, convert-to-pdf, organize-pdf, optimize-pdf) | 25/10 | **5/2** |
| Auth service | 25/10 | 25/10 (unchanged — handles concurrent auth) |
| Job service (API + in-process cleanup sweep) | 25/10 | 50/25 (raised — presigned-upload concurrency) |
| Analytics service | 15/5 | 15/5 (unchanged — already tuned) |

**Impact**: Reduced total max connections well below the earlier ~200 ceiling. Directly reduces memory usage on PostgreSQL and avoids connection pool exhaustion on constrained environments.

**Files**: All worker `internal/models/database.go` files

---

### 8. Optimized RevokeAllUserSessions

**Problem**: `RevokeAllUserSessions` ran the same complex WHERE clause twice — once to `Find` sessions, once to `Delete` them. Two round-trips with identical work.

**Solution**: Single `DELETE FROM user_sessions WHERE ... RETURNING *` query that atomically deletes and returns the deleted rows.

**Impact**: Halved the database round-trips for logout-all operations. Also eliminates a potential race condition where a session could be created between the `Find` and `Delete`.

**File**: `auth-service/internal/models/token.go`

---

## Future Optimizations (Production)

### P0 — High Priority

#### Separate Databases Per Service Domain
**Why**: Currently all services share one PostgreSQL instance via the same `DATABASE_URL`. This creates noisy-neighbor problems (analytics queries starving job writes), shared connection pool pressure, and cross-service blast radius for bad migrations.

**Recommendation**: Use 3 separate databases:
- `fyredocs_auth` — users, sessions, auth metadata, subscription plans
- `fyredocs_jobs` — processing jobs, file metadata
- `fyredocs_analytics` — analytics events, daily metrics

**Benefits**: Independent scaling, isolated connection pools, safe migrations, aligns with microservice data ownership principles.

#### Table Partitioning on `analytics_events`
**Why**: This is an append-only, ever-growing table. Without partitioning, queries on date ranges scan the entire table, and old data can't be efficiently dropped.

**Recommendation**: PostgreSQL native range partitioning by month on `created_at`:
```sql
CREATE TABLE analytics_events (
    ...
) PARTITION BY RANGE (created_at);

CREATE TABLE analytics_events_2026_01 PARTITION OF analytics_events
    FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
```

**Benefits**: Date-range queries only scan relevant partitions. Old partitions can be `DROP`ped instantly (vs. slow `DELETE`). Vacuum and index maintenance operate per-partition.

#### Data Retention Policy for `analytics_events`
**Why**: Without retention, storage grows indefinitely. At 100K events/day with JSONB metadata, this accumulates ~1.5GB/month.

**Recommendation**:
- Keep raw events for 90 days
- Pre-aggregate older data into `daily_metrics` via a nightly cron job
- Drop partitions older than 90 days
- Archive to cold storage (S3/GCS) if compliance requires it

---

### P1 — Medium Priority

#### Pre-aggregate More Metrics into `daily_metrics`
**Why**: Many analytics dashboard endpoints still query raw `analytics_events` with GROUP BY. The `daily_metrics` table exists but is underutilized.

**Recommendation**: Add a scheduled job (cron or NATS timer) that runs nightly:
- DAU/WAU/MAU counts
- Tool usage breakdown
- Success/failure rates per tool
- Processing time percentiles

Dashboard endpoints should prefer `daily_metrics` over raw events for historical data (>24h old).

#### Query Plan Monitoring with `pg_stat_statements`
**Why**: You can't optimize what you can't measure. Slow queries often go unnoticed until they cause timeouts.

**Recommendation**:
```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```
Periodically review:
- `pg_stat_statements` — top queries by total time, calls, mean time
- `pg_stat_user_indexes` — unused indexes (wasting write overhead)
- `pg_stat_user_tables` — sequential scan ratios (missing indexes)

Set up alerts for queries exceeding p95 thresholds.

#### Centralized Migration Runner
**Why**: Currently every service runs `AutoMigrate` on boot. DDL through connection poolers in transaction mode can cause issues. Multiple services racing to migrate the same tables adds unnecessary startup complexity.

**Recommendation**:
- Create a standalone `migrate` CLI tool or CI pipeline step
- Run migrations once before deploying new service versions
- Remove `AutoMigrate` from individual service startup
- Use a proper migration tool (golang-migrate, Atlas, goose) for versioned, reversible migrations

---

### P2 — Lower Priority

#### Read Replicas for Analytics
**Why**: Analytics queries (CTEs, window functions, percentile calculations) are read-heavy and CPU-intensive. They shouldn't compete with write-path operations.

**Recommendation**: Route analytics-service to a read replica. PostgreSQL streaming replication with <1s lag is sufficient for dashboard data.

#### Connection Pooling with PgBouncer
**Why**: As per-service pools grow, a pooler prevents each service from holding many direct connections to Postgres.

**Recommendation**: Deploy PgBouncer in `transaction` mode between services and PostgreSQL. Configure `max_client_conn` per service based on actual usage patterns documented above.

#### Row Level Security (RLS)
**Why**: Currently all access control is application-level. If a service bug or SQL injection bypasses application checks, there's no database-level safety net.

**Recommendation**: For multi-tenant tables (`processing_jobs`, `user_sessions`), add RLS policies:
```sql
ALTER TABLE processing_jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY user_isolation ON processing_jobs
    USING (user_id = current_setting('app.current_user_id')::uuid);
```

---

## General Best Practices

### Indexing Strategy

1. **Create indexes for your queries, not your columns**. A composite index `(user_id, tool_type, created_at DESC)` is far more useful than three separate single-column indexes.

2. **Column order matters**. Put equality filters first (`user_id = ?`), then range/inequality filters (`created_at >= ?`), then sort columns last.

3. **Use partial indexes** for queries that always filter on a condition:
   ```sql
   CREATE INDEX idx_active_sessions ON user_sessions (access_expires_at)
       WHERE refresh_expires_at IS NOT NULL;
   ```

4. **Monitor unused indexes** with `pg_stat_user_indexes`. Every index slows down writes — remove indexes with zero scans.

5. **Avoid indexing JSONB for joins**. If a JSONB field is used in WHERE or JOIN conditions, promote it to a proper column.

### Query Patterns

1. **Avoid N+1 queries**. When processing a list of items that each need related data, use `WHERE id IN (...)` batch fetches instead of per-item queries.

2. **Use RETURNING for delete-and-fetch**. `DELETE FROM ... RETURNING *` is atomic and saves a round-trip vs. `SELECT` then `DELETE`.

3. **Prefer COUNT with filters over subqueries**:
   ```sql
   -- Good: single pass
   SELECT COUNT(*) FILTER (WHERE status = 'completed') as completed,
          COUNT(*) FILTER (WHERE status = 'failed') as failed
   FROM processing_jobs;
   ```

4. **Paginate with LIMIT/OFFSET for small datasets**. For large datasets (>100K rows), use keyset pagination:
   ```sql
   WHERE created_at < ? ORDER BY created_at DESC LIMIT 25
   ```

### JSONB Usage

1. **Use JSONB for truly unstructured data** — variable metadata, user-defined options, configuration blobs.
2. **Promote frequently queried JSONB fields** to proper columns with indexes.
3. **Never use JSONB fields as join keys** — extract to a column if you need to JOIN on it.
4. **Use GIN indexes** only if you need full-document containment queries (`@>`, `?`, `?|`).

### Connection Management

1. **Right-size connection pools** per service based on actual concurrency:
   - High-concurrency services (API-facing): 15-25 max connections
   - Background workers (single-threaded): 3-5 max connections
   - Batch processors: 5-10 max connections

2. **Set `ConnMaxLifetime`** to 30 minutes to prevent stale connections and respect load balancer timeouts.

3. **Always ping on startup** with a timeout to fail fast if the database is unreachable.

4. **Calculate total connections**: Sum of all services' `MaxOpenConns` must not exceed PostgreSQL's `max_connections`.

### Data Integrity

1. **Use FK constraints** for parent-child relationships within the same service. Use `ON DELETE CASCADE` when the child has no meaning without the parent.

2. **Add CHECK constraints** for columns with a fixed set of values. This prevents bad data and gives the query planner cardinality hints.

3. **Use NOT NULL** wherever possible. Nullable columns require special handling in queries and indexes.

4. **Prefer UUIDv7** over UUIDv4 for primary keys. Same format, but time-ordered for B-tree efficiency.

### Transactions

1. **Keep transactions short**. Long transactions hold locks and block other operations.

2. **Use transactions for multi-row consistency** — e.g., creating a job + its file metadata together.

3. **Don't wrap read-only queries in transactions** unless you need snapshot isolation.

### Monitoring Checklist

| Metric | Tool | Alert Threshold |
|--------|------|----------------|
| Active connections | `pg_stat_activity` | >80% of `max_connections` |
| Slow queries | `pg_stat_statements` | mean_time > 500ms |
| Sequential scan ratio | `pg_stat_user_tables` | seq_scan / (seq_scan + idx_scan) > 0.5 on large tables |
| Dead tuples | `pg_stat_user_tables` | n_dead_tup > 10K without recent vacuum |
| Replication lag | `pg_stat_replication` | >5s (if using replicas) |
| Table bloat | `pgstattuple` | >30% dead space |

---

## Endpoint Latency & Database Locality

This records a latency investigation of the user-facing API endpoints, the
application-level fixes applied, and the infrastructure change (co-locating the
database) that remained the single biggest lever.

### How latency was measured

All client traffic flows through the **api-gateway (`:8080`)**; backend services
are only reachable through it. Two methods were used:

1. **Load run** — the existing k6 harness (`scripts/k6/run.sh mixed-realistic`),
   with per-endpoint p50/p95/p99 read from the gateway's Prometheus
   `http_request_duration_seconds` histogram (the same series the analytics
   `/admin/metrics/api-performance` dashboard parses). Because the counters are
   cumulative, a snapshot is taken before and after the run and the bucket counts
   are diffed to isolate latency *during* the window.
2. **Isolated probe** — a valid token, each read endpoint called sequentially
   with no competing load, to separate intrinsic per-request cost from
   CPU/contention noise during the load run.

### Root cause: the database was on another continent

The services originally connected to **Neon serverless PostgreSQL in AWS
`us-east-1`** over TLS. From the deployment used for testing, **each query cost
≈ 240 ms** — the cross-region network round-trip, not query execution. Two
consequences dominated latency:

- **Per-request latency ≈ (number of *sequential* DB round-trips) × ~240 ms.**
  An endpoint that ran 7 queries one after another cost ~1.7 s regardless of how
  trivial each query was.
- **Serverless cold-starts**: Neon autosuspends its compute when idle, so the
  first request after a quiet period paid an extra ~2–5 s wake-up.

Notably, Redis and MinIO are **local containers** (sub-millisecond), so they were
*not* meaningful latency sources — upload init/complete measured 8–24 ms. The
latency lived entirely in remote-DB round-trips.

#### Baseline (worst offenders, isolated, warm)

| Endpoint | Queries (sequential) | Latency (median) |
|---|---|---|
| `GET /api/dashboard` (user) | ~7 | ~2000 ms |
| `POST /auth/login` | user + plan + session-write + bcrypt | ~3200 ms |
| `GET /api/documents` | count + list | ~490 ms |
| `GET /api/notifications` | list + count | ~490 ms |
| `GET /auth/me` | user + plan | ~480 ms |
| `GET /api/jobs/history` | 1 | ~245 ms (≈ the 1-round-trip floor) |

### Application fixes applied

The strategy was to **reduce the number of *sequential* DB round-trips**, since
each one cost a full cross-continent RTT.

| Fix | Where | Effect |
|---|---|---|
| Parallelize independent dashboard queries (`parallelQueries`) | `analytics-service/handlers/dashboard.go` | user dashboard ~2000 → ~520 ms; admin similar |
| Size the analytics DB pool for the fan-out (20/15) | `analytics-service/main.go` | keeps the ~10 concurrent dashboard queries on warm connections |
| In-process TTL cache for plan-by-name (`lookupPlan`) | `auth-service/handlers/plancache.go` | removes one round-trip from login/refresh/`me`/`profile`/internal |
| Parallelize count + list | `document-service/handlers/documents.go` | ~490 → ~285 ms |
| Parallelize list + unread-count | `notification-service/handlers/notifications.go` | ~490 → ~295 ms |

#### Result (isolated, warm — median)

| Endpoint | Before | After | Change |
|---|---|---|---|
| `GET /api/dashboard` | ~2000 ms | ~520 ms | **−74%** |
| `GET /auth/me` | ~480 ms | ~280 ms | −42% |
| `GET /api/notifications` | ~490 ms | ~295 ms | −40% |
| `GET /api/documents` | ~490 ms | ~285 ms | −42% |
| `POST /auth/login` | ~3200 ms | ~2300 ms | −28% (plan round-trip removed) |

Every optimized read endpoint then sat near the **~240 ms single-round-trip
floor** — the physical minimum of one query to `us-east-1`. The application
can't go below that without reducing it to *zero* round-trips (caching) or moving
the data closer.

#### What was deliberately *not* changed
- **Gateway per-request Redis calls** (denylist / rate-limit / plan-cache): Redis
  is local (sub-ms), so collapsing them saves nothing here.
- **Response gzip**: dwarfed by the 240 ms round-trips.
- **bcrypt cost**: lowering it would weaken password security; login's residual
  cost is bcrypt + the mandatory session-row write.

### Reusable patterns introduced

- **`parallelQueries(fns ...func())`** (analytics) — runs independent,
  side-effect-free DB reads concurrently and waits. Each closure must write only
  its own variable.
- **`buildQuery()` closure** (document-service) — produces a fresh, independent
  `*gorm.DB` per call so count + page-fetch can run on separate goroutines
  without sharing a statement. Read request params into locals first;
  `gin.Context` form parsing is not concurrency-safe.
- **In-process TTL cache** (`lookupPlan`, auth-service) — for tiny,
  rarely-changing reference tables read on hot paths against a remote DB.

### Co-located Postgres (implemented — the biggest lever)

The ~240 ms floor and the 2–5 s cold-starts were *infrastructural* — no amount of
application code beats the speed of light to another continent. The database was
moved off Neon to a **self-managed, co-located Postgres container** (`db` service
in `deployment/docker-compose.yml`) on the same Docker network as the services.
Each query is now a localhost hop instead of a cross-continent round-trip.

**Measured (isolated, warm) — Neon vs co-located:**

| Endpoint | Neon (orig) | After code fixes | **Co-located DB** |
|---|---|---|---|
| `GET /api/dashboard` | ~2000 ms | ~520 ms | **~4 ms** |
| `GET /auth/me` | ~480 ms | ~280 ms | **~4 ms** |
| `GET /api/notifications` | ~490 ms | ~295 ms | **~2 ms** |
| `GET /api/documents` | ~490 ms | ~285 ms | **~2 ms** |
| `GET /api/jobs/history` | ~245 ms | ~245 ms | **~3 ms** |
| `POST /auth/login` | ~3200 ms | ~2300 ms | **~110 ms** (now bcrypt-bound) |

Reads dropped ~100×; login is now dominated by bcrypt as predicted. The
application-level parallelization/caching still helps (fewer round-trips), but
the round-trip itself is now sub-millisecond, so absolute latency is tiny.

**Key implementation details:**
- `db`: `postgres:18-alpine`, named volume `postgres_data` (data persists across
  restarts/rebuilds/redeploys; `deploy.sh` uses `down` **without** `-v`).
  Verified: `docker compose restart db` preserved all rows.
- **postgres:18 mount-path change:** the volume is mounted at
  `/var/lib/postgresql` (NOT `/var/lib/postgresql/data` as in ≤17). PG18 stores
  data in a major-version subdirectory; mounting at the old path makes it refuse
  to start ("unused mount/volume"). This bit us during the upgrade.
- **Data migrated from Neon** (not a fresh start): `pg_dump` of the Neon DB
  (PG17, ~9.7 MB, `--no-owner --no-privileges`) restored into the fresh PG18
  `fyredocs` database. All populated tables matched Neon exactly and are writable;
  service `AutoMigrate` ran clean against the restored schema (no-op). The dump
  procedure: `pg_dump "<neon-url>" --no-owner --no-privileges | psql "<local-url>"`
  from a `postgres:18-alpine` container joined to `fyredocs_net`.
- Tuned flags: `max_connections=200` (sum of per-service pools ~155),
  `shared_buffers=256MB`, `work_mem=8MB`. Under k6 `mixed-realistic` load, peak
  was **19/200** connections — ample headroom; PgBouncer is the scale path if
  pools grow.
- All 11 DB-using services `depends_on: db {condition: service_healthy}`;
  schema + `subscription_plans` seed are created automatically on first boot by
  each service's `Migrate()`/`seedPlans()`.
- **DSN fix:** `shared/config/postgres_dsn.go` no longer appends libpq
  keepalive params (`keepalives*`). The pgx driver forwards them to the server,
  and a standard Postgres rejects them (`FATAL: unrecognized configuration
  parameter "keepalives_idle"`); Neon's pooler had silently ignored them.
- If the DB must stay remote instead, lean on the patterns above: parallelize
  independent queries, cache rarely-changing reference data in-process, keep
  enough idle connections warm (`MaxIdleConns`) to avoid fresh TLS handshakes,
  and disable serverless autosuspend to kill cold-starts.

**Remaining self-managed responsibilities:** single VPS = SPOF (no managed
HA/PITR); monitor disk/RAM. Offsite backups are handled by the `db-backup`
sidecar (hourly `pg_dump` → external S3 bucket via rclone) — see
[backup-and-restore.md](./backup-and-restore.md).
