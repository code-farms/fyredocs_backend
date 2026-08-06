# Documents API

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

The gateway forwards `/api/documents/*`, `/api/folders/*`, `/api/tags/*`, and `/api/exports/*` to `document-service:8089`. All requests must go through the gateway, which verifies the JWT and injects `X-User-ID`; every resource is scoped to that user. All responses use the standard envelope `{success, message, data, error, meta}`.

**Organization scoping (RBAC):** document endpoints accept an optional `orgId` query param (or `organizationId` in a create/update body). When present, document-service verifies membership/role with user-service (`GET /api/orgs/:id`): reads require `viewer`+, writes require `editor`+. When absent, the resource is personal (`user_id` scoped, `organization_id IS NULL`).

---

## Documents

### GET /api/documents
List documents for the caller.

| Query param | Description |
|-------------|-------------|
| `status` | Filter by status (`uploaded`\|`processing`\|`ready`\|`failed`) |
| `folderId` | Filter by folder |
| `tagId` | Filter by attached tag |
| `q` | Full-text search over name + extracted content (`websearch_to_tsquery`) |
| `trashed` | `true` returns the Trash view (soft-deleted only) |
| `updatedSince` | RFC3339 timestamp — only rows with `updatedAt` strictly after it (delta sync). Invalid values are **400 INVALID_INPUT**, not ignored |
| `sort` | `updated` orders by `updatedAt` descending (default: `createdAt` descending) — page in modification order for sync |
| `orgId` | Org-scope the listing (viewer+) |
| `page`, `limit` | Paging; `limit` max 100 |

Delta-sync clients combine both: `GET /api/documents?updatedSince=2026-08-01T00:00:00Z&sort=updated`.

**200 OK** — returns `data: [document]` and `meta: {page, limit, total}`.

```json
{
  "success": true,
  "message": "Documents retrieved",
  "data": [
    {
      "id": "018f...",
      "userId": "550e...",
      "organizationId": null,
      "folderId": null,
      "name": "invoice.pdf",
      "fileType": "pdf",
      "mimeType": "application/pdf",
      "fileSize": 20481,
      "status": "ready",
      "metadata": {"jobId": "018e...", "toolType": "compress-pdf"},
      "processedAt": "2026-07-02T10:00:00Z",
      "createdAt": "2026-07-02T10:00:00Z",
      "updatedAt": "2026-07-02T10:00:00Z",
      "tags": []
    }
  ],
  "meta": {"page": 1, "limit": 20, "total": 1}
}
```

### POST /api/documents
Create a document (metadata only; bytes live in object storage).

**Body:**
```json
{
  "name": "invoice.pdf",
  "folderId": "018f...",
  "organizationId": "018f...",
  "fileType": "pdf",
  "mimeType": "application/pdf",
  "fileSize": 20481,
  "storagePath": "outputs/....pdf",
  "status": "ready",
  "metadata": {}
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| name | string | Yes | Document name |
| folderId | string(uuid) | No | Target folder; omit for root |
| organizationId | string(uuid) | No | Org-scope the doc (editor+) |
| fileType, mimeType, storagePath, status, metadata | — | No | Metadata fields |

**storagePath rules:** keys under the `backups/` prefix must sit under the caller's **own** `backups/{userId}/` prefix — this is the `storageKey` returned by job-service's [`POST /api/backup`](./jobs-api.md#document-backups), whose keys embed the owner's user id. A `backups/` key belonging to another account is **400 INVALID_INPUT** ("storagePath does not belong to this account"). All other shapes (job output keys, empty) pass through unvalidated as before.

**201 Created** — `data: {document}`.

### GET /api/documents/:id
Get one owned document (with tags). `orgId` optional (viewer+). **200 OK** / **404 NOT_FOUND**.

### GET /api/documents/:id/download
Download the document's stored bytes. `orgId` optional (viewer+). Responds **302** with `Location` set to a **5-minute presigned GET** on object storage that forces `Content-Disposition: attachment; filename="<name>.<fileType>"` (and the stored `mimeType` when set) — the browser fetches the bytes straight from storage via the Caddy edge, not through the service.

Only documents with a `storagePath` have bytes here (documents registered from a backup, or finalized from a job with an output key). Job-era rows without one answer 404 — fall back to the job download route (`GET /api/<group>/:tool/:id/download`).

| Status | Code | Description |
|--------|------|-------------|
| 302 | — | `Location`: presigned download URL (5-minute expiry, attachment disposition) |
| 400 | INVALID_ID | `id` is not a UUID |
| 404 | NOT_FOUND | Document not found, not owned, or has no stored file (`storagePath` empty) |
| 500 | SERVER_ERROR | Downloads unavailable (object storage unconfigured) or presign failure |

### PATCH /api/documents/:id
Update `name`, `folderId` (empty string moves to root), `status`, `metadata`. `orgId` optional (editor+). At least one field required (`400 NO_CHANGES` otherwise).

### DELETE /api/documents/:id
Soft-delete (move to Trash). editor+ if org-scoped. **200 OK** `data: {id}`.

### POST /api/documents/:id/restore
Restore a soft-deleted document. **200 OK** / **404 NOT_FOUND** (not in Trash).

### DELETE /api/documents/:id/permanent
Hard-delete the document and its tag associations. **200 OK**.

When the document's `storagePath` starts with `backups/`, the purge **also deletes the storage object** — this is the only deletion path for backup bytes (job-service's cleanup sweeps deliberately never touch `backups/`). The row is deleted first; object removal is best-effort (a storage failure is logged, never a request failure).

### POST /api/documents/:id/tags
Attach a tag. Body: `{ "tagId": "018f..." }`. **200 OK** / **404 TAG_NOT_FOUND**.

### DELETE /api/documents/:id/tags/:tagId
Detach a tag. **200 OK**.

### POST /api/documents/workspace-hint
Record that a processing job should finalize into an organization workspace. Consumed and cleared by the finalize subscriber when the job completes.

**Body:**
```json
{ "jobId": "018f...", "organizationId": "018f..." }
```
- `organizationId` non-empty → verified editor+ then UPSERTed. **200 OK** "Workspace hint set".
- `organizationId` empty → clears any hint (personal). **200 OK** "Workspace hint cleared".
- `400 INVALID_JOB` if `jobId` is not a UUID.

---

## Folders

| Method | Path | Body / Query | Description |
|--------|------|--------------|-------------|
| GET | `/api/folders` | `?parentId=` | List folders (filter by parent) |
| POST | `/api/folders` | `{name, parentId?}` | Create folder |
| PATCH | `/api/folders/:id` | `{name?, parentId?}` | Rename / move |
| DELETE | `/api/folders/:id` | — | Soft-delete; contained documents move to root |

## Tags

| Method | Path | Body | Description |
|--------|------|------|-------------|
| GET | `/api/tags` | — | List the caller's tags |
| POST | `/api/tags` | `{name, color?}` | Create tag (idempotent on `(user, name)`) |
| DELETE | `/api/tags/:id` | — | Delete tag + its associations |

## Exports

Exports are generated asynchronously (v1: in-process). The CSV/JSON artifact is stored on the `exports` row.

| Method | Path | Body / Query | Description |
|--------|------|--------------|-------------|
| GET | `/api/exports` | — | List the caller's exports (status/metadata, no bytes) |
| POST | `/api/exports` | `{format: "csv"\|"json", organizationId?, status?, folderId?, tagId?}` | Queue an export of documents in scope; returns the queued export |
| GET | `/api/exports/:id` | — | Poll export status (`queued`\|`processing`\|`ready`\|`failed`) |
| GET | `/api/exports/:id/download` | — | Download a ready export artifact |

---

## Error Codes
`INVALID_INPUT`, `INVALID_ID`, `INVALID_NAME`, `INVALID_FOLDER`, `INVALID_TAG`, `INVALID_ORG`, `INVALID_JOB`, `NO_CHANGES`, `NOT_FOUND`, `TAG_NOT_FOUND`, `AUTH_FORBIDDEN`, and the `*_FAILED` internal errors (`SERVER_ERROR`, `SERVER_ERROR`, `SERVER_ERROR`, `DELETE_FAILED`, `RESTORE_FAILED`, `PURGE_FAILED`, `ATTACH_FAILED`, `HINT_FAILED`).
