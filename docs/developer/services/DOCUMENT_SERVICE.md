# Document Service

## Service Responsibility
Owns the **persistent document library**: documents (metadata for files stored in object storage), folders, tags, and document↔tag associations. This is the document-centric layer on top of the job-centric processing pipeline — a document is a managed, searchable, organizable entity, distinct from an ephemeral processing job.

The service stores **metadata only**; file bytes live in object storage (referenced by `storage_path`). It is read/write scoped to the authenticated user.

## Design Constraints
- Independent microservice: own Postgres schema, own module (`document-service`), no cross-service DB access or shared models (per repo `CLAUDE.md`).
- Stateless: identity comes from the gateway-injected `X-User-ID` header (the gateway verifies the JWT). No session state.
- Standard response envelope (`shared/response`): `{success, message, data, error, meta}`.

## Internal Architecture
- `main.go` — config/logger/telemetry init, DB connect + migrate, gin with metrics/trace/request-id middleware, `/metrics`, graceful shutdown. Port `8089` (env `PORT`).
- `routes/routes.go` — health + the `/api` group guarded by `RequireUser()`.
- `handlers/` — `auth.go` (identity + RBAC helpers), `documents.go`, `folders.go`, `tags.go`, `exports.go`, `hints.go` (workspace hints), `health.go`.
- `internal/models/` — `database.go` (Connect/Migrate), `document.go`, `folder.go`, `tag.go`, `export.go`, `jobhint.go`.
- `subscriber/subscriber.go` — **NATS is a hard dependency** (`main.go` fails fast if the connection or subscriber start fails). A durable JetStream consumer `document-job-events` on the `JOBS_EVENTS` stream (filter `jobs.events.>`, `DeliverNew`, explicit ack) finalizes every `JobCompleted` event for an authenticated user into a `documents` row — the server-side counterpart to direct creation, so any completed job becomes a library document regardless of client. Finalize is idempotent via a partial unique index on `(user_id, source_job_id)` (soft-deleted docs are not resurrected), and files the document into an org workspace when a `JobWorkspaceHint` exists.

## Routes
Health: `GET /healthz` (liveness), `GET /readyz` (DB ping).

All `/api/*` routes require auth (`X-User-ID`) and are scoped to that user. Reached through the gateway at the same paths (`/api/documents`, `/api/folders`, `/api/tags`).

**Organization scoping (RBAC):** document endpoints accept an optional `orgId` (query param, or `organizationId` in the create body). When present, document-service verifies the caller's membership and role with **user-service** (`GET /api/orgs/:id`) and scopes documents by `organization_id`: reads (list/get) require `viewer`+, writes (create/update/delete/restore/purge) require `editor`+. When absent, documents are personal (scoped by `user_id`, `organization_id IS NULL`) — the default and the shape the finalize subscriber creates. Folders/tags remain personal-scoped in this increment.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/documents` | List documents. Filters: `status`, `folderId`, `tagId`, full-text `q`, `trashed=true` (Trash view); paging `page`/`limit` (max 100). Returns `meta` with page/limit/total. |
| POST | `/api/documents` | Create a document (metadata). Body: `name` (required), `folderId?`, `fileType?`, `mimeType?`, `fileSize?`, `storagePath?`, `status?`, `metadata?`. |
| GET | `/api/documents/:id` | Get one owned document (with tags). |
| PATCH | `/api/documents/:id` | Update `name`/`folderId`/`status`/`metadata`. Empty `folderId` moves to root. |
| DELETE | `/api/documents/:id` | Soft-delete (move to Trash). |
| POST | `/api/documents/:id/restore` | Restore a soft-deleted document from Trash. |
| DELETE | `/api/documents/:id/permanent` | Permanently delete (hard delete + tag associations). |
| POST | `/api/documents/:id/tags` | Attach a tag. Body: `tagId`. |
| DELETE | `/api/documents/:id/tags/:tagId` | Detach a tag. |
| POST | `/api/documents/workspace-hint` | Record that a job should finalize into an org workspace (editor+ verified via user-service). Body: `jobId`, `organizationId` (empty clears the hint → personal). Consumed and cleared by the finalize subscriber on completion. |
| GET | `/api/folders` | List folders. Filter: `parentId`. |
| POST | `/api/folders` | Create folder. Body: `name` (required), `parentId?`. |
| PATCH | `/api/folders/:id` | Rename/move (`name?`, `parentId?`). |
| DELETE | `/api/folders/:id` | Soft-delete; contained documents move to root. |
| GET | `/api/tags` | List tags. |
| POST | `/api/tags` | Create tag (idempotent on `(user, name)`). Body: `name` (required), `color?`. |
| DELETE | `/api/tags/:id` | Delete tag + its associations. |
| GET | `/api/exports` | List the caller's exports (status/metadata, no artifact bytes). |
| POST | `/api/exports` | Queue an async export of documents in scope. Body: `format` (`csv`\|`json`), optional `organizationId` (viewer+), `status`/`folderId`/`tagId` filters. Returns the queued export. |
| GET | `/api/exports/:id` | Export status/metadata (poll until `ready`). |
| GET | `/api/exports/:id/download` | Download a ready export artifact. |

**Exports** are generated asynchronously (v1: in-process; an `EXPORTS` NATS work-queue is the scale follow-up). The CSV/JSON artifact bytes are stored on the `exports` row (`exports` table: id, user_id, organization_id?, format, status `queued|processing|ready|failed`, file_name, content, document_count, filters, completed_at). Binary/ZIP exports to object storage are a follow-up.

## DB Schema (own Postgres)
- **documents**: id (uuid v7), user_id, organization_id?, folder_id?, name, file_type, mime_type, file_size, storage_path, thumbnail_path, status (`uploaded|processing|ready|failed`), source_job_id? (links a doc finalized from a completed job), extracted_content, metadata (jsonb), uploaded_at?, processed_at?, created_at, updated_at, deleted_at (soft delete). Plus a generated `search_vector tsvector` over name+extracted_content with a GIN index. Indexes: `(user_id, created_at)`, `(user_id, status, created_at)`, GIN on `search_vector`, and a **partial unique index on `(user_id, source_job_id)`** that makes finalize idempotent.
- **folders**: id, user_id, parent_id? (self-ref tree), name, created_at, updated_at, deleted_at.
- **tags**: id, user_id, name, color, created_at. Unique `(user_id, name)`.
- **document_tags**: many2many join (document_id, tag_id), managed by GORM.
- **exports**: id, user_id, organization_id?, format (`csv|json`), status (`queued|processing|ready|failed`), file_name, content, document_count, filters, completed_at.
- **job_workspace_hints**: job_id (pk), user_id, organization_id, created_at — set at job creation (editor+), consumed by the finalize subscriber to file the resulting document into an org.

Search: `search_vector @@ websearch_to_tsquery('english', q)` (pluggable behind the same API to Meilisearch/OpenSearch later).

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8089 | Service port |
| DATABASE_URL | — | PostgreSQL connection string (required) |
| NATS_URL | nats://nats:4222 | NATS for the finalize subscriber |
| USER_SERVICE_URL | http://user-service:8090 | Membership/RBAC checks for org-scoped requests |
| NOTIFICATION_SERVICE_URL | http://notification-service:8091 | Raises an `export.ready` notification when an export completes |
| TRUSTED_PROXIES | — | Trusted proxy CIDRs |
| OTEL_EXPORTER_OTLP_ENDPOINT | — | OpenTelemetry collector |

## Authentication
The API gateway verifies the JWT and injects `X-User-ID` (and `X-User-Role`) on proxied requests. `RequireUser()` rejects requests without a valid `X-User-ID`. All queries are filtered by that user id; documents are never visible across users.

## Scaling Constraints
Stateless → horizontally scalable behind the gateway. Reads (list/search) are GIN/B-tree indexed; partition `documents` by `user_id`/month at 10M+ rows and use read replicas. Object bytes never transit the service.

## Gateway / Deployment
- Gateway routes `/api/documents`, `/api/folders`, `/api/tags` → `DOCUMENT_SERVICE_URL` (`http://document-service:8089`).
- `deployment/docker-compose.yml` has a `document-service` block; `api-gateway` depends on it and sets `DOCUMENT_SERVICE_URL`.
- In `go.work` and every service Dockerfile's go.mod copy list (workspace builds).

## NATS
- **Consumes** `jobs.events.>` (`JOBS_EVENTS` stream, durable consumer `document-job-events`) to finalize completed jobs into documents. See Internal Architecture above.
- **Publishes** nothing today. Outbound `document.*` events (for indexing/cross-service notifications) are a follow-up.

## Roadmap (next increments)
- Presigned-upload finalize that creates a document directly; jobs reference `document_id`.
- Publish `document.*` events to NATS for indexing/notifications.
- Shares and activity log; binary/ZIP exports to object storage; an `EXPORTS` NATS work-queue for async export at scale.

## Performance
- `GET /api/documents` runs a `COUNT(*)` (total for pagination) and the page
  `Find` as two **independent** queries. They are dispatched concurrently
  (`handlers/documents.go`) so the handler costs ~one DB round-trip instead of
  two — meaningful because the database is remote. The two builders each call a
  `buildQuery()` closure that produces a fresh, independent `*gorm.DB`, so no
  statement is shared across goroutines; filter query-params are read into locals
  first because `gin.Context`'s lazy form parsing is not concurrency-safe.
