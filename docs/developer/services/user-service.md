# User Service

## Service Responsibility
Owns **organizations**, **memberships**, and the **RBAC role model** (the tenancy + access-control layer). An organization is a tenant that documents, billing, and teams hang off of; a membership ties a user to an organization with a role.

## Design Constraints
- Independent microservice: own Postgres schema, own module (`user-service`), no cross-service DB access or shared models.
- Stateless: identity comes from the `AuthContext` resolved by the auth middleware, which **verifies the JWT locally** (`AUTH_TRUST_GATEWAY_HEADERS=false`). Org-level RBAC is enforced per handler from the caller's membership role.
- Standard response envelope (`shared/response`).

## RBAC roles
`owner` > `admin` > `editor` > `viewer` (rank in `internal/models/org.go`).
- **owner**: full control; set only at creation (one per org); cannot be changed/removed via the member APIs (ownership transfer is out of scope for v1).
- **admin**: manage members and org content.
- **editor**: create/process/edit content.
- **viewer**: read-only.

`RoleAtLeast(role, min)` gates capabilities; member-management endpoints require `admin`+.

## Internal Architecture
- `main.go` — config/logger/telemetry, DB connect+migrate, gin + metrics/trace/request-id, `/metrics`, graceful shutdown. Port `8090` (env `PORT`). No NATS.
- `routes/routes.go` — health + the `/api/orgs` group guarded by `RequireUser()`.
- `handlers/` — `auth.go` (identity, `membershipRole`, `slugify`), `orgs.go` (orgs + members), `health.go`.
- `internal/models/` — `database.go`, `org.go` (Organization, Membership, role helpers).

## Routes
Health: `GET /healthz`, `GET /readyz`.

All `/api/orgs/*` require a verified caller; reached through the gateway at the same paths.

| Method | Path | Description | Min role |
|--------|------|-------------|----------|
| GET | `/api/orgs` | List orgs the caller belongs to (each with the caller's `role`). Paged: `page` (default 1), `limit` (default 25, max 100). | member |
| POST | `/api/orgs` | Create an org; caller becomes `owner`. Body: `name`. | any auth |
| GET | `/api/orgs/:id` | Get an org the caller belongs to (+ caller's role). | member |
| GET | `/api/orgs/:id/members` | List members. Paged: `page` (default 1), `limit` (default 25, max 100). | member |
| POST | `/api/orgs/:id/members` | Add or update a member. Body: `userId`, `role` (admin/editor/viewer). | admin |
| PATCH | `/api/orgs/:id/members/:userId` | Change a member's role (not the owner). Body: `role`. | admin |
| DELETE | `/api/orgs/:id/members/:userId` | Remove a member (never the owner). | admin |

Non-members get `404` on org-scoped reads (existence is not leaked); insufficient role gets `403`.

### Internal (mesh-only)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/internal/v1/orgs/:id` | Membership lookup for peer services. Same handler and payload as `GET /api/orgs/:id`. |

document-service calls this to resolve a caller's org role when authorizing
documents: it holds the user's ID but no JWT to forward, so the public route would
reject it with 401. Guarded by `authverify.InternalTokenGin(INTERNAL_API_TOKEN)`
plus `handlers.InternalIdentity()`, which reads `X-User-ID` — that header is only
trusted **because** the shared secret already proved the caller is a peer service.
The secret fails **closed** (503 when unset), so org-scoped document authorization
fails safe rather than open.

Deliberately absent from `docs/developer/swagger/openapi.yaml`: that spec covers
the public surface, and Caddy never routes `/internal`.

> NOTE: the path and the `role` field of the payload are a contract with
> `document-service/internal/orgclient/orgclient.go`.

**Pagination.** The two list endpoints (`GET /api/orgs`, `GET /api/orgs/:id/members`) accept `page` (default 1) and `limit` (default 25, max 100) query params and return the standard envelope with a `meta` object `{page, limit, total}`. (Previously they returned the full unbounded list with no `meta`.)

### Schema migrations

The schema is applied by **versioned SQL migrations** embedded in the binary
(`internal/migrations/*.sql`, applied by goose at startup under a per-service
Postgres advisory lock and recorded in `goose_db_version_user_service`). GORM
`AutoMigrate` is no longer the authority — it remains only behind
`DB_AUTOMIGRATE=true` for local development. A migration failure aborts startup.
See [schema-migrations.md](../architecture/schema-migrations.md).

## DB Schema (own Postgres)
- **organizations**: id (uuid v7), name, slug (unique), owner_user_id, plan_name (default `free`), created_at, updated_at.
- **memberships**: id, organization_id, user_id, role, created_at, updated_at. Unique `(organization_id, user_id)`; index on `user_id`.

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8090 | Service port |
| DATABASE_URL | — | PostgreSQL connection string (required) |
| REDIS_ADDR | redis:6379 | Backs the JWT denylist. Without it a token revoked at logout keeps working here until natural expiry. |
| REDIS_PASSWORD | — | Redis auth |
| JWT_HS256_SECRET | — | Token signing secret (required — this service verifies tokens itself) |
| JWT_ALLOWED_ALGS | HS256 | Permitted signing algorithms |
| JWT_ISSUER / JWT_AUDIENCE | fyredocs / fyredocs-api | Claim checks |
| JWT_CLOCK_SKEW | 60s | Allowed clock drift |
| AUTH_DENYLIST_ENABLED | true | Check revoked tokens against Redis |
| AUTH_TRUST_GATEWAY_HEADERS | false | Believe upstream `X-User-*` instead of verifying. **Keep false** — see Authentication. |
| INTERNAL_API_TOKEN | — | Shared secret guarding `/internal/v1` (fails closed when unset) |
| TRUSTED_PROXIES | — | Trusted proxy CIDRs |
| OTEL_EXPORTER_OTLP_ENDPOINT | — | OpenTelemetry collector |

## Authentication

`main.go`'s `buildAuthMiddleware` resolves each caller's `AuthContext`. With
`AUTH_TRUST_GATEWAY_HEADERS=false` (the default) it **verifies the JWT locally**
rather than believing the gateway's `X-User-ID` header; the gateway forwards
`Authorization` untouched, so the token is available to verify. `RequireUser()`
then rejects requests with no resolved identity. Member-management endpoints
additionally require the caller to hold an `admin`+ membership in the target org.

**Why local verification matters:** trusting `X-User-ID` is only sound while
nothing but the gateway can reach this port. Once app servers span hosts that stops
being true, and a forged header would let anyone act inside any organization. The
middleware resolves identity but does not gate, so `/healthz`, `/readyz`, and the
shared-secret `/internal/v1` routes still work without a token.

See [horizontal-scaling.md](../architecture/horizontal-scaling.md#prerequisite-1-identity-must-not-depend-on-network-isolation).

## Gateway / Deployment
- Gateway routes `/api/orgs` → `USER_SERVICE_URL` (`http://user-service:8090`); `api-gateway` depends on it.
- `deployment/docker-compose.yml` has a `user-service` block; in `go.work` and every service Dockerfile's go.mod copy list.

## Roadmap (next increments)
- Teams (`teams` table) and per-team membership.
- API keys (`api_keys`) for programmatic access.
- Ownership transfer; invitations (email) instead of raw `userId`.
- Gateway injecting `X-Org-ID` (org scoping is already enforced in document-service, which calls `GET /api/orgs/:id` here to resolve the caller's role).
