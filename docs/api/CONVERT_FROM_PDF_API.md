# Convert From PDF API

Base URL: `http://localhost:8080`

Convert PDF files to various document formats.

---

## Supported Tools

| Tool | Output Format | Description |
|------|---------------|-------------|
| `pdf-to-word` | .docx | Convert PDF to Word document |
| `pdf-to-excel` | .xlsx | Convert PDF to Excel spreadsheet |
| `pdf-to-ppt` | .pptx | Convert PDF to PowerPoint presentation |
| `pdf-to-image` | .zip (PNG images) | Convert PDF pages to PNG images |
| `pdf-to-html` | .zip (HTML + images) | Convert PDF to HTML with images |
| `pdf-to-text` | .txt | Extract text from PDF |
| `pdf-to-pdfa` | .pdf (PDF/A-2b) | Convert PDF to archival format |

---

## POST /api/convert-from-pdf/{tool}

Create a new conversion job.

**Authentication:** Required

### Request

```
POST /api/convert-from-pdf/{tool}
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

**Option 2: Direct File Upload**

```
POST /api/convert-from-pdf/{tool}
Content-Type: multipart/form-data

file: <binary PDF>
options: {}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| uploadId | string | Yes* | Upload ID from upload API |
| file | file | Yes* | Direct PDF file upload |
| options | object | No | Tool-specific options |

*One of `uploadId` or `file` is required

### Response

**201 Created**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "pdf-to-word",
  "status": "pending",
  "progress": "0",
  "fileName": "document.docx",
  "fileSize": "512.50 KB",
  "metadata": {
    "inputPaths": ["/uploads/file.pdf"],
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
| 400 | INVALID_INPUT | Invalid tool or file is not a PDF |
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Upload not found |

---

## GET /api/convert-from-pdf/{tool}

List all jobs for a specific tool.

**Authentication:** Required

### Request

```
GET /api/convert-from-pdf/{tool}?limit=25&offset=0
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
    "toolType": "pdf-to-word",
    "status": "completed",
    "progress": "100",
    "fileName": "document.docx",
    "fileSize": "512.50 KB",
    "createdAt": "2025-01-19T10:30:00Z",
    "updatedAt": "2025-01-19T10:35:00Z",
    "completedAt": "2025-01-19T10:35:00Z",
    "expiresAt": null
  }
]
```

---

## GET /api/convert-from-pdf/{tool}/{jobId}

Get a specific job by ID.

**Authentication:** Required

### Request

```
GET /api/convert-from-pdf/{tool}/{jobId}
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
| 404 | NOT_FOUND | Job not found |

---

## GET /api/convert-from-pdf/{tool}/{jobId}/download

Download the converted file.

**Authentication:** Required

### Request

```
GET /api/convert-from-pdf/{tool}/{jobId}/download
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |
| jobId | string | Yes | Job ID |

### Response

**200 OK**

Binary file with appropriate headers based on tool:

| Tool | Content-Type | File Extension |
|------|--------------|----------------|
| pdf-to-word | application/vnd.openxmlformats-officedocument.wordprocessingml.document | .docx |
| pdf-to-excel | application/vnd.openxmlformats-officedocument.spreadsheetml.sheet | .xlsx |
| pdf-to-ppt | application/vnd.openxmlformats-officedocument.presentationml.presentation | .pptx |
| pdf-to-image | application/zip | .zip |
| pdf-to-html | application/zip | .zip |
| pdf-to-text | text/plain | .txt |
| pdf-to-pdfa | application/pdf | .pdf |

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Job not completed |
| 404 | NOT_FOUND | Job or file not found |

---

## DELETE /api/convert-from-pdf/{tool}/{jobId}

Delete a job and its associated files.

**Authentication:** Required

### Request

```
DELETE /api/convert-from-pdf/{tool}/{jobId}
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name |
| jobId | string | Yes | Job ID |

### Response

**204 No Content**

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found |

---

## PATCH /api/convert-from-pdf/{tool}/{jobId}

Update job status (internal use by conversion services).

**Authentication:** Required (service-to-service)

### Request

```
PATCH /api/convert-from-pdf/{tool}/{jobId}
Content-Type: application/json
```

**Body:**
```json
{
  "status": "processing",
  "progress": "50"
}
```

### Response

**200 OK**

Returns updated job object.

---

## Tool-Specific Information

### pdf-to-word

Converts PDF to Microsoft Word format.

**Output:** `.docx`

**Conversion Engine:** LibreOffice

---

### pdf-to-excel

Converts PDF tables to Microsoft Excel format.

**Output:** `.xlsx`

**Conversion Engine:** LibreOffice

**Note:** Works best with PDFs containing tabular data.

---

### pdf-to-ppt

Converts PDF to Microsoft PowerPoint format.

**Output:** `.pptx`

**Conversion Engine:** LibreOffice

---

### pdf-to-image

Converts each PDF page to a PNG image.

**Output:** `.zip` containing PNG images

**Conversion Engine:** pdftoppm (Poppler)

**Output Structure:**
```
output.zip
├── page_1.png
├── page_2.png
├── page_3.png
└── ...
```

---

### pdf-to-html

Converts PDF to HTML with embedded images.

**Output:** `.zip` containing HTML and images

**Conversion Engine:** pdftohtml (Poppler)

**Output Structure:**
```
output.zip
├── index.html
├── page1.png
├── page2.png
└── ...
```

---

### pdf-to-text

Extracts text content from PDF.

**Output:** `.txt`

**Conversion Engine:** pdftotext (Poppler)

**Note:** Text layout may not be preserved exactly.

---

### pdf-to-pdfa

Converts PDF to PDF/A-2b archival format.

**Output:** `.pdf` (PDF/A-2b compliant)

**Conversion Engine:** Ghostscript

**Use Case:** Long-term document archival and compliance.

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

---

## Input Requirements

All tools in this service require a **PDF file** as input.

**Accepted Extension:** `.pdf`

**Max File Size:** 50 MB
