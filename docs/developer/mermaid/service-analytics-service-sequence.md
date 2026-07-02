# Analytics Service -- Sequence Diagrams

Request and event flows through the `analytics-service` (port 8087). It ingests events from two NATS streams and serves role-aware dashboards + super-admin metrics.

## Event Ingestion (NATS subscribers)

```mermaid
sequenceDiagram
    participant Pub as auth-service / job-service / workers
    participant AS as ANALYTICS stream (analytics.events.>)
    participant JE as JOBS_EVENTS stream (jobs.events.>)
    participant SUB as analytics subscriber
    participant PG as PostgreSQL

    par custom analytics events
        Pub->>AS: publish analytics.events.<type><br/>(user.signup/login, plan.changed, job.created, plan.limit_hit, user.proxy_login)
        AS->>SUB: deliver (durable analytics-service)
        SUB->>PG: INSERT analytics_events
        SUB->>AS: Ack
    and job lifecycle events
        Pub->>JE: publish jobs.events.<jobId>.{completed,failed}
        JE->>SUB: deliver (durable analytics-job-events)
        SUB->>SUB: map JobCompleted/JobFailed
        SUB->>PG: INSERT analytics_events
        SUB->>JE: Ack
    end
```

## Unified Dashboard (role-aware, cached)

```mermaid
sequenceDiagram
    participant Client
    participant GW as api-gateway :8080
    participant AN as analytics-service :8087
    participant RD as Redis (dashboard cache)
    participant PG as PostgreSQL

    Client->>GW: GET /api/dashboard?days=30
    GW->>GW: verify JWT · inject X-User-ID, X-User-Role
    GW->>AN: Proxy
    alt role == guest
        AN-->>Client: 403 (guests rejected)
    else authenticated
        AN->>AN: key = cache:dashboard:v1:{admin | user:<uid>}:d<days>
        AN->>RD: GET key
        alt cache hit
            RD-->>AN: payload
            AN-->>Client: 200 (served from cache)
        else miss (or Redis error → fall through)
            alt admin / super-admin
                AN->>PG: compute system-wide KPIs
            else user
                AN->>PG: compute personal KPIs (scoped to user_id)
            end
            AN->>RD: SET key EX DASHBOARD_CACHE_TTL (default 30s)
            AN-->>Client: 200 {role, ...KPIs}
        end
    end
```

## Admin Metrics Query (super-admin)

```mermaid
sequenceDiagram
    participant Admin
    participant GW as api-gateway :8080
    participant AN as analytics-service :8087
    participant PG as PostgreSQL

    Admin->>GW: GET /admin/metrics/<name>?days=30
    GW->>AN: Proxy (X-User-Role)
    AN->>AN: adminAuth() — require super-admin
    alt not super-admin
        AN-->>Admin: 403 FORBIDDEN
    else authorized
        AN->>PG: aggregate over analytics_events / daily_metrics
        AN-->>Admin: 200 {metrics}
    end
```
