# ESYDocs API Guide (Frontend)

This guide documents the public API exposed by the API gateway. Private/internal endpoints are omitted. Paths below are relative to the gateway root.

## Base URL
- Default gateway: http://localhost:8080
- Health check: GET /healthz -> "ok"

## Auth and identity
- Optional for most endpoints; required for `GET /api/jobs/history`.
- Provide either:
  - `Authorization: Bearer <jwt>` (JWT subject must be the user UUID; validated with `JWT_SECRET`).
  - `X-User-ID: <uuid>` (dev shortcut).
- Guests:
  - On job creation, the server sets a `guest_token` cookie.
  - For listing guest jobs, send `X-Guest-Token: <token>` or include cookies (`credentials: "include"`).

## Gateway routing
- `/api/upload` -> upload-service `/api/uploads`
- `/api/convert-from-pdf` -> upload-service by default (`CONVERT_FROM_PDF_URL` can override)
- `/api/convert-to-pdf` -> upload-service by default (`CONVERT_TO_PDF_URL` can override)
- `/api/jobs` -> upload-service `/api/jobs`

If the gateway targets the convert-from/convert-to services directly, responses use capitalized field names (e.g., `ID`, `ToolType`) and only multipart upload is accepted for job creation.

## Common response shapes
ProcessingJob (upload-service gateway):

```json
{
  "id": "uuid",
  "userId": "uuid|null",
  "toolType": "pdf-to-word",
  "status": "queued|processing|completed|failed",
  "progress": "0-100",
  "fileName": "report.pdf",
  "fileSize": "123.45 KB",
  "failureReason": "string|null",
  "metadata": { "key": "value" },
  "createdAt": "RFC3339",
  "updatedAt": "RFC3339",
  "completedAt": "RFC3339|null",
  "expiresAt": "RFC3339|null"
}
```

UploadStatus:

```json
{
  "uploadId": "uuid",
  "fileName": "report.pdf",
  "fileSize": 123456,
  "totalChunks": 4,
  "receivedChunks": 4,
  "complete": true
}
```

Errors: JSON `{"error": "message"}` with 4xx/5xx.

## Uploads (chunked)

### POST /api/upload/init
Create upload session.

Request:

```json
{
  "fileName": "report.pdf",
  "fileSize": 1048576,
  "totalChunks": 4
}
```

Response 201:

```json
{ "uploadId": "uuid" }
```

### PUT /api/upload/{uploadId}/chunk?index={0-based}
Upload one chunk. Use multipart/form-data with field `chunk`.

Response 200: UploadStatus.

### GET /api/upload/{uploadId}/status
Response 200: UploadStatus.

### POST /api/upload/{uploadId}/complete
Assembles chunks. Response 200:

```json
{ "uploadId": "uuid", "storedPath": "uploads/<id>/file.pdf" }
```

Notes:
- Uploads expire after `UPLOAD_TTL` (default 2h).
- Max size enforced by `MAX_UPLOAD_MB` (default 50 MB) for uploads and job creation.
- A completed upload is consumed when used in a job and cannot be reused.

## Conversion jobs

### Tools (gateway -> upload-service)
Convert from PDF:
- `pdf-to-word` -> .docx
- `pdf-to-excel` -> .xlsx
- `pdf-to-powerpoint` -> .pptx
- `pdf-to-image` -> .zip (jpgs)
- `ocr` -> not implemented in worker (jobs will fail)

Convert to PDF and PDF ops:
- `word-to-pdf` (.doc/.docx)
- `excel-to-pdf` (.xls/.xlsx)
- `powerpoint-to-pdf` (.ppt/.pptx)
- `image-to-pdf` (.png/.jpg/.jpeg/.webp)
- `merge-pdf` (multi input)
- `split-pdf` (requires `options.range`)
- `compress-pdf`
- `page-reorder` (not implemented in worker)
- `page-rotate` (not implemented in worker)
- `watermark-pdf` (currently copies input)
- `protect-pdf` (requires `options.password`)
- `unlock-pdf` (currently copies input)
- `sign-pdf` (currently copies input)
- `edit-pdf` (currently copies input)

Aliases accepted: `ppt-to-pdf`, `pdf-to-ppt`, `pdf-to-img`, `img-to-pdf`.

### Create job using uploaded file(s)
`POST /api/convert-from-pdf/{tool}`
`POST /api/convert-to-pdf/{tool}`

Content-Type: `application/json`

Body:

```json
{
  "uploadId": "uuid",
  "uploadIds": ["uuid", "..."] ,
  "options": { "range": "1-3,5" }
}
```

Notes:
- Use `uploadIds` for multi-file tools (e.g., `merge-pdf`, `image-to-pdf`).
- `options` is tool-specific; `split-pdf` requires `range`, `protect-pdf` requires `password`.

### Create job with direct upload (multipart)
Same endpoints as above.

Form fields:
- `files`: one or more files (repeat the field for multiple files).
- `options`: JSON string (optional).

### List jobs by tool
`GET /api/convert-from-pdf/{tool}`
`GET /api/convert-to-pdf/{tool}`

Returns array of ProcessingJob.
- For guests, list is based on `guest_token` or `X-Guest-Token`.
- For authenticated users, list is per user.

### Get job
`GET /api/convert-from-pdf/{tool}/{id}`
`GET /api/convert-to-pdf/{tool}/{id}`

### Delete job
`DELETE /api/convert-from-pdf/{tool}/{id}`
`DELETE /api/convert-to-pdf/{tool}/{id}`
Response 204.

### Download output
`GET /api/convert-from-pdf/{tool}/{id}/download`
`GET /api/convert-to-pdf/{tool}/{id}/download`

Only available when `status` is `completed`.
Content-Type is set based on tool.

## Frontend flow (typical)
1. Start upload: `POST /api/upload/init`
2. Upload chunks with `PUT /api/upload/{id}/chunk?index=...`
3. Complete upload: `POST /api/upload/{id}/complete`
4. Create job: `POST /api/convert-*/{tool}` with `uploadId`
5. Poll job: `GET /api/convert-*/{tool}/{id}`
6. Download: `GET /api/convert-*/{tool}/{id}/download` when completed

## Example (fetch)
```ts
const res = await fetch(`/api/convert-to-pdf/merge-pdf`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ uploadIds: [id1, id2] })
});
const job = await res.json();
```
