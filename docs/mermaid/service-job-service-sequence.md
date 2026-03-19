# Job Service -- Sequence Diagrams

Request flows through the `job-service`.

## Create Job (JSON body with uploadId)

```mermaid
sequenceDiagram
    participant GW as api-gateway
    participant JS as job-service :8081
    participant Redis
    participant PG as PostgreSQL
    participant Disk as File System
    participant NATS as NATS JetStream

    GW->>JS: POST /api/convert-from-pdf/pdf-to-word<br/>X-User-ID: <uuid><br/>{"uploadId": "<uploadId>"}

    JS->>JS: Auth middleware<br/>Extract user from headers or JWT

    JS->>JS: Normalize tool type<br/>Validate against convertFromTools

    JS->>Redis: HGETALL upload:<uploadId>
    Redis-->>JS: {fileName: "doc.pdf", fileSize: 5242880}

    JS->>Disk: Move file from uploads/<uploadId>/doc.pdf<br/>to uploads/<jobId>/doc.pdf
    JS->>Redis: DEL upload:<uploadId>, upload:<uploadId>:chunks
    JS->>Disk: Remove uploads/<uploadId>/ directory

    JS->>PG: BEGIN TRANSACTION
    JS->>PG: INSERT processing_jobs<br/>(id=<jobId>, tool_type=pdf-to-word,<br/>status=queued, user_id=<uuid>)
    JS->>PG: INSERT file_metadata<br/>(job_id=<jobId>, kind=input, path=...)
    JS->>PG: COMMIT

    JS->>JS: routing.ServiceForTool("pdf-to-word")<br/>returns "convert-from-pdf"

    JS->>NATS: Publish to jobs.dispatch.convert-from-pdf<br/>{eventType: "JobCreated", jobId, toolType,<br/>inputPaths, options, correlationId}

    JS-->>GW: 201 Created {job}
```

## Create Job (Multipart upload)

```mermaid
sequenceDiagram
    participant GW as api-gateway
    participant JS as job-service :8081
    participant PG as PostgreSQL
    participant NATS as NATS JetStream

    GW->>JS: POST /api/convert-to-pdf/word-to-pdf<br/>Content-Type: multipart/form-data<br/>files[]=report.docx

    JS->>JS: Parse multipart form
    JS->>JS: Validate file extension (.docx)
    JS->>JS: Check file size <= 50 MB

    JS->>JS: Save to uploads/<jobId>/report.docx

    JS->>PG: INSERT processing_jobs (status=queued)
    JS->>PG: INSERT file_metadata (kind=input)

    JS->>JS: routing.ServiceForTool("word-to-pdf")<br/>returns "convert-to-pdf"

    JS->>NATS: Publish to jobs.dispatch.convert-to-pdf<br/>{JobCreated event}

    JS-->>GW: 201 Created {job}
```

## Get Job Status

```mermaid
sequenceDiagram
    participant GW as api-gateway
    participant JS as job-service :8081
    participant PG as PostgreSQL

    GW->>JS: GET /api/convert-from-pdf/pdf-to-word/<jobId><br/>X-User-ID: <uuid>

    JS->>PG: SELECT * FROM processing_jobs<br/>WHERE id = <jobId> AND tool_type = pdf-to-word

    PG-->>JS: {id, status: "processing", progress: 20, ...}

    JS->>JS: authorizeJobAccess()<br/>Check job.user_id matches X-User-ID

    JS-->>GW: 200 {job}
```

## Download Completed Job

```mermaid
sequenceDiagram
    participant GW as api-gateway
    participant JS as job-service :8081
    participant PG as PostgreSQL
    participant Disk as File System

    GW->>JS: GET /api/convert-from-pdf/pdf-to-word/<jobId>/download<br/>X-User-ID: <uuid>

    JS->>PG: SELECT * FROM processing_jobs<br/>WHERE id = <jobId> AND tool_type = pdf-to-word
    PG-->>JS: {status: "completed"}

    JS->>JS: authorizeJobAccess() -- OK

    JS->>PG: SELECT * FROM file_metadata<br/>WHERE job_id = <jobId> AND kind = output
    PG-->>JS: {path: "outputs/result.docx", size: 2048000}

    JS->>JS: Determine filename: "doc.docx"<br/>Determine Content-Type

    JS->>Disk: Read outputs/result.docx

    JS-->>GW: 200 OK<br/>Content-Disposition: attachment; filename="doc.docx"<br/>Content-Type: application/vnd...wordprocessingml<br/>(file bytes)
```

## Delete Job

```mermaid
sequenceDiagram
    participant GW as api-gateway
    participant JS as job-service :8081
    participant PG as PostgreSQL
    participant Disk as File System
    participant Redis

    GW->>JS: DELETE /api/convert-from-pdf/pdf-to-word/<jobId>

    JS->>PG: SELECT * FROM processing_jobs WHERE id = <jobId>
    JS->>JS: authorizeJobAccess()

    JS->>PG: SELECT * FROM file_metadata WHERE job_id = <jobId>
    PG-->>JS: [input file, output file]

    loop For each file
        JS->>Disk: os.Remove(file.Path)
    end

    JS->>PG: DELETE FROM file_metadata WHERE job_id = <jobId>
    JS->>PG: DELETE FROM processing_jobs WHERE id = <jobId>

    JS->>Redis: SREM guest:<token>:jobs <jobId>

    JS-->>GW: 204 No Content
```

## Guest User Job Access

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway
    participant JS as job-service :8081
    participant Redis
    participant PG as PostgreSQL

    Client->>GW: GET /api/convert-from-pdf/pdf-to-word<br/>(Cookie: guest_token=<token>)

    GW->>Redis: Validate guest token
    GW->>JS: GET /api/convert-from-pdf/pdf-to-word<br/>X-Guest-Token: <token>

    JS->>Redis: SMEMBERS guest:<token>:jobs
    Redis-->>JS: ["job-id-1", "job-id-2"]

    JS->>PG: SELECT * FROM processing_jobs<br/>WHERE id IN (<ids>) AND tool_type = pdf-to-word AND user_id IS NULL

    PG-->>JS: [job1, job2]

    JS-->>GW: 200 {jobs: [...], meta: {page, limit}}
    GW-->>Client: 200 {jobs}
```

## SSE Job Status Updates

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway
    participant JS as job-service :8081
    participant NATS as NATS JetStream
    participant W as Worker Service

    Client->>GW: GET /api/jobs/<jobId>/events<br/>Accept: text/event-stream
    GW->>JS: Proxy request

    JS->>JS: Set SSE headers<br/>(Content-Type: text/event-stream)

    JS->>NATS: Create ephemeral consumer<br/>on JOBS_EVENTS stream<br/>filter: jobs.events.>

    JS-->>Client: event: connected<br/>data: {"jobId": "<jobId>"}

    loop Until job completes/fails or 5min timeout
        W->>NATS: Publish job event<br/>(jobs.events.JobProgress)

        JS->>NATS: Fetch messages (5s wait)
        NATS-->>JS: JobEvent {jobId, eventType, progress}

        alt Event matches requested jobId
            JS-->>Client: event: job-update<br/>data: {"jobId","status","progress","toolType"}
            JS->>NATS: ACK message
        else Event for different job
            JS->>NATS: ACK message (skip)
        end

        Note over JS,Client: Keepalive comment every 15s<br/>": keepalive"
    end

    W->>NATS: Publish JobCompleted/JobFailed
    NATS-->>JS: Terminal event
    JS-->>Client: event: job-update<br/>data: {final status}
    JS-->>Client: event: done<br/>data: {"jobId": "<jobId>"}
    Note over JS,Client: Connection closed
```
