# Convert-to-PDF Service -- Sequence Diagrams

Request flows through the `convert-to-pdf` worker service.

## Job Processing — Office (word/excel/ppt/html)

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as convert-to-pdf worker
    participant Sem as Semaphore (cap=WORKER_CONCURRENCY)
    participant Proc as processing.ProcessFile
    participant Uno as unoserver / unoconvert
    participant LO as libreoffice --headless (fallback)
    participant PG as PostgreSQL
    participant S3 as MinIO / S3
    participant EV as JOBS_EVENTS

    NATS->>W: Pull batch (up to WORKER_CONCURRENCY)
    W->>Sem: Acquire slot
    par per message
        W->>W: Unmarshal JobPayload
        W->>W: Validate AllowedTools[toolType]
        W->>PG: SELECT status — skip if completed/processing
        W->>PG: UPDATE status=processing, progress=20
        W->>EV: jobs.events.&lt;jobId&gt;.processing
        W->>W: MkdirTemp scratch (job-&lt;jobId&gt;-*)
        W->>S3: DownloadToFile per InputPaths key (uploads bucket → scratch/in)
        alt download fails
            W->>NATS: NAK with backoff (recoverable)
        end
        W->>W: startProgressReporter(20→90% ease-out)
        W->>Proc: ProcessFile(toolType, local inputs, scratch/out)
        Proc->>Uno: unoconvert --convert-to pdf input output
        alt unoserver unreachable
            Proc->>LO: libreoffice --headless --convert-to pdf
        end
        Proc-->>W: {OutputPath in scratch/out, Metadata}
        W->>W: stopProgressReporter
        alt success
            W->>S3: UploadFromFile (outputs bucket, jobs/&lt;jobId&gt;/&lt;file&gt;)
            alt upload fails
                W->>NATS: NAK with backoff (recoverable)
            end
            W->>PG: INSERT file_metadata (kind=output, path=object key, size=uploaded bytes)
            W->>PG: UPDATE status=completed, progress=100
            W->>EV: jobs.events.&lt;jobId&gt;.completed (with uploaded fileSize)
        else failure
            W->>PG: UPDATE status=failed, failure_reason=[CODE] msg
            W->>EV: jobs.events.&lt;jobId&gt;.failed
            opt MaxDeliver hit
                W->>NATS: Publish jobs.dlq.convert-to-pdf
            end
        end
        W->>W: RemoveAll scratch dir
        W->>NATS: ACK
    end
    W->>Sem: Release slot
```

## Image-to-PDF (multi-image)

```mermaid
sequenceDiagram
    participant NATS as JOBS_DISPATCH
    participant W as convert-to-pdf worker
    participant Proc as processing.ProcessFile
    participant Pdfcpu as pdfcpu
    participant PG as PostgreSQL
    participant S3 as MinIO / S3
    participant EV as JOBS_EVENTS

    NATS->>W: msg {toolType:image-to-pdf, inputPaths:[keys a.jpg,b.png,c.webp]}
    W->>PG: status=processing, progress=20
    W->>EV: processing
    W->>S3: Download 3 keys (uploads bucket → scratch/in)
    W->>W: startProgressReporter (time-based)
    W->>Proc: ProcessFile("image-to-pdf", [3 local paths], options, scratch/out)
    Proc->>Pdfcpu: Import images one per page
    Pdfcpu-->>Proc: scratch/out/&lt;jobId&gt;.pdf (3 pages)
    Proc-->>W: OutputPath
    W->>W: stop reporter
    W->>S3: Upload outputs bucket jobs/&lt;jobId&gt;/&lt;jobId&gt;.pdf
    W->>PG: file_metadata (kind=output, path=object key)
    W->>PG: status=completed, progress=100
    W->>EV: completed
    W->>W: remove scratch dir
    W->>NATS: ACK
```

## Worker Lifecycle

```mermaid
sequenceDiagram
    participant Main as main()
    participant NATS as JetStream
    participant W as worker.Run()
    participant Cons as Pull Consumer

    Main->>NATS: Connect + EnsureStreams
    Main->>W: go worker.Run(ctx, cfg)
    W->>NATS: CreateOrUpdateConsumer<br/>durable=convert-to-pdf<br/>filter=jobs.dispatch.convert-to-pdf<br/>MaxDeliver=4 · AckWait=30m<br/>BackOff=10s/30s/2m
    W->>W: Init semaphore (WORKER_CONCURRENCY=2)
    loop Until ctx.Done()
        W->>Cons: Fetch(N, MaxWait 30s)
        alt msgs
            Cons-->>W: 1..N
            loop each
                W->>W: sem &lt;- ; go processMessage(msg) ; defer &lt;-sem
            end
        else no msgs
            Note over W: continue loop (sleep 500ms on fetch error)
        end
    end
    Note over W: ctx cancelled
    W->>W: wg.Wait() — drain in-flight
    W-->>Main: return
```

## Failure Handling — DLQ

```mermaid
sequenceDiagram
    participant W as convert-to-pdf worker
    participant NATS as JOBS_DISPATCH
    participant DLQ as JOBS_DLQ
    participant PG as PostgreSQL

    Note over W: Conversion fails
    W->>PG: UPDATE status=failed, failure_reason=[CODE]...
    W->>W: classifyError → CONVERSION_FAILED / TIMEOUT / OUTPUT_FAILED / INVALID_PAYLOAD
    alt deliveryCount &lt; MaxDeliver (4)
        W->>NATS: Nak(delay=BackOff[deliveryCount])
        Note over NATS: Redeliver after backoff
    else deliveryCount == MaxDeliver
        W->>DLQ: Publish jobs.dlq.convert-to-pdf with original payload + error
        W->>NATS: ACK (drop)
    end
```

## Health & Readiness

```mermaid
sequenceDiagram
    participant Probe
    participant W as convert-to-pdf :8083
    participant Redis
    participant NATS
    participant Uno as unoserver TCP

    Probe->>W: GET /healthz
    W->>Redis: PING (2s)
    W->>NATS: Conn.IsConnected
    W-->>Probe: 200/503

    Probe->>W: GET /readyz
    W->>Redis: PING
    W->>NATS: Conn.IsConnected
    W->>PG: SELECT 1
    W->>Uno: TCP dial UNOSERVER_HOST:PORT (1s, informational)
    W-->>Probe: 200 with checks {redis, nats, postgres, unoserver}
```
