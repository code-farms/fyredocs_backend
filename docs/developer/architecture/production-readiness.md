# Fyredocs Backend — Production Readiness Audit

**Date:** 2026-07-02
**Scope:** Full backend (`fyredocs_backend/`) — 13 Go services, ~35k LOC, 249 source files
**Question under review:** Can this backend reliably serve ~1,000,000 requests/day within 3 months, assuming reasonable infrastructure scaling?
**Method:** Full exploration of every service, followed by line-level verification of the gateway, job/auth hot paths, worker loops, cleanup worker, analytics pipeline, shared infrastructure packages, and deployment topology. Every 🔴/🟠 finding cites verified `file:line` evidence.

---

## 1. Executive Summary

This is a **well-engineered backend that is roughly one infrastructure phase away from 1M req/day**. The application code is in the top decile of what I see at this stage: structured logging with request IDs everywhere, Prometheus + OpenTelemetry in every service, a disciplined presigned-upload protocol that keeps file bytes out of application services, JetStream work queues with DLQ and bounded redelivery, transactional multi-writes, consistent response envelopes, and an unusually strong documentation culture enforced by `CLAUDE.md`.

The risks are almost entirely **operational and architectural-physical**, not code-quality:

1. **The HTTP/API tier will handle 1M req/day easily.** ~12 req/s average, ~60–80 req/s peak is trivial for Go services of this quality. This is not where the system breaks.
2. **The conversion workers are the real throughput ceiling.** `WORKER_CONCURRENCY=2` with 1 replica per tool family means the system can complete roughly 10–15k office conversions/day per tool. At 1M req/day traffic levels (≈30–50k jobs/day), peak-hour queue latency grows unboundedly without ~10–20× more worker capacity.
3. **Everything runs on one host with zero HA.** Single Postgres, single Redis, single NATS, single MinIO, single gateway — via Docker Compose with `restart: always` as the entire failover strategy. Any one of five containers dying takes the product down or silently degrades it.
4. **There is no CI/CD at all.** 85 test files exist but nothing runs them automatically; deploys are `deploy.sh` on a host with no zero-downtime story and no rollback.
5. **One authorization hole:** `PUT /auth/plan` lets any authenticated user self-assign the `pro` plan with no payment/entitlement check.

**Overall score: 72/100. Current comfortable capacity: 100K–500K req/day** (API tier far more; job-processing tier less). With the Phase 1–3 roadmap below (≈4–6 weeks of focused work, mostly infrastructure), **500K–1M req/day is realistic within the 3-month window.**

---

## 2. Overall Architecture Assessment

### 2.1 System shape

```
                                Internet
                                   │  (only :8080 exposed; MinIO console loopback-only)
                                   ▼
                         ┌───────────────────┐
                         │    api-gateway    │  JWT verify · guest tokens · denylist
                         │  (Go stdlib, 502  │  per-plan rate limit · CORS · sec headers
                         │   lines, no Gin)  │  SPA static files · MinIO byte proxy
                         └─────┬─────────────┘
        ┌───────────┬──────────┼───────────┬─────────────┬────────────┐
        ▼           ▼          ▼           ▼             ▼            ▼
   auth-service  job-service  document-  user-      notification  analytics-
     :8086         :8081      service    service      service      service
                               :8089      :8090        :8091        :8087
        │            │           │          │             │            │
        └────────────┴───────────┴────┬─────┴─────────────┴────────────┘
                                      ▼
                    ┌──────────────────────────────────────┐
                    │   ONE PostgreSQL 18 (fyredocs DB)    │  ← logical isolation only
                    │   ONE Redis 7 · ONE NATS JetStream   │
                    │   ONE MinIO                          │
                    └──────────────────────────────────────┘
                                      ▲
                 NATS JOBS_DISPATCH (WorkQueue) │ JOBS_EVENTS │ ANALYTICS │ JOBS_DLQ
        ┌───────────────┬───────────────┬───────────────┬──────────────┐
        ▼               ▼               ▼               ▼              ▼
  convert-from-pdf  convert-to-pdf  organize-pdf   optimize-pdf   cleanup-worker
   (pdf2docx,        (LibreOffice/   (pdfcpu)       (Ghostscript,  (TTL reaper,
    Poppler)          unoserver)                     Tesseract)     cross-service DB)
```

### 2.2 Verdict on the architecture

**The logical architecture is sound and will scale.** Service boundaries follow real domain seams (auth / jobs / documents / orgs / notifications / analytics), communication is REST + events, workers are decoupled via a work queue, and the `shared/` package is correctly restricted to infrastructure utilities (logger, metrics, telemetry, natsconn, redisstore, response, storage, config) with zero shared business logic.

**The physical architecture is a monolith wearing a microservices costume:**

- All 11 DB-using services connect to **one `DATABASE_URL` / one `fyredocs` database** (`deployment/docker-compose.yml` — the same `${DATABASE_URL}` injected into every service). Schema-level ownership is honored by convention, but blast radius, connection budget, vacuum pressure, and upgrade risk are all shared. `CLAUDE.md` §3 claims "Each service owns its own DB" — physically, it does not.
- **cleanup-worker directly reads and deletes job-service's tables** (`cleanup-worker/main.go:207-246` operates on `processing_jobs`/`file_metadata` via its own copies of the models), violating the project's own data-ownership rule. This is pragmatic, but it means job-service can never change its schema without silently breaking cleanup-worker — the exact coupling microservices exist to prevent.
- Everything is **one Docker Compose file on one host**. There is no horizontal path: `deploy.sh` + `restart: always` is the entirety of orchestration, failover, and scaling.

**Architectural smells (ranked):**

| Smell | Evidence | Severity |
|---|---|---|
| Single physical Postgres for 11 services | `docker-compose.yml` (12× `DATABASE_URL: ${DATABASE_URL}`) | 🟠 accepted trade-off, but pool math is broken (see §5.6) |
| cleanup-worker cross-service table access | `cleanup-worker/main.go:207-246`, duplicated model structs | ✅ fixed — absorbed into job-service (`job-service/cmd/cleanup` + `internal/cleanup`), reuses job-service's own models; still its own container |
| Duplicated `authverify`, `database.go`, TTL helpers in every service | e.g. `jobExpiry` in `job-service/handlers/jobs.go:1058` vs divergent fallback in `cleanup-worker/main.go:368-378` | ✅ fixed — consolidated into `shared/authverify`, `shared/database`, and `shared/config/defaults.go` |
| Gateway is also SPA server and MinIO byte-relay | `api-gateway/main.go:171-179, 229-247` | ✅ fixed — Caddy edge added (`deployment/caddy/Caddyfile`) for TLS/SPA/object bytes; gateway slimmed to API-only and made internal-only |
| 13 services for a pre-launch product | — | 🟢 more operational surface than the team size likely warrants, but it's built and documented; keep it |

**Config drift concrete example (this class of bug will recur):** job-service's `FREE_JOB_TTL` fallback is 7 days (`job-service/handlers/jobs.go:1084-1095`), cleanup-worker's fallback for the *same variable* is 24 hours (`cleanup-worker/main.go:368-378`). If `FREE_JOB_TTL` is ever unset, cleanup deletes free users' jobs 6 days early. Duplication across services makes this invisible. *(✅ Fixed: the fallbacks now live once in `shared/config/defaults.go` and both binaries read the same helpers.)*

---

## 3. Strengths

1. **Observability is genuinely production-grade** — `shared/logger` (slog, JSON in prod, request IDs propagated), `shared/metrics` (Prometheus histograms per route + job counters), `shared/telemetry` (OTLP tracing with graceful no-op fallback), `/healthz` + `/readyz` with real dependency checks on every service.
2. **The presigned multipart upload protocol is the right design** (`job-service/handlers/uploads.go:23-45`): file bytes never pass through application services; plan limits enforced on declared size *and* re-verified via `StatObject` server-side (`jobs.go:604-615`); MIME sniffed from actual object bytes (`jobs.go:617-624`); path traversal blocked (`sanitizeFileName`).
3. **Job pipeline correctness**: DB transaction wraps job + file-metadata creation with a 10s timeout (`jobs.go:268-286`); upload state is only released *after* commit + queue publish so client retries never force re-upload (`jobs.go:312-321`); dual idempotency (Idempotency-Key header at `jobs.go:74-89` + uploadId replay dedup at `jobs.go:133-140, 670-694`).
4. **Worker consumer configuration is textbook** (`convert-to-pdf/internal/worker/worker.go:214-222`): durable pull consumer, `MaxDeliver: 4`, explicit backoff, `AckWait: 30m`, `MaxAckPending: 2×concurrency` to bound damage from a wedged container, DLQ stream for terminal failures.
5. **Auth is stronger than most seed-stage backends**: JWT secret validated at startup against length and known-bad values (`shared/config/config.go:107-136`), bcrypt with cost bounds on password length, Redis token denylist honored at the gateway, httpOnly cookies with `Secure` defaulting to true, refresh cookie path-scoped to `/auth` (`auth-service/handlers/auth.go:458-467`), constant-time role/scope comparison, internal identity headers stripped from inbound requests (`api-gateway/main.go:347, 375`).
6. **Only port 8080 is exposed** (`docker-compose.yml:238-239`; MinIO console bound to 127.0.0.1) — the internal `X-User-*` header trust model is actually safe in this topology.
7. **cleanup-worker is carefully written**: Redis `SetNX` distributed lock (`cleanup-worker/main.go:156-168`), batched deletes of 100 with batch-fetched file metadata (explicit N+1 fix, `main.go:220-240`), never deletes an object it can't prove is unreferenced (`main.go:251-258`), reaps orphaned S3 multiparts.
8. **Docs culture**: per-service architecture docs, Mermaid diagrams, Swagger, 156KB Postman collection, and `CLAUDE.md` that *mandates* doc/test updates with code changes. 85 test files (~52% of source files have a sibling test).
9. **Graceful shutdown everywhere** (SIGTERM → 30s drain), multi-stage scratch-image Docker builds running as non-root UID 10001, log rotation configured, per-container memory/CPU caps.
10. **Pagination enforced on every list endpoint** with clamped limits (`jobs.go:345-347`), full-text search via generated tsvector + GIN index in document-service, partial unique indexes for idempotent event-driven writes.

---

## 4. Weaknesses

1. **No CI/CD whatsoever** — no `.github/workflows/`, nothing runs the 85 test files, builds happen on the deploy host. Every deploy is an untested artifact by definition.
2. **Single host, five single points of failure** — Postgres, Redis, NATS, MinIO, gateway. No replication, no failover, no zero-downtime deploy (compose recreate = dropped connections), no rollback strategy beyond `git checkout && deploy.sh`.
3. **Conversion throughput ceiling** — 2 concurrent conversions per worker service (`worker.go:195-202`, compose replicas default 1). LibreOffice conversions run 2s + 1.5s/MB (`worker.go:156-172`); the math in §6 shows this saturates at a small fraction of 1M-req/day job volume.
4. **`PUT /auth/plan` has no entitlement check** — any authenticated user upgrades themselves to `pro` (`auth-service/handlers/auth.go:530-584`): 500MB uploads, 600 req/min, 30-day retention, for free.
5. **No API versioning** — all routes unversioned; breaking changes will hurt once mobile/third-party clients exist.
6. **Schema management is `AutoMigrate` at boot** (`job-service/internal/models/database.go:89-118` and equivalents in all services) — no versioned migrations, no rollback, concurrent DDL from 11 services racing at every stack start.
7. **`analytics_events` grows unboundedly** — one row per event, no partitioning, no retention job (cleanup-worker never touches it), and every admin dashboard aggregates over it (`analytics-service/subscriber/subscriber.go:129-135`).
8. **Refresh tokens are never rotated** — `Refresh` issues a new access token but re-uses the same refresh token for its full 7-day life (`auth.go:158-231`); no reuse detection is possible.
9. **Session-store failures are non-fatal warnings** on login (`auth.go:327-329`) — a DB blip yields tokens whose refresh will mysteriously fail later.
10. **Pool budget exceeds Postgres capacity** — default 25 max-open conns/service × 11 services ≈ 275 potential vs `max_connections=200` (`docker-compose.yml:29`, `database.go:28-35`).
11. **Rate limiting fails open** on Redis outage (`api-gateway/main.go:218-227`) — combined with single Redis, one infra failure disables all throttling while auth denylist checks also degrade.
12. **Unbounded in-process cache** — `outputFileCache sync.Map` grows forever (`job-service/handlers/jobs.go:39-41`) and is incoherent across replicas the moment job-service scales past 1.
13. **Stray artifacts / secrets hygiene** — compiled binary `cleanup-worker/cleanup-worker.exe` committed in-tree; live `.env` (4.4KB) and `.jwt_secret` sit at repo root; DSN default appends `sslmode=disable`.

---

## 5. Detailed Findings

### 5.1 Architecture — 8/10
Covered in §2. Layering inside each service is consistent (`main.go` → `routes/` → `handlers/` → `internal/models`), dependency direction is clean (handlers→models, never reverse), no circular imports (enforced by Go + separate modules via `go.work`). Dependency injection is struct-based for auth (`AuthEndpoints`, `auth.go:52-57`) but **package-global singletons elsewhere** (`models.DB`, `redisstore.Client`, `natsconn.JS`) — acceptable in Go, but it makes handler tests rely on fakes-by-substitution and hides dependency order bugs (e.g., a handler running before `Connect()` panics at runtime, not compile time).

### 5.2 Folder structure — 9/10
Feature-first at the top (service per folder), layer-first within services, identical shape across all 13 services — a new engineer who has read one service has read them all. `shared/` is properly scoped. Minor issues: `files/` (dev scratch) and `cleanup-worker.exe` don't belong in the tree; `Fyredocs_API.postman_collection.json` (156KB) is at root while `CLAUDE.md` designates `/postman/`; the `.jwt_secret` file should not exist (see §5.9). No structural reorganization needed — do not spend time here.

### 5.3 Naming conventions — 8/10
Consistent: kebab-case service dirs and NATS-visible names, snake_case env vars and DB columns, PascalCase exported Go types, camelCase JSON fields, UPPER_SNAKE error codes. Deviations worth fixing:
- Gateway route prefix `/api/upload` maps to upstream `/api/uploads` (`api-gateway/main.go:93-95`) — a silent singular/plural alias that will confuse debugging.
- Tool-type aliases normalized in code (`ppt-to-pdf`→`powerpoint-to-pdf`, `jobs.go:750-766`) — the alias list lives only in one function; document it or reject aliases at the edge.
- `handlers.CreateJobFromTool` vs `handlers.InitUpload` vs `AuthEndpoints.Signup` — free functions and methods mixed for the same role; harmless, but pick one style for new code (methods on an endpoints struct, which enables DI).
Recommended standard: exactly what the codebase already does, written down in `CLAUDE.md` §2, plus "route segments are plural nouns."

### 5.4 Code organization — 8/10
- `job-service/handlers/jobs.go` (1,216 lines) mixes HTTP handlers, upload consumption, guest-token management, MIME validation, TTL policy, and response shaping. Split into `jobs.go` (handlers), `consume.go`, `guest.go`, `validation.go`, `ttl.go`. Same for `auth-service/handlers/auth.go` (655 lines: handlers + cookie helpers + plan cache).
- Worker services share ~80% of `worker.go` structure by copy-paste (4× ~600 lines). The `WorkerConfig`/`Run` skeleton in convert-to-pdf is already generic (`worker.go:40-48, 204+`) — it belongs in `shared/` as an infrastructure utility (allowed by `CLAUDE.md` §2: "message queue clients"); only `ProcessFunc` differs per service.
- Dead/legacy code is well-marked (410 on legacy chunk route, legacy `/`-prefixed path skips) — good.
- No god objects; controllers are appropriately thin elsewhere.

### 5.5 API design — 7/10
Envelope (`shared/response/response.go`) is uniform and good. Issues:
- **No versioning.** Add `/api/v1/` before any external client integrates. Effort is a gateway prefix + upstream base path today; it becomes a migration project later.
- **Inconsistent pagination meta**: document-service returns `total` (concurrent count+fetch), job-service returns `Meta{Page, Limit}` with **no Total** (`jobs.go:365, 378, 540`) — clients can't render page counts. Either return totals (or `hasMore`) everywhere or nowhere.
- **`PUT /auth/plan`** — plan change is a billing state transition, not a user-writable attribute (see §5.9, Critical).
- Verb/status usage is otherwise correct (201 on create, 204 on delete, 409 on duplicate email, 413 on size, 429 with `X-RateLimit-*` headers). Idempotency-Key support on job creation is above-average API design.
- Filtering/sorting are fixed (`created_at desc` only) — fine for current product surface.
- SSE endpoints hard-cap at 5 minutes (`sse.go:34`) forcing client reconnect — acceptable, but document it in the API spec so clients implement resume.

### 5.6 Database — 7/10
Schema quality is high: FKs with cascades, check constraints on enums, composite indexes matching real query shapes (`(user_id, tool_type, created_at DESC)`), partial unique indexes for event idempotency, tsvector + GIN for search. Verified problems:

- 🔴 **Connection budget**: `DefaultPoolConfig` = 25 max open (`job-service/internal/models/database.go:28-35`), used by ~11 services → theoretical ~275 vs `max_connections=200` (`docker-compose.yml:29`). Under a burst that saturates several pools simultaneously, later services get connection-refused errors at the worst possible moment. Fix: per-service `MaxOpenConns` sized to a budget (gateway 0, auth 30, job 40, workers 5 each, others 10 → ~150), or deploy pgbouncer in transaction mode.
- 🔴 **`AutoMigrate` at every boot from 11 services** — no version history, no down-migrations, DDL races at stack start (all services start simultaneously; Postgres serializes DDL but failures are logged as warnings and boot continues, e.g. `database.go:102-105`). Adopt golang-migrate/atlas with a single migration-runner job before service start.
- 🟠 **`analytics_events` unbounded growth** — no partitioning/retention; dashboards scan it (§5.11). At the event volume implied by 1M req/day (§6), this table hits tens of millions of rows within months; `daily_metrics` exists but raw events are never aggregated-then-pruned.
- 🟠 **`file_metadata.path` has no index** but cleanup-worker queries `WHERE path = ?` per expired upload (`cleanup-worker/main.go:253`). Sequential scans of a table that grows with every job. One-line index fix.
- 🟡 `exports.content` stored as `bytea` in Postgres — exports belong in MinIO with a storage path; large exports will bloat the table, WAL, and backups.
- 🟡 Soft-deleted `documents` are never purged (no cleanup task) — trash accumulates forever.
- 🟡 Offset pagination allows `page=100000` (`jobs.go:346`) → `OFFSET 2,500,000` scans. Cap realistic page depth or move to keyset pagination for history endpoints.
- N+1: none found in hot paths (verified `Preload("Tags")` in document-service, batch fetch in cleanup-worker). Transactions present on all multi-writes that need them; `DeleteJobByID` interleaves S3 deletes and two DB deletes without a transaction (`jobs.go:421-447`) — acceptable because cleanup-worker is the idempotent backstop.

### 5.7 Performance — 7/10
- Hot path (`CreateJobFromTool`) does: Redis idempotency GET → Redis HGetAll → S3 StatObject → S3 GetObjectRange(512B) → DB tx (2 inserts) → NATS publish → 2 Redis writes. ≈8 sequential network round-trips, all justified, each fast; ~20–40ms total. Fine at 100× current load.
- **Progress writes**: each processing job writes DB + publishes NATS every 10s (`worker.go:109-145`) with plateau suppression — bounded by concurrent jobs, not job count. Good.
- **SSE via `Fetch(1)` polling** (`sse.go:81`): each viewer holds a goroutine doing a 5s-max-wait fetch loop *and* an ephemeral JetStream consumer (`sse.go:44-49`). At ~1–2k concurrent viewers this is thousands of NATS consumer create/delete ops and polling RPCs. Switch to `Consume()` callbacks, or better: one shared consumer per job-service instance fanning out to in-process subscriber channels.
- **`outputFileCache sync.Map`** (`jobs.go:39-41`): unbounded — every completed job downloaded adds an entry that lives until process restart; also stale-prone across replicas. Replace with an LRU (e.g., hashicorp/golang-lru, 10k entries) or drop it — the DB lookup it avoids is a single indexed point query.
- Blocking ops: bcrypt (~60–100ms) on login/signup only — at plausible login volumes (<1% of traffic) this is <1 core. No other CPU hotspots in API tier; all heavy compute is correctly quarantined in workers.
- Gateway relays **all file bytes** (MinIO proxy, `api-gateway/main.go:229-247`) plus SPA assets on a 0.5-CPU / 256MB container. Streaming (`FlushInterval: -1`) keeps memory flat, but bandwidth+syscall CPU for, say, 250GB/day of file traffic shares a core-half with all API routing. Raise the limit and/or serve the SPA from a CDN.

### 5.8 Scalability — 6/10
See §6 for the math. Summary of mechanics:
- **API services are stateless and horizontally scalable today** — with two exceptions: `outputFileCache` (harmless staleness) and SSE (any instance can serve any job's stream since state is in NATS — actually fine). Sessions, rate limits, guest tokens, upload state: all in Redis/Postgres. ✅
- **Workers scale by env var + replicas** and the WorkQueue stream load-balances across them — the *mechanism* is right; only the *numbers* are wrong (2 concurrent/service).
- **Stateful tier does not scale**: single Postgres (no replica), single Redis (no sentinel), single NATS (no cluster — JetStream on one node means queue loss on disk failure), single MinIO (no distributed mode).
- Rate limiting is per-plan sliding window in Redis — correct design for multi-instance gateways. Fails open (`main.go:220`), which at this Redis-dependency density means "Redis down" = no throttling + degraded auth simultaneously.
- `JOBS_DISPATCH` caps: 1GiB / 24h `MaxAge` (`shared/natsconn/natsconn.go:64-71`) — under a worker outage longer than 24h, queued jobs are silently dropped while their DB rows stay `queued` forever (no reconciliation job exists).

### 5.9 Security — 7/10

| # | Finding | Severity | Evidence |
|---|---|---|---|
| S1 | **Plan self-upgrade without entitlement**: `ChangePlan` accepts any existing plan name from any authenticated user | ✅ Fixed — `ChangePlan` now requires `admin`/`super-admin` (`canChangePlan`); end users can't self-upgrade (backend-hardening.md §3.20) | `auth-service/handlers/auth.go` |
| S1b | **SSE job-event IDOR**: `GET /jobs/:id/events` streamed any job's live status/progress/failureReason with no ownership check | ✅ Fixed — now gated by `authorizeJobAccess` like its sibling handlers (backend-hardening.md §3.20) | `job-service/handlers/sse.go` |
| S2 | **No refresh-token rotation** — 7-day static bearer credential; theft is undetectable and unmitigated until expiry or logout | 🟠 Medium | `auth.go:158-231` (new access token, same refresh token) |
| S3 | **`sslmode=disable` DSN default** — fine on one Docker network; the moment Postgres moves to managed/remote (the scaling path), credentials + data go plaintext unless someone remembers | 🟠 Medium | `shared/config/postgres_dsn.go` defaults; `.env.example:15` |
| S4 | **Secrets on disk in working tree**: live `.env` (4.4KB) and `.jwt_secret` at repo root; backup creds, S3 keys, Resend key all in one flat file. Not in git (no repo initialized), but one accidental `git init && git add .` away | 🟠 Medium | `fyredocs_backend/.env`, `.jwt_secret` |
| S5 | **No TLS termination anywhere in the stack** — gateway serves plain HTTP on :8080; cookies are `Secure` by default so auth breaks (or someone flips `AUTH_COOKIE_SECURE=false`, the code even warns: `auth.go:439-441`) | ✅ Fixed — Caddy edge terminates TLS (automatic HTTPS when `PUBLIC_DOMAIN` is set); gateway is now internal-only | `deployment/caddy/Caddyfile`; `caddy` service in compose (ports 80/443) |
| S6 | Rate limiter and denylist fail open on Redis outage | 🟡 Low (given single-host) | `main.go:218-227`; `verifier.go` denylist path |
| S7 | Signup has no email verification and no CAPTCHA; 3/min/IP rate limit is the only brake on bot registration | 🟡 Low | `routes.go` limits; `auth.go:59-122` |
| S8 | No Content-Security-Policy header despite serving the SPA | ✅ Fixed — Caddy edge now sets CSP + `X-Frame-Options`/`nosniff`/`Referrer-Policy` on the SPA and `nosniff` on object routes (caddy-edge.md) | `deployment/caddy/Caddyfile` |
| S9 | CORS preflight returns 204 even for disallowed origins (no ACAO header, so browsers still block — correct but confusing); `*`+credentials misconfig only warns, doesn't refuse to boot | 🟢 | `main.go:197-204, 417-420` |

**What's already right** (verified, worth stating): parameterized queries everywhere — re-verified across all **~29** `Raw()` sites (mostly analytics), the one `fmt.Sprintf`-into-SQL site (`api_trends.go`) is safe via a `resolution` whitelist; no hardcoded secrets in source, JWT alg allow-list with issuer/audience/clock-skew validation, password hash excluded via `json:"-"`, HTML-escaped email templates, path-traversal-safe filenames, MIME sniffing of actual bytes, internal `X-User-*` headers stripped at the api-gateway before it injects verified identity, generic error messages with no stack traces, only the Caddy edge exposed.

> **2026 security audit remediation:** SSE-IDOR (F1), plan self-upgrade (S1/F2), duplicate-job + idempotency + cleanup-lock + upload-ownership races (B1/B2/B3/F4), edge CSP/security headers (S8/A1), and the OCR-language whitelist (I1) are fixed — see [backend-hardening.md §3.20](./backend-hardening.md). Still open / backlog: downstream services trust raw gateway identity headers with no JWT verification (defense-in-depth), refresh-token rotation (S2), unauthenticated `/internal/*` S2S endpoints, and the notification subscriber count-then-insert hot-loop (service currently disabled).

### 5.10 Error handling — 8/10
Global recovery middleware, uniform `response.Errorf`/`InternalErrorf` helpers that log server-side detail (op, IDs) while returning generic client messages — consistently applied across all verified handlers. Structured error codes on worker failures with `classifyError` (`worker.go:69-79`; classification by string matching on error text is fragile — use `errors.Is`/typed errors). Gaps:
- **Analytics subscriber `Nak()`s with no delay** on DB failure (`subscriber/subscriber.go:131, 197`) → immediate redelivery hot-loop during a Postgres outage, hammering both NATS and the recovering DB. Use `NakWithDelay`.
- **No reconciliation for stuck jobs**: publish-after-commit (`jobs.go:306-310`) means a NATS failure after DB commit leaves a `queued` row forever (client got a 500 and retains upload state, so UX recovers — the DB row doesn't). A periodic "queued > 1h → fail or requeue" sweep in cleanup-worker closes the loop.

### 5.11 Logging & observability — 8/10
Best-in-class for this stage (see Strengths #1). Gaps that matter before 1M req/day:
- **Nothing consumes the metrics**: no Prometheus server, no Grafana, no alerting rules in the repo (analytics-service's `promscrape` scrapes for product dashboards, which is not ops monitoring). You cannot run 1M req/day on metrics no one is paging on.
- No log aggregation (json-file driver on one host — `docker logs` is the query interface).
- Request IDs exist but W3C `traceparent` propagation to upstream services through the gateway proxy isn't wired into the NATS path — job lifecycle tracing across publish/consume relies on `correlationId` in payloads (present, good) but isn't linked to OTEL traces.
- No SLO definitions; `http_request_duration_seconds` histograms exist, so defining p99 SLOs is a config exercise, not a code change.

### 5.12 Configuration — 7/10
Single `.env` + `config.GetEnv*` helpers with sane defaults; excellent `.env.example` (8.5KB, documented); startup validation for the JWT secret. Gaps: no startup validation for anything else (a typo'd `RATE_LIMIT_API_PRO=6OO` silently becomes the default); duplicated fallback constants drift across services (the `FREE_JOB_TTL` 24h-vs-7d bug, §2.2); no central config struct per service — values are read at call sites (`os.Getenv` inside `maxUploadBytes()` at `jobs.go:860-870` runs per request, trivially cacheable but more importantly unvalidatable). Recommendation: one `Config` struct per service, parsed and validated in `main()`, failing fast, with defaults defined exactly once.

### 5.13 Dependencies — 9/10
Lean and modern: gin, gorm, go-redis v9, nats.go (current jetstream API — not the deprecated one), minio-go v7, golang-jwt v5, x/crypto, OTEL, prometheus client. No heavyweight frameworks, no abandoned libraries, no known-vulnerable pins spotted. Go 1.25 with `go.work` monorepo. The real dependency risk is **system-level**: LibreOffice/unoserver, Ghostscript, Tesseract, pdf2docx inside worker images — pin their versions in Dockerfiles and add `govulncheck` + image scanning to the (to-be-created) CI.

### 5.14 Testing — 6/10
85 test files across services, concentrated exactly where they should be (auth token/verification, rate limiting, upload sanitization, worker cache/tmpfs logic, config parsing) — the *quality* of what exists is good. But:
- **Zero automated execution** — no CI. A test suite that doesn't run is documentation.
- **No integration tests** against real Postgres/Redis/NATS/MinIO (fakes-by-interface everywhere; e.g. `fakestore_test.go`). The failure modes that matter at 1M req/day (pool exhaustion, redelivery, lock contention) are exactly the ones fakes can't catch. Add testcontainers-based integration tests for the job lifecycle.
- **No E2E** of upload→job→convert→download; k6 scripts exist (`scripts/`) but no baseline results are recorded anywhere.
- Deployment confidence today: adequate for a careful solo operator, insufficient for a team or for 1M req/day. Estimated effective confidence: you'd catch ~70% of regressions before prod.

### 5.15 DevOps readiness — 4/10
- ✅ Multi-stage builds, scratch runtime, non-root, healthchecks, resource caps, log rotation, restart policies, `deploy.sh` bootstrap, optional rclone backups with retention + delete protection.
- ❌ **No CI/CD** (build+test+push on every commit is table stakes).
- ❌ **No zero-downtime deploys**: `docker compose up -d` recreates containers → in-flight requests die, SSE streams drop, conversions abort mid-job (they redeliver via `AckWait`, so work isn't lost — but user-visible blips on every deploy).
- ❌ **No rollback** beyond re-deploying old code; images aren't versioned/tagged in a registry.
- ❌ **Backups are opt-in and unverified** — hourly pg_dump to external S3 only if `BACKUP_S3_*` set; no restore drill documented, and Redis/MinIO (guest sessions, all user files!) have no backup story at all. **MinIO holds user documents and has no replication or backup — this is a data-loss SPOF, not just an availability one.**
- ❌ Single host: kernel patch = full outage.

### 5.16 Best-practices comparison (Google/Uber/Stripe/Netflix-style norms)

| Practice | Industry norm | Fyredocs | Gap |
|---|---|---|---|
| Versioned schema migrations | Mandatory (Stripe: online, reversible) | AutoMigrate at boot | 🔴 |
| CI with test gates | Mandatory everywhere | None | 🔴 |
| API versioning | `/v1/` from day one (Stripe pins per-account) | None | 🟠 |
| Idempotency keys | Stripe-style | ✅ Implemented on job creation | — |
| Structured logs + request IDs | Standard | ✅ | — |
| Tracing | Standard (Uber/Jaeger) | ✅ OTEL wired, no collector deployed | 🟡 |
| Error budgets / SLOs / alerting | Google SRE core | Metrics exist, nobody watches | 🟠 |
| Outbox / exactly-once publish | Uber/Netflix use outbox or CDC | Publish-after-commit + no reconciliation | 🟡 |
| Refresh-token rotation w/ reuse detection | OWASP / Auth0 norm | Static 7-day refresh | 🟠 |
| Config as validated struct | Netflix Archaius-style | Scattered GetEnv | 🟡 |
| Load-shedding / circuit breakers | Netflix Hystrix-descendants | Rate limit only; fails open | 🟡 |
| Stateless services | Standard | ✅ (minus one LRU-able cache) | — |

---

## 6. Scalability Assessment — the math for 1M req/day

**Traffic model.** 1M req/day ≈ **11.6 req/s average**; with a typical 4–6× diurnal peak factor, plan for **~60–80 req/s peak**, ~85% reads (status polling, lists, downloads) / 15% writes.

**API tier.** A Go stdlib reverse proxy handles several thousand req/s per core; Gin services similar. At 80 req/s peak, gateway + services run at **<5% of one core each** even with the 0.5-CPU caps. Per-request Redis ops (auth denylist GET + rate-limit INCR ≈ 2–3 ops/req → ~250 ops/s peak) are noise against Redis's ~100k ops/s. **The API tier passes with ~50× headroom. Not the bottleneck.**

**Auth.** bcrypt DefaultCost ≈ 60–100ms/hash. Even 20k logins+signups/day peaks at <1/s → one-tenth of a core. Fine.

**Postgres.** Peak ~200–400 queries/s of indexed point/range queries — a fraction of what one Postgres 18 instance with 256MB shared_buffers does. The risks are the **connection budget** (§5.6) and **`analytics_events`**: at 1M req/day, job+auth+limit events plausibly generate **150–300k rows/day → 5–9M rows/month**. Insert load is trivial; the 18 admin dashboard endpoints aggregating over an unpartitioned, ever-growing table are not. Partition by month + aggregate-then-prune to `daily_metrics`, or dashboards degrade within a quarter.

**The actual bottleneck: conversion workers.** Assume 3–5% of requests create jobs → **30–50k jobs/day, peak ~3–4 jobs/s**. Average office conversion at 5MB ≈ 2s + 7.5s ≈ **10s of dedicated CPU** (`worker.go:166`); pdfcpu ops ~2s. Required steady-state concurrency at peak = arrival × duration ≈ 3.5 × 8s ≈ **~30 concurrent conversions**. Current capacity: 4 worker services × concurrency 2 × 1 replica = **8 concurrent, only 2 of which can be LibreOffice conversions**. During peak hours the queue grows monotonically; `JOBS_DISPATCH` absorbs 24h of backlog and then **silently expires jobs** (`natsconn.go:68`). CPU math: ~30 concurrent × ~1 core ≈ **30 cores of conversion compute at peak** — this is a multi-host requirement, which Docker Compose cannot express beyond one machine.

**MinIO / gateway bandwidth.** 40k jobs/day × ~5MB in + ~5MB out ≈ **400GB/day through the gateway's MinIO relay** (~40Mbps average, ~250Mbps peak). Feasible on one NIC, but it all funnels through one 0.5-CPU gateway container that is also your API edge and SPA server.

**Redis memory.** Upload sessions, guest sets, denylist, plan cache — all TTL'd, `volatile-lru`, 400MB cap: comfortable. ✅

**Traffic-readiness bracket (verified):**

| Bracket | Verdict |
|---|---|
| <100K/day | ✅ Ready today |
| 100K–500K/day | ✅ API fine; worker capacity needs raising (env-var change + CPU); add monitoring |
| **500K–1M/day** | ⚠️ Requires Phase 1–3: worker fleet scale-out (multi-host), pool budget fix, analytics retention, HA for Redis/NATS/Postgres, CI/CD |
| 1M–5M/day | ❌ Requires k8s/nomad-class orchestration, managed Postgres + pgbouncer, distributed MinIO or real S3, NATS cluster |

---

## 7. Production Readiness Scorecard

| Category | Score /10 | Anchoring findings |
|---|---|---|
| Architecture | 8 | Clean logical boundaries; shared-DB physical reality; cleanup cross-access |
| Folder structure | 9 | Uniform, predictable; stray artifacts only |
| Naming consistency | 8 | Strong conventions; upload/uploads alias, tool aliases |
| Code organization | 8 | Two oversized files; copy-pasted worker skeleton |
| API design | 7 | No versioning; inconsistent pagination totals; ChangePlan semantics |
| Database | 7 | Great indexes; AutoMigrate, pool math, analytics growth |
| Performance | 7 | Clean hot paths; SSE polling, unbounded cache, gateway relay |
| Scalability | 6 | Stateless ✅; worker ceiling, single-host, no HA |
| Security | 7 | Strong authn hygiene; plan self-upgrade, no rotation, no TLS |
| Testing | 6 | Good unit tests; zero CI, no integration/E2E |
| Logging | 9 | Structured, request-ID'd, leveled, rotated |
| Observability | 8 | Metrics+traces wired; nothing consumes them, no alerts |
| Maintainability | 8 | Docs culture, consistency; duplicated fallbacks drift |
| DevOps | 4 | No CI/CD, no zero-downtime, unverified backups, MinIO data-loss SPOF |

**Overall Production Readiness Score: 72/100**

**Traffic readiness today: 100K–500K requests/day.** (API tier alone: 1M+; job pipeline and ops maturity set the binding constraint.)

---

## 8. Critical Issues (🔴 must fix before scaling traffic)

1. **No CI/CD pipeline** — nothing runs 85 test files; deploys are unverified builds. *(DevOps)*
2. **`PUT /auth/plan` allows free self-upgrade to pro** — `auth-service/handlers/auth.go:530-584`. Revenue and abuse-limit bypass (500MB files, 600 req/min). *(Security/Business)*
3. **Worker throughput ceiling** — 2 concurrent conversions per tool family (`worker.go:195-202`, compose replicas 1) vs ~30 needed at 1M-req/day peak; `JOBS_DISPATCH` silently drops queued jobs after 24h (`natsconn.go:68`) with no stuck-job reconciliation. *(Scalability)*
4. **Postgres connection budget over-committed** — ~275 potential pool connections vs `max_connections=200` (`database.go:28-35` × 11 services; `docker-compose.yml:29`). *(Reliability)*
5. **Unmanaged schema migrations** — 11 services race `AutoMigrate` DDL at boot with warn-and-continue failure handling (`database.go:89-118`). *(Reliability)*
6. **MinIO is an unreplicated, unbacked-up store of all user files**; Postgres backups are opt-in and restore has never been drilled. *(Data loss)*
7. **Five single points of failure on one host** with `restart: always` as the only recovery mechanism. *(Availability)*

---

## 9. Refactoring Roadmap

> The staged, do-this-then-this **how-to** for executing the horizontal-scaling
> parts of this roadmap lives in [horizontal-scaling.md](./horizontal-scaling.md).
> This section remains the authoritative *analysis*; that doc is the operator runbook.

### Priority-classified findings

🔴 **Critical**
| Issue | Why it matters | Risk if ignored | Fix | Effort |
|---|---|---|---|---|
| No CI/CD | Untested deploys | Regressions ship straight to prod | GitHub Actions: `go build ./... && go vet ./... && go test ./...` per module + docker build+push on tag | **S** (1–2 days) |
| Plan self-upgrade (`auth.go:530-584`) | Revenue/abuse bypass | Free users take pro limits | Gate `ChangePlan` behind admin role or payment-webhook-driven internal endpoint; downgrade-only for users | **S** |
| Worker capacity + silent job expiry | Queue melts at peak | Jobs lost after 24h, users see eternal "queued" | Raise `WORKER_CONCURRENCY`/replicas per math in §6; add stuck-job sweep to cleanup-worker (`queued`>1h → requeue or fail); alert on JetStream depth | **M** |
| Pool budget vs max_connections | Connection storms | Login/job failures under burst | Explicit per-service `PoolConfig` budget ≤150 total, or pgbouncer | **S** |
| AutoMigrate in prod | DDL races, no rollback | Corrupted/blocked deploys | golang-migrate; single migration job in compose `depends_on` chain; services stop calling AutoMigrate | **M** |
| Backups/restore + MinIO durability | Data loss | Company-ending on disk failure | Make pg backups mandatory; add MinIO versioning + replication (or move to real S3/R2); document + drill restore | **M** |

🟠 **High**
| Issue | Fix | Effort |
|---|---|---|
| No TLS / no edge | Put Caddy/Traefik (or Cloudflare) in front; HSTS; offload SPA + static — ✅ fixed: Caddy edge added (TLS via `PUBLIC_DOMAIN`, serves SPA, routes object bytes; gateway internal-only) | S |
| Refresh-token rotation | Rotate on every `/auth/refresh`, store new hash, deny reuse (delete session on reuse detection) | M |
| `analytics_events` growth | Monthly partitions (native Postgres) + nightly aggregate→`daily_metrics`→prune >90d in cleanup-worker | M |
| Alerting | Prometheus rules → analytics-service's built-in Discord receiver (`shared/discord`, no Alertmanager container); shipped rules cover target-down, 5xx rate, p95 latency, job-failure rate. Extend with JetStream/DLQ depth, pg connections, disk | M |
| Rate-limit/denylist fail-open | Config flag to fail-closed for auth denylist; at minimum alert on Redis errors | S |
| `sslmode=disable` default | Default to `require` outside dev profile | S |
| Session store failures warn-only (`auth.go:327-329`) | Make `StoreSession` failure abort login (it's one insert) | S |
| Analytics `Nak` hot-loop (`subscriber.go:131`) | `NakWithDelay(30s)` | S |

🟡 **Medium**
| Issue | Fix | Effort |
|---|---|---|
| `outputFileCache` unbounded (`jobs.go:41`) | LRU cap or delete the cache | S |
| SSE ephemeral consumer per viewer (`sse.go:44-81`) | `Consume()` callbacks or shared per-instance consumer with in-process fanout | M |
| API versioning | `/api/v1` prefix at gateway + upstream base paths | S–M |
| Pagination totals inconsistent | Standardize `Meta.Total` (or `hasMore`) across services | S |
| `file_metadata.path` index | `CREATE INDEX ... ON file_metadata(path)` | S |
| Duplicated worker skeleton + TTL fallbacks | Extract `shared/worker` runtime; move TTL defaults into one shared constant set — ✅ TTL half fixed (`shared/config/defaults.go`; `shared/authverify` + `shared/database` also consolidated); worker skeleton still open | M |
| Split `jobs.go` (1,216 lines) and `auth.go` (655) | Mechanical file split | S |
| Config validation | Per-service validated Config struct, fail-fast in main() | M |
| `exports.content` bytea | Store exports in MinIO outputs bucket | M |
| Soft-deleted documents never purged | Add purge task (>30d in trash) to cleanup-worker | S |
| Zero-downtime deploys | Two-replica services behind the edge proxy + `docker compose up --no-deps --wait` rolling script; or move to Swarm/k8s in Phase 4 | M |

🟢 **Nice to have**
Remove `cleanup-worker.exe` and `files/` from tree; move Postman file to `/postman/`; CSP header; email verification + CAPTCHA on signup; keyset pagination for history; typed worker errors instead of string matching; `traceparent` propagation through NATS payloads; integration test suite with testcontainers; record k6 baselines in repo.

### Phased plan (3 months)

**Phase 1 — Immediate (weeks 1–2):** CI pipeline (build/vet/test/image push) · fix `ChangePlan` authorization · pool budget or pgbouncer · mandatory pg backups + restore drill · `StoreSession` fail-hard · `NakWithDelay` · `file_metadata.path` index · remove stray artifacts/secrets from tree · TLS edge proxy.

**Phase 2 — Before 100K req/day (weeks 3–5):** Prometheus + Grafana with the alerts above (delivery via analytics-service's Discord receiver, no Alertmanager container) · golang-migrate cutover · stuck-job reconciliation sweep · raise worker concurrency to CPU-matched values and load-test with the existing k6 scripts · refresh-token rotation · analytics partitioning + retention · `/api/v1`.

**Phase 3 — Before 500K req/day (weeks 6–9):** Second host for workers (compose per host targeting shared NATS, or adopt Nomad/k3s) · Redis Sentinel or managed Redis · NATS 3-node cluster (JetStream R3 for JOBS_DISPATCH) · MinIO replication or migrate to R2/S3 · SSE consumer refactor · LRU cache fix · zero-downtime rolling deploys · integration test suite in CI.

**Phase 4 — Before 1M req/day (weeks 10–13):** Managed/HA Postgres (streaming replica + failover) with pgbouncer · worker autoscaling on JetStream queue depth (KEDA if k8s) · CDN for SPA + presigned downloads (bypass gateway for GET bytes) · SLOs on the existing histograms · chaos drill: kill each stateful service in staging and verify behavior matches expectations.

---

## 10. Final Verdict

**Approve for continued operation at current scale; conditional approval for the 1M req/day target.**

The engineering fundamentals here are unusually strong — this codebase does correctly a dozen things (idempotency, presigned uploads, work-queue semantics, structured observability, secret validation, graceful shutdown) that most backends at this stage haven't attempted. Nothing in the application code architecture needs rethinking to reach 1M req/day.

What stands between this system and that target is not code but **operations**: a CI pipeline that doesn't exist, a deployment topology with five single points of failure and no second host, a worker fleet sized for a demo, a database schema managed by boot-time side effects, and one authorization check that gives away the paid plan. Every one of these has a well-understood, bounded fix, and the roadmap above fits comfortably inside the 3-month window with one to two engineers.

**Fix the 🔴 list before marketing pushes traffic. Ship Phase 1 this sprint — most of it is days, not weeks.**

---
*Audit artifacts: findings verified against source at commit-time snapshot of 2026-07-02. Line references are to files as read during this audit.*

---

## Appendix — Deployment Strategy Review (2026-03-19)

An earlier, infrastructure-focused review of `docker-compose.yml`, `deploy.sh`, and the Dockerfiles. Where it overlaps the audit above, **the audit (2026-07-02) is authoritative** — several of the original Critical/Medium items have since been resolved and are folded into the audit's findings and roadmap. Retained here for the deployment-specific positives, the resource-budget design, and a status log of the original 13 findings.

### What the deployment does well

- **Multi-stage Docker builds** with `scratch` base images — minimal attack surface, small images
- **Non-root containers** (`appuser` UID 10001) across all services
- **Health checks on all services** with dependency ordering via `depends_on` + `condition`
- **Shared Go builder base image** (`fyredocs-go-builder`) bakes the module cache + a warm `go-build` cache into image layers, so it survives `docker builder prune`/fresh machines — each service recompiles only its own packages (a cold ~21-min compile becomes seconds). See `docs/developer/architecture/base-image-setup.md`.
- **Sequential builds** in `deploy.sh` with `go build -p ${GO_BUILD_PARALLELISM}` (default 6) — leaves CPU headroom for the daemon/OS on constrained hosts
- **Shared base image** (`fyredocs-base`) for PDF tooling — avoids redundant layers across workers
- **Go workspace** (`go.work`) for unified dependency management

### Resource limits — auto-budgeted (resolved)

Every service carries `deploy.resources.limits` (memory + cpus), and `deploy.sh` auto-computes them so the **whole stack stays under a configurable percentage of the host's total RAM/CPU** (`RESOURCE_BUDGET_PCT`, default 70, clamped 50–90), on any machine, with no specs hardcoded:

- "Total available" is read from `docker info` (`.MemTotal` / `.NCPU`) — the full host on a Linux VPS, the Docker Desktop VM's allocation on macOS.
- `MEM_BUDGET = PCT% × MemTotal`, `CPU_BUDGET = PCT% × NCPU`, distributed across containers by **responsibility-based weights** (LibreOffice/OCR workers + MinIO get the bulk; the api-gateway gets a real CPU share as it sits on every request's hot path; near-idle services get a sliver), so **Σ(limits) ≤ budget** even with everything maxed at once. The co-located Postgres (`db`) and the `db-backup` sidecar are **included in the weighted budget** (`db` is the largest non-worker memory weight, ~1G on a 16GB box); the only always-on container deliberately left out is `caddy`, which belongs to the reserved OS/edge headroom.
- Each limit is exposed as `${<SERVICE>_MEM_LIMIT:-<default>}` / `${<SERVICE>_CPU_LIMIT:-<default>}` (including `DB_*` and `DB_BACKUP_*`); deploy.sh exports the computed values (exported env wins over `.env` defaults). A plain `docker compose up` falls back to the built-in defaults, which equal the pre-budget hardcoded values. On hosts small enough that `db` lands below ~512MB (roughly sub-8GB at 80%), lower `DB_SHARED_BUFFERS` (default `256MB`) or point `DATABASE_URL` at a managed Postgres.
- **`notification-service` is off by default** behind the `notifications` profile (config preserved, image still built) — enable with `--profile notifications`.
- Worker pools (`*_CONCURRENCY`, `OCR_MAX_WORKERS`, `UNOSERVER_INSTANCES`) are derived from each worker's *scaled* memory cap, so no pool can be sized past the RAM its container is allowed.

Preview for any host: `./deployment/deploy.sh --dry-run` (override with `MEM_TOTAL_MB=… NCPU=…` to model a different box).

### Status of the original 13 findings

| # | Issue | Severity | Current status |
|---|-------|----------|----------------|
| 1 | Hardcoded DB credentials | Critical | ✅ Resolved — moved to gitignored root `.env` with variable interpolation (secrets hygiene still tracked as audit S4) |
| 2 | No reverse proxy / TLS | Critical | ✅ Resolved — Caddy edge terminates TLS (automatic HTTPS via `PUBLIC_DOMAIN`), serves the SPA, routes object bytes; gateway is now internal-only (audit S5) |
| 3 | No CI/CD pipeline | Critical | ❌ Open — see audit §8.1 and roadmap Phase 1 |
| 4 | No rollback strategy | Critical | ❌ Open — images not tagged/pushed to a registry (audit §5.15) |
| 5 | `chmod 777` on volumes | Medium | ✅ Obsolete — no shared filesystem volume; all bytes live in MinIO |
| 6 | Infra ports exposed to host | Medium | ✅ Resolved — db/redis/nats internal-only; only the edge (80/443) and MinIO console (loopback) are published |
| 7 | No resource limits | Medium | ✅ Resolved — see "Resource limits" above |
| 8 | No log retention | Medium | ✅ Resolved — `json-file` with `max-size: 10m` / `max-file: 3` on all services |
| 9 | OTel collector not deployed | Low | ❌ Open — services export to `otel-collector:4318` but no collector runs (audit §5.11) |
| 10 | No DB backups | Medium | ⚠️ Partial — `db-backup` sidecar does hourly `pg_dump` → external S3 via rclone (opt-in on `BACKUP_S3_*`); restore drill still undocumented (audit §5.15). See [backup-and-restore.md](./backup-and-restore.md) |
| 11 | `deploy.sh` health-check gaps | Medium | ⚠️ Partial — edge/DB waits exist; per-service health waits added for single-service deploys |
| 12 | No frontend deployment | Medium | ✅ Resolved — SPA is built and served by the Caddy edge from `fyredocs_frontend/dist` |
| 13 | Redis no authentication | Medium | ✅ Resolved — `--requirepass ${REDIS_PASSWORD}`, required by `deploy.sh` |
