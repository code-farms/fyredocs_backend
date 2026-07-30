# Optimize PDF API

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

Optimize PDF files - compress, repair, add OCR text layers, and flatten annotations/forms.

---

## Supported Tools

| Tool | Description |
|------|-------------|
| `compress-pdf` | Reduce PDF file size |
| `repair-pdf` | Fix corrupted or damaged PDFs |
| `ocr-pdf` | Add searchable text layer to scanned PDFs |
| `flatten-pdf` | Bake annotations and form fields into page content |

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

**Options** (validated by job-service at job creation — violations return 400 `INVALID_OPTIONS`):

| Option | Type | Default | Rule | Description |
|--------|------|---------|------|-------------|
| language | string | `"eng"` | one of `"en"`, `"eng"` (case-insensitive) or omitted | OCR language. Only English is supported today — the worker image ships only the English tesseract pack. The allowlist grows together with the image when more language packs are added. |
| dpi | number or string | `150` (server env `OCR_DEFAULT_DPI`) | whole number, 72–600 | Rasterization resolution for OCR. Both `300` and `"300"` are accepted. The worker may additionally cap the effective DPI on very large pages to bound image size. |

**Supported Languages:**

| Code | Language |
|------|----------|
| `en` / `eng` | English |

More languages will be accepted once their tesseract packs are added to the optimize-pdf image; requests for any other code are currently rejected with 400 `INVALID_OPTIONS`.

**Request Example:**

```json
{
  "uploadId": "upload-id",
  "options": {
    "language": "eng",
    "dpi": "300"
  }
}
```

**DPI Recommendations:**

| DPI | Use Case |
|-----|----------|
| 150 | Low-quality scans, faster processing (server default) |
| 300 | Standard scans (recommended) |
| 400 | High-quality scans, small text |
| 600 | Very high quality, detailed documents (maximum accepted) |

**Note:** Higher DPI improves accuracy but increases processing time. Values outside 72–600 are rejected.

**Completed-job metadata:**

The completed job's `metadata` object carries the OCR results:

| Key | Type | Description |
|-----|------|-------------|
| `language` | string | Tesseract language actually used |
| `dpi` | number | Effective rasterization DPI |
| `pagesOCRed` | number | Number of pages processed |
| `extractedText` | string (optional) | Plain text recognized by OCR, pages concatenated in output-PDF page order and separated by form-feed (`\f`, U+000C). Pages where OCR failed (the original image is embedded instead) contribute an empty slot, so form-feed positions always align with PDF page breaks. Capped at 512 KB; omitted when no text was recognized. |
| `textTruncated` | boolean (optional) | Present (`true`) only when `extractedText` was cut at the 512 KB cap. |

```json
{
  "status": "completed",
  "metadata": {
    "language": "eng",
    "dpi": 300,
    "pagesOCRed": 3,
    "extractedText": "Page one text...\fPage two text...\fPage three text..."
  }
}
```

**Note:** `extractedText` and `textTruncated` are returned only by the single-job `GET /api/optimize-pdf/ocr-pdf/{id}`; job list and history endpoints omit them to keep list payloads small (see [Jobs API](jobs-api.md)).

---

### flatten-pdf

Bake annotations and form-field appearance streams into the page content so the PDF looks the same everywhere and is no longer editable as a form.

**Input:** Single PDF file

**Output:** Flattened PDF

**Conversion Engine:** Ghostscript (`pdfwrite` re-distillation with `-dPreserveAnnots=false -dPrinted`)

**Options:** None

**Request Example:**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

**What it does:**
- Merges annotation appearance streams into the page content
- Renders filled form-field values as static page content
- Keeps text vector and searchable (no rasterization)

**Note:** Form fields without appearance streams (rare `NeedAppearances` documents) render nothing after flattening. A Ghostscript error fails the job with the standard failure flow.

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
| metadata | object | Job metadata including options; for `ocr-pdf` also `language`, `dpi`, `pagesOCRed`, and (single-job GET only) `extractedText` / `textTruncated` |
| createdAt | string | ISO 8601 timestamp |
| updatedAt | string | ISO 8601 timestamp |
| completedAt | string | ISO 8601 timestamp |
| expiresAt | string | ISO 8601 timestamp (guest jobs) |

---

## Input Requirements

All tools in this service require a **PDF file** as input.

**Accepted Extension:** `.pdf`

**Max File Size:** 50 MB
