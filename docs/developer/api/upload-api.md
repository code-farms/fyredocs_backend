# Upload API

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

The Upload API brokers **presigned S3 multipart uploads**. File bytes never pass through the application: the browser `PUT`s each part **directly to object storage** (MinIO) using presigned URLs, then tells job-service to finalize. The old chunk-streaming protocol (chunks assembled on disk) is retired.

> **Path note:** the edge exposes these endpoints under `/api/upload/*` and job-service serves them at `/api/uploads/*` — either path works through the edge.

> **Object bytes vs API:** the presigned `PUT`/`GET` part URLs point at the edge's `/uploads/*` route, which streams straight to MinIO (bypassing the api-gateway and all auth middleware — the SigV4 signature is the credential). Only the small JSON control calls below hit job-service.

**Rate limit:** `RATE_LIMIT_UPLOAD` (default 30) requests per `RATE_LIMIT_WINDOW` (default 60s) per IP.

---

## Upload Flow

1. **Initialize** (`POST /api/upload/init`) → get `uploadId`, `key`, `partSize`, `totalParts`, and a presigned PUT URL per part.
2. **Upload parts** → `PUT` each part's bytes directly to its presigned URL; keep the `ETag` response header of each.
3. **(Optional) Re-presign** (`GET /api/upload/{uploadId}/parts`) → fresh URLs to resume an interrupted upload or after the URLs expire.
4. **Complete** (`POST /api/upload/{uploadId}/complete`) → submit the collected `{partNumber, etag}` list; the object is assembled server-side and its true size re-verified.
5. **Create a job** → `POST /api/{group}/:tool` with the `uploadId`; the object is consumed in place.

Abort at any time with `DELETE /api/upload/{uploadId}`.

---

## POST /api/upload/init

Create a multipart upload session and issue presigned part URLs.

**Authentication:** Optional (guest mode allowed)

### Request

```
POST /api/upload/init
Content-Type: application/json
```

**Body:**
```json
{
  "fileName": "document.pdf",
  "fileSize": 5242880,
  "contentType": "application/pdf"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| fileName | string | Yes | Original file name (sanitized to its base name server-side) |
| fileSize | integer | Yes | Total file size in bytes — validated against the plan limit **before** any URL is issued (re-verified on complete) |
| contentType | string | No | MIME type (defaults to `application/octet-stream`) |

### Response

**201 Created**
```json
{
  "success": true,
  "data": {
    "uploadId": "550e8400-e29b-41d4-a716-446655440000",
    "key": "uploads/550e8400-.../document.pdf",
    "partSize": 8388608,
    "totalParts": 1,
    "urlExpiresAt": "2026-07-03T12:00:00Z",
    "parts": [
      { "partNumber": 1, "url": "http://localhost/uploads/uploads/550e8400-.../document.pdf?uploadId=...&partNumber=1&X-Amz-Signature=..." }
    ]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| uploadId | string (UUID) | Upload session identifier |
| key | string | Object key the file will live at in the uploads bucket |
| partSize | integer | Size of every part except possibly the last, in bytes (`UPLOAD_PART_SIZE_MB`, default 8 MiB) |
| totalParts | integer | Number of parts the client must upload (`ceil(fileSize/partSize)`, capped at 1000) |
| urlExpiresAt | string (date-time) | When the presigned part URLs expire — re-presign via `GET …/parts` after this |
| parts | array | One `{partNumber, url}` per part; `url` is a presigned PUT to object storage |

### Notes

- Upload session state (Redis `upload:<uploadId>`) expires after `UPLOAD_TTL` (default **30m**); job-service's in-process cleanup loop reaps stale sessions at 2× that age.
- The declared `fileSize` is checked against the caller's plan limit (`X-User-Plan-Max-File-MB`) before URLs are issued.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing required fields, or the file would need more than 1000 parts |
| 413 | FILE_TOO_LARGE | Declared file size exceeds the plan limit |
| 429 | RATE_LIMITED | Upload rate limit exceeded |

---

## Uploading parts (directly to object storage)

For each entry in `parts`, `PUT` that slice of the file to its presigned `url`:

```
PUT <parts[i].url>
Content-Length: <byte length of this part>

<raw part bytes>
```

- Parts are 1-based and `partSize` bytes each, except the last which is the remainder.
- Capture the **`ETag`** response header of every part — it is required to complete.
- These requests go to the edge's `/uploads/*` route (→ MinIO), not to job-service. No auth header is needed; the presigned signature is the credential.

---

## GET /api/upload/{uploadId}/parts

Re-presign part URLs to resume an interrupted upload or refresh expired ones.

**Authentication:** Optional (guest mode allowed)

### Request

```
GET /api/upload/{uploadId}/parts?partNumbers=2,3
```

| Parameter | In | Required | Description |
|-----------|----|----------|-------------|
| uploadId | path | Yes | Upload session ID |
| partNumbers | query | No | Comma-separated 1-based part numbers (e.g. `2,3`); all parts when omitted |

### Response

**200 OK** — same `data` shape as init (`uploadId`, `partSize`, `urlExpiresAt`, `parts[]`).

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Malformed `partNumbers` |
| 404 | NOT_FOUND | Upload session expired or not found |

---

## GET /api/upload/{uploadId}/status

Return the session metadata of an in-progress upload.

**Authentication:** Optional (guest mode allowed)

### Response

**200 OK**
```json
{
  "success": true,
  "data": {
    "uploadId": "550e8400-e29b-41d4-a716-446655440000",
    "fileName": "document.pdf",
    "declaredSize": 5242880,
    "totalParts": 1
  }
}
```

`declaredSize` is the size declared at init; the true size is verified on complete.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Upload session not found or expired |

---

## POST /api/upload/{uploadId}/complete

Finalize the multipart upload from the client-collected part ETags.

**Authentication:** Optional (guest mode allowed)

### Request

```
POST /api/upload/{uploadId}/complete
Content-Type: application/json
```

**Body:**
```json
{
  "parts": [
    { "partNumber": 1, "etag": "\"d41d8cd98f00b204e9800998ecf8427e\"" }
  ]
}
```

The `parts` count must equal `totalParts` from init; each entry carries the `ETag` returned when that part was uploaded.

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Upload complete",
  "data": {
    "uploadId": "550e8400-e29b-41d4-a716-446655440000",
    "fileName": "document.pdf",
    "size": 5242880,
    "complete": true
  }
}
```

`size` is the **true** object size (from `StatObject`), re-verified against the plan limit.

### Behavior

- Assembles the S3 multipart upload from the submitted parts into the object at `uploads/<uploadId>/<fileName>` in the uploads bucket — no on-disk assembly, no temporary chunk directory.
- Re-verifies the true object size against the plan limit; an oversized object is deleted and `413` returned.
- The Redis upload session is **kept** so the next `POST /api/{group}/:tool` can reference this `uploadId`; it is released after the job is committed and queued (or reaped by the cleanup loop after `UPLOAD_TTL`).

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | UPLOAD_INCOMPLETE | Part count mismatch, or object storage rejected the part list |
| 413 | FILE_TOO_LARGE | True object size exceeds the plan limit (object is deleted) |
| 404 | NOT_FOUND | Upload session not found or expired |

---

## DELETE /api/upload/{uploadId}

Abort an in-progress upload.

**Authentication:** Optional (guest mode allowed)

Cancels the S3 multipart upload (freeing stored parts) and removes the session. **Idempotent** — aborting an unknown or expired session still returns `204`.

**204 No Content** on success.

---

## PUT /api/upload/{uploadId}/chunk — retired

One-release migration stub for the old chunk-streaming protocol. Always returns **`410 Gone`** with code `UPLOAD_PROTOCOL_CHANGED` (`"Please refresh the page to continue uploading."`) so stale frontend bundles reload into the presigned flow. Scheduled for removal next release.

---

## File Size & Part Limits

| Constraint | Value |
|------------|-------|
| Plan file-size limit | `X-User-Plan-Max-File-MB` (enforced on declared size at init and true size at complete) |
| Part size | `UPLOAD_PART_SIZE_MB` (default **8 MiB**), clamped to the S3 5 MiB minimum |
| Max parts | 1000 (`totalParts = ceil(fileSize / partSize)`) |
| Upload session TTL | `UPLOAD_TTL` (default **30m**) |
| API gateway body size | JSON control calls capped at 1 MiB; part bytes go straight to MinIO via the edge (no app body limit) |
