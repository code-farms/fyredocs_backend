# Jobs API

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

The job-service exposes job creation, listing, retrieval, deletion, downloads, history, and a Server-Sent Events stream. Jobs are scoped under tool-group prefixes (`/api/<group>/:tool/...`) — there is no generic `/api/jobs/:jobId` resource. The history and SSE endpoints are the only routes under the bare `/api/jobs` namespace.

Authentication options:
- Authenticated user — `access_token` cookie or `Authorization: Bearer <jwt>`
- Guest — `X-Guest-Token` header or `guest_token` cookie (the gateway issues this on first contact)

Tool groups:
- `/api/convert-from-pdf/:tool`
- `/api/convert-to-pdf/:tool`
- `/api/organize-pdf/:tool`
- `/api/optimize-pdf/:tool`

---

## POST /api/&lt;group&gt;/:tool

Create a job using one of two request shapes:

1. **Presigned-upload pre-completed flow** (`Content-Type: application/json`) — references a previously completed `uploadId`/`uploadIds`. Recommended for large files: the presigned multipart protocol PUTs bytes directly to object storage and supports resume (re-presign parts). See [Upload API](./upload-api.md).
2. **Direct multipart upload** (`Content-Type: multipart/form-data`) — for small files or simple single-shot scripts.

### Common headers

| Header | Optional | Description |
|--------|----------|-------------|
| `Idempotency-Key` | Yes | If you replay a POST with the same key within 10 minutes, the original job is returned. |
| `X-Guest-Token` | Yes | Guest sessions; mutually exclusive with auth cookie. |

### JSON body shape

```json
{
  "uploadId": "0c2b...UUID",
  "uploadIds": ["uuid1", "uuid2"],
  "options": { "anyToolSpecific": "object" }
}
```

`uploadIds` (plural) is preferred. `uploadId` (singular) is accepted for backward compat. If you POST again with the same `uploadIds` (e.g. a network retry), you get the original `jobId` back transparently.

### Multipart body shape

```
files: <binary> (one or more, key="files")
options: <stringified JSON>
```

### Response — 201 Created

```json
{
  "success": true,
  "message": "Your file is being processed!",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "userId": null,
    "toolType": "pdf-to-word",
    "status": "queued",
    "progress": 0,
    "fileName": "document.docx",
    "fileSize": 2456789,
    "createdAt": "2026-05-09T10:30:00Z",
    "updatedAt": "2026-05-09T10:30:00Z",
    "completedAt": null,
    "expiresAt": "2026-05-10T10:30:00Z",
    "guestToken": "<set on first guest job>"
  }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing fields, unknown tool, MIME mismatch |
| 400 | TOO_MANY_FILES | More files than the user's plan permits |
| 401 | UNAUTHORIZED | Auth required for non-guest tools |
| 413 | FILE_TOO_LARGE | File exceeds the plan's max-file-size |
| 500 | SERVER_ERROR | Object storage / DB / NATS publish failure |

---

## GET /api/&lt;group&gt;/:tool

List jobs created by the current user (or guest) for a specific tool, paginated.

| Query param | Default | Range | Description |
|-------------|---------|-------|-------------|
| `limit` | 25 | 1–100 | Results per page |
| `page` | 1 | 1–100000 | Page number |

### Response — 200 OK

```json
{
  "success": true,
  "message": "Jobs loaded successfully",
  "data": [ /* Job objects, newest first */ ],
  "meta": { "page": 1, "limit": 25, "total": 0 }
}
```

Guest callers see only jobs associated with their `guest_token`. Authenticated users see jobs owned by their `userId` for the given tool.

---

## GET /api/&lt;group&gt;/:tool/:id

Fetch a single job by ID. Auth must match (job owner or guest token holder).

### Response — 200 OK

Same envelope as `POST` above, with `data` containing one Job object.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found, expired, or accessor unauthorized |

---

## DELETE /api/&lt;group&gt;/:tool/:id

Delete the job and all associated files. Auth must match.

### Response — 200 / 204

Returns 200 with the standard envelope (or 204 No Content depending on response helper). Side effects: removes the input + output objects from the `uploads`/`outputs` buckets and deletes the `processing_jobs` row.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found, expired, or accessor unauthorized |

---

## GET /api/&lt;group&gt;/:tool/:id/download

Returns a **302 redirect** to a short-lived (5-minute) presigned GET URL; the browser fetches the bytes straight from object storage via the Caddy edge (`/outputs/*`), not through this service. The `Content-Disposition` filename is derived from the original input and the MIME type is set per the tool (e.g. `application/pdf` for `*-to-pdf`, `application/zip` for split / multi-output jobs) via the presigned response headers.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not completed, missing output, or accessor unauthorized |

---

## GET /api/jobs/history

Paginated history across **all** tools for the **authenticated user**. Guest callers cannot use this endpoint.

| Query param | Default | Range | Description |
|-------------|---------|-------|-------------|
| `limit` | 25 | 1–100 | Results per page |
| `page` | 1 | 1–100000 | Page number |

### Response — 200 OK

```json
{
  "success": true,
  "message": "Jobs loaded successfully",
  "data": [ /* Job objects, newest first, across all tools */ ],
  "meta": { "page": 1, "limit": 25, "total": 0 }
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## GET /api/jobs/:id/events  (Server-Sent Events)

Open an SSE stream of real-time updates for one job. Internally the handler creates an **ephemeral** NATS JetStream consumer with `FilterSubject="jobs.events.<jobId>.>"`, `DeliverPolicy=DeliverNewPolicy`, and `InactiveThreshold=1m`, so the server does the filtering and the consumer self-cleans.

### Connection

```http
GET /api/jobs/<jobId>/events
Accept: text/event-stream
Cookie: access_token=<JWT>   # or guest_token
```

The connection auto-closes after 5 minutes (server-side timeout) or when the job reaches a terminal status.

### Event format

Initial connect:
```
event: connected
data: {"jobId":"<uuid>"}
```

Per progress / completion / failure update:
```
event: job-update
data: {"jobId":"<uuid>","status":"progress","progress":42,"toolType":"pdf-to-word","fileSize":1048576}
```

Keepalive comments are sent every 15 seconds:
```
: keepalive
```

If the underlying NATS stream is unavailable:
```
event: error
data: {"message":"event stream unavailable"}
```

---

## Job Status Lifecycle

```
queued → processing → completed
                   ↘ failed
```

| Status | Description |
|--------|-------------|
| `queued` | Job created, JobMessage published to NATS, awaiting worker pickup |
| `processing` | Worker has acked the message and is running |
| `completed` | Worker finished successfully; `outputPath` populated |
| `failed` | Worker reported failure; `failureReason` populated |

Workers also emit `jobs.events.<jobId>.progress` events while running. These are visible only via the SSE endpoint, not as a separate HTTP status.

---

## Job Object Schema

| Field | Type | Nullable | Description |
|-------|------|----------|-------------|
| id | string (UUID) | No | Unique job identifier |
| userId | string (UUID) | Yes | User ID (null for guest jobs) |
| toolType | string | No | Tool used for processing |
| status | string | No | `queued` / `processing` / `completed` / `failed` |
| progress | integer | No | 0–100 |
| fileName | string | No | Original or canonical output filename |
| fileSize | integer | No | Size in bytes (input total, or output for completed jobs) |
| failureReason | string | Yes | Error message (only if `failed`) |
| metadata | object | No | `{options, correlationId, ...}` |
| createdAt | string (RFC3339) | No | Creation timestamp |
| updatedAt | string (RFC3339) | No | Last update timestamp |
| completedAt | string (RFC3339) | Yes | Set when `completed` |
| expiresAt | string (RFC3339) | Yes | TTL set per plan (guest 30m / free 7d / pro 30d) — always finite |

---

## Tool Types (per group)

### `/api/convert-from-pdf/:tool`
`pdf-to-image` (alias `pdf-to-img`), `pdf-to-pdfa`, `pdf-to-word` (alias `pdf-to-docx`), `pdf-to-excel` (alias `pdf-to-xlsx`), `pdf-to-ppt` (aliases `pdf-to-powerpoint`, `pdf-to-pptx`), `pdf-to-html`, `pdf-to-text` (alias `pdf-to-txt`), `pdf-to-odt`, `pdf-to-ods`, `pdf-to-odp`

### `/api/convert-to-pdf/:tool`
`word-to-pdf`, `ppt-to-pdf` (alias `powerpoint-to-pdf`), `excel-to-pdf`, `html-to-pdf`, `image-to-pdf` (alias `img-to-pdf`)

### `/api/organize-pdf/:tool`
`merge-pdf`, `split-pdf`, `remove-pages`, `extract-pages`, `organize-pdf`, `scan-to-pdf`, `rotate-pdf`, `watermark-pdf`, `protect-pdf`, `unlock-pdf`, `sign-pdf`, `edit-pdf`, `add-page-numbers`

### `/api/optimize-pdf/:tool`
`compress-pdf`, `repair-pdf`, `ocr-pdf`

---

## Guest Jobs

Jobs created by unauthenticated (guest) users:

| Property | Value |
|----------|-------|
| `userId` | `null` |
| `expiresAt` | TTL set by job-service per `GUEST_JOB_TTL` |
| Identification | `X-Guest-Token` header **or** `guest_token` cookie (issued by the gateway) |

The gateway issues a `guest_token` cookie automatically on first contact when the request has no auth. Subsequent calls scope to that token via `guest:{token}:jobs` Redis sets.

---

## Polling vs SSE

Prefer **SSE** (`/api/jobs/:id/events`) for tracking progress — it is push-based and automatically cleans up. Polling individual jobs via `GET /api/<group>/:tool/:id` works as a fallback but is more expensive and lossy for `progress` events.

---

## File Cleanup

- Expired jobs are reaped by job-service's in-process [cleanup loop](../services/job-service.md#background-cleanup-loop) every `CLEANUP_INTERVAL` (compose sets 5m; code fallback 15m).
- All jobs have a finite `expires_at` (pro = `PRO_JOB_TTL`, default 30d); pro jobs are reaped by the cleanup loop like any other once expired.
- `DELETE /api/<group>/:tool/:id` immediately removes the job and its files.
