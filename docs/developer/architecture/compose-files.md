# Docker Compose Files Layout

All compose files live in `deployment/` and share the same project name (`fyredocs`) and network (`fyredocs_net`), so containers started from different files see each other.

## The three layers

| File | Role | When to use |
|------|------|-------------|
| `docker-compose.yml` | **Canonical stack** — all 11 services + infra (db, redis, nats, minio, caddy) + backups. The single source of truth for every service's config. | Full deploys (`deployment/deploy.sh` uses it). |
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

## Validation

```bash
for f in deployment/docker-compose*.yml; do
  docker compose -f "$f" --env-file .env config -q || echo "FAIL $f"
done
```

## Multi-host

The same per-service `extends` files are how you run a subset of services (e.g.
the conversion workers) on a **second host** pointed at shared infra. See the
[horizontal-scaling runbook](./horizontal-scaling.md#4-stage-2--add-a-second-host-for-workers).
