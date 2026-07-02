# Document Service -- Sequence Diagrams

Request flows through the `document-service` (port 8089). Identity is the gateway-injected `X-User-ID`; org-scoped requests are RBAC-checked against user-service.

## List Documents (personal)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant DS as document-service :8089
    participant PG as PostgreSQL

    Client->>GW: GET /api/documents?status=&folderId=&tagId=&q=&trashed=&page=&limit=
    GW->>GW: Verify JWT · inject X-User-ID
    GW->>DS: Proxy
    DS->>DS: RequireUser · read filters into locals
    par concurrent (one round-trip)
        DS->>PG: COUNT(*) matching (user_id, filters)
    and
        DS->>PG: SELECT page (search_vector @@ websearch_to_tsquery for q)
    end
    DS-->>Client: 200 {data:[...], meta:{page, limit, total}}
```

## Create Document (org-scoped, RBAC)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant DS as document-service :8089
    participant US as user-service :8090
    participant PG as PostgreSQL

    Client->>GW: POST /api/documents {name, organizationId?, folderId?, ...}
    GW->>DS: Proxy (X-User-ID)
    DS->>DS: RequireUser
    alt organizationId present
        DS->>US: GET /api/orgs/:id (verify membership + role)
        alt role < editor
            DS-->>Client: 403 FORBIDDEN
        else editor+
            DS->>PG: INSERT documents (organization_id set)
            DS-->>Client: 201 {document}
        end
    else personal
        DS->>PG: INSERT documents (organization_id NULL, user_id scoped)
        DS-->>Client: 201 {document}
    end
```

## Finalize a Completed Job into a Document (NATS subscriber)

```mermaid
sequenceDiagram
    participant W as Worker (convert/organize/optimize)
    participant NATS as NATS JetStream (JOBS_EVENTS)
    participant DS as document-service subscriber
    participant PG as PostgreSQL

    W->>NATS: publish jobs.events.<jobId>.completed {EventType:JobCompleted, userId, jobId, outputPath, toolType, fileSize}
    NATS->>DS: deliver (durable document-job-events, filter jobs.events.>)
    DS->>DS: skip if not JobCompleted or userId empty (ack)
    DS->>PG: COUNT documents WHERE (user_id, source_job_id) [Unscoped]
    alt already finalized (incl. soft-deleted)
        DS->>NATS: Ack (idempotent skip)
    else new
        DS->>PG: SELECT job_workspace_hints WHERE job_id (→ orgID or nil)
        DS->>PG: INSERT documents (source_job_id, storage_path=outputPath, status=ready, organization_id=orgID)
        opt org-scoped
            DS->>PG: DELETE job_workspace_hints WHERE job_id
        end
        DS->>NATS: Ack
    end
```

## Set Workspace Hint (file future job into an org)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant DS as document-service :8089
    participant US as user-service :8090
    participant PG as PostgreSQL

    Client->>GW: POST /api/documents/workspace-hint {jobId, organizationId}
    GW->>DS: Proxy (X-User-ID)
    alt organizationId empty
        DS->>PG: DELETE job_workspace_hints WHERE job_id, user_id
        DS-->>Client: 200 "Workspace hint cleared"
    else org given
        DS->>US: GET /api/orgs/:id (require editor+)
        alt role < editor
            DS-->>Client: 403 FORBIDDEN
        else editor+
            DS->>PG: UPSERT job_workspace_hints (job_id pk)
            DS-->>Client: 200 "Workspace hint set"
        end
    end
```

## Create + Download Export

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant DS as document-service :8089
    participant PG as PostgreSQL
    participant NS as notification-service :8091

    Client->>GW: POST /api/exports {format: csv|json, organizationId?, status?, folderId?, tagId?}
    GW->>DS: Proxy (X-User-ID)
    DS->>PG: INSERT exports (status=queued)
    DS->>DS: generate CSV/JSON of documents in scope (async, in-process v1)
    DS->>PG: UPDATE exports SET content, status=ready, completed_at
    DS-->>NS: POST /internal/notifications (export.ready)
    DS-->>Client: 201 {export: status queued}

    Client->>GW: GET /api/exports/:id (poll)
    GW->>DS: Proxy
    DS-->>Client: 200 {status: ready}
    Client->>GW: GET /api/exports/:id/download
    GW->>DS: Proxy
    DS-->>Client: 200 (artifact bytes from exports.content)
```
