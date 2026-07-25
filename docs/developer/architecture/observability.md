# Observability Stack

The Go services are instrumented for observability out of the box:

- **Tracing** — `shared/telemetry` initializes an OTLP/HTTP tracer in every
  service and installs HTTP/Gin middleware that starts a span per request and
  propagates W3C trace context. Services export to
  `OTEL_EXPORTER_OTLP_ENDPOINT` (`http://otel-collector:4318` in compose).

  **Traces cross NATS, not just HTTP.** `shared/queue` injects the W3C context
  into NATS message headers on publish (`PublishJobEvent`,
  `PublishAnalyticsEvent`, and `PublishRaw` for DLQ/redrive hops, which also
  preserve the original message's headers) and extracts it on consume
  (`StartConsumerSpan`, used by the four PDF workers' `processMessage` and by
  the analytics/notification/document subscribers). One job is therefore one
  trace: gateway → job-service → `jobs.dispatch.*` → worker → terminal event.
  Worker log lines use the `*Context` slog variants on that context, so they
  carry `trace_id` and the Grafana log→trace jump works from the worker path
  too.
- **Metrics** — `shared/metrics` registers Prometheus collectors
  (`http_request_duration_seconds`, `jobs_processed_total`, `jobs_failed_total`,
  `job_duration_seconds`)
  and every service serves `GET /metrics`. `http_request_duration_seconds` is
  recorded by the HTTP/Gin middleware; `jobs_processed_total` (label `status`) and
  `jobs_failed_total` (label `reason`), both keyed by `tool_type`, are emitted per
  job outcome from `shared/queue.PublishJobEvent` — the single choke point every
  worker's terminal `JobCompleted`/`JobFailed` event flows through.

  `job_duration_seconds` (histogram, labels `tool_type`/`status`) is observed at
  that same choke point and measures **end-to-end job latency**: job-service
  stamps `JobEvent.QueuedAt` at dispatch, workers echo it on terminal events,
  and the difference is recorded. Because a redelivery reuses the original
  dispatch message, the measurement includes retry and DLQ dwell time — that is
  deliberate, it is the latency a user actually experiences. Events without
  `QueuedAt` (pre-upgrade messages) are skipped rather than mis-measured. The
  `JobLatencyP95High` alert fires when the completed-job p95 exceeds 10 minutes
  for 15 minutes.

  `rate_limit_fail_open_total` (labels `service`/`limiter`) counts requests that
  bypassed rate limiting because the limiter's Redis backend was unavailable.
  Failing open is deliberate — a Redis blip must not take down request serving —
  but it means throttling is silently off, so the `RateLimitFailingOpen` alert
  treats any sustained rate as critical.

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
| `prometheus` | `prom/prometheus` | Scrapes every service's `/metrics` + the collector; evaluates alert rules and POSTs firing alerts to analytics-service's built-in receiver | `9090` (loopback-only UI) |
| `loki` | `grafana/loki` | Log storage & query backend (filesystem, ~7-day retention) | `3100` (loopback-only API) |
| `alloy` | `grafana/alloy` | Log shipper — tails every container's stdout (Docker socket) and pushes to Loki | `12345` (internal UI) |
| `grafana` | `grafana/grafana` | Dashboards + trace explorer + log explorer, over Prometheus / Tempo / Loki | `3000` (loopback-only UI) |

All are internal to `fyredocs_net`. Only the Grafana, Prometheus, and Loki
endpoints publish a host port, all bound to `127.0.0.1` (operator-only,
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

## Resource budget

The six observability containers are **inside** `deploy.sh`'s host resource
budget (`RESOURCE_BUDGET_PCT`, default 70): their `*_MEM_LIMIT`/`*_CPU_LIMIT`
are scaled from the same pool as the app services, so Σ(limits) ≤ budget holds
for the whole stack. Previously they sat on top of the cap (~2.5 GB) and could
push a small host into the OOM killer.

Below `OBSERVABILITY_MIN_HOST_MB` (default 8192) the deploy **refuses** to
enable the profile rather than squeezing the app services into caps they cannot
run in — disable it (`COMPOSE_PROFILES= ./deployment/deploy.sh`) or override
with `OBSERVABILITY_FORCE=1`.

The collector runs a `memory_limiter` processor ahead of `batch`
(75% soft / +25% spike, read from the cgroup limit) so it sheds load instead of
being OOM-killed, and the OTLP exporter's default `sending_queue`/retry buffers
spans across a brief Tempo restart.

**Grafana's admin password has no default.** `GRAFANA_ADMIN_PASSWORD` is
required whenever the profile is active (compose fails fast; `deploy.sh`
validates it up front with a friendlier message). The old `admin` fallback made
anyone with loopback/SSH-tunnel/SSRF access to `:3000` a Grafana admin.
Anonymous **Viewer** access stays enabled — it is what lets the admin SPA embed
dashboards same-origin through the Caddy `@grafana` block, which is itself
guarded by `forward_auth`; the strong admin password removes the escalation
path that made the combination dangerous.

## Sampling

`OTEL_TRACES_SAMPLER_RATIO` (0.0–1.0, default 1.0) controls trace sampling. The
sampler is **ParentBased**, so the decision made at the edge is honored by every
downstream service and NATS consumer — otherwise a trace would be sampled in one
hop and dropped in the next, producing broken partial traces. Lower it (e.g.
0.1) once production RPS makes 100% sampling expensive in Tempo storage.

## Log volume

Successful `/metrics`, `/healthz`, and `/readyz` requests are **not** logged:
Prometheus scrapes every 15s and Docker healthchecks every 5–10s, which buried
real traffic and dominated Loki ingest. A failing probe is still logged, at warn
level.

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

The admin **Observability** tab (`/admin/observability`, super-admin only) embeds
Grafana same-origin via an `<iframe>` served through the Caddy edge, with a
**Metrics / Logs toggle**:
- **Metrics** → the `fyredocs-overview` dashboard (`/grafana/d/fyredocs-overview/...?kiosk`).
- **Logs** → the `fyredocs-logs` dashboard (`/grafana/d/fyredocs-logs/...?kiosk`) — the
  centralized Loki logs view (service dropdown + free-text search), so admins can read
  and search logs without leaving the app. An "Open … in Grafana" link opens the active
  dashboard full-screen in a new tab.

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
| `prometheus/prometheus.yml` | Scrape jobs: all 11 services (by service DNS + port) + `otel-collector:8888`; `rule_files` glob + `alerting.alertmanagers` → `analytics-service:8087` (path_prefix `/internal/alerts`) |
| `prometheus/rules/*.yml` | Alert rule definitions loaded by Prometheus (see Alerting below) |
| _(alert delivery)_ | No Alertmanager container. `shared/discord` — mounted in analytics-service at `POST /internal/alerts/api/v2/alerts` — receives Prometheus alerts and forwards them to `DISCORD_WEBHOOK_URL` as native Discord embeds |
| `loki/loki-config.yaml` | Single-binary Loki: filesystem storage, 7-day retention, compaction |
| `alloy/config.alloy` | Alloy: Docker service discovery → tail container stdout → relabel (`service`/`container`) → push to `loki:3100` |
| `grafana/provisioning/datasources/datasources.yaml` | Prometheus (default) + Tempo + Loki datasources, with trace↔log correlation (`tracesToLogsV2`, `trace_id` `derivedField`) |
| `grafana/provisioning/dashboards/dashboards.yaml` | Dashboard file provider |
| `grafana/dashboards/fyredocs-overview.json` | Overview (Metrics) dashboard: a KPI stat header (request rate, error %, p95 latency, jobs/1h) over grouped rows of by-service timeseries (request rate, p95 latency, 5xx error rate, jobs processed/failed) with value-table legends |
| `grafana/dashboards/fyredocs-logs.json` | Logs dashboard (Loki, uid `fyredocs-logs`): a logs panel with a `service` dropdown (`label_values(service)`) + free-text `search` variable — the centralized-logs view embedded in the admin Observability tab |

> **Note:** `prometheus.yml` hardcodes the api-gateway target as
> `api-gateway:8080`. It is coupled to `${API_GATEWAY_PORT:-8080}` in
> `docker-compose.yml` — keep them in sync if the gateway port changes.

## Alerting

Prometheus does more than scrape and store — it **evaluates alert rules** and posts
firing/resolved alerts straight to a **built-in receiver inside analytics-service**
(the `shared/discord` package), which forwards them to a Discord channel. There is
**no standalone Alertmanager container** — one fewer image to run.

```
prometheus (evaluates rules/*.yml)
   --POST /internal/alerts/api/v2/alerts--> analytics-service (shared/discord)
   --native embed--> DISCORD_WEBHOOK_URL
```

- **Rules** — Prometheus loads alert rules from `deployment/prometheus/rules/*.yml`
  (globbed via `rule_files` in `prometheus.yml`).
- **Receiver** — `shared/discord` exposes a handler mounted in analytics-service at
  `POST /internal/alerts/api/v2/alerts` (unauthenticated, internal-only — Caddy never
  routes `/internal`). It speaks the Alertmanager v2 API, so Prometheus's
  `alerting.alertmanagers` just targets `analytics-service:8087` with
  `path_prefix: /internal/alerts`. analytics-service is always running, so no profile
  gating is needed.
- **Dedup/throttle** — because Prometheus re-POSTs firing alerts every evaluation
  interval, the receiver keeps an in-memory per-alert fingerprint and re-notifies only
  on a state change (firing↔resolved) or after `DISCORD_ALERT_REPEAT` (default 4h) —
  the small slice of Alertmanager behaviour we still need. (We give up Alertmanager's
  richer routing/silencing/inhibition; acceptable for a single Discord channel.)
- **Delivery** — an **executive rich-card embed**: brand author (+ logo), severity-colored
  side bar (critical=red, warning=orange, resolved=green), clickable title (→ Grafana),
  a humanized alert name, inline **Status / Severity / Service** fields, a **Details**
  block, and a footer `Fyredocs • <env>` with a timestamp. Built once in `shared/discord`
  (`embedFor`) so every notification looks identical. Set `DISCORD_WEBHOOK_URL` to a
  **plain** Discord channel webhook — **no `/slack` suffix**. Unset → the receiver still
  200s and no-ops (rules stay visible in the Prometheus UI, `:9090`).
- **Branding env** (optional): `DISCORD_ALERT_ICON_URL` (logo; public HTTPS only),
  `DISCORD_DASHBOARD_URL` (clickable-title link), `ENVIRONMENT` (footer badge). The
  **db-backup** sidecar reuses the same style for its alerts (`backup.sh`) — use a plain
  `BACKUP_ALERT_WEBHOOK_URL` for the rich card, or a `/slack` URL for a plain-text fallback.
  All these env vars must be present in the container (`docker-compose.yml` passes them to
  analytics-service and db-backup).

### Shipped alert rules

| Alert | Fires when |
|-------|------------|
| `ServiceDown` | a scrape target's `up == 0` |
| `ObservabilityCollectorDown` | the otel-collector target is down |
| `HighServerErrorRate` | >5% of responses are 5xx over 5m |
| `HighRequestLatencyP95` | request p95 latency > 2s over 10m |
| `HighJobFailureRate` | job failure rate is elevated (from `jobs_failed_total` / `jobs_processed_total`) |
| `DLQBacklog` | `nats_dlq_pending_messages > 0` for 5m — dead-lettered jobs piling up |
| `DependencyDown` | `dependency_up == 0` for 2m — Redis/NATS/Postgres unreachable from analytics-service |
| `DBPoolNearExhaustion` | a service's `db_pool_in_use / db_pool_max_open > 0.9` for 5m |

### Metrics feeding the rules (where the data comes from)

| Metric | Source |
|--------|--------|
| `up` | Prometheus itself — did the `/metrics` scrape of each target succeed |
| `http_request_duration_seconds` (count/buckets) | `shared/metrics` HTTP + Gin middleware on every service (RED: rate/errors/latency) |
| `jobs_processed_total` / `jobs_failed_total` | `shared/metrics` counters, incremented by workers via `shared/queue` on job events |
| `db_pool_{open,in_use,max_open}_connections`, `db_pool_wait_count_total` | `shared/database` — a collector reading each service's `sql.DB.Stats()` at scrape time (all DB services expose it; labelled by scrape `instance`) |
| `dependency_up{dependency}` , `nats_dlq_pending_messages` | analytics-service ops-metrics poller (`internal/opsmetrics`) — pings Redis/Postgres and reads JOBS_DLQ `State.Msgs` every `OPS_METRICS_INTERVAL` (20s) |

### Wiring the Discord webhook

1. Discord → Server Settings → Integrations → Webhooks → **New Webhook** → copy the URL.
2. In `.env` (plain URL, no suffix):

   ```bash
   DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/<id>/<token>
   # optional: DISCORD_ALERT_REPEAT=4h
   ```

3. Deploy with the observability profile (default): `./deployment/deploy.sh`.

Leave `DISCORD_WEBHOOK_URL` unset in dev — alerts remain inspectable in the Prometheus
UI without pinging a channel.

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
| `DISCORD_WEBHOOK_URL` | `""` (unset) | Plain Discord channel webhook the analytics-service alert receiver posts native embeds to (no `/slack` suffix). Unset = alerts received but not delivered (rules still visible in Prometheus). |
| `DISCORD_ALERT_REPEAT` | `4h` | Re-notify interval for a still-firing alert (dedup, like Alertmanager's `repeat_interval`). |
| `DISCORD_ALERT_ICON_URL` | `""` | Logo shown in the embed author/footer. Public HTTPS only (Discord fetches it server-side); omitted on localhost. |
| `DISCORD_DASHBOARD_URL` | `""` | Makes the embed title clickable → your Grafana view. Falls back to the alert's Prometheus `generatorURL`. |
| `ENVIRONMENT` | `""` | Footer badge (`Fyredocs • production`) on every embed; also drives the production security guard. |
| `OPS_METRICS_INTERVAL` | `20s` | How often analytics-service samples `dependency_up` + `nats_dlq_pending_messages`. |
| `OTEL_COLLECTOR_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | collector resources |
| `TEMPO_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Tempo resources |
| `PROMETHEUS_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `512M` / `0.5` / `128M` | Prometheus resources |
| `GRAFANA_MEM_LIMIT` / `_CPU_LIMIT` / `_MEM_RESERVATION` | `256M` / `0.5` / `64M` | Grafana resources |
