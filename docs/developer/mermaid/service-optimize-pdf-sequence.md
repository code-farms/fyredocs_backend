# Optimize-PDF Service -- Sequence Diagrams

Request flows through the `optimize-pdf` worker service.

## Compress PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as optimize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message from<br/>jobs.dispatch.optimize-pdf

    Worker->>Worker: Unmarshal JobPayload<br/>{jobId, toolType: "compress-pdf",<br/>inputPaths: ["large.pdf"],<br/>options: {quality: "medium"}}

    Worker->>Worker: Validate toolType in AllowedTools

    Worker->>PG: UPDATE processing_jobs<br/>SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "compress-pdf",<br/>["large.pdf"], {quality: "medium"}, outputDir)

    Processing->>Disk: Read large.pdf (e.g., 50 MB)
    Processing->>Processing: Compress with quality settings
    Processing->>Disk: Write compressed output (e.g., 12 MB)

    Processing-->>Worker: {OutputPath: "outputs/compressed.pdf",<br/>Metadata: {originalSize: 50MB, compressedSize: 12MB}}

    Worker->>PG: INSERT file_metadata (kind=output)
    Worker->>PG: Merge compression metadata
    Worker->>PG: UPDATE status=completed, progress=100

    Worker->>NATS: ACK message
```

## OCR PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as optimize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "ocr-pdf", inputPaths: ["scanned.pdf"], options: {language: "en"}}

    Worker->>PG: SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "ocr-pdf",<br/>["scanned.pdf"], {language: "en"}, outputDir)

    Processing->>Disk: Read scanned PDF
    Processing->>Processing: Run OCR engine<br/>(extract text from images)
    Processing->>Processing: Add searchable text layer
    Processing->>Disk: Write OCR-enhanced PDF

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, merge metadata
    Worker->>PG: SET status=completed, progress=100
    Worker->>NATS: ACK
```

## Repair PDF Processing

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as optimize-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message<br/>{toolType: "repair-pdf", inputPaths: ["corrupted.pdf"]}

    Worker->>PG: SET status=processing, progress=20

    Worker->>Processing: ProcessFile(ctx, jobId, "repair-pdf",<br/>["corrupted.pdf"], {}, outputDir)

    Processing->>Disk: Read corrupted PDF
    Processing->>Processing: Analyze and repair structure
    Processing->>Disk: Write repaired PDF

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, SET status=completed
    Worker->>NATS: ACK
```

## Failure and Retry Flow

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as optimize-pdf worker
    participant PG as PostgreSQL

    Note over NATS,Worker: Attempt 1

    NATS->>Worker: Deliver message (delivery 1)
    Worker->>PG: SET status=processing
    Worker->>Worker: ProcessFile() fails<br/>(timeout - recoverable)
    Worker->>PG: SET status=queued, failure_reason="retrying: timeout"
    Worker->>NATS: NAK with delay=10s

    Note over NATS,Worker: Attempt 2 (after 10s)

    NATS->>Worker: Redeliver (delivery 2)
    Worker->>PG: SET status=processing
    Worker->>Worker: ProcessFile() fails again
    Worker->>PG: SET status=queued, failure_reason="retrying: ..."
    Worker->>NATS: NAK with delay=30s

    Note over NATS,Worker: Attempt 3 (after 30s)

    NATS->>Worker: Redeliver (delivery 3)
    Worker->>PG: SET status=processing
    Worker->>Worker: ProcessFile() fails again
    Worker->>NATS: NAK with delay=2m

    Note over NATS,Worker: Attempt 4 (final, after 2m)

    NATS->>Worker: Redeliver (delivery 4)
    Worker->>PG: SET status=processing
    Worker->>Worker: ProcessFile() fails
    Worker->>Worker: deliveryCount=4 >= maxDeliver=4
    Worker->>PG: SET status=failed, failure_reason="<error>"
    Worker->>NATS: ACK (stop redelivery)
```
