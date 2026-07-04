# Organize PDF API

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

Manipulate and organize PDF files - merge, split, reorder pages, and more.

---

## Supported Tools

13 tools — see `organize-pdf/main.go:59` `AllowedTools` for the canonical list.

| Tool | Description |
|------|-------------|
| `merge-pdf` | Combine multiple PDFs into one |
| `split-pdf` | Split PDF into separate pages or ranges |
| `rotate-pdf` | Rotate pages by 90°, 180°, or 270° |
| `remove-pages` | Remove specific pages from PDF |
| `extract-pages` | Extract specific pages to new PDF |
| `organize-pdf` | Reorder pages in PDF |
| `scan-to-pdf` | Convert images to PDF with optional OCR |
| `watermark-pdf` | Add a text or image watermark to every page |
| `protect-pdf` | Encrypt with a password |
| `unlock-pdf` | Remove password from an encrypted PDF |
| `sign-pdf` | Stamp a signature image on a chosen page |
| `edit-pdf` | Add free-form text annotations |
| `add-page-numbers` | Add page-number stamps |

---

## POST /api/organize-pdf/{tool}

Create a new organization job.

**Authentication:** Required

### Request

```
POST /api/organize-pdf/{tool}
Content-Type: application/json
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| tool | string | Yes | Tool name from supported list |

**Single File (most tools):**

```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "options": {
    "pages": "1,3,5-7"
  }
}
```

**Multiple Files (merge-pdf, scan-to-pdf):**

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

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| uploadId | string | Yes* | Upload ID for single file |
| uploadIds | array | Yes* | Array of upload IDs for multi-file tools |
| options | object | No | Tool-specific options |

*Use `uploadId` for single-file tools, `uploadIds` for multi-file tools

### Response

**201 Created**
```json
{
  "id": "job-550e8400-e29b-41d4-a716-446655440000",
  "userId": "user-550e8400-e29b-41d4-a716-446655440000",
  "toolType": "merge-pdf",
  "status": "pending",
  "progress": "0",
  "fileName": "merged.pdf",
  "fileSize": "1.25 MB",
  "metadata": {
    "inputPaths": ["/uploads/file1.pdf", "/uploads/file2.pdf"],
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
| 400 | INVALID_INPUT | Invalid tool, options, or file type |
| 400 | INVALID_OPTIONS | Options payload fails the per-tool schema (scan-to-pdf) |
| 401 | UNAUTHORIZED | Not authenticated |
| 404 | NOT_FOUND | Upload not found |

---

## POST /api/organize-pdf/detect-edges

Detect the document quad in an uploaded photo (mobile scanner assist). Peeks at the upload **without consuming it** — the same `uploadId` remains valid for a subsequent scan-to-pdf job. job-service relays the request synchronously to the organize-pdf service's internal detection endpoint.

**Authentication:** Required (same as other tool routes)

**Rate limit:** per-IP, `RATE_LIMIT_DETECT` (default 60 per window)

### Request

```
POST /api/organize-pdf/detect-edges
Content-Type: application/json
```

```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| uploadId | string | Yes | ID of a **completed** presigned upload. Image only (JPEG/PNG/WebP) — PDF uploads are rejected |

### Response

**200 OK**
```json
{
  "success": true,
  "message": "Edges detected",
  "data": {
    "corners": {
      "tl": {"x": 0.04, "y": 0.06},
      "tr": {"x": 0.97, "y": 0.05},
      "br": {"x": 0.96, "y": 0.95},
      "bl": {"x": 0.05, "y": 0.94}
    },
    "confidence": 0.83,
    "width": 3024,
    "height": 4032
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| corners | object | Detected document quad — `tl`/`tr`/`br`/`bl`, each `{x, y}` normalized `0..1` against the decoded pixel grid |
| confidence | number | Detection confidence `0..1`. When no document is found, full-image corners with `confidence: 0` are returned (still 200) |
| width / height | integer | Decoded image dimensions in pixels |

The `corners` value can be passed straight into the scan-to-pdf `options.pages[i].corners` field.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing or unknown uploadId, or the upload is a PDF |
| 422 | UNDECODABLE_IMAGE / IMAGE_TOO_LARGE | Upstream rejection relayed from organize-pdf (not a decodable image / object over 30 MiB or 50 MP) |
| 429 | RATE_LIMIT_EXCEEDED | Too many detect requests from this IP |
| 502 | DETECTION_UNAVAILABLE | organize-pdf down, 5xx, or 15s timeout |

---

## GET /api/organize-pdf/{tool}

List all jobs for a specific tool.

**Authentication:** Required

### Request

```
GET /api/organize-pdf/{tool}?limit=25&offset=0
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
    "toolType": "merge-pdf",
    "status": "completed",
    "progress": "100",
    "fileName": "merged.pdf",
    "fileSize": "1.25 MB",
    "createdAt": "2025-01-19T10:30:00Z",
    "updatedAt": "2025-01-19T10:35:00Z",
    "completedAt": "2025-01-19T10:35:00Z"
  }
]
```

---

## GET /api/organize-pdf/{tool}/{jobId}

Get a specific job by ID.

**Authentication:** Required

### Request

```
GET /api/organize-pdf/{tool}/{jobId}
```

### Response

**200 OK**

Returns job object with full metadata.

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Job not found |

---

## GET /api/organize-pdf/{tool}/{jobId}/download

Download the result file.

**Authentication:** Required

### Request

```
GET /api/organize-pdf/{tool}/{jobId}/download
```

### Response

**200 OK**

| Tool | Content-Type | Output |
|------|--------------|--------|
| merge-pdf | application/pdf | Single PDF |
| split-pdf | application/zip | ZIP with individual PDFs |
| rotate-pdf | application/pdf | Single PDF with rotated pages |
| remove-pages | application/pdf | Single PDF |
| extract-pages | application/pdf | Single PDF |
| organize-pdf | application/pdf | Single PDF |
| scan-to-pdf | application/pdf | Single PDF |
| watermark-pdf | application/pdf | Single PDF |
| protect-pdf | application/pdf | Single password-protected PDF |
| unlock-pdf | application/pdf | Single unprotected PDF |
| sign-pdf | application/pdf | Single PDF with signature stamp |
| edit-pdf | application/pdf | Single PDF with text annotations |
| add-page-numbers | application/pdf | Single PDF with page numbers |

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Job not completed |
| 404 | NOT_FOUND | Job or file not found |

---

## DELETE /api/organize-pdf/{tool}/{jobId}

Delete a job and its associated files.

**Authentication:** Required

### Request

```
DELETE /api/organize-pdf/{tool}/{jobId}
```

### Response

**204 No Content**

---

## Tool-Specific Information

### merge-pdf

Combine multiple PDF files into a single document.

**Input:** Multiple PDF files (use `uploadIds`)

**Output:** Single merged PDF

**Conversion Engine:** pdfcpu

**Request Example:**
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

**Note:** Files are merged in the order provided in the `uploadIds` array.

---

### split-pdf

Split a PDF into individual pages or page ranges.

**Input:** Single PDF file

**Output:** ZIP file containing split PDFs

**Conversion Engine:** pdfcpu

**Options:**

| Option | Type | Description |
|--------|------|-------------|
| range | string | Page range to split (e.g., "1-5,10") or "all" |

**Request Examples:**

Split all pages:
```json
{
  "uploadId": "upload-id",
  "options": {
    "range": "all"
  }
}
```

Split specific ranges:
```json
{
  "uploadId": "upload-id",
  "options": {
    "range": "1-3,5,7-10"
  }
}
```

**Output Structure:**
```
output.zip
├── page_1.pdf
├── page_2.pdf
├── page_3.pdf
└── ...
```

---

### rotate-pdf

Rotate pages in a PDF by a specified angle.

**Input:** Single PDF file

**Output:** PDF with rotated pages

**Conversion Engine:** pdfcpu

**Options:**

| Option | Type | Required | Values | Description |
|--------|------|----------|--------|-------------|
| rotation | integer | Yes | 90, 180, 270 | Rotation angle in degrees |
| applyToPages | string | No | all, odd, even | Which pages to rotate (default: all) |

**Request Examples:**

Rotate all pages 90° clockwise:
```json
{
  "uploadId": "upload-id",
  "options": {
    "rotation": 90,
    "applyToPages": "all"
  }
}
```

Rotate only odd pages 180°:
```json
{
  "uploadId": "upload-id",
  "options": {
    "rotation": 180,
    "applyToPages": "odd"
  }
}
```

---

### remove-pages

Remove specific pages from a PDF.

**Input:** Single PDF file

**Output:** PDF with specified pages removed

**Conversion Engine:** pdfcpu

**Options:**

| Option | Type | Description |
|--------|------|-------------|
| pages | string | Comma-separated page numbers or ranges |

**Request Example:**
```json
{
  "uploadId": "upload-id",
  "options": {
    "pages": "2,4,6-8"
  }
}
```

This removes pages 2, 4, 6, 7, and 8 from the PDF.

---

### extract-pages

Extract specific pages into a new PDF.

**Input:** Single PDF file

**Output:** New PDF containing only extracted pages

**Conversion Engine:** pdfcpu

**Options:**

| Option | Type | Description |
|--------|------|-------------|
| pages | string | Comma-separated page numbers or ranges |

**Request Example:**
```json
{
  "uploadId": "upload-id",
  "options": {
    "pages": "1,3,5-7"
  }
}
```

This creates a new PDF with only pages 1, 3, 5, 6, and 7.

---

### organize-pdf

Reorder pages in a PDF.

**Input:** Single PDF file

**Output:** PDF with reordered pages

**Conversion Engine:** pdfcpu

**Options:**

| Option | Type | Description |
|--------|------|-------------|
| order | string | New page order as comma-separated numbers |

**Request Example:**
```json
{
  "uploadId": "upload-id",
  "options": {
    "order": "3,1,2,5,4"
  }
}
```

This reorders a 5-page PDF so page 3 becomes first, page 1 becomes second, etc.

**Note:** All page numbers must be specified. The number of pages in the order must match the total page count.

---

### scan-to-pdf

Convert images to a PDF document, with optional per-page perspective correction (crop to detected corners), rotation, enhancement, page sizing, and OCR.

**Input:** Multiple image files (use `uploadIds`; each ≤ 50 megapixels)

**Output:** Single PDF

**Conversion Engine:** pure-Go imaging pipeline (warp/rotate/enhance) + pdfcpu (image import) + Tesseract (when `ocr=true`)

**Supported Image Formats:** `.png`, `.jpg`, `.jpeg`, `.webp`

**Options** (all optional — `{"ocr":true}` alone remains valid; the schema is validated at job creation and violations return 400 `INVALID_OPTIONS`):

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| ocr | boolean | false | Apply OCR to make text searchable |
| language | string | `"eng"` | OCR language — one of `eng`, `deu`, `fra`, `spa`, `hin` |
| pageSize | string | `"auto"` | PDF page size — `""`/`"auto"` (page = image size), `"a4"`, `"letter"`, `"id"` (ISO ID-1 card, 85.6×54 mm). **Ignored when `ocr=true`** (Tesseract emits image-sized pages) |
| enhance | string | — | `""`/`"none"`, `"grayscale"`, `"bw"` (adaptive-threshold scan look), `"color-boost"` |
| pages | array | — | Per-page directives, **index-aligned with `uploadIds`** (max 50 entries); each entry optional |
| pages[i].corners | object | — | Normalized document quad to perspective-warp: `tl`/`tr`/`br`/`bl`, each `{x, y}` in `[0, 1]`. All four corners or omitted. Get suggested corners from `POST /api/organize-pdf/detect-edges` |
| pages[i].rotation | integer | 0 | Clockwise rotation applied after the warp — `0`, `90`, `180`, or `270` |

**Request Example:**
```json
{
  "uploadIds": [
    "image-upload-1",
    "image-upload-2"
  ],
  "options": {
    "ocr": true,
    "language": "eng",
    "pageSize": "a4",
    "enhance": "bw",
    "pages": [
      {
        "corners": {
          "tl": {"x": 0.04, "y": 0.06},
          "tr": {"x": 0.97, "y": 0.05},
          "br": {"x": 0.96, "y": 0.95},
          "bl": {"x": 0.05, "y": 0.94}
        },
        "rotation": 90
      },
      {}
    ]
  }
}
```

**Notes:**
- Images are added to the PDF in the order provided. Each image becomes one page. PDF inputs pass through preprocessing untouched.
- Per image the worker runs: decode → warp to `corners` (if present) → rotate → enhance → re-encode (PNG for `bw`, JPEG q92 otherwise) → pdfcpu import at the requested page size.
- **EXIF contract:** the server ignores EXIF orientation — `corners` and `rotation` are relative to the **decoded pixel grid**. Clients must bake EXIF orientation into the pixels before uploading.
- Unknown option fields are ignored; string enums are case-insensitive. The worker re-clamps values defensively and never fails a job on a bad enum — it degrades to the default.

---

### watermark-pdf

Adds a text or image watermark to every page.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| type | `"text"` \| `"image"` | `"text"` | Watermark type |
| text | string | `"CONFIDENTIAL"` | Watermark text (when `type=text`) |
| imageData | string (data URL) | — | Base64 watermark image (when `type=image`) |
| position | `"center"` \| `"diagonal"` \| `"tiled"` | `"diagonal"` | Watermark placement |
| opacity | number (10–100) | `50` | Opacity percentage |
| fontSize | number (12–120) | `48` | Font size (text only) |
| color | string (hex) | `"#6366f1"` | Text color |

---

### protect-pdf

Encrypts a PDF with a password.

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| password | string | Yes | ≥ 4 characters |

---

### unlock-pdf

Removes the password from an encrypted PDF.

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| password | string | Yes | The current password on the PDF |

---

### sign-pdf

Stamps an image onto a chosen page.

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| imageData | string (data URL) | Yes | Base64 image (PNG/JPEG/WebP) |
| page | integer | No | 1-indexed page number (default `1`) |
| position | string | No | `top-left`, `top-right`, `bottom-left`, `bottom-right`, `center` (default) |

---

### edit-pdf

Adds free-form text annotations to a PDF.

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| text | string | Yes | Annotation text |
| page | integer | Yes | 1-indexed page number |
| x / y | number | Yes | Coordinates in PDF user space |
| fontSize | number | No | Default 12 |
| color | string (hex) | No | Default `#000000` |

---

### add-page-numbers

Adds page-number stamps to every page.

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| position | string | No | top/bottom + left/center/right (e.g., `bottom-center`) |
| format | string | No | e.g., `"%d / %d"` (current / total) |
| fontSize | number | No | Default 10 |
| color | string (hex) | No | Default `#000000` |

---

## Page Range Syntax

For tools that accept page ranges:

| Syntax | Description | Example |
|--------|-------------|---------|
| Single page | Individual page number | `5` |
| Range | Consecutive pages | `1-5` |
| Multiple | Comma-separated | `1,3,5` |
| Combined | Mix of singles and ranges | `1-3,5,7-10` |
| All | All pages | `all` |

**Examples:**
- `"1"` - Page 1 only
- `"1-5"` - Pages 1 through 5
- `"1,3,5"` - Pages 1, 3, and 5
- `"1-3,5,7-10"` - Pages 1, 2, 3, 5, 7, 8, 9, 10
- `"all"` - All pages in the document

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
