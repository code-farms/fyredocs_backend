# Upload API

Base URL: `http://localhost:8080`

The Upload API supports chunked file uploads for large files. Files are uploaded in chunks and assembled on completion.

---

## Upload Flow

1. **Initialize** → Get `uploadId`
2. **Upload Chunks** → Send file chunks with index
3. **Check Status** → Verify all chunks received
4. **Complete** → Assemble final file

---

## POST /api/upload/init

Initialize a new file upload session.

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
  "fileSize": 1024000,
  "totalChunks": 5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| fileName | string | Yes | Original file name |
| fileSize | integer | Yes | Total file size in bytes |
| totalChunks | integer | Yes | Number of chunks file will be split into |

### Response

**201 Created**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000"
}
```

| Field | Type | Description |
|-------|------|-------------|
| uploadId | string (UUID) | Unique upload session identifier |

### Notes

- Upload session expires after 2 hours (configurable via `UPLOAD_TTL`)
- Use this `uploadId` for all subsequent chunk uploads

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Missing required fields |

---

## PUT /api/upload/{uploadId}/chunk

Upload a single file chunk.

**Authentication:** Optional (guest mode allowed)

### Request

```
PUT /api/upload/{uploadId}/chunk?index={chunkIndex}
Content-Type: multipart/form-data
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| uploadId | string | Yes | Upload session ID from init |

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| index | integer | Yes | Chunk index (0-based) |

**Form Data:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| chunk | file | Yes | File chunk binary data |

### Response

**200 OK**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "fileName": "document.pdf",
  "fileSize": 1024000,
  "totalChunks": 5,
  "receivedChunks": 2,
  "complete": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| uploadId | string | Upload session ID |
| fileName | string | Original file name |
| fileSize | integer | Total file size in bytes |
| totalChunks | integer | Total number of chunks expected |
| receivedChunks | integer | Number of chunks received so far |
| complete | boolean | Whether all chunks have been received |

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Invalid chunk index or missing chunk |
| 404 | NOT_FOUND | Upload session not found or expired |

---

## GET /api/upload/{uploadId}/status

Check the status of an upload session.

**Authentication:** Optional (guest mode allowed)

### Request

```
GET /api/upload/{uploadId}/status
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| uploadId | string | Yes | Upload session ID |

### Response

**200 OK**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "fileName": "document.pdf",
  "fileSize": 1024000,
  "totalChunks": 5,
  "receivedChunks": 5,
  "complete": true
}
```

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 404 | NOT_FOUND | Upload session not found or expired |

---

## POST /api/upload/{uploadId}/complete

Complete the upload and assemble all chunks into the final file.

**Authentication:** Optional (guest mode allowed)

### Request

```
POST /api/upload/{uploadId}/complete
```

**URL Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| uploadId | string | Yes | Upload session ID |

### Response

**200 OK**
```json
{
  "uploadId": "550e8400-e29b-41d4-a716-446655440000",
  "storedPath": "/uploads/550e8400-e29b-41d4-a716-446655440000/document.pdf"
}
```

| Field | Type | Description |
|-------|------|-------------|
| uploadId | string | Upload session ID |
| storedPath | string | Path where the assembled file is stored |

### Behavior

- Assembles all chunks into a single file
- Cleans up temporary chunk files
- Returns file path for use in job creation

### Errors

| Status | Code | Description |
|--------|------|-------------|
| 400 | INVALID_INPUT | Not all chunks received |
| 404 | NOT_FOUND | Upload session not found or expired |

---

## File Size Limits

| Constraint | Value |
|------------|-------|
| Max file size | 50 MB (configurable via `MAX_UPLOAD_MB`) |
| Upload TTL | 2 hours (configurable via `UPLOAD_TTL`) |

---

## Chunking Strategy

Recommended chunk size: **1-5 MB per chunk**

For a 10 MB file:
- Chunk size: 2 MB
- Total chunks: 5
- Indexes: 0, 1, 2, 3, 4

### Example Chunk Calculation

```
File size: 10,485,760 bytes (10 MB)
Chunk size: 2,097,152 bytes (2 MB)
Total chunks: Math.ceil(10485760 / 2097152) = 5
```
