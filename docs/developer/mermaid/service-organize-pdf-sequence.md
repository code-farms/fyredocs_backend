# Organize-PDF Service -- Sequence Diagrams

Request flows through the `organize-pdf` worker service.

## Split PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as organize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message from<br/>jobs.dispatch.organize-pdf

    Worker->>Worker: Unmarshal JobPayload<br/>{jobId, toolType: "split-pdf", inputPaths: ["doc.pdf"], options: {pages: "1-3,5"}}

    Worker->>Worker: Validate toolType in AllowedTools

    Worker->>PG: UPDATE processing_jobs<br/>SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "split-pdf", ["doc.pdf"], {pages: "1-3,5"}, outputDir)

    Processing->>Disk: Read input PDF
    Processing->>Processing: Split by page ranges
    Processing->>Disk: Write individual page PDFs
    Processing->>Disk: Package into ZIP archive

    Processing-->>Worker: {OutputPath: "outputs/<jobId>.zip", Metadata: {pages: 4}}

    Worker->>PG: INSERT file_metadata (kind=output)
    Worker->>PG: Merge metadata into job
    Worker->>PG: UPDATE status=completed, progress=100

    Worker->>NATS: ACK message
```

## Rotate PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as organize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message from<br/>jobs.dispatch.organize-pdf

    Worker->>Worker: Unmarshal JobPayload<br/>{toolType: "rotate-pdf", options: {rotation: 90, applyToPages: "all"}}

    Worker->>PG: UPDATE processing_jobs SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "rotate-pdf", ["doc.pdf"], {rotation: 90, applyToPages: "all"}, outputDir)

    Processing->>Processing: Parse rotation=90, applyToPages="all"
    Processing->>Processing: Build pdfcpu page selection (nil = all pages)
    Processing->>Processing: api.RotateFile(inputPath, outputPath, 90, nil, nil)
    Processing->>Disk: Write rotated PDF

    Processing-->>Worker: {OutputPath: "outputs/<jobId>.pdf"}

    Worker->>PG: INSERT file_metadata (kind=output)
    Worker->>PG: UPDATE status=completed, progress=100
    Worker->>NATS: ACK message
```

## Merge PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as organize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "merge-pdf", inputPaths: ["a.pdf", "b.pdf", "c.pdf"]}

    Worker->>PG: SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "merge-pdf", [3 paths], {}, outputDir)

    Processing->>Disk: Read a.pdf
    Processing->>Disk: Read b.pdf
    Processing->>Disk: Read c.pdf
    Processing->>Processing: Merge in order
    Processing->>Disk: Write merged output

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, SET status=completed
    Worker->>NATS: ACK
```

## Extract Pages Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as organize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "extract-pages", inputPaths: ["report.pdf"], options: {pages: "2,4,6-10"}}

    Worker->>PG: SET status=processing

    Worker->>Processing: ProcessFile(ctx, jobId, "extract-pages", ["report.pdf"], {pages: "2,4,6-10"}, outputDir)

    Processing->>Disk: Read report.pdf
    Processing->>Processing: Extract specified pages
    Processing->>Disk: Write extracted pages PDF

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, SET status=completed
    Worker->>NATS: ACK
```

## Service Startup

```mermaid
sequenceDiagram
    participant Main as main()
    participant Config as shared/config
    participant DB as PostgreSQL
    participant Redis
    participant NATS as NATS JetStream
    participant Worker as worker.Run()
    participant Gin as Gin HTTP :8084

    Main->>Config: LoadConfig()
    Main->>Main: logger.Init("organize-pdf")
    Main->>Main: telemetry.Init("organize-pdf")
    Main->>DB: models.Connect() + Migrate()
    Main->>Redis: redisstore.Connect()
    Main->>NATS: natsconn.Connect()
    Main->>NATS: EnsureStreams()

    Main->>Worker: go worker.Run(ctx, WorkerConfig{<br/>  ServiceName: "organize-pdf",<br/>  AllowedTools: 13 tools — merge-pdf · split-pdf · remove-pages · extract-pages ·<br/>  organize-pdf · scan-to-pdf · rotate-pdf · watermark-pdf · protect-pdf ·<br/>  unlock-pdf · sign-pdf · edit-pdf · add-page-numbers<br/>})

    Main->>Gin: Setup /healthz, /readyz, /metrics
    Main->>Gin: ListenAndServe(:8084)

    Note over Main: Block until SIGINT/SIGTERM
    Main->>Worker: Cancel context
    Main->>Gin: Graceful shutdown
```

## Watermark / Sign / Edit / Page Numbers (annotation paths)

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as organize-pdf worker
    participant Proc as processing.ProcessFile
    participant PC as pdfcpu
    participant PG as PostgreSQL
    participant Disk as File System
    participant EV as JOBS_EVENTS

    NATS->>W: msg {toolType: watermark-pdf | sign-pdf | edit-pdf | add-page-numbers, options: {...}}
    W->>PG: status=processing, progress=20
    W->>EV: jobs.events.&lt;jobId&gt;.processing
    W->>Proc: ProcessFile(toolType, [in.pdf], options, outputDir)
    alt watermark-pdf
        Proc->>PC: pdfcpu add text or image watermark (position, opacity, fontSize, color)
    else sign-pdf
        Proc->>PC: pdfcpu image stamp on selected page (decode base64 → temp PNG)
    else edit-pdf
        Proc->>PC: pdfcpu text stamp at (x,y) on page
    else add-page-numbers
        Proc->>PC: pdfcpu add page numbers (position + format template)
    end
    PC-->>Disk: outputs/&lt;jobId&gt;.pdf
    Proc-->>W: OutputPath
    W->>PG: file_metadata kind=output · status=completed · progress=100
    W->>EV: jobs.events.&lt;jobId&gt;.completed
    W->>NATS: ACK
```

## Protect / Unlock (encryption paths)

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as organize-pdf worker
    participant Proc as processing.ProcessFile
    participant PC as pdfcpu
    participant PG as PostgreSQL

    NATS->>W: msg {toolType: protect-pdf or unlock-pdf, options: {password}}
    alt missing/short password
        W->>PG: status=failed, reason "missing password" / "password too short"
    else
        W->>Proc: ProcessFile(toolType, [in.pdf], {password})
        alt protect-pdf
            Proc->>PC: pdfcpu encrypt with user/owner password
        else unlock-pdf
            Proc->>PC: pdfcpu decrypt with provided password
        end
        PC-->>W: outputs/&lt;jobId&gt;.pdf
        W->>PG: file_metadata + status=completed
    end
    W->>NATS: ACK
```

## Failure → DLQ

```mermaid
sequenceDiagram
    participant W as organize-pdf worker
    participant NATS as JOBS_DISPATCH
    participant DLQ as JOBS_DLQ
    participant PG as PostgreSQL
    participant EV as JOBS_EVENTS

    Note over W: ProcessFile fails
    W->>W: classifyError → CONVERSION_FAILED / TIMEOUT / OUTPUT_FAILED / INVALID_PAYLOAD
    alt deliveryCount &lt; MaxDeliver (4)
        W->>PG: status=queued · failure_reason "retrying: ..."
        W->>NATS: Nak(delay=BackOff[deliveryCount])
    else deliveryCount == MaxDeliver
        W->>PG: status=failed · failure_reason [CODE] msg
        W->>EV: jobs.events.&lt;jobId&gt;.failed
        W->>DLQ: Publish jobs.dlq.organize-pdf
        W->>NATS: ACK
    end
```
