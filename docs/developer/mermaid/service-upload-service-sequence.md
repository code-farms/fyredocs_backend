# Upload Service -- Sequence Diagrams

Request flows through the `upload-service`.

## Chunked File Upload Flow

```mermaid
sequenceDiagram
    participant Client
    participant US as upload-service :8081
    participant Redis
    participant Disk as File System

    Client->>US: POST /api/uploads/init<br/>{"fileName": "doc.pdf", "fileSize": 10485760, "totalChunks": 5}

    US->>US: Generate uploadId (UUID)
    US->>Redis: HSET upload:<id> fileName, fileSize, totalChunks, createdAt
    US->>Redis: EXPIRE upload:<id> 2h
    US-->>Client: 201 {uploadId: "<uuid>"}

    loop For each chunk (0..4)
        Client->>US: PUT /api/uploads/<id>/chunk?index=N<br/>(multipart: chunk file)

        US->>Redis: HGETALL upload:<id>
        Redis-->>US: {fileName, fileSize, totalChunks, createdAt}

        US->>Disk: Save chunk to uploads/tmp/<id>/00000N.part
        US->>Redis: SADD upload:<id>:chunks N
        US->>Redis: EXPIRE upload:<id>:chunks 2h
        US-->>Client: 200 {uploadId, receivedChunks: N+1, complete: false}
    end

    Client->>US: POST /api/uploads/<id>/complete

    US->>Redis: HGETALL upload:<id>
    US->>Redis: SCARD upload:<id>:chunks
    Note over US: Verify receivedChunks == totalChunks

    US->>Disk: Assemble chunks into uploads/<id>/doc.pdf
    US->>Disk: Remove uploads/tmp/<id>/
    US->>US: Validate assembled file size <= MAX_UPLOAD_MB

    US-->>Client: 200 {uploadId, storedPath}
```

## Job Creation from Upload

```mermaid
sequenceDiagram
    participant Client
    participant US as upload-service :8081
    participant Redis
    participant PG as PostgreSQL
    participant Disk as File System

    Client->>US: POST /api/convert-from-pdf/pdf-to-word<br/>{"uploadId": "<uuid>"}

    US->>US: Normalize tool type
    US->>US: Validate tool is supported

    US->>Redis: HGETALL upload:<uploadId>
    Redis-->>US: {fileName, fileSize}

    US->>Disk: Move file from uploads/<uploadId>/ to uploads/<jobId>/
    US->>Redis: DEL upload:<uploadId>, upload:<uploadId>:chunks

    US->>PG: BEGIN TRANSACTION
    US->>PG: INSERT processing_jobs (id, tool_type, status=queued, ...)
    US->>PG: INSERT file_metadata (job_id, kind=input, path, ...)
    US->>PG: COMMIT

    US->>US: Determine queue: toolQueueMap[pdf-to-word] = convert-from-pdf
    US->>Redis: RPUSH queue:convert-from-pdf {jobId, toolType, inputPaths, ...}

    US-->>Client: 201 Created {job}
```

## Multipart Direct Upload with Job Creation

```mermaid
sequenceDiagram
    participant Client
    participant US as upload-service :8081
    participant PG as PostgreSQL
    participant Redis

    Client->>US: POST /api/convert-to-pdf/word-to-pdf<br/>(multipart: files[]=report.docx, options={})

    US->>US: Normalize & validate tool type
    US->>US: Validate file extension (.docx for word-to-pdf)
    US->>US: Check file size <= MAX_UPLOAD_MB (50 MB)

    US->>US: Save file to uploads/<jobId>/report.docx

    US->>PG: BEGIN TRANSACTION
    US->>PG: INSERT processing_jobs (status=queued)
    US->>PG: INSERT file_metadata (kind=input)
    US->>PG: COMMIT

    US->>Redis: RPUSH queue:convert-to-pdf {jobPayload}

    Note over US: If guest user, assign guest token cookie

    US-->>Client: 201 Created {job}
```

## User Signup

```mermaid
sequenceDiagram
    participant Client
    participant US as upload-service :8081
    participant Redis
    participant PG as PostgreSQL

    Client->>US: POST /auth/signup<br/>{"email", "password", "fullName", "country"}

    Note over US: Rate limit: 3 req/min per IP

    US->>US: Validate inputs (email, password 8-128 chars, fullName, country)
    US->>PG: SELECT * FROM users WHERE email = ?
    PG-->>US: Not found (ErrRecordNotFound)

    US->>US: bcrypt.GenerateFromPassword(password)
    US->>PG: INSERT INTO users (email, full_name, password_hash, ...)

    US->>US: Issuer.IssueAccessToken(userId, "user")
    US->>US: Set access_token cookie (HttpOnly, Secure, SameSite)

    US-->>Client: 200 {user: {id, email, fullName, role}}
```
