# Observability Stack

The Go services are instrumented for observability out of the box:

- **Tracing** — `shared/telemetry` initializes an OTLP/HTTP tracer in every
  service and installs HTTP/Gin middleware that starts a span per request and
  propagates W3C trace context. Services export to
  `OTEL_EXPORTER_OTLP_ENDPOINT` (`http://otel-collector:4318` in compose).
- **Metrics** — `shared/metrics` registers Prometheus collectors
  (`http_request_duration_seconds`, `jobs_processed_total`, `jobs_failed_total`)
  and every service serves `GET /metrics`. `http_request_duration_seconds` is
  recorded by the HTTP/Gin middleware; `jobs_processed_total` (label `status`) and
  `jobs_failed_total` (label `reason`), both keyed by `tool_type`, are emitted per
  job outcome from `shared/queue.PublishJobEvent` — the single choke point every
  worker's terminal `JobCompleted`/`JobFailed` event flows through.

  **Low-cardinality route labels.** The api-gateway labels
  `http_request_duration_seconds` by the **route template** (e.g. `/api/jobs`), not
  the raw request path. Raw paths carry per-resource UUIDs
  (`/api/jobs/018f…/download`) and would explode Prometheus cardinality — one time
  series per resource id. Requests that match no known route template are bucketed
  under the label `other`.

This document describes the **backing stack** that receives and visualizes that
telemetry. It is **opt-in**: nothing here runs unless you explicitly start the
`observability` compose profile.

## Components

| Container | Image | Role | Ports |
|-----------|-------|------|-------|
| `otel-collector` | `otel/opentelemetry-collector-contrib` | Single OTLP ingestion point; batches spans and forwards to Tempo | `4318` (OTLP/HTTP, internal), `4317` (OTLP/gRPC, internal), `8888` (self-metrics) |
| `tempo` | `grafana/tempo` | Trace storage & query backend | `4317` (OTLP/gRPC in), `3200` (HTTP API) |
| `prometheus` | `prom/prometheus` | Scrapes every service's `/metrics` + the collector; evaluates alert rules and forwards firing alerts to Alertmanager | `9090` (loopback-only UI) |
| `alertmanager` | `prom/alertmanager` | Receives firing alerts from Prometheus and delivers them via a Slack-formatted webhook (Slack or Discord); opt-in `alerting` profile | `9093` (loopback-only) |
| `loki` | `grafana/loki` | Log storage & query backend (filesystem, ~7-day retention) | `3100` (loopback-only API) |
| `alloy` | `grafana/alloy` | Log shipper — tails every container's stdout (Docker socket) and pushes to Loki | `12345` (internal UI) |
| `grafana` | `grafana/grafana` | Dashboards + trace explorer + log explorer, over Prometheus / Tempo / Loki | `3000` (loopback-only UI) |

All are internal to `fyredocs_net`. Only the Grafana, Prometheus, Loki, and
Alertmanager endpoints publish a host port, all bound to `127.0.0.1` (operator-only,
like the MinIO console) — never public.

## Data flow

```
11 services --OTLP/HTTP :4318--> otel-collector --OTLP/gRPC :4317--> tempo :3200   (traces)
prometheus  --scrape /metrics--> 11 services + otel-collector (:8888)              (metrics)
11 services --stdout--> alloy (docker.sock) --push--> loki :3100                   (logs)
grafana --> datasources: Prometheus (:9090) + Tempo (:3200) + Loki (:3100)
```

The **services → collector** hop is OTLP/HTTP and is fixed by the service code
(`otlptracehttp`, port `4318`). The **collector → Tempo** hop is OTLP/gRPC
(`4317`). **Logs** are structured JSON on stdout (`shared/logger`); Alloy
discovers containers via the Docker socket, labels each stream by compose
`service` + `container`, and ships to Loki.

### Trace ↔ log correlation
Every log line carries `trace_id`/`span_id`/`request_id` (injected by the shared
`contextHandler`). Grafana wires this both ways: the **Tempo** datasource has
`tracesToLogsV2` (span → its Loki logs by `trace_id`), and the **Loki** datasource
has a `trace_id` `derivedField` (log line → its Tempo trace). The gateway
propagates `X-Request-ID` + W3C `traceparent` downstream, so one request keeps a
single trace and request id across every service. See the diagram in
[mermaid/system-overview.md](../mermaid/system-overview.md#observability-opt-in-observability-profile).

## Starting it

The stack is behind the compose profile `observability`. `deploy.sh` activates
that profile by default (`COMPOSE_PROFILES=observability`), so a normal deploy
already starts it — opt out with `COMPOSE_PROFILES= ./deployment/deploy.sh`. To
start it standalone against an already-running core stack:

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

## In-app embedding (admin Observability tab)

The `fyredocs-overview` dashboard is embedded in the frontend admin area under
**Admin → Observability** (`/admin/observability`, super-admin only). The SPA
renders an `<iframe>` pointing at `/grafana/d/fyredocs-overview/...?kiosk`,
served same-origin through the Caddy edge.

Wiring:

- **Edge** — `deployment/caddy/Caddyfile` adds a `@grafana path /grafana /grafana/*`
  handle block that `forward_auth`s each request to the gateway's `/admin/authz`
  probe, then `reverse_proxy grafana:3000`. Because this path bypasses the
  gateway proxy, `forward_auth` is what enforces access: the gateway verifies the
  JWT and analytics-service's `adminAuth` requires `super-admin`, so a non-admin
  gets 401/403 and Grafana is never reached.
- **Authz probe** — `analytics-service` exposes `GET /admin/authz` inside its
  existing admin group (returns 200 for super-admins). It does no work; it exists
  only for `forward_auth`. See [analytics-service.md](../services/analytics-service.md).
- **Grafana** — runs in **anonymous Viewer** mode (`GF_AUTH_ANONYMOUS_ENABLED=true`)
  and under the `/grafana` sub-path (`GF_SERVER_SERVE_FROM_SUB_PATH=true`,
  `GF_SERVER_ROOT_URL`), with `GF_SECURITY_ALLOW_EMBEDDING=true` so the same-origin
  iframe is permitted. The edge is the real gate; anonymous mode only removes the
  second login for the embed.
- **Frontend** — `src/pages/admin/ObservabilityPage.tsx`, registered in
  `src/components/admin/adminNav.ts` and routed in `src/App.tsx` under the
  super-admin `RoleRoute`. The iframe theme follows the app theme (`next-themes`).

When the observability profile is down, the iframe 502s and the tab shows a
helper note — the rest of the app is unaffected.

## Configuration files

Config lives under `deployment/`, mounted read-only into each container
(same convention as `deployment/caddy/Caddyfile`):

| File | Purpose |
|------|---------|
| `otel-collector/config.yaml` | OTLP receivers (4318/4317), batch processor, OTLP exporter to `tempo:4317`, self-metrics on `:8888` |
| `tempo/tempo.yaml` | OTLP/gRPC receiver, local trace storage, HTTP API `:3200` |
| `prometheus/prometheus.yml` | Scrape jobs: all 11 services (by service DNS + port) + `otel-collector:8888`; `rule_files` glob + `alerting.alertmanagers` → `alertmanager:9093` |
| `prometheus/rules/*.yml` | Alert rule definitions loaded by Prometheus (see Alerting below) |
| `alertmanager/alertmanager.yml` | Alertmanager routing: a Slack-formatted receiver (`slack_configs`) at `ALERTMANAGER_WEBHOOK_URL` — works for Slack and Discord (`/slack` suffix) |
| `loki/loki-config.yaml` | Single-binary Loki: filesystem storage, 7-day retention, compaction |
| `alloy/config.alloy` | Alloy: Docker service discovery → tail container stdout → relabel (`service`/`container`) → push to `loki:3100` |
| `grafana/provisioning/datasources/datasources.yaml` | Prometheus (default) + Tempo + Loki datasources, with trace↔log correlation (`tracesToLogsV2`, `trace_id` `derivedField`) |
| `grafana/provisioning/dashboards/dashboards.yaml` | Dashboard file provider |
| `grafana/dashboards/fyredocs-overview.json` | Overview dashboard: a KPI stat header (request rate, error %, p95 latency, jobs/1h) over grouped rows of by-service timeseries (request rate, p95 latency, 5xx error rate, jobs processed/failed) with value-table legends |

> **Note:** `prometheus.yml` hardcodes the api-gateway target as
> `api-gateway:8080`. It is coupled to `${API_GATEWAY_PORT:-8080}` in
> `docker-compose.yml` — keep them in sync if the gateway port changes.

## Alerting

Prometheus does more than scrape and store — it **evaluates alert rules** and forwards
firing alerts to a dedicated **Alertmanager** service.

```
prometheus (evaluates rules/*.yml) --firing alerts--> alertmanager :9093 --slack/discord--> ALERTMANAGER_WEBHOOK_URL
```

- **Rules** — Prometheus loads alert rules from `deployment/prometheus/rules/*.yml`
  (globbed via `rule_files` in `prometheus.yml`).
- **Alertmanager** — a compose service (`prom/alertmanager`, config
  `deployment/alertmanager/alertmanager.yml`) bound to `127.0.0.1:9093` (loopback-only,
  like the other observability UIs). It runs under its own **`alerting`** profile
  (not `observability`), because it **requires** `ALERTMANAGER_WEBHOOK_URL` — an empty
  URL is an invalid config and Alertmanager would refuse to start. Enable delivery with
  `COMPOSE_PROFILES=observability,alerting`. When it is not running, Prometheus still
  evaluates the rules and shows them at `:9090`; it just can't deliver.
- **Delivery** — Alertmanager uses a **Slack-formatted receiver** (`slack_configs`),
  which works for **both Slack and Discord**. The endpoint is the env var
  **`ALERTMANAGER_WEBHOOK_URL`**:
  - **Discord** — a channel webhook URL with **`/slack` appended**:
    `https://discord.com/api/webhooks/<id>/<token>/slack`
  - **Slack** — the incoming-webhook URL: `https://hooks.slack.com/services/…`

### Shipped alert rules

| Alert | Fires when |
|-------|------------|
| `ServiceDown` | a scrape target's `up == 0` |
| `ObservabilityCollectorDown` | the otel-collector target is down |
| `HighServerErrorRate` | >5% of responses are 5xx over 5m |
| `HighRequestLatencyP95` | request p95 latency > 2s over 10m |
| `HighJobFailureRate` | job failure rate is elevated (from `jobs_failed_total` / `jobs_processed_total`) |

### Wiring the webhook (Discord example)

1. Discord → Server Settings → Integrations → Webhooks → **New Webhook** → copy the URL.
2. In `.env`, append **`/slack`** to that URL:

   ```bash
   ALERTMANAGER_WEBHOOK_URL=https://discord.com/api/webhooks/<id>/<token>/slack
   ```

   (For Slack instead, use the incoming-webhook URL verbatim — no suffix.)
3. Deploy with the alerting profile enabled:

   ```bash
   COMPOSE_PROFILES=observability,alerting ./deployment/deploy.sh
   ```

Leave it unset in dev and omit the `alerting` profile — alerts remain inspectable in the
Prometheus UI without starting Alertmanager or spamming a channel.

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
| `ALERTMANAGER_WEBHOOK_URL` | `""` (unset) | Slack/Discord webhook Alertmanager posts firing alerts to (Discord needs the `/slack` suffix). Required when the `alerting` profile is on; unset = don't enable the profile (rules still visible in Prometheus). |
| `OTEL_COLLECTOR_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | collector resources |
| `TEMPO_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Tempo resources |
| `PROMETHEUS_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Prometheus resources |
| `GRAFANA_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | Grafana resources |
