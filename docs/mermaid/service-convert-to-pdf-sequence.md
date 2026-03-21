# Convert-to-PDF Service -- Sequence Diagrams

Request flows through the `convert-to-pdf` worker service.

## Job Processing (Happy Path)

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-to-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message from<br/>jobs.dispatch.convert-to-pdf

    Worker->>Worker: Unmarshal JobPayload<br/>{jobId, toolType: "word-to-pdf", inputPaths, options}

    Worker->>Worker: Validate toolType in AllowedTools

    Worker->>PG: UPDATE processing_jobs<br/>SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "word-to-pdf", inputPaths, options, outputDir)

    Processing->>Disk: Read input Word document
    Processing->>Processing: Convert Word to PDF
    Processing->>Disk: Write output PDF to outputs/

    Processing-->>Worker: {OutputPath: "outputs/<jobId>.pdf", Metadata: {...}}

    Worker->>PG: INSERT file_metadata (kind=output, path, size)
    Worker->>PG: Merge metadata into job record
    Worker->>PG: UPDATE status=completed, progress=100

    Worker->>NATS: ACK message
```

## Merge PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-to-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "merge-pdf", inputPaths: [file1.pdf, file2.pdf, file3.pdf]}

    Worker->>PG: SET status=processing

    Worker->>Processing: ProcessFile(ctx, jobId, "merge-pdf", [3 paths], {}, outputDir)

    Processing->>Disk: Read file1.pdf
    Processing->>Disk: Read file2.pdf
    Processing->>Disk: Read file3.pdf
    Processing->>Processing: Merge PDFs in order
    Processing->>Disk: Write merged.pdf

    Processing-->>Worker: {OutputPath: "outputs/merged.pdf"}

    Worker->>PG: Record output file metadata
    Worker->>PG: SET status=completed, progress=100

    Worker->>NATS: ACK
```

## Image-to-PDF Conversion

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-to-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "image-to-pdf", inputPaths: ["photo.jpg"]}

    Worker->>PG: SET status=processing

    Worker->>Processing: ProcessFile(ctx, jobId, "image-to-pdf", ["photo.jpg"], options, outputDir)

    Processing->>Disk: Read image file
    Processing->>Processing: Create PDF with embedded image
    Processing->>Disk: Write output.pdf

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, SET status=completed
    Worker->>NATS: ACK
```

## Worker Lifecycle

```mermaid
sequenceDiagram
    participant Main as main()
    participant NATS as NATS JetStream
    participant Worker as worker.Run()
    participant Consumer as Pull Consumer

    Main->>NATS: Connect + EnsureStreams
    Main->>Worker: go worker.Run(ctx, config)

    Worker->>NATS: CreateOrUpdateConsumer<br/>(durable: convert-to-pdf,<br/>filter: jobs.dispatch.convert-to-pdf,<br/>maxDeliver: 4, ackWait: 30m)
    Worker->>Worker: Init semaphore (WORKER_CONCURRENCY=2)

    NATS-->>Worker: Consumer ready

    loop Until context cancelled
        Worker->>Consumer: Fetch(maxConcurrency, maxWait=30s)

        alt Messages available
            Consumer-->>Worker: 1..N Messages
            loop For each message
                Worker->>Worker: Acquire semaphore slot
                Worker->>Worker: go processMessage(msg)
                Note over Worker: Release slot on completion
            end
        else No messages (timeout)
            Consumer-->>Worker: ErrNoMessages
            Note over Worker: Continue loop
        end
    end

    Note over Worker: Context cancelled
    Worker->>Worker: wg.Wait() (drain in-flight jobs)
    Worker-->>Main: Return
```
