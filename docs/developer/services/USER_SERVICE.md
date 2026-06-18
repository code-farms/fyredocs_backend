# User Service

## Service Responsibility
Owns **organizations**, **memberships**, and the **RBAC role model** (the tenancy + access-control layer). An organization is a tenant that documents, billing, and teams hang off of; a membership ties a user to an organization with a role.

## Design Constraints
- Independent microservice: own Postgres schema, own module (`user-service`), no cross-service DB access or shared models.
- Stateless: identity from the gateway-injected `X-User-ID` header. Org-level RBAC is enforced per handler from the caller's membership role.
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

All `/api/orgs/*` require auth (`X-User-ID`); reached through the gateway at the same paths.

| Method | Path | Description | Min role |
|--------|------|-------------|----------|
| GET | `/api/orgs` | List orgs the caller belongs to (each with the caller's `role`). | member |
| POST | `/api/orgs` | Create an org; caller becomes `owner`. Body: `name`. | any auth |
| GET | `/api/orgs/:id` | Get an org the caller belongs to (+ caller's role). | member |
| GET | `/api/orgs/:id/members` | List members. | member |
| POST | `/api/orgs/:id/members` | Add or update a member. Body: `userId`, `role` (admin/editor/viewer). | admin |
| PATCH | `/api/orgs/:id/members/:userId` | Change a member's role (not the owner). Body: `role`. | admin |
| DELETE | `/api/orgs/:id/members/:userId` | Remove a member (never the owner). | admin |

Non-members get `404` on org-scoped reads (existence is not leaked); insufficient role gets `403`.

## DB Schema (own Postgres)
- **organizations**: id (uuid v7), name, slug (unique), owner_user_id, plan_name (default `free`), created_at, updated_at.
- **memberships**: id, organization_id, user_id, role, created_at, updated_at. Unique `(organization_id, user_id)`; index on `user_id`.

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8090 | Service port |
| DATABASE_URL | — | PostgreSQL connection string (required) |
| TRUSTED_PROXIES | — | Trusted proxy CIDRs |
| OTEL_EXPORTER_OTLP_ENDPOINT | — | OpenTelemetry collector |

## Authentication
The gateway verifies the JWT and injects `X-User-ID`. `RequireUser()` rejects requests without it. Member-management endpoints additionally require the caller to hold an `admin`+ membership in the target org.

## Gateway / Deployment
- Gateway routes `/api/orgs` → `USER_SERVICE_URL` (`http://user-service:8090`); `api-gateway` depends on it.
- `deployment/docker-compose.yml` has a `user-service` block; in `go.work` and every service Dockerfile's go.mod copy list.

## Roadmap (next increments)
- Teams (`teams` table) and per-team membership.
- API keys (`api_keys`) for programmatic access.
- Ownership transfer; invitations (email) instead of raw `userId`.
- Org scoping wired into document-service (documents gain `organization_id` enforcement) and the gateway injecting `X-Org-ID`.
