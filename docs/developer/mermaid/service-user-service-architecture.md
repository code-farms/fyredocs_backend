# User Service -- Architecture

Internal structure and component diagram of the `user-service` (port 8090). Owns organizations, memberships, and the RBAC role model. No NATS.

## Component Diagram

```mermaid
graph TB
    subgraph user-service[" user-service :8090 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry · GinTraceMiddleware"]
            METRICS["Prometheus · GinMetricsMiddleware"]
            REQID["GinRequestID"]
            LOGGER["GinRequestLogger"]
            REQUSER["RequireUser<br/>(X-User-ID from gateway)"]
        end

        subgraph Routes["/api/orgs (auth-required)"]
            LIST["GET / · POST /"]
            ONE["GET /:id"]
            MEMBERS["GET /:id/members · POST /:id/members"]
            MEMBER["PATCH · DELETE /:id/members/:userId"]
            HEALTHZ["/healthz · /readyz"]
            METRICSEP["/metrics"]
        end

        subgraph Handlers
            OH["orgs.go<br/>(ListOrganizations · CreateOrganization · GetOrganization<br/>ListMembers · AddMember · UpdateMemberRole · RemoveMember)"]
            AUTHH["auth.go<br/>(userID · membershipRole · slugify · RoleAtLeast)"]
        end

        subgraph RBAC["RBAC role model"]
            ROLES["owner &gt; admin &gt; editor &gt; viewer<br/>member-management requires admin+"]
        end

        subgraph Models["internal/models (GORM)"]
            ORG["organizations (id, name, slug uniq, owner_user_id, plan_name)"]
            MEM["memberships (org_id, user_id, role)<br/>unique (org_id, user_id)"]
        end
    end

    Client["api-gateway :8080<br/>(also document-service for RBAC checks)"] --> TRACE --> METRICS --> REQID --> LOGGER --> REQUSER
    REQUSER --> Routes
    Routes --> OH
    OH --> AUTHH
    AUTHH --> ROLES
    OH --> ORG
    OH --> MEM
    ORG & MEM --> PG[(PostgreSQL)]
```

## Dependency Graph

```mermaid
graph LR
    US[user-service] --> |shared/config| Config
    US --> |shared/logger| Logger
    US --> |shared/metrics| Metrics
    US --> |shared/telemetry| Telemetry
    US --> |shared/response| Response
    US --> |internal/models| Models
    Models --> |gorm + pgx| PG[(PostgreSQL)]
```
