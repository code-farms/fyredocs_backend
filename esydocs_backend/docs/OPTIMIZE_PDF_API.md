# Optimize PDF API

Base URL: `http://localhost:8080`

Optimize PDF files - compress, repair, and add OCR text layers.

---

## Supported Tools

| Tool | Description |
|------|-------------|
| `compress-pdf` | Reduce PDF file size |
| `repair-pdf` | Fix corrupted or damaged PDFs |
| `ocr-pdf` | Add searchable text layer to scanned PDFs |

---

## POST /api/optimize-pdf/{tool}

Create a new optimization job.

**Authentication:** Required

### Request

```
POST /api/optimize-pdf/{tool}
Content-Type: application/json
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name from supported list |

**Body:**

```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "options": {
    "quality": "ebook"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| uploadId | string | Yes | Upload ID from upload API |
| options | object | No | Tool-specific options |

### Response

**201 Created**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "compress-pdf",
  "status": "pending",
  "progress": "0",
  "fileName": "compressed.pdf",
  "fileSize": "2.50 MB",
  "metadata": {
    "inputPaths": ["/uploads/file.pdf"],
    "options": {
      "quality": "ebook"
    },
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
| 400 | INVALID_INPUT | Invalid tool or options |
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Upload not found |

---

## GET /api/optimize-pdf/{tool}

List all jobs for a specific tool.

**Authentication:** Required

### Request

```
GET /api/optimize-pdf/{tool}?limit=25&offset=0
```

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
    "toolType": "compress-pdf",
    "status": "completed",
    "progress": "100",
    "fileName": "compressed.pdf",
    "fileSize": "1.25 MB",
    "createdAt": "2025-01-19T10:30:00Z",
    "updatedAt": "2025-01-19T10:35:00Z",
    "completedAt": "2025-01-19T10:35:00Z"
  }
]
```

---

## GET /api/optimize-pdf/{tool}/{jobId}

Get a specific job by ID.

**Authentication:** Required

### Request

```
GET /api/optimize-pdf/{tool}/{jobId}
```

### Response

**200 OK**

Returns job object with full metadata.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found |

---

## GET /api/optimize-pdf/{tool}/{jobId}/download

Download the optimized PDF.

**Authentication:** Required

### Request

```
GET /api/optimize-pdf/{tool}/{jobId}/download
```

### Response

**200 OK**

```
Content-Type: application/pdf
Content-Disposition: attachment; filename="optimized.pdf"
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Job not completed |
| 404 | NOT_FOUND | Job or file not found |

---

## DELETE /api/optimize-pdf/{tool}/{jobId}

Delete a job and its associated files.

**Authentication:** Required

### Request

```
DELETE /api/optimize-pdf/{tool}/{jobId}
```

### Response

**204 No Content**

---

## PATCH /api/optimize-pdf/{tool}/{jobId}

Update job status (internal use).

**Authentication:** Required (service-to-service)

### Request

```
PATCH /api/optimize-pdf/{tool}/{jobId}
Content-Type: application/json
```

**Body:**
```json
{
  "status": "processing",
  "progress": "50"
}
```

---

## Tool-Specific Information

### compress-pdf

Reduce PDF file size by compressing images and optimizing content.

**Input:** Single PDF file

**Output:** Compressed PDF

**Conversion Engine:** Ghostscript

**Options:**

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| quality | string | "ebook" | Compression quality level |

**Quality Levels:**

| Quality | DPI | Description | Best For |
|---------|-----|-------------|----------|
| `screen` | 72 | Lowest quality, smallest size | Screen viewing only |
| `ebook` | 150 | Good quality, moderate size | E-readers, web viewing |
| `printer` | 300 | High quality, larger size | Printing documents |
| `prepress` | 300+ | Highest quality, largest size | Professional printing |

**Request Examples:**

Maximum compression (smallest file):
```json
{
  "uploadId": "upload-id",
  "options": {
    "quality": "screen"
  }
}
```

Balanced (default):
```json
{
  "uploadId": "upload-id",
  "options": {
    "quality": "ebook"
  }
}
```

High quality for printing:
```json
{
  "uploadId": "upload-id",
  "options": {
    "quality": "printer"
  }
}
```

---

### repair-pdf

Attempt to fix corrupted or damaged PDF files.

**Input:** Single PDF file (potentially corrupted)

**Output:** Repaired PDF

**Conversion Engine:** Ghostscript

**Options:** None

**Request Example:**
```json
{
  "uploadId": "upload-id",
  "options": {}
}
```

**What it fixes:**
- Corrupted cross-reference tables
- Missing or damaged page objects
- Invalid PDF structure
- Encoding issues

**Note:** Not all PDFs can be repaired. Severely damaged files may still fail.

---

### ocr-pdf

Add a searchable text layer to scanned PDF documents.

**Input:** Single PDF file (scanned/image-based)

**Output:** PDF with searchable text layer

**Conversion Engine:** Tesseract OCR

**Options:**

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| language | string | "eng" | OCR language code |
| dpi | string | "300" | Image resolution for OCR |

**Supported Languages:**

| Code | Language |
|------|----------|
| `eng` | English |
| `deu` | German |
| `fra` | French |
| `spa` | Spanish |
| `ita` | Italian |
| `por` | Portuguese |
| `nld` | Dutch |
| `rus` | Russian |
| `chi_sim` | Chinese (Simplified) |
| `chi_tra` | Chinese (Traditional) |
| `jpn` | Japanese |
| `kor` | Korean |
| `ara` | Arabic |
| `hin` | Hindi |

**Request Examples:**

English OCR (default):
```json
{
  "uploadId": "upload-id",
  "options": {
    "language": "eng",
    "dpi": "300"
  }
}
```

German OCR with higher DPI:
```json
{
  "uploadId": "upload-id",
  "options": {
    "language": "deu",
    "dpi": "400"
  }
}
```

**DPI Recommendations:**

| DPI | Use Case |
|-----|----------|
| 150 | Low-quality scans, faster processing |
| 300 | Standard scans (recommended) |
| 400 | High-quality scans, small text |
| 600 | Very high quality, detailed documents |

**Note:** Higher DPI improves accuracy but increases processing time.

---

## Job Object

| Field | Type | Description |
|-------|------|-------------|
| id | string | Unique job identifier |
| userId | string | User ID (null for guest jobs) |
| toolType | string | Tool used |
| status | string | pending/processing/completed/failed |
| progress | string | Progress percentage (0-100) |
| fileName | string | Output file name |
| fileSize | string | Human-readable file size |
| failureReason | string | Error message (if failed) |
| metadata | object | Job metadata including options |
| createdAt | string | ISO 8601 timestamp |
| updatedAt | string | ISO 8601 timestamp |
| completedAt | string | ISO 8601 timestamp |
| expiresAt | string | ISO 8601 timestamp (guest jobs) |

---

## Input Requirements

All tools in this service require a **PDF file** as input.

**Accepted Extension:** `.pdf`

**Max File Size:** 50 MB
