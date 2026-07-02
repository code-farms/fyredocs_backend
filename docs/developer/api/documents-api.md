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
| `orgId` | Org-scope the listing (viewer+) |
| `page`, `limit` | Paging; `limit` max 100 |

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

**201 Created** — `data: {document}`.

### GET /api/documents/:id
Get one owned document (with tags). `orgId` optional (viewer+). **200 OK** / **404 NOT_FOUND**.

### PATCH /api/documents/:id
Update `name`, `folderId` (empty string moves to root), `status`, `metadata`. `orgId` optional (editor+). At least one field required (`400 NO_CHANGES` otherwise).

### DELETE /api/documents/:id
Soft-delete (move to Trash). editor+ if org-scoped. **200 OK** `data: {id}`.

### POST /api/documents/:id/restore
Restore a soft-deleted document. **200 OK** / **404 NOT_FOUND** (not in Trash).

### DELETE /api/documents/:id/permanent
Hard-delete the document and its tag associations. **200 OK**.

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
`INVALID_BODY`, `INVALID_ID`, `INVALID_NAME`, `INVALID_FOLDER`, `INVALID_TAG`, `INVALID_ORG`, `INVALID_JOB`, `NO_CHANGES`, `NOT_FOUND`, `TAG_NOT_FOUND`, `FORBIDDEN`, and the `*_FAILED` internal errors (`LIST_FAILED`, `CREATE_FAILED`, `UPDATE_FAILED`, `DELETE_FAILED`, `RESTORE_FAILED`, `PURGE_FAILED`, `ATTACH_FAILED`, `HINT_FAILED`).
