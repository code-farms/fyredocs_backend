# Endpoint Latency & Database Locality

This document records a latency investigation of the user-facing API endpoints,
the application-level fixes that were applied, and the infrastructure changes
that remain the single biggest lever.

## How latency was measured

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

## Root cause: the database is on another continent

The services connect to **Neon serverless PostgreSQL in AWS `us-east-1`** over
TLS (`DATABASE_URL=…us-east-1.aws.neon.tech…sslmode=require`). From the
deployment used for testing, **each query costs ≈ 240 ms** — the cross-region
network round-trip, not query execution. Two consequences dominated latency:

- **Per-request latency ≈ (number of *sequential* DB round-trips) × ~240 ms.**
  An endpoint that ran 7 queries one after another cost ~1.7 s regardless of how
  trivial each query was.
- **Serverless cold-starts**: Neon autosuspends its compute when idle, so the
  first request after a quiet period paid an extra ~2–5 s wake-up.

Notably, Redis and MinIO are **local containers** (sub-millisecond), so they were
*not* meaningful latency sources — upload init/complete measured 8–24 ms. The
latency lived entirely in remote-DB round-trips.

### Baseline (worst offenders, isolated, warm)

| Endpoint | Queries (sequential) | Latency (median) |
|---|---|---|
| `GET /api/dashboard` (user) | ~7 | ~2000 ms |
| `POST /auth/login` | user + plan + session-write + bcrypt | ~3200 ms |
| `GET /api/documents` | count + list | ~490 ms |
| `GET /api/notifications` | list + count | ~490 ms |
| `GET /auth/me` | user + plan | ~480 ms |
| `GET /api/jobs/history` | 1 | ~245 ms (≈ the 1-round-trip floor) |

## Application fixes applied

The strategy was to **reduce the number of *sequential* DB round-trips**, since
each one costs a full cross-continent RTT.

| Fix | Where | Effect |
|---|---|---|
| Parallelize independent dashboard queries (`parallelQueries`) | `analytics-service/handlers/dashboard.go` | user dashboard ~2000 → ~520 ms; admin similar |
| Size the analytics DB pool for the fan-out (20/15) | `analytics-service/main.go` | keeps the ~10 concurrent dashboard queries on warm connections |
| In-process TTL cache for plan-by-name (`lookupPlan`) | `auth-service/handlers/plancache.go` | removes one round-trip from login/refresh/`me`/`profile`/internal |
| Parallelize count + list | `document-service/handlers/documents.go` | ~490 → ~285 ms |
| Parallelize list + unread-count | `notification-service/handlers/notifications.go` | ~490 → ~295 ms |

### Result (isolated, warm — median)

| Endpoint | Before | After | Change |
|---|---|---|---|
| `GET /api/dashboard` | ~2000 ms | ~520 ms | **−74%** |
| `GET /auth/me` | ~480 ms | ~280 ms | −42% |
| `GET /api/notifications` | ~490 ms | ~295 ms | −40% |
| `GET /api/documents` | ~490 ms | ~285 ms | −42% |
| `POST /auth/login` | ~3200 ms | ~2300 ms | −28% (plan round-trip removed) |

Every optimized read endpoint now sits near the **~240 ms single-round-trip
floor** — the physical minimum of one query to `us-east-1`. The application can't
go below that without reducing it to *zero* round-trips (caching) or moving the
data closer.

### What was deliberately *not* changed
- **Gateway per-request Redis calls** (denylist / rate-limit / plan-cache): Redis
  is local (sub-ms), so collapsing them saves nothing here.
- **Response gzip**: dwarfed by the 240 ms round-trips.
- **bcrypt cost**: lowering it would weaken password security; login's residual
  cost is bcrypt + the mandatory session-row write.

## Infrastructure recommendations (the biggest remaining lever)

The ~240 ms floor and the 2–5 s cold-starts are *infrastructural* — no amount of
application code beats the speed of light to another continent. In priority
order:

1. **Co-locate the app and the database.** ✅ **IMPLEMENTED** — see
   "Co-located Postgres (implemented)" below. Running Postgres as a container on
   the same Docker network collapsed the ~240 ms round-trip to <1 ms.
2. **Disable Neon autosuspend** (or set a generous "suspend after" / use a
   non-scale-to-zero compute) to eliminate the 2–5 s first-request cold-starts
   that intermittent users feel most.
3. **Keep using the pooled Neon endpoint** (`-pooler` host) and ensure each
   service keeps enough idle connections warm (`MaxIdleConns`) so requests don't
   pay a fresh TLS handshake to a remote host — see the analytics-service pool
   sizing above.
4. **If the DB must stay remote**, lean further on the patterns in this doc:
   parallelize independent queries, cache rarely-changing reference data
   in-process, and minimize sequential round-trips per request.

## Reusable patterns introduced

- **`parallelQueries(fns ...func())`** (analytics) — runs independent,
  side-effect-free DB reads concurrently and waits. Each closure must write only
  its own variable.
- **`buildQuery()` closure** (document-service) — produces a fresh, independent
  `*gorm.DB` per call so count + page-fetch can run on separate goroutines
  without sharing a statement. Read request params into locals first;
  `gin.Context` form parsing is not concurrency-safe.
- **In-process TTL cache** (`lookupPlan`, auth-service) — for tiny,
  rarely-changing reference tables read on hot paths against a remote DB.

## Co-located Postgres (implemented)

The database was moved off Neon to a **self-managed, co-located Postgres
container** (`db` service in `deployment/docker-compose.yml`) on the same Docker
network as the services. Each query is now a localhost hop instead of a
cross-continent round-trip.

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
  `fyredocs` database. All 14 populated tables matched Neon exactly (19 users,
  13 documents, 46 jobs, 426 analytics events, …) and are writable; service
  `AutoMigrate` ran clean against the restored schema (no-op). The Neon dump
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
- The previous Neon `DATABASE_URL` is retained (commented) in `deployment/.env`
  for rollback.

**Remaining self-managed responsibilities:** single VPS = SPOF (no managed
HA/PITR); add a daily `pg_dump` with an off-host copy; monitor disk/RAM.
