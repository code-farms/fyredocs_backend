# Docker Compose Files Layout

All compose files live in `deployment/` and share the same project name (`fyredocs`) and network (`fyredocs_net`), so containers started from different files see each other.

## The three layers

| File | Role | When to use |
|------|------|-------------|
| `docker-compose.yml` | **Canonical stack** — all 11 services + infra (db, redis, nats, minio, caddy) + backups, plus two opt-in profiles: `observability` (otel-collector, tempo, prometheus, grafana) and `notifications` (notification-service — off by default). The single source of truth for every service's config. | Full deploys (`deployment/deploy.sh` uses it). |
| `docker-compose.essentials.yml` | **Infra only** — db (5432), redis (6379), nats (4222), minio (9000, console 9001), minio-init. Ports published to the host. Creates `fyredocs_net`. | Local dev where services run on the host via `go run`, or as the dependency provider for per-service files. |
| `docker-compose-<service>.yml` × 11 | **One service each** — extends the service's definition from `docker-compose.yml`. | Build/redeploy a single service against already-running infra. |

Per-service files exist for: `api-gateway`, `auth-service`, `job-service`, `convert-from-pdf`, `convert-to-pdf`, `organize-pdf`, `optimize-pdf`, `analytics-service`, `document-service`, `user-service`, `notification-service`.

## How per-service files stay in sync

They contain **no duplicated config**. Each one is ~15 lines:

```yaml
name: fyredocs
services:
  auth-service:
    extends:
      file: docker-compose.yml
      service: auth-service
    depends_on: !reset []   # deps run from the main/essentials file
networks:
  fyredocs_net:
    external: true
```

Edit a service's env/limits/healthcheck **only in `docker-compose.yml`** — every per-service file picks it up automatically. `depends_on` is reset because the dependencies (db/redis/nats/minio) are expected to be running already; the network is `external` for the same reason.

## Usage

Always pass the root `.env` (compose does not auto-load it from the repo root):

```bash
# infra first (skip if the full stack is already up)
docker compose -f deployment/docker-compose.essentials.yml --env-file .env up -d

# build + (re)deploy one service
docker compose -f deployment/docker-compose-auth-service.yml --env-file .env up -d --build
```

Shortcut — the deploy scripts wrap the same thing and also apply the host resource budget, `.env` and JWT secret exactly like a full deploy, then wait for the service's healthcheck:

```bash
./deployment/deploy.sh auth-service        # macOS/Linux (multiple names allowed)
deployment\deploy.bat auth-service         # Windows
```

Equivalent Makefile shorthand against the canonical file: `make up SVC=auth-service`.

## Observability profile (opt-in)

The canonical file carries an optional monitoring stack behind the compose
profile `observability`: `otel-collector`, `tempo`, `prometheus`, `grafana`.
Because they declare `profiles: ["observability"]`, a plain `docker compose up`
(and `deploy.sh`) never starts them — services keep emitting telemetry, and when
the stack is down their OTLP endpoint probe fails so tracing self-disables
cleanly.

```bash
# start the monitoring stack alongside a running core stack
docker compose -f deployment/docker-compose.yml --env-file .env --profile observability up -d
```

Config lives in `deployment/{otel-collector,tempo,prometheus,grafana}/` (mounted
read-only). Grafana (`http://127.0.0.1:3000`) and Prometheus
(`http://127.0.0.1:9090`) are bound loopback-only. See
[observability.md](./observability.md) for the full data flow and details.

## Notifications profile (opt-in — off by default)

`notification-service` carries `profiles: ["notifications"]`, so a plain
`docker compose up` (and `deploy.sh`) does **not** start it — it is disabled by
default. Its config stays fully intact and `docker compose build` still builds
the image (build is not profile-gated), so it stays ready to enable with no
rebuild. Runtime coupling is best-effort — document-service's notifyclient
swallows errors and the gateway only proxies `/api/notifications` on demand — so
the rest of the stack runs normally without it. Because Compose auto-starts a
profiled service that an enabled service `depends_on`, the two
`depends_on: notification-service` edges (api-gateway, document-service) were
removed; their `NOTIFICATION_SERVICE_URL` env still resolves once it is enabled.

```bash
# enable it later, alongside a running stack (no rebuild needed)
docker compose -f deployment/docker-compose.yml --env-file .env --profile notifications up -d
# or via the dedicated single-service file / wrapper:
docker compose -f deployment/docker-compose-notification-service.yml --env-file .env up -d --build
./deployment/deploy.sh notification-service
```

## Validation

```bash
for f in deployment/docker-compose*.yml; do
  docker compose -f "$f" --env-file .env config -q || echo "FAIL $f"
done
```

## Port-exposure invariant

**Only the Caddy edge publishes host ports (`80`/`443`). Every other service is
internal to `fyredocs_net`, and any dev-only published infra port is bound to
`127.0.0.1`** (Postgres, Redis, NATS, MinIO in `docker-compose.essentials.yml`).
Backend services (8081–8091) declare no `ports:` at all, so they are never
reachable from the host or internet — and the api-gateway's port is configurable
via `API_GATEWAY_PORT` (see [caddy-edge.md](./caddy-edge.md#scaling-the-gateway)).

This matters because Docker's published ports **bypass host firewalls** (e.g.
UFW), so a stray `ports:` mapping silently exposes an internal service. Guard
against it — no Docker daemon required:

```bash
make check-ports        # -> deployment/scripts/check-port-exposure.sh
```

It fails if any compose file host-publishes a port other than Caddy's `80`/`443`
or a `127.0.0.1`-bound mapping. Run it in CI and before deploys.

## Multi-host

The same per-service `extends` files are how you run a subset of services (e.g.
the conversion workers) on a **second host** pointed at shared infra. See the
[horizontal-scaling runbook](./horizontal-scaling.md#4-stage-2--add-a-second-host-for-workers).
