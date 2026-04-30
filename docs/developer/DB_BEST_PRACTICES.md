# Database Performance & Optimization Guide

This document covers database best practices for the Fyredocs backend, organized into completed optimizations and future production recommendations.

**Stack**: PostgreSQL (Neon dev / self-hosted production) + GORM + Redis

---

## Table of Contents

1. [Completed Optimizations](#completed-optimizations)
2. [Future Optimizations (Production)](#future-optimizations-production)
3. [General Best Practices](#general-best-practices)

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

### 3. N+1 Query Elimination in Cleanup Worker

**Problem**: The cleanup worker ran one `SELECT` per expired job to fetch file metadata (100 jobs = 100 queries), and one `SELECT COUNT(*)` per filesystem directory to check job existence.

**Solution**:
- **`cleanupExpiredJobs`**: Batch-fetch all files with `WHERE job_id IN (...)`, group in-memory by job ID, then batch-delete with `WHERE job_id IN (...)` and `WHERE id IN (...)`
- **`cleanupOrphanedDirs`**: Collect all candidate UUIDs from filesystem, single `SELECT id FROM processing_jobs WHERE id IN (...)`, compute orphans in-memory

**Impact**: Reduced from O(N) queries per batch to O(1) — typically 100x fewer database round-trips per cleanup cycle.

**File**: `cleanup-worker/main.go`

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

**Impact**: Database-level integrity guarantee. The cleanup worker no longer needs to manually delete file metadata before jobs — CASCADE handles it.

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
| Cleanup worker | 25/10 | **10/3** |
| Auth service | 25/10 | 25/10 (unchanged — handles concurrent auth) |
| Job service | 25/10 | 25/10 (unchanged — handles concurrent uploads) |
| Analytics service | 15/5 | 15/5 (unchanged — already tuned) |

**Impact**: Reduced total max connections from ~200 to ~120. Directly reduces memory usage on PostgreSQL and avoids connection pool exhaustion on constrained environments.

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
**Why**: Currently every service runs `AutoMigrate` on boot through pgbouncer. DDL through connection poolers in transaction mode can cause issues. Multiple services racing to migrate the same tables adds unnecessary startup complexity.

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

#### Connection Pooling with PgBouncer (Self-Hosted)
**Why**: When moving off Neon (which provides built-in pgbouncer), you'll need your own connection pooler to prevent each service from holding direct connections.

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

4. **Calculate total connections**: Sum of all services' `MaxOpenConns` must not exceed PostgreSQL's `max_connections` (default: 100).

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
