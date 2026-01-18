# Jobs API

Base URL: `http://localhost:8080`

Manage and track all processing jobs across services.

---

## GET /api/jobs

Get all jobs for the authenticated user.

**Authentication:** Required

### Request

```
GET /api/jobs?limit=25&page=1
```

**Query Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| limit | integer | 25 | 1-100 | Results per page |
| page | integer | 1 | 1-100000 | Page number |

### Response

**200 OK**
```json
[
  {
    "id": "job-550e8400-e29b-41d4-a716-446655440000",
    "userId": "user-550e8400-e29b-41d4-a716-446655440000",
    "toolType": "pdf-to-word",
    "status": "completed",
    "progress": "100",
    "fileName": "document.docx",
    "fileSize": "512.50 KB",
    "createdAt": "2025-01-19T10:30:00Z",
    "updatedAt": "2025-01-19T10:35:00Z",
    "completedAt": "2025-01-19T10:35:00Z"
  },
  {
    "id": "job-660f9500-f30c-52e5-b827-557766551111",
    "userId": "user-550e8400-e29b-41d4-a716-446655440000",
    "toolType": "compress-pdf",
    "status": "processing",
    "progress": "45",
    "fileName": "compressed.pdf",
    "fileSize": "2.10 MB",
    "createdAt": "2025-01-19T11:00:00Z",
    "updatedAt": "2025-01-19T11:01:00Z",
    "completedAt": null
  }
]
```

### Notes

- Jobs are returned in reverse chronological order (newest first)
- Only returns jobs belonging to the authenticated user

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |

---

## GET /api/jobs/{jobId}

Get a specific job by ID.

**Authentication:** Required

### Request

```
GET /api/jobs/{jobId}
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| jobId | string | Yes | Job ID |

### Response

**200 OK**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "pdf-to-word",
  "status": "completed",
  "progress": "100",
  "fileName": "document.docx",
  "fileSize": "512.50 KB",
  "metadata": {
    "inputPaths": ["/uploads/file.pdf"],
    "outputPath": "/outputs/document.docx",
    "options": {}
  },
  "createdAt": "2025-01-19T10:30:00Z",
  "updatedAt": "2025-01-19T10:35:00Z",
  "completedAt": "2025-01-19T10:35:00Z",
  "expiresAt": null
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Job not found |

---

## DELETE /api/jobs/{jobId}

Delete a job and its associated files.

**Authentication:** Required

### Request

```
DELETE /api/jobs/{jobId}
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| jobId | string | Yes | Job ID |

### Response

**204 No Content**

### Behavior

- Deletes job record from database
- Removes all associated input files
- Removes all associated output files
- Removes guest token association (if applicable)

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Job not found |

---

## Job Status Lifecycle

```
pending → processing → completed
                    ↘ failed
```

| Status | Description |
|--------|-------------|
| `pending` | Job created and queued for processing |
| `processing` | Job is actively being processed |
| `completed` | Job finished successfully, output ready for download |
| `failed` | Job failed (check `failureReason` for details) |

---

## Job Object Schema

| Field | Type | Nullable | Description |
|-------|------|----------|-------------|
| id | string | No | Unique job identifier (UUID) |
| userId | string | Yes | User ID (null for guest jobs) |
| toolType | string | No | Tool used for processing |
| status | string | No | Current job status |
| progress | string | No | Progress percentage (0-100) |
| fileName | string | No | Output file name |
| fileSize | string | No | Human-readable file size |
| failureReason | string | Yes | Error message (only if failed) |
| metadata | object | No | Job metadata |
| createdAt | string | No | ISO 8601 creation timestamp |
| updatedAt | string | No | ISO 8601 last update timestamp |
| completedAt | string | Yes | ISO 8601 completion timestamp |
| expiresAt | string | Yes | ISO 8601 expiration timestamp |

### Metadata Object

| Field | Type | Description |
|-------|------|-------------|
| inputPaths | array | Paths to input files |
| outputPath | string | Path to output file (when completed) |
| options | object | Tool-specific options used |
| correlationId | string | Request correlation ID |

---

## Tool Types

Jobs can have the following `toolType` values:

### Convert To PDF
- `word-to-pdf`
- `excel-to-pdf`
- `powerpoint-to-pdf`
- `image-to-pdf`
- `html-to-pdf`

### Convert From PDF
- `pdf-to-word`
- `pdf-to-excel`
- `pdf-to-ppt`
- `pdf-to-image`
- `pdf-to-html`
- `pdf-to-text`
- `pdf-to-pdfa`

### Organize PDF
- `merge-pdf`
- `split-pdf`
- `remove-pages`
- `extract-pages`
- `organize-pdf`
- `scan-to-pdf`

### Optimize PDF
- `compress-pdf`
- `repair-pdf`
- `ocr-pdf`

---

## Guest Jobs

Jobs created by unauthenticated (guest) users have special behavior:

| Property | Value |
|----------|-------|
| userId | `null` |
| expiresAt | 2 hours from creation |
| Identification | Via `X-Guest-Token` header/cookie |

### Guest Token

- Generated automatically on first job creation
- Must be included in subsequent requests
- Jobs are automatically deleted after expiration

**Header:**
```
X-Guest-Token: guest-token-value
```

**Or Cookie:**
```
Cookie: guest_token=guest-token-value
```

---

## Polling Strategy

For tracking job progress, poll the job status endpoint:

**Recommended Intervals:**

| Job Duration | Poll Interval |
|--------------|---------------|
| < 30 seconds | 2 seconds |
| 30s - 2 min | 5 seconds |
| > 2 minutes | 10 seconds |

**Example Flow:**

1. Create job → Get `jobId`
2. Poll `GET /api/jobs/{jobId}` every N seconds
3. Check `status` field:
   - `pending` or `processing` → Continue polling
   - `completed` → Download result
   - `failed` → Handle error (check `failureReason`)

---

## File Cleanup

### Automatic Cleanup

- Guest job files are deleted after `expiresAt`
- Cleanup worker runs periodically to remove expired files

### Manual Cleanup

- Use `DELETE /api/jobs/{jobId}` to immediately remove job and files
- Recommended after downloading results to free storage
