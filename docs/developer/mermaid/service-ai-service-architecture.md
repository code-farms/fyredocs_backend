# AI Service -- Architecture

Internal structure of `ai-service` (port 8092): the server-side proxy for AI features. Owns the op catalog and prompt templates, and streams model answers over SSE.

## Component Diagram

```mermaid
graph TB
    subgraph ai-service[" ai-service :8092 "]
        direction TB

        subgraph Middleware["Middleware Chain (Gin)"]
            TRACE["OpenTelemetry · GinTraceMiddleware"]
            METRICS["Prometheus · GinMetricsMiddleware"]
            REQID["GinRequestID · GinRequestLogger"]
            AUTH["GinAuthMiddleware<br/>(local JWT verify)"]
            REQUSER["RequireUser<br/>(guests → 401)"]
        end

        subgraph Routes["Routes"]
            OPS["POST /api/ai/ops/:op<br/>(SSE or JSON)"]
            HEALTHZ["/healthz · /readyz"]
            METRICSEP["/metrics"]
        end

        subgraph Core["Op pipeline"]
            CAT["internal/ops<br/>catalog · templates · limits ·<br/>FormatDocument · Build"]
            SSEW["internal/sse<br/>lazy-commit frame writer"]
            LLM["internal/llm.Compat<br/>OpenAI-compatible stream ·<br/>circuit breaker"]
            QUOTA["internal/quota<br/>atomic reserve → settle"]
        end

        subgraph Models["internal/models (GORM)"]
            USAGE["ai_usage ledger"]
        end
    end

    GW["api-gateway<br/>/api/ai → :8092"] --> TRACE
    TRACE --> METRICS --> REQID --> AUTH --> REQUSER --> OPS
    OPS --> QUOTA --> USAGE
    OPS --> CAT --> LLM
    OPS --> SSEW
    LLM -.->|Chat Completions| PROVIDER["Model provider<br/>(any OpenAI-compatible endpoint)"]
    USAGE --- PG[("PostgreSQL")]
    OPS -.->|analytics.events.ai.op.completed| NATS[("NATS JetStream<br/>ANALYTICS")]
    AUTH -.->|denylist| REDIS[("Redis")]
```

## Notes
- **Publish-only on NATS.** No consumer exists, because the backend emits no user-lifecycle event to react to.
- The provider is not a readiness dependency: `/readyz` checks Postgres only.
