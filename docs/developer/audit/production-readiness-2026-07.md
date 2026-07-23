# Fyredocs — Production-Readiness Engineering Audit

**Date:** 2026-07-23
**Scope:** `fyredocs_backend` (11 Go microservices + `shared/`, deployment, DB, security, observability) — deep audit; `fyredocs_frontend` (React SPA) — light pass. Mobile app (`fyredocs_app`) excluded.
**Method:** Read-only static review across 7 domain audits (security, Go concurrency/memory, database, API design, infra/DevOps, observability, frontend), plus objective `go build` / `go vet` / `go test` runs. Every finding below is cited to `file:line`. The highest-impact and most subtle findings were independently re-verified against source before inclusion; where a claim did not survive verification, it was corrected or downgraded (noted inline).

> This is not a feature review. It evaluates whether the system is ready to run as a high-availability, horizontally scalable SaaS platform.

---

## 1. Executive Summary

Fyredocs is a **well-architected system with strong engineering fundamentals** that is **not yet production-ready** for a high-availability launch. The microservice boundaries are real and disciplined, the file pipeline is genuinely well-designed (presigned S3 multipart, streaming everywhere, tmpfs budgets, DLQ, circuit breakers), authentication *verification* is correct, and the observability *plumbing* is thoughtfully built. The blockers are concentrated in a small number of high-impact areas: **access-token revocation is silently broken**, there is **no alerting**, the metrics pipeline has a **cardinality self-DoS**, the deploy path has **downtime and no rollback**, and **disaster recovery has real data-loss gaps**.

None of the blockers are architectural dead-ends. They are fixable in a focused pre-launch sprint.

### Scores (0–100)

| Dimension | Score | One-line justification |
|---|---:|---|
| **Overall production readiness** | **58** | Strong foundation; several must-fix blockers in security-revocation, alerting, DR, and deploy safety. |
| SaaS maturity | 56 | Multi-tenant RBAC, plans, quotas, guest flow all present; ops maturity (alerting/rollback/DR) lags. |
| Architecture | 80 | Clean, honest microservices; correct async pipeline; sound edge design. |
| Security | 55 | Excellent fundamentals (authz, IDOR, injection-safe) undercut by broken token revocation + weak defaults + unauthenticated internal mesh. |
| Performance | 70 | Streaming file path, result cache, pooling; thin on saturation metrics and a few unbounded queries. |
| Scalability | 58 | Stateless services + queue workers scale well; single-host + single Postgres/Redis/NATS/MinIO are hard SPOFs; migration-on-boot blocks scale-out. |
| Maintainability | 76 | Readable, consistent, mostly well-tested; some worker drift and a red test in CI. |
| Reliability | 54 | No alerting, deploy downtime, no rollback, revocation bug, DR gaps. |
| Observability | 60 | Great sync-path correlation and Grafana wiring; no alerting, cardinality bomb, async pipeline untraceable. |

### The 8 things to fix before launch (detail in §3 and §5)

1. **Access-token revocation is non-functional** on every server-side path (logout via cookie, password-reset, admin force-logout). *(Security)*
2. **No alerting / Alertmanager / SLOs** — every incident is customer-discovered. *(Observability)*
3. **Prometheus cardinality bomb** — raw URL paths (with UUIDs) used as metric labels at the gateway → metrics-stack OOM. *(Observability)*
4. **Multiple services run `AutoMigrate` on the same shared tables** with divergent schema definitions → concurrent DDL + non-deterministic schema. *(Database / Deploy)*
5. **Liveness `/healthz` checks external dependencies** on the gateway + 4 workers → a Redis/NATS blip restarts healthy containers (cascade). *(Reliability)*
6. **Disaster-recovery gaps** — `uploads` bucket unbacked, no PITR, single Postgres volume, **no image tagging/registry → no rollback**. *(Infra / DR)*
7. **Weak, committed default JWT secret** becomes the live signing key on any non-`deploy.sh` deployment. *(Security — config)*
8. **Insecure production defaults in `.env`** (`AUTH_COOKIE_SECURE=false`, `sslmode=disable`, plain-HTTP origins). *(Security — config)*

---

## 2. Objective build/test evidence

Run in a Go 1.25.6 workspace (`go.work`, 12 modules), per-module:

- `go build ./...` — **clean, all 12 modules.**
- `go vet ./...` — **clean, all 12 modules.**
- `go test ./...` — **one failing package: `api-gateway`.**

The failure is real and reproducible:

- `TestNewProxyStreamsResponses` (`api-gateway/main_test.go:204`) type-asserts the result of `newProxy(...)` to `*httputil.ReverseProxy`. But `newProxy` was changed (during the error-handling hardening) to wrap the reverse proxy in a per-upstream circuit breaker and return an `http.HandlerFunc` (`api-gateway/main.go:309,360-380`). The assertion now fails.
- **No production bug** — the streaming config it means to guard (`proxy.FlushInterval = -1`) is intact at `api-gateway/main.go:318`. But the committed test suite does not pass, which contradicts the repo's own `CLAUDE.md` §9 ("Claude must run `go test ./...` … Missing or broken tests are treated as an incomplete task") and means any CI gate on `go test` is currently red.
- **Fix:** update the test to assert against the wrapped handler's behavior (make a request, assert it streams / that the breaker is wired) rather than the concrete type. *Fix before prod: recommended (unblocks CI).*

---

## 3. Findings by severity

Every Critical/High below was verified against source (many re-read directly during this audit; citations are exact).

### CRITICAL

#### C1 — Access-token revocation is silently non-functional on every server-side path *(Security)*
**Evidence (verified directly):**
- The Redis denylist keys entries by `sha256(input)`: `shared/authverify/guest.go:128-131`. The verifier looks up the **raw** JWT: `shared/authverify/verifier.go:125` (`IsTokenDenied(ctx, tokenString)`).
- **Logout (cookie path):** `Logout` only revokes if `extractAccessToken` returns a token (`auth-service/handlers/auth.go:294-299`). `extractAccessToken` reads **only** the `Authorization: Bearer` header or a `c.Get("access_token")` context value (`auth.go:514-533`). The SPA authenticates via HttpOnly cookies; the Gin auth middleware reads the cookie only to *verify* and stores the parsed `AuthContext` via `SetGinAuth` — it never sets `c.Set("access_token")` (`shared/authverify/middleware_gin.go:64-84`). Nothing in production sets that key (only `auth_test.go:106`). So on the cookie path `extractAccessToken` returns `("",false)` → `denyAccessToken` never runs → neither the Redis denylist add **nor the DB session-row delete** (both live inside `denyAccessToken`, `auth.go:366-368`) happens. Logout merely clears client cookies.
- **Password-reset & admin force-logout (double-hash):** `ResetPassword` (`auth-service/handlers/password_reset.go:185`), `RevokeUserSessions` (`admin.go:41`), and `RevokeSession` (`admin.go:82`) pass `s.AccessTokenHash` — already `hex(sha256(jwt))` (`internal/models/token.go:34-37,44`) — into `DenyToken`, which hashes it **again**. Stored key = `sha256(hex(sha256(jwt)))`; verifier looks up `sha256(jwt)`. They can never match.

**Production impact:** A leaked or captured access token remains valid across the gateway and every domain service for its full TTL (default **8h**) after the user logs out, after a **password reset** (the canonical account-compromise recovery), and after an **admin force-logout**. "Revoke all sessions" does not revoke the live credential. This is the single most serious finding.

**Fix:** (a) make `extractAccessToken` also read the `access_token` cookie; (b) add a denylist method that stores a *pre-hashed* value verbatim (or pass the raw token) so reset/admin paths match the verifier's lookup; (c) add a regression test asserting a reset/revoked token is rejected by `Verify`. **Fix before prod: YES.**

#### C2 — No alerting, Alertmanager, or SLOs — incidents are customer-discovered *(Observability)*
**Evidence:** `deployment/prometheus/prometheus.yml` has no `rule_files:` and no `alerting:` block; there is no Alertmanager service in `deployment/docker-compose.yml`; no alert/rule files exist under `deployment/`. Prometheus only scrapes; `grafana/.../fyredocs-overview.json` is a passive dashboard.

**Production impact:** A service can be down, error rate at 100%, the DLQ filling, or Postgres unreachable, and no one is paged. Every incident is found by a customer.

**Fix:** Add Alertmanager + `rule_files`. Minimum set: `up == 0` per target, high 5xx ratio per service, p95 latency SLO burn, `jobs_failed_total` rate, DLQ depth, readiness flapping. Define SLOs and alert on burn rate. **Fix before prod: YES.**

#### C3 — Prometheus label-cardinality bomb (raw URL path as a metric label) *(Observability)*
**Evidence:** `shared/metrics/metrics.go:65` labels `RequestDuration` by `r.URL.Path`, wired around the whole gateway mux at `api-gateway/main.go:244`. The gateway sees per-resource IDs (`/api/jobs/{uuid}`, `/api/documents/{uuid}`, SPA assets, MinIO presigned paths). The Gin variant has the same hazard via its `path = c.Request.URL.Path` fallback when `FullPath()==""` (`metrics.go:43-45`), so unmatched/404-scanned paths also mint series.

**Production impact:** Every distinct UUID → a new time series (× method × status × ~12 histogram buckets) → unbounded series growth → Prometheus OOM, taking down all metrics (and any future alerts with them). An attacker scanning random paths accelerates it — a self-DoS.

**Fix:** Label by a low-cardinality route template (map to the matched proxy prefix, e.g. `/api/jobs`), never the raw path. Add a max-cardinality guard. **Fix before prod: YES.**

#### C4 — Multiple services run `AutoMigrate` against the same shared tables with divergent schemas *(Database / Deploy)*
**Evidence (verified directly):** `processing_jobs` and `file_metadata` are declared and migrated by job-service **and** all four PDF workers. `convert-to-pdf/internal/models/database.go` `Migrate()` calls `AutoMigrate(&ProcessingJob{}, &FileMetadata{})`. The definitions differ: job-service's `FileMetadata.JobID` has `constraint:OnDelete:CASCADE` + a `ProcessingJob` FK association and uses UUIDv7 PKs (`job-service/internal/models/job.go:42-43,33,60`); the workers' copy has a plain `index;not null` (no FK, no cascade) and UUIDv4 PKs (`convert-to-pdf/internal/models/job.go:41,33,52`).

**Production impact:** On a full `up`, ~10 services run DDL against one Postgres roughly in parallel — concurrent `ACCESS EXCLUSIVE` DDL on shared catalogs. Whether the FK/CASCADE exists on `file_metadata` depends on **which service migrated last** (non-deterministic). Every rolling deploy re-runs migrations and can re-thrash the schema. The worker migrations are pure overhead on the hottest table and a live drift vector.

**Fix:** Exactly one owner (job-service) migrates these tables; workers must not `AutoMigrate` them. Longer term, run migrations as a one-shot pre-deploy job under an advisory lock (see C5). **Fix before prod: YES.**

#### C5 — Disaster-recovery gaps: no rollback path, uploads unbacked, no PITR, single volume *(Infra / DR)*
**Evidence:**
- **No image tagging/registry → no rollback:** images are built locally as implicit `:latest` and `deploy.sh:523` runs `docker image prune -f`, deleting the previous image. Rollback requires `git checkout <sha>` + full rebuild (downtime), and a host loss leaves no image artifact (`deploy.sh:441-449`, all Dockerfiles `FROM …:latest`).
- **`uploads` bucket never backed up:** `docker-compose.yml:1035` (`BACKUP_FILES_BUCKETS:-outputs`). Raw user inputs are excluded; DR is a single hourly `pg_dump | gzip | rclone` + `rclone sync` of `outputs` only.
- **No PITR / WAL archiving**, and all primary state (Postgres volume, `minio_data`) is on one host — RPO ≈ 1h and total loss of in-flight uploads on host failure.
- The restore doc (`docs/developer/architecture/backup-and-restore.md`) claims uploads "auto-expire in 2 days (bucket lifecycle rule)", but `minio-init` explicitly *removes* lifecycle rules (`docker-compose.yml:196`) — the doc is stale.

**Production impact:** No safe rollback of a bad deploy; up to 1h of committed DB writes lost and all in-flight uploads permanently lost on host/volume failure.

**Fix:** Tag every build with the git SHA + push to a registry; stop pruning the previous image; make `deploy.sh` accept a rollback tag. Back up `uploads` (or explicitly accept+document the loss window). Add WAL-based PITR (WAL-G/pgBackRest) or a streaming replica on a second host. Reconcile the restore doc. **Fix before prod: YES (rollback + DR decision at minimum).**

#### C6 — Liveness `/healthz` checks external dependencies → dependency blips restart healthy containers *(Reliability)*
**Evidence:** `/healthz` pings Redis on the gateway (`api-gateway/main.go:184-198`) and pings **Redis and NATS** on all four workers (`convert-to-pdf/main.go:103-116`, `convert-from-pdf/main.go:106`, `organize-pdf/main.go:106`, `optimize-pdf/main.go:94`); Docker healthchecks target `/healthz` (`docker-compose.yml:344,592,657,721,788`). The correct pattern already exists in-repo: auth-service, job-service, and the four "quiet" services expose static-200 `/healthz` liveness with deps in `/readyz` (`user-service/handlers/health.go:16-36`, `auth-service/routes/routes.go:105`, `job-service/routes/upload_routes.go:105`).

**Production impact:** A transient Redis/NATS hiccup flips liveness to 503; with `restart: always` (and Swarm-style `deploy:` blocks) the container is killed/rescheduled — a *dependency* blip triggers a restart cascade across otherwise-healthy application containers, precisely when the dependency is already struggling.

**Fix:** Make `/healthz` a static 200 on the gateway + 4 workers; move the Redis/NATS checks to `/readyz` (already present on the workers). **Fix before prod: YES.**

### HIGH

#### H1 — Async job pipeline is untraceable end-to-end (no trace propagation across NATS) *(Observability)*
`shared/queue/events.go:15-43` — `JobEvent` has no `traceparent`; `PublishJobEvent` injects no NATS headers; consumers `json.Unmarshal` with no `propagator.Extract` (`convert-to-pdf/internal/worker/worker.go:313`). A freshly minted `CorrelationID` (`job-service/handlers/jobs.go:324`) is carried instead, unrelated to the request's `trace_id`. You cannot follow a failing job from the HTTP request into the worker. **Fix:** inject W3C context into NATS headers at publish, extract on consume, start the worker span as a child. Fix before prod: YES (async is the core product path).

#### H2 — Unbounded async export: goroutine-per-request + full in-memory buffering + Postgres-`bytea` storage *(Go / DB)*
**Evidence (verified directly):** `document-service/handlers/exports.go:67` spawns a bare `go generateExport(exp.ID)` — no semaphore/pool/queue — using `context.Background()` (`:124`), untracked by any WaitGroup. `exportDocs` does `Find(&docs)` with no `LIMIT` (`:207-208`), builds the whole artifact in memory, stores it in a `bytea` column (`models/export.go:30`), and `DownloadExport` serves it via `c.Data` (`:116`). Shutdown (`document-service/main.go:91-96`) neither cancels nor awaits it, so in-flight exports die mid-run and the row is stuck `processing` forever. **Impact:** memory exhaustion under concurrent/large exports (OOM/DoS), TOAST bloat inflating every backup, and permanently-stuck exports after any deploy. **Fix:** bound concurrency (queue-backed like the PDF pipeline), cap rows, stream artifacts to object storage + presign the download, track the goroutine for shutdown. Fix before prod: YES (concurrency + row cap minimum).

#### H3 — No refresh-token rotation or reuse detection *(Security)*
`Refresh` (`auth-service/handlers/auth.go:174-246`) issues a **new access token only** and updates `AccessTokenHash`; it never re-issues the refresh token or updates `RefreshTokenHash`. The same refresh token is reusable for its full 7-day TTL, with no stolen-token reuse detection. This contradicts the "refresh rotation" claim in the README. **Fix:** rotate the refresh token every `/auth/refresh`; treat reuse of a retired refresh hash as a breach signal and revoke the session family. Fix before prod: recommended.

#### H4 — Warn-only creation of idempotency UNIQUE indexes can leave integrity guardrails silently absent *(Database)*
The partial UNIQUE indexes that backstop finalize/idempotency dedup — `idx_doc_user_source_job` (`document-service/internal/models/database.go:57`), `idx_notif_user_source` (`notification-service/internal/models/database.go:49`) — are created by best-effort `Exec` that only `slog.Warn`s on failure, with no migration-history table to detect a miss. If one fails to create, the service starts "healthy" with the dedup constraint missing, and the count-then-create TOCTOU in the event subscribers (`document-service/subscriber/subscriber.go:109-135`, `notification-service/subscriber/subscriber.go:130-140`) can then create duplicates. **Fix:** treat idempotency/uniqueness indexes as fail-fast (verify existence at boot), and adopt versioned migrations. Fix before prod: YES (promote these indexes to fail-fast at minimum).

#### H5 — No versioned migrations; `AutoMigrate` cannot do safe destructive/type changes *(Database)*
Schema is managed by GORM `AutoMigrate` at every service boot (e.g. `auth-service/internal/models/database.go:46-79`) plus warn-only raw SQL. AutoMigrate never drops/renames and will attempt in-place `ALTER TYPE` (table rewrite under `ACCESS EXCLUSIVE`) with no rollback; under the DSN's `statement_timeout=15s` a large-table rewrite aborts mid-flight or blocks writers. No history table means no record of what actually ran. **Fix:** introduce versioned migrations (golang-migrate/goose/atlas) run once as a pre-deploy step; keep AutoMigrate for dev only. Fix before prod: YES (for the process).

#### H6 — Full deploy takes the entire stack down *(Infra)*
`deploy.sh:417` runs `docker compose down --remove-orphans` **before** building 11 services sequentially, then `up -d`. Caddy (the public edge) is down for the whole multi-minute window. Single-service mode (`up -d --build --force-recreate <svc>`) is near-zero-downtime and is the right tool, but any change touching shared code forces the full path. **Fix:** build images before `down` (or build-then-swap per service via a registry, C5). Fix before prod: YES if any uptime SLA.

#### H7 — Weak, committed default JWT secret becomes live on non-`deploy.sh` deployments *(Security — config)*
**Evidence (verified directly, incl. precedence):** `.env:22` sets `JWT_HS256_SECRET=aT9kLmW3xQr7vBn5yHs2jFp8cUe6dGi4` (32 chars, human-typeable), and the identical value is hard-coded in `shared/config/config_test.go:126`. `deploy.sh` sources `.env` (line 284) and **then overrides** `JWT_HS256_SECRET` by exporting the generated 64-hex `.jwt_secret` (line 315), which wins for compose interpolation — so a `deploy.sh` deployment uses a strong random secret. **But** the documented dev/single-service workflows (Makefile `make up`, `docker compose up`, essentials stack) do not run `deploy.sh`, so they use the committed 32-char `.env`/test value as the live HS256 signing key. With HS256 the signing key is the verification key, so anyone with repo access can then forge admin/super-admin JWTs across all services. `ValidateJWTSecret` (`shared/config/config.go:112-141`) only blocks four specific literals, not this one. **Fix:** never commit a real-shaped secret (make the test generate a random one); require the secret from an injected env/secret manager on every deploy path; raise the minimum to 64 chars. Fix before prod: YES.

#### H8 — Insecure-by-default production config shipped in `.env` *(Security — config)*
`.env` ships `AUTH_COOKIE_SECURE=false`, `sslmode=disable`, and plain-HTTP origins (`PUBLIC_DOMAIN=:80`, `PUBLIC_ORIGIN=http://localhost`); prod values exist only in trailing comments. Cookie flags at `auth-service/handlers/auth.go:457-500` set HttpOnly (good) but drive `Secure` from that flag. Deployed as-is, session cookies travel in cleartext and DB traffic is unencrypted. **Fix:** set `AUTH_COOKIE_SECURE=true`, real HTTPS origins, and `sslmode=require`/`verify-full` before deploy; fail startup (not just `slog.Warn`) if `AUTH_COOKIE_SECURE=false` under a prod flag. Fix before prod: YES (configuration).

#### H9 — Unbounded list endpoints *(API / DB)*
`user-service/handlers/orgs.go:22,38` (`ListOrganizations`) and `:136` (`ListMembers`) return the full result set with no `LIMIT` and no pagination. A large tenant returns everything in one response (memory/latency blowup, DoS vector). Secondary: `notification-service/handlers/notifications.go:139` and `document-service/handlers/exports.go:75` hardcode `.Limit(50)` with no paging, so clients can never see older records. **Fix:** add capped `page`/`limit` (mirror document-service's 25/100 pattern) and return `Meta{Page,Limit,Total}`. Fix before prod: YES for the uncapped org/member lists.

#### H10 — Admin can impersonate a super-admin (privilege escalation) *(Security)*
**Evidence (verified directly):** `ProxyLogin` admits callers with role `admin` **or** `super-admin` (`auth-service/handlers/proxy_login.go:27-31,54`), fetches the target user, and issues a token carrying the **target's** role (`respondWithImpersonationToken`, `proxy_login.go:96-116`) with **no check that the target's privilege ≤ the caller's**. An `admin` can proxy-login as a `super-admin` and obtain super-admin scope (analytics `/admin/*`, DLQ redrive, plan changes). **Fix:** add a role-rank check forbidding impersonation of an equal/higher-privileged role. Fix before prod: recommended (yes if `admin` is a real, lower-trust role).

### MEDIUM

- **M1 — `/internal/*` endpoints are unauthenticated (network-isolation only).** `auth-service/routes/routes.go:98-103` (`revoke-sessions`, `DELETE /sessions/:id`, `GET /users/:id/plan`) and `notification-service` `POST /internal/notifications` have no auth. An `INTERNAL_API_TOKEN` mechanism exists in organize-pdf (`internal/httpapi/detect.go:45-50`) but the var is unset, so it's inert. Not reachable via the edge (verified: Caddy routes only `/api /auth /admin /healthz /grafana`), so exploitation needs a network foothold or SSRF — but there is no blast-radius containment if any one service is compromised. **Fix:** enforce `INTERNAL_API_TOKEN` (or mTLS) on all `/internal` routes. *(Security)*
- **M2 — Gateway guest rate-limit keyed on spoofable `X-Forwarded-For`.** `api-gateway/internal/ratelimit/ratelimit.go:96-106` takes the leftmost XFF entry; Caddy doesn't strip client-supplied XFF, so a guest can rotate the header to get a fresh bucket per request and bypass the 30/min guest ceiling on expensive OCR/convert jobs. (Auth-service's login limiter is *not* affected — `TRUSTED_PROXIES` is set in compose.) **Fix:** derive client IP from a trusted-proxy hop count. *(Security)* — *Note: one agent flagged the auth-service limiter as also broken; verification showed compose sets `TRUSTED_PROXIES=172.18.0.0/16,…`, so that concern does not apply.*
- **M3 — Connection-pool headroom is thin under scale-out.** Summed pools ≈145 vs `max_connections=200` (`docker-compose.yml:29`); scaling workers ×2 (+20) plus `pg_dump` and admin sessions approaches the ceiling. At exhaustion, callers block on pool-wait until their context deadline (job create uses a 10s tx ctx, `job-service/handlers/jobs.go:349`). **Fix:** PgBouncer (transaction pooling) + lower per-service `MaxOpenConns`, or raise `max_connections` with sized `shared_buffers`. *(DB)*
- **M4 — Worker `outputFileCache sync.Map` never evicts.** `job-service/handlers/jobs.go:41`; `DeleteJobByID` (`:492-540`) and the cleanup sweep never call `.Delete`, so every distinct downloaded job leaks a `FileMetadata` entry → slow unbounded heap growth on long-lived replicas. **Fix:** `outputFileCache.Delete(job.ID)` in delete + cleanup, or a bounded/TTL LRU. *(Go)*
- **M5 — Direct-multipart temp-file leak.** `job-service/handlers/jobs.go:244` calls `c.MultipartForm()` (`MaxMultipartMemory = 50<<20`, `main.go:115`) with no `MultipartForm.RemoveAll()`, so large direct uploads leave stdlib temp files until the OS reaps them. **Fix:** `defer c.Request.MultipartForm.RemoveAll()`. *(Go)*
- **M6 — Worker graceful shutdown never joins `worker.Run`.** All four workers run `Run` in a bare goroutine and exit on SIGTERM without awaiting its `wg.Wait()` drain (`convert-to-pdf/main.go:75,188-198` et al.); JetStream `AckWait` redelivery prevents data loss, but active jobs are interrupted and redone, and a job killed mid-write can leave partial state. **Fix:** signal `Run` completion and `<-done` (bounded) before exit. *(Go)*
- **M7 — Thin metrics coverage: no USE/saturation, no queue/DLQ/latency metrics.** `shared/metrics/metrics.go` defines only HTTP duration + `jobs_processed/failed_total`. Missing: in-flight gauge, DB-pool stats, worker-semaphore saturation, **DLQ depth** (`JOBS_DLQ` never measured), **job latency**, upload-size histogram, plan-limit-hit counter. You cannot alert on backpressure or a stalling pipeline. **Fix:** add low-cardinality gauges/histograms (label by `tool`/`service`). *(Observability)*
- **M8 — Metadata read-modify-write lost-update race in 2 of 4 workers.** `convert-from-pdf` and `organize-pdf` do `First → merge map → Update("metadata")` (`convert-from-pdf/internal/worker/worker.go:565-588`), while `convert-to-pdf`/`optimize-pdf` use the atomic Postgres JSONB `|| ?::jsonb` merge (whose own comment calls the RMW form "a lost update"). **Fix:** port the atomic merge to all four. *(Go)*
- **M9 — Concurrent `AutoMigrate` + single-sweeper assumption block horizontal scale-out.** `docker compose up --scale <svc>=N` starts N replicas each running `Migrate()` on the same tables, and `--scale` cannot set the per-replica `CLEANUP_ENABLED=false` the job-service cleanup sweeper needs. **Fix:** migrations as a one-shot job under `pg_advisory_lock`; leader-elect the cleanup loop. *(Infra/DB)*
- **M10 — Grafana anonymous access + default `admin/admin`.** `docker-compose.yml:1229,1241-1242` — password falls back to `admin`, `.env` doesn't set `GRAFANA_ADMIN_PASSWORD`, anonymous Viewer on. Mitigated by loopback binding + edge `forward_auth`, but anyone with loopback/SSH-tunnel/SSRF access to `:3000` gets admin. **Fix:** set a strong password. *(Infra)*
- **M11 — Observability stack runs by default but is outside the resource budget.** `deploy.sh` enables the `observability` profile by default, but `compute_resource_budget` only caps the 16 weighted app/infra containers; the six observability services (~2.5 GB) sit on top of the 70% cap → host over-commit / OOM risk on ≤8 GB hosts. **Fix:** fold observability into the budget or subtract its fixed limits; refuse to enable below a RAM floor. *(Infra)*
- **M12 — Deep-`OFFSET` pagination + `COUNT(*)` on append-heavy tables.** `analytics-service/handlers/metrics.go:256-263`, `document-service/handlers/documents.go:126-135` (Count+Find on two goroutines = 2 pool conns/req), `job-service/handlers/jobs.go:431` (page up to 100000). Degrades as tables grow. **Fix:** keyset/cursor pagination (UUIDv7/`created_at` indexes already support it); approximate counts. *(DB)*
- **M13 — otel-collector: SPOF, no export buffering, and tracing self-disables permanently on a startup race.** `tracer.go:39-43` probes the collector once at `Init`; if unreachable at startup, the process emits **no traces** until restarted (fails open, silently). Single collector, `batch` only — no `memory_limiter`/`sending_queue`/retry (`otel-collector/config.yaml:21-46`). (Metrics + logs bypass the collector, so a collector outage costs only traces — a sound separation.) **Fix:** periodic probe / rely on exporter retry; add `memory_limiter` + `sending_queue`. *(Observability)*
- **M14 — API `error.code` vocabulary is inconsistent.** Middleware emits canonical `AUTH_UNAUTHORIZED`/`AUTH_FORBIDDEN`/`INVALID_INPUT` (`shared/authverify/errors.go:13-14`), but ~11 handlers emit bare `"UNAUTHORIZED"`/`"FORBIDDEN"`/`"INVALID_BODY"` literals for the same semantics. A client switching on `error.code` sees two vocabularies for one condition. This is the deferred migration in `TODO.md`; it changes wire values, so pair it with API versioning (see M15). *(API)*
- **M15 — No API versioning.** No `/v1` prefix anywhere (`api-gateway/main.go:99-175`); `openapi.yaml` version is cosmetic. With the M14 code migration pending (a wire-breaking change), there's no namespace to introduce it without breaking clients. **Fix:** add `/api/v1` before public launch and do the code migration under it. *(API)*
- **M16 — OpenAPI/Postman drift.** 5 admin analytics endpoints are unregistered in `openapi.yaml` (`acquisition`, `api-trends`, `executive`, `queues`, `revenue` vs `analytics-service/routes/routes.go:36-55`); the Postman collection documents `?limit&offset` for job lists but `GetJobsByTool` reads `page`+`limit` and ignores `offset` (`jobs.go:430-432`). Docs are shipped artifacts per `CLAUDE.md` §5.5. *(API)*
- **M17 — Manual-only request validation; emails never validated.** Exactly one `binding:` tag exists across 22 `ShouldBindJSON` sites; signup accepts any string as email (no `mail.ParseAddress`/regex anywhere). Bad data persists and breaks reset/notification delivery. **Fix:** standardize binding tags (`required,email,min,max,oneof`). *(API)*
- **M18 — Cross-service orphan rows with no reaping.** No cross-service FKs (by design), and no user-deleted/org-deleted fan-out events, so deleting a user leaves documents/folders/tags/memberships/notifications dangling; `Organization.OwnerUserID` can point at a missing user. **Fix:** domain events + subscribers to purge/reassign, or a reconciliation sweep. *(DB)*
- **M19 — Frontend CSP will silently break webfonts + analytics in prod.** `fyredocs_frontend/index.html:7-9` loads Google Fonts + Plausible, but the Caddy CSP is strict same-origin (`style-src 'self'`, `font-src 'self' data:`, `script-src 'self'`, `connect-src 'self'`). Fonts fall back to system, Plausible loads nothing and its beacon is blocked — silently. **Fix:** self-host the font + analytics, or allowlist the origins; verify live. *(Frontend)*

### LOW (condensed)

- **L1** — Worker logs use non-`Context` slog calls → no `trace_id`/`request_id`, breaking Grafana log→trace jump for the worker path (`optimize-pdf/internal/worker/worker.go:121,363-365`). *(Observability)*
- **L2** — Per-request info log on every request including `/metrics` + `/healthz` scrapes → Loki noise/cost (`shared/logger/requestid.go:46-60`). *(Observability)*
- **L3** — Dev mailer `fmt.Printf`s the password-reset URL (live token) to stdout → Loki, if `RESEND_API_KEY` is ever unset in a real env (`auth-service/internal/email/noop_mailer.go:23`). *(Security)*
- **L4** — Service-to-service REST clients drop `traceparent`/`X-Request-ID` (`document-service/internal/{notifyclient,orgclient}`), breaking correlation on internal hops. *(Observability)*
- **L5** — Login timing side-channel (bcrypt only on existing email) enables account enumeration; signup returns explicit `409 USER_ALREADY_EXISTS` (`auth.go:96,121,154-164`). *(Security)*
- **L6** — 100% always-on trace sampling (`shared/telemetry/tracer.go:66-69`) — costly at production RPS. *(Observability)*
- **L7** — `/readyz` echoes raw dependency error strings (host:port) in the body (`auth-service/routes/routes.go:118` et al.); internal-only, so low. *(API/Security)*
- **L8** — Rate limiters fail **open** on Redis error (`ratelimit.go:132-147`) — a Redis outage removes all throttling. Deliberate; note as accepted risk. *(Security)*
- **L9** — `organize-pdf` sets `AckWait: 5m` while its per-job timeout is 10m → a legitimately-long job can be redelivered and processed twice (`organize-pdf/.../worker.go`). *(Go)*
- **L10** — Missing `CHECK` constraints on several enum columns (`documents.status`, `memberships.role`, `organizations.plan_name`) while others have them — inconsistent enforcement. *(DB)*
- **L11** — Progress-model + NAK-status drift across the 4 workers (queued vs processing on retry; some emit no intermediate progress) — cosmetic/UX inconsistency (matches the `TODO.md` deferred item). *(Go)*
- **L12** — Worker containers run the Go binary as PID 1 with no `tini`/`init: true` → orphaned grandchild subprocesses (LibreOffice/gs/tesseract) can accumulate as zombies over long uptimes (`convert-from-pdf/Dockerfile:48` et al.). *(Infra)*
- **L13** — Three lockfiles in the frontend (`bun.lock`, `bun.lockb`, `package-lock.json`) → dependency-resolution drift; 5 moderate `npm audit` advisories (react-router open-redirect, uuid bounds). *(Frontend)*
- **L14** — No `start_period` on any healthcheck → false `unhealthy` during cold-start migrations (`docker-compose.yml` healthchecks). *(Infra)*
- **L15** — Internal architecture docs (service topology, env var names, JWT alg) are bundled into a client-fetchable SPA chunk (`fyredocs_frontend/src/config/developerDocs.ts`); `RoleRoute` gates rendering, not delivery — recon-value info disclosure. *(Frontend)*
- **L16** — Dead `files/` directory at backend root (pre-MinIO artifact, one stale chunk-upload leftover) — removable once any legacy filesystem rows are migrated. *(Cleanup)*

---

## 4. Detailed analysis by review category

### 1–4. Architecture, System Design, Code Flow, Code Quality
**Current:** Honest microservices on `go.work` (12 modules). Each service owns its tables, imports only utility packages from `shared/`, and communicates via REST (trusted mesh) or NATS JetStream — the `CLAUDE.md` boundary rules are actually followed (`authverify` is imported only by gateway/auth/job; domain services trust gateway-injected `X-User-*`). The gateway is a clean net/http reverse proxy with a static route table, per-upstream circuit breakers, and prefix-rewrite director. Request lifecycle is coherent: Caddy → gateway (CORS, headers, auth, rate limit, 1 MiB cap) → service → Postgres/NATS, with file bytes deliberately bypassing services via presigned MinIO.
**Strengths:** Genuine separation of concerns; async work correctly offloaded to a durable queue; standard response envelope enforced (`shared/response`); readable, idiomatic Go; good use of context timeouts. Build and vet are clean.
**Weaknesses:** The "each service owns its DB" rule is aspirational — it's one shared Postgres with table-ownership convention (§15), and the worker/job-service shared-table `AutoMigrate` (C4) is a real boundary leak. Worker loops are duplicated by design and have drifted (M8, L11).
**Recommendation:** Keep the architecture; fix the shared-table ownership (one migrator), reconcile worker drift, and treat the shared-Postgres reality as an explicit, documented decision.

### 5. Go (memory / concurrency / performance)
**Strengths:** Sound worker pool (semaphore + WaitGroup + per-message panic recovery + drain), `exec.CommandContext` everywhere, per-job 10-min timeout, tmpfs budget guard + large-job serialization, ETag result cache. Concurrency reviewed for races: `ListDocuments`'s parallel queries correctly read params into locals and build independent GORM statements; the cleanup Redis lock uses a correct compare-and-delete Lua script; the auth session sweep is properly ctx+WaitGroup joined.
**Weaknesses:** H2 (export goroutine), M4 (`sync.Map` leak), M5 (multipart temp files), M6 (worker shutdown not joined), M8 (lost-update race), L9/L11/L12.
**Recommendation:** Address H2 first (OOM vector), then the leaks (M4/M5) and shutdown join (M6).

### 6. API
**Strengths:** Status codes are a genuine strength — 201/204/409/413/422/429/410/502/503 all used correctly; no handler returns 200 with `success:false`; envelope divergences (SSE, downloads, `/metrics`, health) are all justified. Idempotency-Key on job creation is well done (atomic Redis `SetNX`).
**Weaknesses:** No versioning (M15), inconsistent `error.code` vocabulary (M14), unbounded/inconsistent pagination (H9, M12), manual-only validation (M17), doc drift (M16).
**Recommendation:** Add `/api/v1`, then unify error codes under it; cap all list endpoints; adopt binding-tag validation.

### 7. File upload/download
**Strengths — this is the best-engineered part of the system.** Browser ↔ MinIO presigned multipart; bytes never transit services. Declared size checked at init and the **true** size re-verified via `StatObject` on complete (oversize objects deleted); MIME sniff of the first 512 bytes at consume against an allowlist; filenames sanitized via `filepath.Base` + separator rejection; object keys server-generated (`uploads/{uuid}/{base}`); downloads are 302 → 5-min presigned GET with forced attachment disposition. Workers stream inputs to tmpfs and outputs back to MinIO with per-job scratch `MkdirTemp` + `defer RemoveAll`.
**Weaknesses:** Direct-multipart temp-file leak (M5); no decompression-ratio/output-size guard (zip-bomb, L2 from security); exports buffer in memory + DB (H2). No resumable downloads (uploads are resumable via multipart parts). No virus scanning hook.
**Recommendation:** Fix M5; add page-count/output-size ceilings in workers; move exports to object storage.

### 8. Error handling
**Strengths:** Unified envelope with sanitized user messages while logging the underlying error with trace/request IDs (`shared/response/gin_helpers.go`); panic recovery middleware on Gin and net/http; gateway maps unreachable upstream → 502 and open breaker → 503; workers classify recoverable vs terminal and route terminal failures to a DLQ with friendly messages. The prior error-handling hardening (per `TODO.md`) is real and visible in the code.
**Weaknesses:** Event-subscriber unique-violation handling treats a benign duplicate as a generic failure → NAK/DLQ churn (should be `ON CONFLICT DO NOTHING` / detect `23505`). `error.code` inconsistency (M14).

### 9. Logging
**Strengths:** Structured slog everywhere with a context handler auto-injecting `trace_id`/`span_id`/`request_id`/service; disciplined secret handling (passwords/tokens are `json:"-"`, emails masked in auth logs). No `fmt.Print`/`log.Print` in non-test code except one dev-mailer line.
**Weaknesses:** Worker logs use non-`Context` calls (L1); per-request logging includes scrape/probe noise (L2); dev mailer prints a live reset token (L3); no dedicated audit-log stream for security events (login/plan-change/impersonation are emitted as analytics events, not a tamper-evident audit log).

### 10–11. Observability & Monitoring
**Strengths:** OTel tracing + Prometheus `/metrics` in every service; W3C propagation on the sync path (gateway forwards request-id and injects traceparent); Grafana auto-provisioned with Prometheus/Tempo/Loki and **bidirectional trace↔log correlation**; opt-in backing stack via a compose profile.
**Weaknesses:** The operational layer is missing — no alerting/SLOs (C2), cardinality bomb (C3), liveness-checks-deps (C6), async pipeline untraceable (H1), thin metrics (M7), collector SPOF/self-disable (M13). **Net: excellent telemetry plumbing, not yet an on-call-ready system.**
**Recommendation:** C2/C3/C6 are prerequisites for calling observability production-ready; then H1 + M7 (DLQ depth, job latency).

### 12. Containers
**Strengths:** Multi-stage builds; Go services on `scratch` with only certs/zoneinfo/passwd + static binary, `USER appuser` (uid 10001), exec-form CMD (SIGTERM reaches PID 1) and graceful `srv.Shutdown`; healthchecks on every service; `.dockerignore` keeps secrets/tests/data out of context.
**Weaknesses:** `:latest`-only tags → no rollback (C5); LibreOffice/poppler/gs/tesseract/unoserver unpinned (non-reproducible builds, M4-infra); base LibreOffice image doesn't set a non-root user itself (consumers do); worker PID-1 zombie risk (L12); no `start_period` (L14).

### 13. Infrastructure
**Current:** Single-host Docker Compose; Caddy is the only host-published service (80/443), everything else internal or loopback-bound (verified by `check-port-exposure.sh`). Caddy does auto-TLS, SPA hosting, presigned MinIO byte-routing (Host preserved for SigV4), and dynamic-DNS load-balancing to the gateway.
**Strengths:** Tight port surface; sensible edge; a documented restore procedure exists.
**Weaknesses:** Every infra component (Postgres/Redis/NATS/MinIO/Caddy) is a single instance = SPOF (H-topology); full-deploy downtime (H6); no rollback (C5); DR gaps (C5); single root `.env` with no env separation.

### 14. Resource allocation
**Strengths:** Every service has env-driven `deploy.resources` limits + reservations, auto-budgeted by `deploy.sh` to a 70% host cap, with derived worker-pool sizes. Workers get `tmpfs /tmp:size=1g`.
**Weaknesses:** Observability stack (~2.5 GB) is outside the budget (M11) → over-commit risk on small hosts; Compose can't autoscale; the budget invariant silently doesn't hold for what's actually deployed.

### 15–16. Database (architecture & performance)
**Strengths:** UUIDv7 PKs (index-sequential inserts); genuinely good index coverage for documented hot paths (`idx_job_user_created`, `idx_doc_user_status_created`, tsvector + GIN full-text, partial sweep indexes); race-safe upsert for memberships (`ON CONFLICT DO UPDATE`); the three real transactions are correctly scoped; server-side `statement_timeout`/`idle_in_transaction_session_timeout` in the DSN; no `sql.Rows` leak (all reads via GORM).
**Weaknesses:** Shared Postgres for all services (isolation by convention); multi-service `AutoMigrate` on shared tables (C4); warn-only integrity indexes (H4); no versioned migrations (H5); export bytes in `bytea` (H2); thin pool headroom (M3); deep-OFFSET + COUNT (M12); write-amplification from ~7 indexes on `analytics_events`; no partitioning/retention for append-heavy tables; cross-service orphans (M18).
**Recommendation:** One migrator per table + versioned migrations; PgBouncer; keyset pagination; range-partition `analytics_events`/`processing_jobs` by month; plan a read replica for analytics.

### 17. Security
Covered in §3 (C1, H3, H7, H8, H10, M1, M2, L3, L5, L8). **Verified-sound (no action):** JWT alg-confusion not exploitable (`none` rejected, `WithValidMethods`, per-alg keyfunc); gateway strips inbound `X-User-*` before injecting verified identity; IDOR well-defended (ownership/org-membership scoping everywhere); no SQL/command injection (parameterized GORM, arg-slice `exec`, allowlisted OCR lang/quality); path traversal blocked; password-reset tokens are 256-bit, hashed, 1h, single-use; presigned uploads owner-bound with true-size re-verification.

### 18. Scalability — see §5.

### 19. Production-readiness checklist

| Item | Status |
|---|---|
| Graceful shutdown | ✅ services; ⚠️ workers don't join drain (M6) |
| Health / readiness | ⚠️ liveness checks deps on gateway+4 workers (C6); correct elsewhere |
| Config management | ⚠️ single root `.env`, no env separation, insecure defaults (H8) |
| Secrets management | ⚠️ plain env; weak committed default (H7); no manager |
| Backups | ⚠️ hourly pg_dump; uploads unbacked, no PITR (C5) |
| Monitoring / Alerting | ❌ no alerting (C2) |
| Observability / Logging | ⚠️ strong sync-path; async untraceable (H1); no audit log |
| Retry / Timeout | ✅ per-job timeout, backoff, DLQ, circuit breaker |
| Idempotency | ✅ job creation; ⚠️ event-consumer TOCTOU relies on warn-only index (H4) |
| Resource limits | ✅ per-container; ⚠️ observability outside budget (M11) |
| DB backups / DR | ❌ no PITR, uploads gap, no rollback (C5) |
| Deployment strategy | ⚠️ full deploy = downtime, no rollback (H6, C5) |
| Rate limiting | ✅ per-plan; ⚠️ guest XFF-spoofable (M2), fails open (L8) |

---

## 5. Scalability assessment

Topology: stateless services (gateway, workers) behind Caddy dynamic-DNS LB; stateful singletons Postgres/Redis/NATS/MinIO on one host.

- **1,000 users:** Fine as-is. Single host with the 70% budget handles this; watch worker concurrency during conversion bursts.
- **10,000 users:** Reachable, but first fix migration-on-boot (C4/M9) so services can `--scale`, add PgBouncer (M3), and cap unbounded queries (H9, M12). Still single-host — a host failure is a full outage.
- **100,000 users:** Requires topology change. Move Postgres to managed/replicated with a read replica for analytics (M12/M18); Redis to managed/clustered; NATS to a cluster; MinIO distributed or external S3; multiple edge nodes; an orchestrator (k8s/Nomad) for rolling deploys + autoscaling (Compose cannot). Partition `analytics_events`/`processing_jobs`.
- **1,000,000 users:** The architecture supports it *in principle* (queue-based workers scale horizontally, files bypass services), but every stateful singleton must become a managed/clustered tier, analytics must move to a warehouse/OLAP path, and the metrics cardinality (C3) and 100% sampling (L6) must be fixed or the observability tier collapses first.

**Bottlenecks, in order:** migration-on-boot (blocks scale-out) → single Postgres → metrics cardinality → single Redis/NATS/MinIO → single host/edge.

---

## 6. Action plan

### Must fix before production
- **C1** Access-token revocation (cookie logout + double-hash) — security-critical.
- **C2** Add alerting + Alertmanager + minimal SLOs.
- **C3** Route-template metric labels (kill the cardinality bomb).
- **C4** One migrator per shared table (workers stop migrating `processing_jobs`/`file_metadata`).
- **C5** Image tagging + registry + rollback; back up `uploads` (or accept+document); DR/PITR decision; reconcile restore doc.
- **C6** Make `/healthz` static-200 on gateway + 4 workers; deps → `/readyz`.
- **H7** Generate/inject the JWT secret on every deploy path; remove the committed value from `.env` + test.
- **H8** Flip `.env` to secure prod defaults (`AUTH_COOKIE_SECURE`, TLS origins, `sslmode`).
- **H9** Cap the unbounded org/member list endpoints.
- **H4** Promote idempotency UNIQUE indexes to fail-fast.
- **H2** Bound export concurrency + row cap (full object-storage move can be fast-follow).
- **api-gateway test** — fix `TestNewProxyStreamsResponses` so `go test ./...` is green.

### Should fix in the next release
- **H1** NATS trace propagation; **H3** refresh-token rotation + reuse detection; **H5** versioned migrations; **H6** build-before-down deploy; **H10** proxy-login role-rank check.
- **M1** enforce `INTERNAL_API_TOKEN` on `/internal`; **M2** trusted-proxy XFF; **M3** PgBouncer; **M4/M5/M6** Go leaks + shutdown join; **M7** DLQ-depth + job-latency metrics; **M10** Grafana password; **M11** budget the observability stack; **M14/M15** versioning + error-code unify (together); **M17** binding-tag validation; **M19** frontend CSP.

### Nice-to-have
- **M8/M12/M16/M18**, **L1–L16** (worker log context, log noise, dev-mailer token, internal-hop correlation, enumeration timing, sampling ratio, `start_period`, `tini`, frontend lockfile/deps, dead `files/` dir, CHECK constraints, worker drift reconciliation).

### Long-term architecture
- Managed/replicated Postgres + read replica; Redis/NATS clustering; MinIO distributed or external S3; multi-node edge; orchestrator (k8s/Nomad) for rolling/blue-green + autoscaling; range-partitioning + retention for append-heavy tables; a dedicated audit-log stream; per-environment config + secret manager.

---

## 7. Known & deferred (from `TODO.md` — not double-counted above)
- **Error-code literal migration** — the wire-value cleanup behind M14; the repo already flags it as needing a coordinated frontend change. (Pair with M15 versioning.)
- **Worker retry-status unify** — the queued-vs-processing NAK inconsistency = L11; the repo already marks it cosmetic.
- The repo's own note that the prior error-handling sweep (panic recovery, consumer retry+DLQ, per-job timeout, gateway dial-timeout + circuit breaker, DLQ redrive, DB ctx-scoping) is **done** — this audit confirms those are present and working; they are not re-raised.

---

## 8. Method & confidence notes
- **Confirmed by direct source re-read:** C1 (all three revocation paths + the middleware that proves the cookie path), C4 (worker `AutoMigrate` + divergent model tags), H2 (export goroutine + ctx), H7 (secret value + `deploy.sh` precedence), H10 (no role-rank check), and the api-gateway test failure.
- **Corrected during verification:** H7 was downgraded from Critical to High after confirming `deploy.sh` overrides the committed secret with a generated one (it is only live on non-`deploy.sh` deploys). The auth-service login rate-limiter was *cleared* (compose sets `TRUSTED_PROXIES`), contrary to one agent's initial flag.
- **Inferred from code, not measured at runtime (no live environment):** memory behavior of exports (H2), real row volumes for M12/partitioning, actual host RAM for M11, and whether the deploy target uses Swarm vs plain Compose (affects the severity of the C6 restart cascade). These are flagged as projections where relevant.
- **Not determinable from the repo:** whether the warn-only indexes (H4) exist in the running database (no migration-history table), and whether `BACKUP_ALERT_WEBHOOK_URL` is actually wired. These require environment access to close out.
