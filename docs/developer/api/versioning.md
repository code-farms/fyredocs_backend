# API Versioning

Every public route is served under a versioned root:

```
/api/v1/auth/login          /api/v1/jobs/{id}          /api/v1/admin/metrics/overview
/api/v1/upload/init         /api/v1/documents          /api/v1/dashboard
```

The old unversioned spellings (`/auth/login`, `/api/jobs/{id}`, `/admin/...`)
**still work and return identical responses** — they are aliases of the same
routes, not a separate API.

## How it works

The alias table is generated at gateway startup by `withV1Aliases`
(`api-gateway/main.go`): for every configured route it registers a second
prefix under `/api/v1`, pointing at the same upstream and the same base path.

```
legacy prefix          v1 alias                 upstream (unchanged)
/auth              →   /api/v1/auth         →   auth-service     /auth
/api/upload        →   /api/v1/upload       →   job-service      /api/uploads
/api/jobs          →   /api/v1/jobs         →   job-service      /api/jobs
/admin             →   /api/v1/admin        →   analytics-service /admin
```

Downstream services know nothing about versions — the gateway strips whichever
prefix matched and re-roots the request under the upstream's base path, exactly
as before. A route and its alias share one circuit breaker (same upstream) but
have their own proxy instance (each strips its own prefix).

### The one exception: public share links

`withV1Aliases` builds the alias as `"/api/v1" + strings.TrimPrefix(prefix, "/api")`.
`TrimPrefix` is a no-op on a prefix that does not start with `/api` — deliberately,
so `/auth` mirrors to `/api/v1/auth`. The consequence is that a `/x` route and an
`/api/x` route generate the **same** alias:

```
/api/share   →   /api/v1/share      (authenticated create / revoke)
/share       →   /api/v1/share      (public redeem)   << collision
```

`http.ServeMux` panics on a duplicate pattern during registration, which kills the
gateway at boot — every request 502s, not just that path. So the public redeem
route sets `noV1Alias: true` and is reachable **only** at the unversioned root:

```
/share/{token}   →   job-service   GET /share/{token}     (public, no v1 form)
/api/v1/share    →   job-service   /api/share             (authenticated)
```

Share links are short and unversioned by design, so nothing is lost. Two rules
follow, both covered by tests in `api-gateway/main_test.go`:

- Adding a route whose prefix omits `/api` while an `/api`-prefixed sibling exists
  requires `noV1Alias` on one of them. `TestServiceRoutesHaveNoAliasCollisions`
  asserts this against the real table (`serviceRoutes`) — not a fixture, which is
  how this collision originally shipped.
- `/api/v1/share/` must never appear in the gateway's `PublicPrefixes`: it belongs
  to the authenticated management group, so making it public would allow
  `DELETE /api/v1/share/{id}` without a token. `TestPublicPrefixesExcludeVersionedShare`
  pins that.

## Clients

Both first-party clients build every URL through one function, so the switch is
a single rewrite rather than hundreds of string edits:

| Client | File | Escape hatch |
|---|---|---|
| Web (Vite/React) | `src/lib/apiVersion.ts` → used by `buildApiUrl` and `buildAuthUrl` | `VITE_API_VERSION_PREFIX=''` |
| Mobile (Expo) | `src/config/env.ts` (`versionPath`, used by `buildApiUrl`) | `EXPO_PUBLIC_API_VERSION_PREFIX=''` |

Call sites keep the legacy spelling (`apiRequest('/api/jobs')`); the helper
rewrites it. Setting the prefix to an empty string targets an older gateway that
has no aliases yet.

## Deprecating the legacy paths

Legacy prefixes are marked `deprecated` in the gateway route table, but the
`Deprecation: true` / `Link: rel="deprecation"` response headers are **off by
default** (`API_DEPRECATION_HEADERS=false`). That is deliberate: a mobile build
already installed cannot be force-updated, and its only known paths are the
legacy ones — advertising deprecation to it is noise the user cannot act on.

Sequence:

1. Ship the gateway aliases (done) — nothing changes for clients.
2. Ship web + mobile builds that target `/api/v1` (done).
3. Once the mobile release has propagated, set `API_DEPRECATION_HEADERS=true`.
4. Watch the split before removing anything:

```promql
sum by (path) (rate(http_request_duration_seconds_count{path=~"/api/jobs|/api/v1/jobs"}[1h]))
```

5. Remove a legacy prefix only when its traffic is ~0. Removal means dropping
   the `deprecated` entries from the gateway route table — downstream services
   still need no changes.

## Adding a new route

Add it to the `routes` slice in `api-gateway/main.go` with its legacy prefix as
usual; the `/api/v1` alias is generated automatically. New public endpoints
should be documented under `/api/v1` in
[`swagger/openapi.yaml`](../swagger/openapi.yaml).

## Why URL versioning

Header/content-negotiation versioning would be worse for this stack: Caddy
routes by path at the edge, Prometheus labels by route template, and
`EventSource` (the SSE job-progress stream) cannot set custom request headers.
A path prefix keeps all three working unchanged.
