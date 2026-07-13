# Observability Stack

The Go services are instrumented for observability out of the box:

- **Tracing** — `shared/telemetry` initializes an OTLP/HTTP tracer in every
  service and installs HTTP/Gin middleware that starts a span per request and
  propagates W3C trace context. Services export to
  `OTEL_EXPORTER_OTLP_ENDPOINT` (`http://otel-collector:4318` in compose).
- **Metrics** — `shared/metrics` registers Prometheus collectors
  (`http_request_duration_seconds`, `jobs_processed_total`, `jobs_failed_total`)
  and every service serves `GET /metrics`.

This document describes the **backing stack** that receives and visualizes that
telemetry. It is **opt-in**: nothing here runs unless you explicitly start the
`observability` compose profile.

## Components

| Container | Image | Role | Ports |
|-----------|-------|------|-------|
| `otel-collector` | `otel/opentelemetry-collector-contrib` | Single OTLP ingestion point; batches spans and forwards to Tempo | `4318` (OTLP/HTTP, internal), `4317` (OTLP/gRPC, internal), `8888` (self-metrics) |
| `tempo` | `grafana/tempo` | Trace storage & query backend | `4317` (OTLP/gRPC in), `3200` (HTTP API) |
| `prometheus` | `prom/prometheus` | Scrapes every service's `/metrics` + the collector | `9090` (loopback-only UI) |
| `grafana` | `grafana/grafana` | Dashboards over Prometheus + trace explorer over Tempo | `3000` (loopback-only UI) |

All four are internal to `fyredocs_net`. Only the Grafana and Prometheus UIs
publish a host port, and both are bound to `127.0.0.1` (operator-only, like the
MinIO console) — never public.

## Data flow

```
11 services --OTLP/HTTP :4318--> otel-collector --OTLP/gRPC :4317--> tempo :3200
prometheus --scrape /metrics--> 11 services + otel-collector (:8888)
grafana --> datasources: Prometheus (:9090) + Tempo (:3200)
```

The **services → collector** hop is OTLP/HTTP and is fixed by the service code
(`otlptracehttp`, port `4318`). The **collector → Tempo** hop is OTLP/gRPC
(`4317`). See the diagram in
[mermaid/system-overview.md](../mermaid/system-overview.md#observability-opt-in-observability-profile).

## Starting it

The stack is behind the compose profile `observability`, so a plain
`docker compose up` / `deploy.sh` never starts it. With the core stack running:

```bash
docker compose -f deployment/docker-compose.yml --env-file .env --profile observability up -d
```

Then open:

- **Grafana** — http://127.0.0.1:3000 (default login `admin` /
  `${GRAFANA_ADMIN_PASSWORD:-admin}`). The `fyredocs-overview` dashboard and the
  Prometheus + Tempo datasources are auto-provisioned.
- **Prometheus** — http://127.0.0.1:9090 → Status → Targets to confirm all 11
  services + the collector are `UP`.

Stop just the observability stack:

```bash
docker compose -f deployment/docker-compose.yml --env-file .env --profile observability down
```

## Graceful degradation

When the collector is not running (the default), each service's endpoint probe
(`shared/telemetry` `probeEndpoint`, a 2s TCP dial) fails, logs
`"OTLP collector unreachable, tracing disabled"`, and installs a no-op tracer.
The service runs normally — no export timeouts, no errors. Start the profile and
restart (or redeploy) the services and they log
`"OpenTelemetry tracing initialized"` instead.

## Configuration files

Config lives under `deployment/`, mounted read-only into each container
(same convention as `deployment/caddy/Caddyfile`):

| File | Purpose |
|------|---------|
| `otel-collector/config.yaml` | OTLP receivers (4318/4317), batch processor, OTLP exporter to `tempo:4317`, self-metrics on `:8888` |
| `tempo/tempo.yaml` | OTLP/gRPC receiver, local trace storage, HTTP API `:3200` |
| `prometheus/prometheus.yml` | Scrape jobs: all 11 services (by service DNS + port) + `otel-collector:8888` |
| `grafana/provisioning/datasources/datasources.yaml` | Prometheus (default) + Tempo datasources |
| `grafana/provisioning/dashboards/dashboards.yaml` | Dashboard file provider |
| `grafana/dashboards/fyredocs-overview.json` | Starter dashboard: request rate, p95 latency, 5xx rate, jobs processed/failed |

> **Note:** `prometheus.yml` hardcodes the api-gateway target as
> `api-gateway:8080`. It is coupled to `${API_GATEWAY_PORT:-8080}` in
> `docker-compose.yml` — keep them in sync if the gateway port changes.

## Relation to the in-app analytics dashboard

`analytics-service` independently scrapes service `/metrics` endpoints
(`internal/promscrape`) to power its own admin dashboards. That is unrelated to
and unaffected by this stack — Prometheus here is an additional, external
consumer of the same endpoints.

## Environment variables

All optional; defaults shown.

| Var | Default | Purpose |
|-----|---------|---------|
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Grafana admin password |
| `OTEL_COLLECTOR_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | collector resources |
| `TEMPO_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Tempo resources |
| `PROMETHEUS_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Prometheus resources |
| `GRAFANA_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | Grafana resources |
