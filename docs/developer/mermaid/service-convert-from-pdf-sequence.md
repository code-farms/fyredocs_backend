# Convert-from-PDF Service -- Sequence Diagrams

Request flows through the `convert-from-pdf` worker service.

## Result Cache Short-Circuit

Checked before download on every job: identical input + tool + options reuse a prior output, skipping the pdf2docx/LibreOffice conversion entirely. Best-effort — any cache error falls through to a normal run.

```mermaid
sequenceDiagram
    participant W as convert-from-pdf worker
    participant Cache as Redis (result cache)
    participant S3 as MinIO / S3
    participant PG as PostgreSQL
    participant EV as JOBS_EVENTS

    W->>S3: StatObject(uploads) per input &rarr; ETags (no download)
    W->>Cache: GET rescache:v1:convert-from-pdf:sha256(toolType+options+ETags)
    alt cache hit
        W->>S3: StatObject(outputs) verify cached output exists
        alt output present
            W->>S3: CopyObject cached output &rarr; jobs/&lt;jobId&gt;/&lt;file&gt; (server-side)
            W->>PG: INSERT file_metadata + UPDATE status=completed, progress=100
            W->>EV: completed (download + conversion skipped)
        else expired / missing
            Note over W: fall through to normal conversion
        end
    else miss
        Note over W: normal conversion; on success SET cache key (TTL=RESULT_CACHE_TTL_SECONDS)
    end
```

## Job Processing — Happy Path

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as convert-from-pdf worker
    participant Proc as processing.ProcessFile
    participant Tools as pdf2docx · LibreOffice · poppler · ghostscript
    participant PG as PostgreSQL
    participant S3 as MinIO / S3
    participant EV as JOBS_EVENTS

    NATS->>W: Pull (1 msg, MaxWait 30s)
    W->>W: Unmarshal JobPayload
    W->>W: Validate AllowedTools[toolType]
    W->>PG: SELECT status — skip if completed/processing
    W->>PG: UPDATE status=processing, progress=20
    W->>EV: jobs.events.&lt;jobId&gt;.processing
    W->>W: Parse options JSON

    W->>W: MkdirTemp scratch (job-&lt;jobId&gt;-*)
    W->>S3: DownloadToFile per inputPaths key (uploads bucket → scratch/in)
    alt download fails
        W->>NATS: NAK with backoff (recoverable)
    end

    W->>Proc: ProcessFile(toolType, local inputs, options, scratch/out, onProgress)
    Proc->>Tools: tool-specific dispatch (see arch diagram)
    Tools-->>Proc: output file in scratch/out
    Proc-->>W: {OutputPath, Metadata{outputExt}}

    W->>S3: UploadFromFile (outputs bucket, jobs/&lt;jobId&gt;/&lt;file&gt;) → size
    alt upload fails
        W->>NATS: NAK with backoff (recoverable)
    end
    W->>PG: DELETE file_metadata WHERE job_id=:id AND kind='output' (idempotent re-run)
    W->>PG: INSERT file_metadata (kind='output', path=object key, size_bytes=uploaded size)
    W->>PG: Merge metadata JSON
    W->>PG: UPDATE status=completed, progress=100, completed_at=NOW(), failure_reason=NULL
    W->>EV: jobs.events.&lt;jobId&gt;.completed (with uploaded fileSize)
    W->>W: RemoveAll scratch dir
    W->>NATS: ACK
```

## DOCX Path — pdf2docx + LibreOffice fallback

```mermaid
sequenceDiagram
    participant W as worker
    participant Proc as processing.ProcessFile
    participant Native as pdf2docx (Python CLI)
    participant LO as libreoffice --headless
    participant EV as JOBS_EVENTS
    participant PG as PostgreSQL

    Proc->>Proc: case "pdf-to-word"/"pdf-to-docx"
    Proc->>Native: pdfToDocxNativeTicking(...)<br/>spawn `pdf2docx convert input output`
    Native-->>Proc: exit code + stdout/stderr
    alt success
        Proc-->>W: docx (real paragraphs/tables/lists)
    else failure (non-zero exit OR empty output)
        Proc->>Proc: log warn "pdf2docx failed, falling back to LibreOffice"
        Proc->>EV: progress reset (so UI ticker keeps moving)
        Proc->>LO: pdfToOfficeTicking(... "docx")
        LO-->>Proc: docx (positioned text frames)
        Proc-->>W: docx (lower fidelity)
    end
    W->>PG: status=completed
```

## PPTX Path — image-based slide builder

```mermaid
sequenceDiagram
    participant W as worker
    participant Proc as processing.ProcessFile
    participant Pop as pdftoppm
    participant Builder as pptx builder
    participant Disk as File System
    participant EV as JOBS_EVENTS

    Proc->>Proc: case "pdf-to-ppt"/"pdf-to-pptx"
    Proc->>Disk: mkdir tmp/&lt;jobId&gt;/
    Proc->>Pop: pdftoppm input.pdf → page-001.png ... page-N.png
    loop For each page image
        Proc->>Builder: addSlideWithImage(pngPath)
        Proc->>EV: progress (i/N) — onProgress callback
    end
    Builder-->>Disk: outputs/&lt;jobId&gt;.pptx
    Proc-->>W: OutputPath
```

## Failure with Retry / DLQ

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as convert-from-pdf worker
    participant DLQ as JOBS_DLQ
    participant PG as PostgreSQL
    participant EV as JOBS_EVENTS

    NATS->>W: Deliver msg (deliveryCount=N)
    W->>W: ProcessFile fails

    alt N &lt; MaxDeliver (4) — recoverable
        W->>PG: UPDATE status=queued, progress=0,<br/>failure_reason="retrying: &lt;err&gt;"
        W->>NATS: Nak(delay=BackOff[N]) — 10s · 30s · 2m
        Note over NATS: Redeliver after backoff
    else N == MaxDeliver — exhausted
        W->>PG: UPDATE status=failed, progress=0, failure_reason=[CODE] msg
        W->>EV: jobs.events.&lt;jobId&gt;.failed
        W->>DLQ: Publish jobs.dlq.convert-from-pdf
        W->>NATS: ACK (stop redelivery)
    end
```

## Service Startup

```mermaid
sequenceDiagram
    participant Main as main()
    participant Cfg as shared/config
    participant DB as PostgreSQL
    participant Redis
    participant NATS as JetStream
    participant W as worker.Run()
    participant Gin

    Main->>Cfg: LoadConfig()
    Main->>Main: logger.Init · telemetry.Init
    Main->>DB: models.Connect + Migrate
    Main->>Redis: redisstore.Connect
    Main->>NATS: natsconn.Connect
    Main->>NATS: EnsureStreams (JOBS_DISPATCH · JOBS_EVENTS · JOBS_DLQ · ANALYTICS)
    Main->>W: go worker.Run(ctx, cfg)
    W->>NATS: CreateOrUpdateConsumer<br/>durable=convert-from-pdf<br/>filter=jobs.dispatch.convert-from-pdf<br/>MaxDeliver=4 · AckWait=30m
    Main->>Gin: register /healthz · /readyz · /metrics
    Main->>Gin: ListenAndServe(:8082)
    Note over Main: block on SIGINT/SIGTERM
    Main->>W: cancel ctx
    W->>W: drain in-flight goroutines (wg.Wait)
    Main->>Gin: graceful shutdown (10s)
```

## Health & Readiness

```mermaid
sequenceDiagram
    participant Probe
    participant W as convert-from-pdf :8082
    participant Redis
    participant NATS

    Probe->>W: GET /healthz
    W->>Redis: PING (2s)
    W->>NATS: Conn.IsConnected
    W-->>Probe: 200/503

    Probe->>W: GET /readyz
    W->>Redis: PING
    W->>NATS: Conn.IsConnected
    W->>PG: SELECT 1
    W-->>Probe: 200 with checks {redis, nats, postgres}
```
