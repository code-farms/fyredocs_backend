# Caddy Edge

The Caddy edge is the stack's **only host-exposed component** (ports 80/443) and
the public entrypoint for everything. It absorbs two responsibilities the
api-gateway used to carry in-process — serving the SPA and relaying object bytes
to MinIO — so file bandwidth no longer competes with API routing for the
gateway's CPU. Config: [`deployment/caddy/Caddyfile`](../../../deployment/caddy/Caddyfile); service: the `caddy` block in [`deployment/docker-compose.yml`](../../../deployment/docker-compose.yml).

## Responsibilities

1. **TLS termination** — plain HTTP on `:80` for local/dev; automatic HTTPS via
   Let's Encrypt in production (see [TLS modes](#tls-modes)).
2. **SPA static hosting** — serves the built frontend from `/srv/spa` with
   client-side-routing fallback.
3. **Object-byte routing** — proxies `/uploads/* /outputs/*` straight to
   `minio:9000` for presigned uploads/downloads (bytes never touch the gateway).
4. **API proxying** — forwards `/api/* /auth/* /admin/* /healthz` to
   `api-gateway:8080` on the internal network.

## Topology

```mermaid
graph TB
    Browser["Browser / client"]

    subgraph edge["Caddy edge :80/:443 (only host-exposed service)"]
        TLS["TLS termination<br/>gzip / zstd"]
        R{"path router"}
    end

    Browser -->|HTTPS| TLS --> R
    R -->|"/uploads/* · /outputs/*<br/>(presigned, Host preserved)"| MINIO["MinIO :9000<br/>(internal)"]
    R -->|"/api/* /auth/* /admin/* /healthz"| GW["api-gateway :8080<br/>(internal)"]
    R -->|"/ (everything else)"| SPA["/srv/spa<br/>static files"]

    GW --> SVCS["auth · job · document · user<br/>notification · analytics services"]

    style edge fill:#1f2937,color:#fff
```

`/metrics` is **intentionally not routed** — Prometheus scrapes services on the
internal network, so metrics are not internet-reachable.

Likewise the **NATS monitoring endpoint** (`nats:8222`, enabled via `-m 8222`)
is not exposed at the edge. The admin NATS dashboard tab (`/admin/metrics/nats`)
does not reach NATS directly — it goes through Caddy → api-gateway →
analytics-service, which scrapes `http://nats:8222` from inside `fyredocs_net`
and shapes the JSON. NATS itself stays internal-only.

## Request routing (sequence)

```mermaid
sequenceDiagram
    participant B as Browser
    participant C as Caddy edge
    participant M as MinIO (internal)
    participant G as api-gateway (internal)

    Note over B,C: 1. App shell
    B->>C: GET /
    C-->>B: index.html from /srv/spa (assets cached 1y, immutable)

    Note over B,C: 2. API call
    B->>C: POST /api/convert-to-pdf/word-to-pdf
    C->>G: proxy (flush_interval -1)
    G-->>C: 201 + job JSON
    C-->>B: 201

    Note over B,M: 3. Object bytes (presigned, bypasses the gateway)
    B->>C: PUT /uploads/uploads/<id>/file?partNumber=1&X-Amz-...
    C->>M: reverse_proxy minio:9000 (Host preserved, verbatim path)
    M-->>C: 200 + ETag
    C-->>B: 200 + ETag
```

## Route rules (from the Caddyfile)

| Matcher | Paths | Upstream / action | Notes |
|---------|-------|-------------------|-------|
| `@objects` | `/{$S3_BUCKET_UPLOADS}/*`, `/{$S3_BUCKET_OUTPUTS}/*` (default `uploads`/`outputs`) | `reverse_proxy minio:9000` | `header_up Host {host}` (SigV4), `flush_interval -1`, no auth middleware — the presigned signature is the credential |
| `@api` | `/api/*`, `/auth/*`, `/admin/*`, `/healthz` | `reverse_proxy` → **dynamic `a` upstreams** for `api-gateway:8080` | Load balanced (`lb_policy least_conn`) across every running gateway replica; `flush_interval -1` for SSE streams. See [Scaling the gateway](#scaling-the-gateway) |
| `@grafana` | `/grafana`, `/grafana/*` | `forward_auth` → gateway `/admin/authz`, then `reverse_proxy grafana:3000` | Serves the embedded observability dashboards. `forward_auth` enforces super-admin (this path bypasses the gateway proxy). Only present when the `observability` compose profile is up. See [observability.md](./observability.md#in-app-embedding-admin-observability-tab) |
| (fallback) | everything else | `file_server` from `/srv/spa` | `try_files {path} /index.html`; `/assets/*` served `Cache-Control: public, max-age=31536000, immutable`; **security headers** (see below) |

### Security headers (finding A1 / production-readiness S8)

The api-gateway sets response security headers only on the routes it proxies
(`/api /auth /admin /healthz`). The SPA document and the object routes are served
by Caddy directly, so they previously carried none. Caddy now sets them at the edge:

- **SPA `handle`** — `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin`, and a
  `Content-Security-Policy`:
  `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`.
  `style-src 'unsafe-inline'` is required for Tailwind/Radix inline styles, the
  chart `<style>`, and inline Mermaid SVG; `default-src 'self'` still permits the
  same-origin Grafana iframe. **Validate the CSP against the running SPA** (open the
  docs page with Mermaid + charts and watch the browser console) before treating it
  as final — relax a single directive only for a legitimately-blocked resource.
- **`@objects`** — `X-Content-Type-Options: nosniff` so object bytes can't be
  MIME-sniffed into an executable type on this origin (downloads also force
  attachment disposition via the presigned `response-content-disposition`).

#### Allowing ad networks — `CSP_AD_DOMAINS`

The CSP is strict (same-origin) by default, which blocks third-party ads. To run
one or more ad networks without editing the Caddyfile, set `CSP_AD_DOMAINS` in the
root `.env` to the networks' hosts (space-separated). The value is substituted into
the `script-src`, `img-src`, `connect-src`, and `frame-src` directives via
`{$CSP_AD_DOMAINS}` (empty by default → strict, no ads). Compose passes it to the
caddy container (`CSP_AD_DOMAINS: ${CSP_AD_DOMAINS:-}`).

```dotenv
# several networks can share one line; 'unsafe-inline' covers most ad init snippets
CSP_AD_DOMAINS='unsafe-inline' https://pagead2.googlesyndication.com https://*.doubleclick.net https://*.google.com
```

One knob feeds all four directives (ad hosts span every type; a host landing in a
directive it doesn't need is ignored by the browser). `'unsafe-inline'` is only
meaningful in `script-src`. `frame-ancestors 'none'` is unaffected — it controls
who may frame *our* site, not the ad iframes *we* embed.

> **Security tradeoff:** allowing external ad scripts (and `'unsafe-inline'`)
> measurably weakens the CSP's XSS protection. This is unavoidable for ad-supported
> sites; the strict default protects everything until ads are enabled. After
> setting the var, load the site and clear any remaining console CSP errors by
> adding the exact hosts they name. (Ads usually also need a cookie-consent banner
> for EU users — separate concern.)

### Why Host preservation is load-bearing

Presigned SigV4 URLs are signed against the **public origin the browser uses**
(`PUBLIC_ORIGIN`, i.e. this edge), and the signature covers the `Host` header
and the canonical path. Caddy forwards the **original** `Host` (`header_up Host
{host}`) and the path **verbatim** to `minio:9000`; MinIO recomputes the
signature against what it receives. Rewriting the Host (or stripping the bucket
prefix) would invalidate every presigned URL. See
[object-storage.md](./object-storage.md#presigned-flow-through-the-caddy-edge-same-origin).

## Scaling the gateway

The `@api` route load balances across api-gateway replicas using Caddy's
**dynamic upstreams** (`dynamic a`). Instead of a fixed `api-gateway:8080`
target, Caddy re-resolves the Compose service name against Docker's embedded
DNS (`127.0.0.11`) every `5s`. Docker returns one A record per running replica,
so scaling is a runtime operation — no Caddyfile edit:

> **Gateway port is configurable.** The upstream port is
> `port {$API_GATEWAY_PORT:8080}`, resolved from the `API_GATEWAY_PORT` env var
> (default `8080`, set in the root `.env`). The same var drives the gateway
> container's `PORT`/`expose`/healthcheck and the analytics dashboard's
> gateway-scrape URLs, so they always agree. The gateway is **internal-only**
> (never host-published — only Caddy's 80/443 are), so this port is not reachable
> from outside; choosing a non-obvious value is defense-in-depth on the internal
> network. The `.env` ships a non-default value (e.g. `47821`).

```bash
docker compose up -d --scale api-gateway=3   # add replicas
docker compose up -d --scale api-gateway=1   # scale back
```

The `api-gateway` service intentionally has **no `container_name`** so Compose
can run multiple instances; keep it that way.

Balancing and resilience settings on the route:

- `lb_policy least_conn` — send each request to the replica with the fewest
  active connections.
- `lb_try_duration 5s` / `lb_try_interval 250ms` — a failed request is
  transparently retried against another replica.
- Passive health (`max_fails 3`, `fail_duration 30s`, `unhealthy_status 5xx`) —
  a replica returning 5xx is ejected temporarily. Active `health_uri` checks do
  **not** apply to dynamic upstreams; the DNS refresh removes departed replicas
  and passive checks cover failing ones.

## TLS modes

The site address is `{$PUBLIC_DOMAIN::80}`:

- **`PUBLIC_DOMAIN` unset** → the container receives the compose default `:80`
  (`PUBLIC_DOMAIN: ${PUBLIC_DOMAIN:-:80}`) → **plain HTTP on :80** for local/dev.
  > The default is supplied by compose, not by Caddy's `{$VAR:default}` — compose
  > always defines the variable in the container, and Caddy's fallback only
  > applies to an *unset* variable, not a defined-but-empty one.
- **`PUBLIC_DOMAIN=docs.example.com`** → Caddy obtains and renews a certificate
  automatically (Let's Encrypt) and serves **HTTPS on :443** (with :80 redirect).
  Certificates persist in the `caddy_data` volume.

## Deployment

```yaml
caddy:
  image: caddy:2-alpine
  ports: ["80:80", "443:443"]        # the only published ports in the stack
  environment:
    PUBLIC_DOMAIN: ${PUBLIC_DOMAIN:-:80}
    S3_BUCKET_UPLOADS: ${S3_BUCKET_UPLOADS:-uploads}
    S3_BUCKET_OUTPUTS: ${S3_BUCKET_OUTPUTS:-outputs}
  volumes:
    - ./caddy/Caddyfile:/etc/caddy/Caddyfile:ro
    - ../../fyredocs_frontend/dist:/srv/spa:ro   # SPA build output
    - caddy_data:/data                            # certificates persist here
    - caddy_config:/config
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost/healthz"]
  depends_on: [api-gateway, minio]
```

- The SPA build (`fyredocs_frontend/dist`) is bind-mounted read-only; rebuild the
  frontend and Caddy serves the new assets (immutable-cached under `/assets/*`).
- `S3_BUCKET_*` are passed so the `@objects` matcher tracks any non-default
  bucket names.
- Health: the edge's own `/healthz` proxies through to the gateway, so a healthy
  Caddy container also implies the gateway is reachable.

## Operational notes

- **Adapt errors are fatal & crash-loop.** Caddy validates the whole Caddyfile
  at startup; a bad global option or an empty site address makes it exit and
  restart. Check `docker compose logs caddy` for `adapting config … error`.
- **Local HTTPS**: leave `PUBLIC_DOMAIN` unset (plain HTTP) — Let's Encrypt
  can't issue for `localhost`.
- **Metrics**: not exposed at the edge by design; scrape services internally.

## Related documentation

- [Object Storage](./object-storage.md) — presigned flow, buckets, SigV4 Host preservation
- [API Gateway](../services/api-gateway.md) — the internal service behind the edge
- [Compose files layout](./compose-files.md) — where the edge fits in the stack
- [System overview diagram](../mermaid/system-overview.md)
