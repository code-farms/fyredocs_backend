# Convert To PDF API

Base URL: `http://localhost:8080`

Convert various document formats to PDF.

---

## Supported Tools

| Tool | Input Formats | Description |
|------|---------------|-------------|
| `word-to-pdf` | .doc, .docx | Convert Word documents to PDF |
| `excel-to-pdf` | .xls, .xlsx | Convert Excel spreadsheets to PDF |
| `powerpoint-to-pdf` | .ppt, .pptx | Convert PowerPoint presentations to PDF |
| `image-to-pdf` | .png, .jpg, .jpeg, .webp | Convert images to PDF (one page per image) |
| `html-to-pdf` | .html, .htm | Convert HTML files to PDF |

---

## POST /api/convert-to-pdf/{tool}

Create a new conversion job.

**Authentication:** Required

### Request

```
POST /api/convert-to-pdf/{tool}
Content-Type: application/json
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name from supported list |

**Option 1: Using Pre-uploaded File**

```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "options": {}
}
```

**Option 2: Using Multiple Pre-uploaded Files (for image-to-pdf)**

```json
{
  "uploadIds": [
    "upload-id-1",
    "upload-id-2",
    "upload-id-3"
  ],
  "options": {}
}
```

**Option 3: Direct File Upload**

```
POST /api/convert-to-pdf/{tool}
Content-Type: multipart/form-data

file: <binary>
options: {}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| uploadId | string | Yes* | Upload ID from upload API |
| uploadIds | array | Yes* | Array of upload IDs (for multi-file tools) |
| file | file | Yes* | Direct file upload |
| options | object | No | Tool-specific options |

*One of `uploadId`, `uploadIds`, or `file` is required

### Response

**201 Created**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "word-to-pdf",
  "status": "pending",
  "progress": "0",
  "fileName": "document.pdf",
  "fileSize": "512.50 KB",
  "metadata": {
    "inputPaths": ["/uploads/file.docx"],
    "options": {},
    "correlationId": "correlation-550e8400"
  },
  "createdAt": "2025-01-19T10:30:00Z",
  "updatedAt": "2025-01-19T10:30:00Z",
  "expiresAt": "2025-01-19T12:30:00Z"
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Invalid tool or unsupported file type |
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Upload not found |

---

## GET /api/convert-to-pdf/{tool}

List all jobs for a specific tool.

**Authentication:** Required

### Request

```
GET /api/convert-to-pdf/{tool}?limit=25&offset=0
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| limit | integer | 25 | Results per page (1-100) |
| offset | integer | 0 | Number of results to skip |

### Response

**200 OK**
```json
[
  {
    "id": "job-id-1",
    "userId": "user-id",
    "toolType": "word-to-pdf",
    "status": "completed",
    "progress": "100",
    "fileName": "document.pdf",
    "fileSize": "512.50 KB",
    "createdAt": "2025-01-19T10:30:00Z",
    "updatedAt": "2025-01-19T10:35:00Z",
    "completedAt": "2025-01-19T10:35:00Z",
    "expiresAt": null
  }
]
```

---

## GET /api/convert-to-pdf/{tool}/{jobId}

Get a specific job by ID.

**Authentication:** Required

### Request

```
GET /api/convert-to-pdf/{tool}/{jobId}
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |
| jobId | string | Yes | Job ID |

### Response

**200 OK**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "word-to-pdf",
  "status": "completed",
  "progress": "100",
  "fileName": "document.pdf",
  "fileSize": "512.50 KB",
  "metadata": {
    "inputPaths": ["/uploads/file.docx"],
    "outputPath": "/outputs/document.pdf",
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
| 404 | NOT_FOUND | Job not found |

---

## GET /api/convert-to-pdf/{tool}/{jobId}/download

Download the converted PDF file.

**Authentication:** Required

### Request

```
GET /api/convert-to-pdf/{tool}/{jobId}/download
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |
| jobId | string | Yes | Job ID |

### Response

**200 OK**

Binary file with headers:
```
Content-Type: application/pdf
Content-Disposition: attachment; filename="document.pdf"
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Job not completed |
| 404 | NOT_FOUND | Job or file not found |

---

## DELETE /api/convert-to-pdf/{tool}/{jobId}

Delete a job and its associated files.

**Authentication:** Required

### Request

```
DELETE /api/convert-to-pdf/{tool}/{jobId}
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |
| jobId | string | Yes | Job ID |

### Response

**204 No Content**

### Behavior

- Deletes job record from database
- Removes input and output files
- Removes guest token association (if applicable)

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found |

---

## PATCH /api/convert-to-pdf/{tool}/{jobId}

Update job status (internal use by conversion services).

**Authentication:** Required (service-to-service)

### Request

```
PATCH /api/convert-to-pdf/{tool}/{jobId}
Content-Type: application/json
```

**Body:**
```json
{
  "status": "processing",
  "progress": "50"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| status | string | No | Job status |
| progress | string | No | Progress percentage (0-100) |

### Response

**200 OK**

Returns updated job object.

---

## Tool-Specific Information

### word-to-pdf

Converts Microsoft Word documents to PDF.

**Supported Extensions:** `.doc`, `.docx`

**Conversion Engine:** LibreOffice

---

### excel-to-pdf

Converts Microsoft Excel spreadsheets to PDF.

**Supported Extensions:** `.xls`, `.xlsx`

**Conversion Engine:** LibreOffice

---

### powerpoint-to-pdf

Converts Microsoft PowerPoint presentations to PDF.

**Supported Extensions:** `.ppt`, `.pptx`

**Conversion Engine:** LibreOffice

---

### image-to-pdf

Converts one or more images to a single PDF (one image per page).

**Supported Extensions:** `.png`, `.jpg`, `.jpeg`, `.webp`

**Conversion Engine:** ImageMagick

**Note:** Use `uploadIds` array for multiple images.

---

### html-to-pdf

Converts HTML files to PDF.

**Supported Extensions:** `.html`, `.htm`

**Conversion Engine:** LibreOffice

---

## Job Object

| Field | Type | Description |
|-------|------|-------------|
| id | string | Unique job identifier |
| userId | string | User ID (null for guest jobs) |
| toolType | string | Tool used for conversion |
| status | string | Job status (pending/processing/completed/failed) |
| progress | string | Progress percentage (0-100) |
| fileName | string | Output file name |
| fileSize | string | Human-readable file size |
| failureReason | string | Error message (if failed) |
| metadata | object | Job metadata |
| createdAt | string | ISO 8601 timestamp |
| updatedAt | string | ISO 8601 timestamp |
| completedAt | string | ISO 8601 timestamp (when completed) |
| expiresAt | string | ISO 8601 timestamp (for guest jobs) |
