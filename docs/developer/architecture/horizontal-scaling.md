# Horizontal Scaling Runbook

**A practical, staged "what to do when one machine isn't enough" guide.**

You are deploying on a **single host** with Docker Compose (Caddy edge →
api-gateway → 11 services; Postgres, Redis, NATS, MinIO as infra). That is the
right call for launch. This document is the step-by-step playbook for the day the
box stops keeping up.

This is the *how-to*. The *why* — the capacity math, the SPOF analysis, the
1M-req/day roadmap — lives in
[production-readiness.md](./production-readiness.md) and is authoritative where
the two overlap. Read its §6 (the math) and §9 (the roadmap) alongside this.

> **The one-line summary:** the application tier is already horizontally
> scalable; the **conversion workers** are your real throughput ceiling and the
> first thing to scale out; the **stateful tier** (Postgres/Redis/NATS/MinIO) is
> five single points of failure that must be made HA before a second host buys
> you real durability. Do the stages in order.

---

## 0. Before you scale: know the signals

Scale in response to evidence, not vibes. The bottleneck is almost never the HTTP
tier — at ~12 req/s average / ~60–80 req/s peak the Go services are bored
(production-readiness.md §6). It is the **conversion workers**. Watch these:

| Signal | Where to look today | Means |
|---|---|---|
| **JetStream `JOBS_DISPATCH` depth rising at peak** | NATS monitoring `http://nats:8222` (internal), or the admin dashboard | Workers can't keep up — **the #1 scale-out trigger** |
| **Job latency: `queued` → `processing` growing** | job-service metrics / job status | Same as above, user-visible |
| **Jobs silently disappearing after ~24h** | Users stuck on "queued" forever | `JOBS_DISPATCH` MaxAge=24h **expires** backlog it can't drain (`shared/natsconn/natsconn.go`) — you are already over capacity |
| **Worker container CPU pinned at its `deploy.limits.cpus`** | `docker stats` | That worker family needs more concurrency or more replicas |
| **Postgres connection refusals under burst** | service logs | Pool budget over-committed (see §3 / production-readiness.md §5.6) |
| **Container hitting its memory limit / OOM** | `docker stats`, `docker events` | Raise the host budget or move the service off-box |

> ⚠️ **Observability gap — fix this first.** The stack wires
> `OTEL_EXPORTER_OTLP_ENDPOINT` into every service but **no `otel-collector`,
> Prometheus, or Grafana is actually deployed** — tracing is a no-op today and
> nothing scrapes the `/metrics` endpoints (only analytics-service's in-process
> sampler pulls the gateway's `/metrics` once a minute). **You cannot operate a
> multi-instance, multi-host system blind.** Standing up
> Prometheus + Alertmanager + Grafana (production-readiness.md §9, Phase 2) is a
> prerequisite for everything below, not an afterthought. Alert on the six
> signals in the table above.

---

## 1. The mental model: two tiers

Everything in the system is one of two things.

### Tier A — Stateless app services (scale freely)

All 11 Go services keep their durable state in Redis, Postgres, or NATS, so any
replica can serve any request. Load-balancing is already wired:

- **api-gateway** — Caddy balances across replicas via dynamic DNS upstreams
  (see [caddy-edge.md](./caddy-edge.md#scaling-the-gateway)); already done.
- **service → service REST** — the `*_SERVICE_URL` values are Compose DNS names,
  which Docker round-robins across replicas automatically.
- **workers** — the NATS `JOBS_DISPATCH` WorkQueue load-balances jobs across all
  replicas of a worker family sharing the same durable consumer name.
- **SSE** — *not* sticky. Job/notification event state lives in NATS, so any
  instance can serve any client's stream.

### Tier B — Stateful infrastructure (needs HA work before it scales)

| Component | Today | To scale |
|---|---|---|
| Postgres (`db`) | single node, `postgres_data` volume, `max_connections=200` | streaming replica + failover, pgbouncer (§4) |
| Redis (`redis`) | single node, AOF, `requirepass` | Sentinel or managed Redis (§4) |
| NATS (`nats`) | single node, JetStream on one disk | 3-node cluster, JetStream R3 (§4) |
| MinIO (`minio`) | single node, **no backup/replication** | distributed erasure or migrate to S3/R2 (§4) — **data-loss SPOF** |

### The classification table

| Component | Status | Note |
|---|---|---|
| api-gateway | ✅ Scale now | Caddy dynamic-upstream LB already configured |
| auth / user / document / notification / analytics | ✅ Scale now | stateless; see per-instance caveats below |
| job-service | ✅ Scale now | cleanup loop is Redis-`SetNX`-locked so only one replica sweeps |
| convert-* / optimize-pdf / organize-pdf (workers) | ✅ Scale now | WorkQueue load-balances; **your real bottleneck** |
| Postgres / Redis / NATS / MinIO | 🟠 Needs HA | single instances; §4 |
| `outputFileCache` (job-service) | 🔵 Fix at scale | per-process `sync.Map`, stale across replicas (harmless staleness) — replace with LRU |
| auth `plancache` | 🔵 Fix at scale | per-process TTL cache; each replica keeps its own copy (harmless) |
| auth hourly session cleanup, analytics sampler | 🔵 Fix at scale | per-instance **unlocked** periodic tasks — duplicate work if scaled (see §5) |
| Observability (Prometheus/otel) | 🔴 Missing | must exist before scaling — see §0 |

---

## 2. Stage 0 — squeeze the single box first (vertical)

**Always do this before adding a machine — it's the cheapest capacity you'll
ever buy.** Compose limits and worker pools are auto-budgeted by `deploy.sh` from
the host's RAM/CPU, so a bigger box needs almost no config change.

1. **Give the box more resources / raise the budget.** `deploy.sh` sizes every
   container to `RESOURCE_BUDGET_PCT` (default 70%) of host RAM/CPU. Move to a
   bigger VPS, then preview and apply:
   ```bash
   ./deployment/deploy.sh --dry-run                 # see computed limits
   RESOURCE_BUDGET_PCT=80 ./deployment/deploy.sh     # apply a higher budget
   # model a hypothetical box:
   MEM_TOTAL_MB=32768 NCPU=16 ./deployment/deploy.sh --dry-run
   ```

2. **Raise worker throughput in place** — the highest-leverage knob. Prefer
   more concurrency *per replica* over multiple worker replicas on one host
   (duplicate LibreOffice/OCR daemon pools just fight for the same cores):
   - `WORKER_CONCURRENCY` (all workers), `UNOSERVER_INSTANCES` (convert-to-pdf),
     `OCR_MAX_WORKERS` (optimize-pdf). These are auto-derived from each worker's
     memory cap, so raise the cap (via the budget) and they follow.

3. **Fix the Postgres connection budget** *(do this before you scale any DB-using
   service — see production-readiness.md §5.6).* Default pools sum to ~275
   potential connections against `max_connections=200`. Either set explicit
   per-service `PoolConfig` (`shared/database`) to a total ≤150 (e.g. gateway 0,
   auth 30, job 40, workers 5 each, others 10), **or** put **pgbouncer** in
   transaction mode in front of `db`. Adding replicas multiplies pools, so this
   is a hard prerequisite for Stage 1/2.

**Verify:** re-run a load test ([load-testing.md](./load-testing.md)) and confirm
`JOBS_DISPATCH` depth stays flat at peak and no Postgres connection errors.

---

## 3. Stage 1 — scale stateless services on the same host

When one box has spare RAM/CPU but a single replica of a service is the limiter,
add replicas without leaving the machine.

```bash
# Caddy already balances the gateway across replicas — no Caddyfile edit
docker compose -f deployment/docker-compose.yml --env-file .env \
  up -d --scale api-gateway=3

# convert-to-pdf already has a replicas var:
CONVERT_TO_PDF_REPLICAS=3 docker compose ... up -d
```

Nothing sets `container_name`, so `--scale` works for every service. **Keep it
that way** — a fixed `container_name` breaks scaling.

**Fix before you scale these past 1 replica:**

- **job-service** — replace the unbounded per-process `outputFileCache`
  (`job-service/handlers/jobs.go`) with an LRU (e.g. `hashicorp/golang-lru`,
  ~10k entries) or drop it (the DB lookup it avoids is one indexed point query).
  Staleness across replicas is otherwise harmless but the leak isn't.
- **analytics / auth periodic tasks** — see §5; scaling these duplicates
  unlocked background work.

**Safe as-is (no change needed):** SSE (NATS-backed), Redis rate limiting /
denylist / guest store, worker WorkQueue balancing, per-worker `tmpfs /tmp`
scratch (ephemeral, no shared disk).

**Metrics caveat:** the analytics sampler scrapes `api-gateway:8080/metrics` by
Compose DNS, so with N gateway replicas it samples only *one* per tick and
undercounts. Once you have Prometheus (§0), scrape all replicas instead.

**Verify:** `docker compose ps` shows N replicas; requests spread across them in
`docker compose logs`; kill one replica and traffic continues.

---

## 4. Stage 2 — add a second host for workers

This is the real horizontal step: the conversion tier needs ~30 concurrent
conversions (~30 cores) at 1M-req/day peak — a multi-host requirement Compose
cannot express on one box (production-readiness.md §6). Workers are the ideal
first thing to move because the WorkQueue balances them **with zero application
changes** — a worker on host #2 pulls from the same `JOBS_DISPATCH` durable and
just starts draining jobs.

### The prerequisite: shared infra must be reachable from host #2

Today infra ports are **not published** (MinIO object port internal, NATS/Redis
loopback-only) — correct for one host, blocking for two. Before host #2 can run
workers you must expose Postgres/Redis/NATS/MinIO to it **securely**:

1. **Private network only.** Put both hosts on a private network / VPC / VPN
   (WireGuard, Tailscale, cloud VPC). **Never** expose Redis/NATS/MinIO/Postgres
   to the public internet.
2. **Turn on auth + TLS** for anything now crossing a host boundary:
   - Redis already has `requirepass` — keep `REDIS_PASSWORD`.
   - NATS — enable token/user auth (currently open on the trusted bridge).
   - Postgres — the DSN defaults to `sslmode=disable`; switch to `require`/
     `verify-full` once the DB is remote (`shared/config` DSN builder).
   - MinIO — already uses scoped app credentials (`S3_ACCESS_KEY`), keep them.
3. **Publish the infra ports on the private interface** (bind to the private IP,
   not `0.0.0.0`) or keep infra in a shared overlay network.

### Deploy workers on host #2

Reuse the per-service `extends` pattern ([compose-files.md](./compose-files.md)).
On host #2, run only the worker compose files, pointing their env at host #1's
**private** infra addresses:

```bash
# host #2 .env — point at host #1's private IP
NATS_URL=nats://<host1-private-ip>:4222
REDIS_ADDR=<host1-private-ip>:6379
DATABASE_URL=postgres://...@<host1-private-ip>:5432/fyredocs?sslmode=require
S3_ENDPOINT=<host1-private-ip>:9000
S3_PUBLIC_ENDPOINT=https://<your-public-origin>    # presigned URLs use this

docker compose -f deployment/docker-compose-convert-to-pdf.yml \
  --env-file .env up -d --build
# repeat for convert-from-pdf / optimize-pdf / organize-pdf
```

The workers register durable consumers on `JOBS_DISPATCH` and immediately share
the queue with host #1's workers. **No gateway or job-service change needed.**

### Also address at this stage

- **Silent job expiry.** `JOBS_DISPATCH` drops queued jobs after 24h under
  sustained overload. Add a stuck-job reconciliation sweep (`queued` > 1h →
  requeue or fail) to the job-service cleanup loop, and alert on JetStream depth
  (production-readiness.md §9). Scaling workers reduces the risk but doesn't
  remove it.
- **Presigned URLs.** Ensure `S3_PUBLIC_ENDPOINT` on all hosts points at the
  public origin (the Caddy edge), since SigV4 signatures are host-bound
  ([object-storage.md](./object-storage.md)).

**Verify:** submit a batch of conversions; confirm jobs complete on **both**
hosts (`docker compose logs` on each); stop host #2's workers and confirm host #1
drains the queue alone.

---

## 5. Per-instance state & periodic tasks (fix as you scale)

Scaling multiplies anything that lives *inside a process*. Audit these:

| Item | File | Behavior when scaled | Fix |
|---|---|---|---|
| `outputFileCache` | `job-service/handlers/jobs.go` | per-replica, unbounded, stale | LRU or remove (§3) |
| auth `plancache` | `auth-service/handlers/plancache.go` | per-replica copy, short TTL | harmless; optionally move to Redis |
| auth hourly session/reset-token cleanup | `auth-service/main.go` | **runs on every replica** (idempotent DELETEs, so duplicated-but-safe) | wrap in a Redis `SetNX` lock like job-service's cleanup, or run in one replica only |
| analytics API-metrics sampler | `analytics-service/internal/apisampler` | duplicate samples if analytics scaled; samples one gateway replica | single-instance the sampler (lock/flag), scrape via Prometheus |
| job-service cleanup loop | `job-service/internal/cleanup` | ✅ already Redis-`SetNX`-locked (`cleanup-worker:lock`); `CLEANUP_ENABLED=false` opts replicas out | none |

**Pattern to copy:** job-service's cleanup lock is the reference implementation
for "run this periodic task on exactly one replica." Apply it to the auth and
analytics tasks before scaling those services.

---

## 6. Stage 3 — make the stateful tier HA

A second host for workers improves *throughput* but not *durability* — Postgres,
Redis, NATS, and MinIO are still single points of failure, and MinIO holds every
user file with no backup. Before you depend on multi-host in production, make the
stateful tier highly available. Tie every step to
[backup-and-restore.md](./backup-and-restore.md) and **drill a restore**.

| Component | Move to | Notes |
|---|---|---|
| **Postgres** | Managed HA (streaming replica + automatic failover) + **pgbouncer** | Managed (RDS/Cloud SQL/Crunchy) is usually cheaper than self-run HA. pgbouncer also solves the pool budget (§2). |
| **Redis** | Redis Sentinel (3 nodes) or managed Redis | Sessions, denylist, guest tokens, rate limits all live here — losing it degrades auth. |
| **NATS** | 3-node cluster, **JetStream R3** for `JOBS_DISPATCH` | JetStream on one node = queue loss on disk failure. R3 replicates the work queue across nodes. |
| **MinIO** | Distributed mode (erasure coding) **or migrate to S3 / Cloudflare R2** | 🔴 **Highest-priority data risk.** Single MinIO with no replication/backup = company-ending on disk failure. Migrating to S3/R2 also unlocks a CDN for downloads. |

**Verify (chaos drill):** in staging, kill each stateful node in turn and
confirm the system behaves as designed (failover, retries, no data loss).

---

## 7. Cross-cutting must-haves at scale

These aren't a stage — they must be true throughout multi-instance/multi-host
operation:

- **Zero-downtime deploys.** `docker compose up` recreation drops connections.
  With ≥2 replicas behind the edge, use a rolling script
  (`docker compose up -d --no-deps --wait <service>` one replica at a time), or
  move to an orchestrator (§8).
- **CI/CD + tagged images.** Today there is no pipeline and images aren't tagged/
  pushed, so there is no rollback. Build/test/push tagged images to a registry so
  every host pulls the same version and you can roll back
  (production-readiness.md §9, Phase 1).
- **Analytics retention.** `analytics_events` grows ~5–9M rows/month at 1M
  req/day; partition by month + prune to `daily_metrics` or the admin dashboards
  degrade (production-readiness.md §6).
- **Config & secrets across hosts.** The single gitignored root `.env` must be
  distributed to every host (a secrets manager once you have >1 host).
- **Observability everywhere.** Prometheus scraping every replica on every host,
  central log aggregation (today logs are per-container `json-file`).

---

## 8. When to leave Docker Compose

Compose is fine for one host and workable for a **small** static fleet (~2–3
hosts) with the per-service `extends` files pointed at shared infra. Move to an
orchestrator when you need any of:

- **Autoscaling** — scale workers automatically on JetStream queue depth
  (KEDA on Kubernetes is the canonical fit; production-readiness.md §9, Phase 4).
- **Self-healing / rescheduling** across hosts when a node dies.
- **>3 hosts**, or rolling deploys and service discovery you don't want to script
  by hand.

Options, simplest first:
- **Docker Swarm** — smallest jump from Compose (same file format, adds
  multi-host scheduling, `docker service scale`, rolling updates). Good stepping
  stone.
- **Nomad / k3s** — lighter than full k8s, real multi-host scheduling.
- **Kubernetes (+ KEDA)** — when you need queue-depth autoscaling, HPA, and a
  managed ecosystem. Most operational overhead; adopt when the above no longer
  suffice.

---

## Staged checklist

- [ ] **Stage 0 (vertical):** bigger box + `RESOURCE_BUDGET_PCT`; raise
      `WORKER_CONCURRENCY`/`UNOSERVER_INSTANCES`/`OCR_MAX_WORKERS`; **fix Postgres
      pool budget or add pgbouncer**; stand up Prometheus/Grafana/Alertmanager.
- [ ] **Stage 1 (scale stateless, same host):** `--scale` gateway/services;
      replace `outputFileCache` with LRU; lock the auth/analytics periodic tasks.
- [ ] **Stage 2 (second host for workers):** private network + auth/TLS on infra;
      publish infra on private IP; run worker compose files on host #2 pointed at
      shared infra; add stuck-job sweep + JetStream-depth alert.
- [ ] **Stage 3 (HA stateful tier):** HA Postgres + pgbouncer; Redis Sentinel/
      managed; NATS 3-node R3; MinIO distributed or S3/R2; mandatory backups +
      restore drill.
- [ ] **Cross-cutting:** zero-downtime rolling deploys; CI/CD + tagged images;
      analytics partitioning/retention; secrets distribution; fleet-wide
      observability.
- [ ] **Orchestrator:** adopt Swarm/Nomad/k8s when you need autoscaling,
      self-healing, or >3 hosts.

---

## See also

- [production-readiness.md](./production-readiness.md) — capacity math (§6), SPOF
  analysis (§5.8), and the Phase 1–4 roadmap (§9). **Authoritative** on the *why*.
- [caddy-edge.md](./caddy-edge.md#scaling-the-gateway) — gateway load balancing
  (already configured).
- [compose-files.md](./compose-files.md) — canonical / essentials / per-service
  `extends` topology used in Stage 2.
- [redis-architecture.md](./redis-architecture.md),
  [database.md](./database.md),
  [object-storage.md](./object-storage.md) — per-component detail.
- [backup-and-restore.md](./backup-and-restore.md) — backups feeding the Stage 3
  HA work.
- [load-testing.md](./load-testing.md) — k6 scripts to validate each stage.
