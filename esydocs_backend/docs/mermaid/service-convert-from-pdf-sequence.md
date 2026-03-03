# Convert-from-PDF Service -- Sequence Diagrams

Request flows through the `convert-from-pdf` worker service.

## Job Processing (Happy Path)

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-from-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL
    participant Disk as File System

    NATS->>Worker: Fetch message from<br/>jobs.dispatch.convert-from-pdf

    Worker->>Worker: Unmarshal JobPayload<br/>{jobId, toolType, inputPaths, options}

    Worker->>Worker: Validate toolType in AllowedTools

    Worker->>PG: UPDATE processing_jobs<br/>SET status=processing, progress=20<br/>WHERE id=<jobId>

    Worker->>Worker: Parse options JSON

    Worker->>Processing: ProcessFile(ctx, jobId, toolType, inputPaths, options, outputDir)

    Processing->>Disk: Read input file(s)
    Processing->>Processing: Execute conversion<br/>(e.g., pdf-to-word)
    Processing->>Disk: Write output file to outputs/

    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: DELETE file_metadata WHERE job_id=<id> AND kind=output
    Worker->>Disk: Stat output file (get size)
    Worker->>PG: INSERT file_metadata<br/>(job_id, kind=output, path, size)

    Worker->>PG: Merge metadata into job.metadata JSON
    Worker->>PG: UPDATE processing_jobs<br/>SET status=completed, progress=100, completed_at=now()
    Worker->>PG: UPDATE processing_jobs<br/>SET failure_reason=NULL

    Worker->>NATS: ACK message

    Note over Worker: Log: "job completed"
```

## Job Processing (Failure with Retry)

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-from-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL

    NATS->>Worker: Deliver message (attempt 1)

    Worker->>PG: SET status=processing, progress=20

    Worker->>Processing: ProcessFile(...)
    Processing-->>Worker: Error (recoverable, e.g., timeout)

    Worker->>Worker: Check: deliveryCount=1 < maxDeliver=4<br/>AND error is recoverable

    Worker->>PG: UPDATE status=queued, progress=0<br/>failure_reason="retrying: timeout"

    Worker->>NATS: NAK with delay=10s

    Note over NATS: Wait 10 seconds

    NATS->>Worker: Redeliver message (attempt 2)

    Worker->>PG: SET status=processing, progress=20

    Worker->>Processing: ProcessFile(...)
    Processing-->>Worker: {OutputPath, Metadata}

    Worker->>PG: Record output, SET status=completed
    Worker->>NATS: ACK
```

## Job Processing (Permanent Failure)

```mermaid
sequenceDiagram
    participant NATS as NATS JetStream
    participant Worker as convert-from-pdf worker
    participant Processing as processing.ProcessFile()
    participant PG as PostgreSQL

    NATS->>Worker: Deliver message (attempt 4, final)

    Worker->>PG: SET status=processing

    Worker->>Processing: ProcessFile(...)
    Processing-->>Worker: Error (recoverable but retries exhausted)

    Worker->>Worker: Check: deliveryCount=4 >= maxDeliver=4<br/>Retries exhausted

    Worker->>PG: UPDATE status=failed, progress=0<br/>failure_reason="<error message>"

    Worker->>NATS: ACK (stop redelivery)

    Note over Worker: Log: "job failed"
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
    participant Gin as Gin HTTP Server

    Main->>Config: LoadConfig()
    Main->>Main: logger.Init("convert-from-pdf")
    Main->>Main: telemetry.Init("convert-from-pdf")
    Main->>DB: models.Connect() + Migrate()
    Main->>Redis: redisstore.Connect()
    Main->>NATS: natsconn.Connect()
    Main->>NATS: EnsureStreams(JOBS_DISPATCH, JOBS_EVENTS)

    Main->>Worker: go worker.Run(ctx, config)<br/>(background goroutine)

    Note over Worker: Creates durable consumer<br/>FilterSubject: jobs.dispatch.convert-from-pdf<br/>MaxDeliver: 4, AckWait: 30m

    Main->>Gin: Setup /healthz, /metrics
    Main->>Gin: ListenAndServe(:8082)

    Note over Main: Block on SIGINT/SIGTERM
    Main->>Worker: Cancel context
    Main->>Gin: Graceful shutdown (10s)
```
