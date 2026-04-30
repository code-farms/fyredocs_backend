# Fyredocs Deployment Strategy Review

> Reviewed: 2026-03-19 | Scope: `docker-compose.yml`, `deploy.sh`, Dockerfiles, infrastructure config

---

## What You're Doing Well

- **Multi-stage Docker builds** with `scratch` base images — minimal attack surface, small image sizes
- **Non-root containers** (`appuser` UID 10001) — good security posture across all services
- **Health checks on all services** with proper dependency ordering via `depends_on` + `condition`
- **BuildKit caching** for Go module and build cache — faster rebuilds
- **Sequential builds** in `deploy.sh` to avoid CPU/memory exhaustion on constrained hosts
- **JWT secret generation** with `chmod 600` — reasonable for development
- **Shared base image** (`fyredocs-base`) for PDF processing tools — avoids redundant layers across workers
- **Go workspace** (`go.work`) for unified dependency management across services

---

## Critical Issues

### 1. Hardcoded Database Credentials

**Files:** `docker-compose.yml:9-10`, all service environment blocks
**Severity:** CRITICAL

```yaml
POSTGRES_DB: fyredocs
POSTGRES_USER: user
POSTGRES_PASSWORD: password
DATABASE_URL: postgresql://user:password@db:5432/fyredocs?sslmode=disable
```

Credentials are plaintext in the compose file. Anyone with repo access has full database access.

**Fix (immediate):** Extract to a `.env` file (gitignored) and use variable interpolation:

```yaml
# .env (gitignored)
POSTGRES_USER=fyredocs_admin
POSTGRES_PASSWORD=<generated-secure-password>

# docker-compose.yml
environment:
  POSTGRES_USER: ${POSTGRES_USER}
  POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
  DATABASE_URL: postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@db:5432/fyredocs?sslmode=disable
```

**Fix (production):** Use Docker secrets or an external secrets manager (HashiCorp Vault, AWS Secrets Manager, etc.).

---

### 2. No Reverse Proxy / TLS Termination

**Severity:** CRITICAL

All 8 service ports (8080-8086) are exposed directly to the host. In production this means:

- No TLS/HTTPS — all traffic is unencrypted
- No rate limiting at the edge
- No centralized access logging
- Worker services (8082-8085) are directly accessible when they should be internal-only

**Fix:** Add Nginx, Traefik, or Caddy as a reverse proxy service in `docker-compose.yml`:

- Only expose ports 80 and 443 to the host
- TLS termination with Let's Encrypt (Caddy does this automatically)
- Route external traffic **only** to the API gateway
- Remove `ports:` from all worker services — they communicate internally over `fyredocs_net`

```yaml
# Example: Add to docker-compose.yml
caddy:
  image: caddy:2-alpine
  restart: always
  ports:
    - "80:80"
    - "443:443"
  volumes:
    - ./Caddyfile:/etc/caddy/Caddyfile
    - caddy_data:/data
  depends_on:
    api-gateway:
      condition: service_healthy
  networks:
    - fyredocs_net
```

Then remove `ports:` from `convert-from-pdf`, `convert-to-pdf`, `organize-pdf`, `optimize-pdf`, `cleanup-worker`, and infrastructure services.

---

### 3. No CI/CD Pipeline

**Severity:** CRITICAL

Deployment is entirely manual via `./deploy.sh`. This means:

- No automated testing before deploy
- No build artifact caching across deploys
- No rollback mechanism
- No audit trail of who deployed what and when
- Human error risk on every deployment

**Fix:** Add a GitHub Actions workflow (or equivalent) with at minimum:

```yaml
# .github/workflows/deploy.yml
name: CI/CD
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - run: go vet ./...
        working-directory: fyredocs_backend
      - run: go test ./...
        working-directory: fyredocs_backend

  build:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: |
          for svc in api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf cleanup-worker; do
            docker build -t fyredocs-$svc:${{ github.sha }} -f $svc/Dockerfile .
          done
        working-directory: fyredocs_backend
```

Add image push to a container registry (GHCR, ECR, Docker Hub) and deployment triggers on merge to main.

---

### 4. No Rollback Strategy

**Severity:** CRITICAL

`deploy.sh` runs `docker compose down` then rebuilds everything from source. If the new build fails or a service is broken:

- There is no previous image to roll back to
- All services go down during the build phase (can take several minutes)
- No blue/green or canary deployment capability

**Fix (immediate):** Tag images with the git SHA before deploying:

```bash
GIT_SHA=$(git rev-parse --short HEAD)
for SERVICE in "${GO_SERVICES[@]}"; do
    docker compose build "$SERVICE"
    docker tag "fyredocs_backend-${SERVICE}:latest" "fyredocs_backend-${SERVICE}:${GIT_SHA}"
done
```

This lets you roll back by retagging a known-good image as `latest`.

**Fix (longer term):** Push images to a container registry. Deploy by updating the image tag in compose, not by rebuilding from source. Keep the last N tagged images for instant rollback.

---

## Important Issues

### 5. `volume-init` Uses `chmod 777`

**File:** `docker-compose.yml:61`
**Severity:** MEDIUM

```yaml
command: ["sh", "-c", "chmod -R 777 /app/uploads /app/outputs"]
```

World-writable directories allow any process on the host or any container on the network to read/write/execute files in these directories.

**Fix:** Use specific UID/GID matching your `appuser` (10001):

```yaml
command: ["sh", "-c", "chown -R 10001:10001 /app/uploads /app/outputs && chmod -R 755 /app/uploads /app/outputs"]
```

---

### 6. Infrastructure Ports Exposed to Host

**File:** `docker-compose.yml:14, 29, 44-45`
**Severity:** MEDIUM

PostgreSQL (5432), Redis (6379), and NATS (4222, 8222) are all published to the host network. In production, this means anyone who can reach the host can connect directly to these services.

**Fix:** Remove `ports:` from all infrastructure services. They communicate with application services over the internal `fyredocs_net` bridge network. For local debugging, use `docker compose exec`:

```bash
docker compose exec db psql -U user -d fyredocs
docker compose exec redis redis-cli
```

---

### 7. No Resource Limits on Containers

**Severity:** MEDIUM

No CPU or memory limits on any container. A single runaway PDF conversion (LibreOffice, Tesseract, Ghostscript) could consume all host memory and kill other services via OOM.

**Fix:** Add resource constraints, especially on PDF worker services:

```yaml
# For PDF workers (resource-intensive)
deploy:
  resources:
    limits:
      memory: 1G
      cpus: '2.0'
    reservations:
      memory: 256M

# For lightweight services (api-gateway, auth, cleanup)
deploy:
  resources:
    limits:
      memory: 256M
      cpus: '0.5'
    reservations:
      memory: 64M
```

---

### 8. No Log Retention Configuration

**Severity:** MEDIUM

Services log to stdout (correct approach), but there is no Docker logging driver configuration. Default `json-file` driver has no size limit, so logs can fill the disk over time.

**Fix (immediate):** Add log rotation to all services:

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"
    max-file: "3"
```

**Fix (production):** Ship logs to a centralized system (Grafana Loki, ELK, CloudWatch) for searchability and retention.

---

### 9. OpenTelemetry Collector Not Deployed

**Severity:** LOW

All services reference `OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel-collector:4318"` but no `otel-collector` service exists in `docker-compose.yml`. Every service is silently failing to export traces on every request.

**Fix:** Either deploy the collector:

```yaml
otel-collector:
  image: otel/opentelemetry-collector-contrib:latest
  restart: always
  ports:
    - "4318:4318"
  volumes:
    - ./infra/otel-config.yaml:/etc/otelcol-contrib/config.yaml
  networks:
    - fyredocs_net
```

Or remove the `OTEL_EXPORTER_OTLP_ENDPOINT` env vars to avoid confusion and silent errors on startup.

---

### 10. No Database Backup Strategy

**Severity:** MEDIUM

PostgreSQL data lives in a Docker named volume (`postgres_data`). If the volume is corrupted or deleted, all data is permanently lost. There are no backup scripts or retention policies.

**Fix (immediate):** Add a backup script:

```bash
#!/bin/bash
BACKUP_DIR="./backups"
mkdir -p "$BACKUP_DIR"
docker compose exec -T db pg_dump -U user fyredocs | gzip > "$BACKUP_DIR/fyredocs_$(date +%Y%m%d_%H%M%S).sql.gz"
# Retain last 7 days
find "$BACKUP_DIR" -name "*.sql.gz" -mtime +7 -delete
```

Run via cron daily. For production: ship backups to S3/GCS with lifecycle policies.

---

### 11. `deploy.sh` Health Check Gaps

**File:** `deploy.sh:100-118`
**Severity:** MEDIUM

The health check loops only verify the database and API gateway. If Redis, NATS, or any worker service fails to start, the script reports "Deployment successful!" anyway. The loops also don't exit with an error code when the timeout is reached — they silently continue.

**Fix:** Check all critical services and fail on timeout:

```bash
wait_for_service() {
    local name=$1
    local check_cmd=$2
    local timeout=${3:-30}

    echo -n "Waiting for $name... "
    for i in $(seq 1 $timeout); do
        if eval "$check_cmd" &> /dev/null; then
            print_success "$name ready!"
            return 0
        fi
        echo -n "."
        sleep 1
    done

    print_error "$name failed to start within ${timeout}s!"
    docker compose logs "$name" | tail -20
    exit 1
}

wait_for_service "Database" "docker compose exec -T db pg_isready -U user -d fyredocs"
wait_for_service "Redis" "docker compose exec -T redis redis-cli ping"
wait_for_service "NATS" "curl -s http://localhost:8222/healthz"
wait_for_service "API Gateway" "curl -s http://localhost:8080/healthz"
```

---

### 12. No Frontend Deployment

**Severity:** MEDIUM

The React frontend (`fyredocs_frontend/`) has Vite build scripts but no Dockerfile, no container, and no deployment configuration. It is not part of the Docker Compose stack.

**Fix:** Add a frontend container with Nginx serving the static build:

```dockerfile
# fyredocs_frontend/Dockerfile
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
```

Then add it to `docker-compose.yml` and route through the reverse proxy (see #2).

---

### 13. Redis Has No Authentication

**File:** `docker-compose.yml:27-28`
**Severity:** MEDIUM

```yaml
command: redis-server --appendonly yes
REDIS_PASSWORD: ""
```

Redis is running without any authentication on the shared Docker network.

**Fix:** Set a password:

```yaml
redis:
  command: redis-server --appendonly yes --requirepass ${REDIS_PASSWORD}

# All services:
REDIS_PASSWORD: ${REDIS_PASSWORD}
```

Add `REDIS_PASSWORD=<secure-value>` to your `.env` file.

---

## Summary

| # | Issue | Severity | Effort | Category |
|---|-------|----------|--------|----------|
| 1 | Hardcoded DB credentials | Critical | Low | Security |
| 2 | No reverse proxy / TLS | Critical | Medium | Security / Networking |
| 3 | No CI/CD pipeline | Critical | Medium | Automation |
| 4 | No rollback strategy | Critical | Medium | Reliability |
| 5 | `chmod 777` on volumes | Medium | Low | Security |
| 6 | Infra ports exposed to host | Medium | Low | Security |
| 7 | No resource limits | Medium | Low | Reliability |
| 8 | No log retention | Medium | Low | Observability |
| 9 | OTel collector not deployed | Low | Low | Observability |
| 10 | No DB backups | Medium | Low | Reliability |
| 11 | `deploy.sh` health check gaps | Medium | Low | Reliability |
| 12 | No frontend deployment | Medium | Medium | Completeness |
| 13 | Redis no auth | Medium | Low | Security |

---

## Recommended Implementation Order

### Phase 1 — Quick Wins (do now)
Items #1, #5, #6, #7, #8, #13 — all low effort with immediate security and stability gains.

### Phase 2 — Core Infrastructure
Items #2 (reverse proxy + TLS), #11 (fix deploy.sh), #10 (DB backups) — medium effort, required before any production traffic.

### Phase 3 — Automation & Reliability
Items #3 (CI/CD pipeline), #4 (image tagging + rollback), #12 (frontend container) — enables safe, repeatable deployments.

### Phase 4 — Observability
Item #9 (deploy otel-collector) — when you're ready to invest in distributed tracing and monitoring dashboards.
