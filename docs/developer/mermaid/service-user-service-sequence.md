# User Service -- Sequence Diagrams

Request flows through the `user-service` (port 8090). Identity is the gateway-injected `X-User-ID`; org-level RBAC comes from the caller's membership role.

## Create Organization

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant US as user-service :8090
    participant PG as PostgreSQL

    Client->>GW: POST /api/orgs {name}
    GW->>GW: Verify JWT · inject X-User-ID
    GW->>US: Proxy
    US->>US: RequireUser · validate name · slugify(name)
    US->>PG: INSERT organizations (owner_user_id=caller, plan_name='free')
    US->>PG: INSERT memberships (org, caller, role='owner')
    US-->>Client: 201 {organization, role: "owner"}
```

## Add / Update Member (admin+)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant US as user-service :8090
    participant PG as PostgreSQL

    Client->>GW: POST /api/orgs/:id/members {userId, role}
    GW->>US: Proxy (X-User-ID)
    US->>PG: SELECT caller's membershipRole in org
    alt caller role < admin
        US-->>Client: 403 FORBIDDEN
    else admin+
        US->>US: validate role in {admin, editor, viewer}
        alt membership exists
            US->>PG: UPDATE memberships SET role (never the owner)
            US-->>Client: 200 "Member updated"
        else new
            US->>PG: INSERT memberships (org, userId, role)
            US-->>Client: 201 "Member added"
        end
    end
```

## RBAC Check from document-service (mesh call)

```mermaid
sequenceDiagram
    participant DS as document-service :8089
    participant US as user-service :8090
    participant PG as PostgreSQL

    Note over DS: org-scoped request needs the caller's role
    DS->>US: GET /api/orgs/:id (X-User-ID forwarded)
    US->>PG: SELECT membership WHERE org_id, user_id
    alt not a member
        US-->>DS: 404 NOT_FOUND (existence not leaked)
    else member
        US-->>DS: 200 {organization, role}
        Note over DS: enforce viewer+ (read) / editor+ (write)
    end
```
