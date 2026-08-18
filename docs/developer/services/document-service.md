# Document Service

## Service Responsibility
Owns the **persistent document library**: documents (metadata for files stored in object storage), folders, tags, and document↔tag associations. This is the document-centric layer on top of the job-centric processing pipeline — a document is a managed, searchable, organizable entity, distinct from an ephemeral processing job.

The service stores **metadata only**; file bytes live in object storage (referenced by `storage_path`). It is read/write scoped to the authenticated user.

Since Phase 9 (cloud backup & sync) the service also owns the **document-side lifecycle of backup bytes**: it serves presigned downloads for stored documents, enforces the `backups/{userId}/` ownership contract on registration, deletes the backup object when a document is purged, and exposes delta-sync listing (`updatedSince` + `sort=updated`) for sync clients. The upload PROTOCOL (presigned multipart, `POST /api/backup`) stays in job-service.

## Design Constraints
- Independent microservice: own Postgres schema, own module (`document-service`), no cross-service DB access or shared models (per repo `CLAUDE.md`).
- Stateless: identity comes from the `AuthContext` resolved by the auth middleware, which **verifies the JWT locally** (`AUTH_TRUST_GATEWAY_HEADERS=false`). No session state.
- Standard response envelope (`shared/response`): `{success, message, data, error, meta}`.

## Internal Architecture
- `main.go` — config/logger/telemetry init, DB connect + migrate, gin with metrics/trace/request-id middleware, `/metrics`, graceful shutdown. Port `8089` (env `PORT`). Also initializes the **object-storage client** (Phase 9) — see below.
- `routes/routes.go` — health + the `/api` group guarded by `RequireUser()`.
- `handlers/` — `auth.go` (identity + RBAC helpers), `documents.go`, `folders.go`, `tags.go`, `exports.go`, `hints.go` (workspace hints), `health.go`, `storage.go` (the `ObjectStore` interface + boot-time injection).

### Object storage client (Phase 9)

`main.go` calls `shared/storage.NewFromEnv()` at boot and injects the client via `handlers.SetObjectStore`. Two deliberate design points:

- **Utility package, not shared business logic.** `shared/storage` is a utility package by charter (it "carries NO business logic"), so importing it here does not breach the no-shared-business-logic rule — the same way every worker imports it. The upload protocol (multipart presign, MIME sniff, the `/api/backup` copy) remains job-service's alone; document-service only presigns GETs and removes backup objects.
- **NON-fatal when unconfigured.** Unlike job-service (which fails fast without S3), a missing/invalid S3 config here logs a warning and boots anyway: document CRUD must never depend on storage availability. With a nil store, `GET /api/documents/:id/download` degrades to a 500 `SERVER_ERROR` ("Downloads are unavailable") and a purge deletes the row but leaks the object (logged with the key for manual reclaim).

`handlers/storage.go` defines the narrow `ObjectStore` interface this service actually uses — `BucketOutputs()`, `PresignGet(...)`, `RemoveObject(...)` — so tests inject a fake and the handler surface cannot quietly grow storage dependencies.
- `internal/models/` — `database.go` (Connect/Migrate), `document.go`, `folder.go`, `tag.go`, `export.go`, `jobhint.go`.
- `subscriber/subscriber.go` — **NATS is a hard dependency** (`main.go` fails fast if the connection or subscriber start fails). A durable JetStream consumer `document-job-events` on the `JOBS_EVENTS` stream (filter `jobs.events.>`, `DeliverNew`, explicit ack) finalizes every `JobCompleted` event for an authenticated user into a `documents` row — the server-side counterpart to direct creation, so any completed job becomes a library document regardless of client. Finalize is idempotent via a partial unique index on `(user_id, source_job_id)` (soft-deleted docs are not resurrected), and files the document into an org workspace when a `JobWorkspaceHint` exists.

## Routes
Health: `GET /healthz` (liveness), `GET /readyz` (DB ping).

All `/api/*` routes require a verified caller and are scoped to that user. Reached through the gateway at the same paths (`/api/documents`, `/api/folders`, `/api/tags`).

**Organization scoping (RBAC):** document endpoints accept an optional `orgId` (query param, or `organizationId` in the create body). When present, document-service verifies the caller's membership and role with **user-service** (`GET /api/orgs/:id`) and scopes documents by `organization_id`: reads (list/get) require `viewer`+, writes (create/update/delete/restore/purge) require `editor`+. When absent, documents are personal (scoped by `user_id`, `organization_id IS NULL`) — the default and the shape the finalize subscriber creates. Folders/tags remain personal-scoped in this increment.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/documents` | List documents. Filters: `status`, `folderId`, `tagId`, full-text `q`, `trashed=true` (Trash view), `updatedSince` (RFC3339 — delta sync); `sort=updated` orders by `updated_at DESC`; paging `page`/`limit` (max 100). Returns `meta` with page/limit/total. |
| POST | `/api/documents` | Create a document (metadata). Body: `name` (required), `folderId?`, `fileType?`, `mimeType?`, `fileSize?`, `storagePath?`, `status?`, `metadata?`. `storagePath` under `backups/` must sit under the caller's own `backups/{userId}/` prefix (400 otherwise). |
| GET | `/api/documents/:id` | Get one owned document (with tags). |
| GET | `/api/documents/:id/download` | 302 to a 5-minute presigned GET for the document's stored bytes (attachment disposition). 404 when the row has no `storage_path`; 500 `SERVER_ERROR` when storage is unconfigured. |
| PATCH | `/api/documents/:id` | Update `name`/`folderId`/`status`/`metadata`. Empty `folderId` moves to root. |
| DELETE | `/api/documents/:id` | Soft-delete (move to Trash). |
| POST | `/api/documents/:id/restore` | Restore a soft-deleted document from Trash. |
| DELETE | `/api/documents/:id/permanent` | Permanently delete (hard delete + tag associations). Also deletes the storage object when `storage_path` starts with `backups/` — nothing else sweeps those objects. |
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

**Export limits (bounded generation).** Async export generation is capped so a burst of large exports can't exhaust memory or DB:
- A **semaphore** caps concurrent export generation at `EXPORT_MAX_CONCURRENCY` (default `4`).
- An export waits up to `EXPORT_QUEUE_WAIT` (default `30s`) for a slot; if none frees up it fails with a **retryable** message (the client can re-queue).
- The document query backing an export is capped at `EXPORT_MAX_ROWS` (default `50000`) rows.

### Cloud backup & sync (Phase 9)

**storagePath ownership contract (`POST /api/documents`).** `storagePathAllowed` (`handlers/documents.go`) reserves the `backups/` key prefix for job-service's backup endpoint, whose keys embed the OWNER's user id (`backups/{userId}/{uuid}/{fileName}`). A caller may only register keys under their own `backups/{callerUserId}/` prefix — anything else under `backups/` is 400 `INVALID_INPUT` ("storagePath does not belong to this account"). A `backups/` path containing a bare `..` segment is also rejected: a dot-segment-normalizing hop (edge proxy, presign URL handling) could re-resolve `backups/{me}/../{other}/f` to another user's key, and legitimate backup keys are server-minted so a `..` segment never appears in one. Every other storagePath shape (job outputs, empty) passes through unvalidated, exactly as before, for web-app compatibility.

**Download (`GET /api/documents/:id/download`).** Ownership + org scoped like every document read (viewer+ when org-scoped). Rows without a `storage_path` — job-era documents whose bytes live behind the job download route and may have expired — answer 404 and the client falls back to the job route. Otherwise the handler 302s to a **5-minute presigned GET** on the outputs bucket with `response-content-disposition: attachment; filename="<name>.<fileType>"` (and `response-content-type` when `mime_type` is set) — the same presign window job downloads use. A nil object store answers 500 `SERVER_ERROR` ("Downloads are unavailable").

**Purge deletes backup bytes (`DELETE /api/documents/:id/permanent`).** The row is fetched first (proving ownership), then its `document_tags` associations are deleted, then the row, then — when `storage_path` starts with `backups/` — the object is removed from the outputs bucket. Associations must precede the row: `fk_document_tags_document` rejects deleting a document that still has tag rows, which once made every tagged document permanently unpurgeable (fixed 2026-08). The row is the source of truth and goes before the object; object removal is **best-effort** (a failure or nil store logs the leaked key via `documents.purge.remove_object` / `documents.purge.storage_unavailable` rather than failing the request). This purge is the ONLY deletion path for `backups/` objects — job-service's cleanup sweeps deliberately never touch them. `DELETE /api/tags/:id` follows the same shape for the same FK reason: scoped fetch (ownership), associations, then the tag row.

**Delta sync (`GET /api/documents`).** Two additions for sync clients:
- `updatedSince=<RFC3339>` filters to rows with `updated_at > t`. Invalid values are **rejected with 400** rather than ignored — a sync client that silently full-listed would re-download everything.
- `sort=updated` orders by `updated_at DESC` (default remains `created_at DESC`), so clients page in modification order.

Both are backed by the `(user_id, updated_at DESC)` index from migration `00003_updated_at_index.sql` — without it every sync poll would be a per-user sequential scan.

### Schema migrations

The schema is applied by **versioned SQL migrations** embedded in the binary
(`internal/migrations/*.sql`, applied by goose at startup under a per-service
Postgres advisory lock and recorded in `goose_db_version_document_service`). GORM
`AutoMigrate` is no longer the authority — it remains only behind
`DB_AUTOMIGRATE=true` for local development. A migration failure aborts startup.
See [schema-migrations.md](../architecture/schema-migrations.md).

## DB Schema (own Postgres)
- **documents**: id (uuid v7), user_id, organization_id?, folder_id?, name, file_type, mime_type, file_size, storage_path, thumbnail_path, status (`uploaded|processing|ready|failed`), source_job_id? (links a doc finalized from a completed job), extracted_content, metadata (jsonb), uploaded_at?, processed_at?, created_at, updated_at, deleted_at (soft delete). Plus a generated `search_vector tsvector` over name+extracted_content with a GIN index. Indexes: `(user_id, created_at)`, `(user_id, status, created_at)`, GIN on `search_vector`, a **partial unique index on `(user_id, source_job_id)`** that makes finalize idempotent, and `idx_documents_user_updated` on `(user_id, updated_at DESC)` (migration `00003_updated_at_index.sql`, Phase 9) backing the `updatedSince` filter and `sort=updated` ordering.
- **folders**: id, user_id, parent_id? (self-ref tree), name, created_at, updated_at, deleted_at.
- **tags**: id, user_id, name, color, created_at. Unique `(user_id, name)`.
- **document_tags**: many2many join (document_id, tag_id), managed by GORM.
- **exports**: id, user_id, organization_id?, format (`csv|json`), status (`queued|processing|ready|failed`), file_name, content, document_count, filters, completed_at.
- **job_workspace_hints**: job_id (pk), user_id, organization_id, created_at — set at job creation (editor+), consumed by the finalize subscriber to file the resulting document into an org.

Search: `search_vector @@ websearch_to_tsquery('english', q)` (pluggable behind the same API to Meilisearch/OpenSearch later).

**Boot-critical index.** The idempotency UNIQUE index `idx_doc_user_source_job` on `documents` (partial: `WHERE source_job_id IS NOT NULL`) is what makes the finalize subscriber safe against duplicate job-completion events. If it cannot be created at startup the service **fails to start** (`os.Exit`) rather than logging a warning and continuing — running without it would silently allow duplicate documents per job. The other, purely-performance indexes remain best-effort (a warning, boot continues).

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8089 | Service port |
| DATABASE_URL | — | PostgreSQL connection string (required) |
| NATS_URL | nats://nats:4222 | NATS for the finalize subscriber |
| USER_SERVICE_URL | http://user-service:8090 | Membership/RBAC checks for org-scoped requests |
| NOTIFICATION_SERVICE_URL | http://notification-service:8091 | Raises an `export.ready` notification when an export completes |
| EXPORT_MAX_CONCURRENCY | 4 | Max concurrent async export generations (semaphore) |
| EXPORT_QUEUE_WAIT | 30s | How long an export waits for a semaphore slot before failing with a retryable message |
| EXPORT_MAX_ROWS | 50000 | Max document rows an export query returns |
| REDIS_ADDR | redis:6379 | Backs the JWT denylist. Without it a token revoked at logout keeps working here until natural expiry. |
| REDIS_PASSWORD | — | Redis auth |
| JWT_HS256_SECRET | — | Token signing secret (required — this service verifies tokens itself) |
| JWT_ALLOWED_ALGS | HS256 | Permitted signing algorithms |
| JWT_ISSUER / JWT_AUDIENCE | fyredocs / fyredocs-api | Claim checks |
| JWT_CLOCK_SKEW | 60s | Allowed clock drift |
| AUTH_DENYLIST_ENABLED | true | Check revoked tokens against Redis |
| AUTH_TRUST_GATEWAY_HEADERS | false | Believe upstream `X-User-*` instead of verifying. **Keep false** — see Authentication. |
| INTERNAL_API_TOKEN | — | Shared secret sent to user-service's `/internal/v1` membership lookup (fails closed when unset) |
| S3_ENDPOINT | minio:9000 | Object storage for document downloads and backup-object deletion on purge (Phase 9). **Non-fatal when unset**: CRUD keeps working, downloads answer 500 `SERVER_ERROR`, purges leak the object (logged) |
| S3_PUBLIC_ENDPOINT | falls back to `S3_ENDPOINT` | Origin presigned download URLs are signed against (the Caddy edge / public origin) |
| S3_ACCESS_KEY / S3_SECRET_KEY | — | Same scoped app credentials job-service uses (created by minio-init) |
| S3_USE_SSL | false | TLS to the internal endpoint |
| S3_BUCKET_UPLOADS / S3_BUCKET_OUTPUTS | uploads / outputs | Bucket names; only outputs is read here (backup keys and job outputs live there) |
| TRUSTED_PROXIES | — | Trusted proxy CIDRs |
| OTEL_EXPORTER_OTLP_ENDPOINT | — | OpenTelemetry collector |

## Authentication

`main.go`'s `buildAuthMiddleware` resolves each caller's `AuthContext`. With
`AUTH_TRUST_GATEWAY_HEADERS=false` (the default) it **verifies the JWT locally**
rather than believing the gateway's `X-User-ID` header; the gateway forwards
`Authorization` untouched, so the token is available to verify. `RequireUser()`
rejects requests with no resolved identity. All queries are filtered by that user
id; documents are never visible across users.

**Why local verification matters:** trusting the gateway's `X-User-*` headers is
only sound while nothing but the gateway can reach this port. Once app servers span
hosts that stops being true, and a forged header would be account takeover by user
ID. The middleware resolves identity but does **not** gate, so credential-less
requests pass through as anonymous — which is what keeps `/healthz`, `/readyz`, and
shared-secret `/internal` routes working. See
[horizontal-scaling.md](../architecture/horizontal-scaling.md#prerequisite-1-identity-must-not-depend-on-network-isolation).

### Outbound: org membership

`internal/orgclient` calls user-service `GET /internal/v1/orgs/:id` to resolve the
caller's org role. That hop carries the user's ID plus the `INTERNAL_API_TOKEN`
shared secret — **not** a JWT, which document-service does not hold. It targets the
`/internal/v1` mirror rather than the public `/api/orgs/:id` precisely because
user-service now verifies tokens and would 401 a header-only call. An unset secret
makes user-service fail closed (503), which the circuit breaker surfaces as a
dependency failure so authorization fails **safe**.

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
