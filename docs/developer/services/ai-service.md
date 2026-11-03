# AI Service

## Service Responsibility
The **server-side proxy for every AI feature** (mobile Phase 23). It owns the op catalog, the prompt templates, input limits and (with the provider client) the per-user token quota. It talks to the model provider over the **OpenAI-compatible Chat Completions API**, so the provider and the models are configuration, not code. No client ever holds a provider key: a key in a mobile bundle or device keystore is an extractable key.

## Design Constraints
- Independent microservice: own schema (`ai_usage`), own module (`ai-service`), no cross-service DB access or shared models.
- **Templates are server-owned.** Clients send inputs only (`pages`, `question`, `history`, `targetLang`, `passage`). There is no free-form prompt op, so the endpoint cannot be used as a general-purpose LLM proxy on our bill.
- **Provider-neutral.** Only portable Chat Completions features are used (system/user/assistant messages, streaming, `max_tokens`): no tools, no JSON mode, no vendor cache APIs. Answers use plain-text conventions (`[p.N]` citations, `- ` bullets, `Q:`/`A:` flashcards) that the app parses tolerantly.
- **Sign-in only.** Guests and anonymous callers get `401 AUTH_UNAUTHORIZED` before any input is read; the signed-in identity is what quota is charged to.
- Identity from the `AuthContext` resolved by the auth middleware, which **verifies the JWT locally** (`AUTH_TRUST_GATEWAY_HEADERS=false`). A forged `X-User-ID` would otherwise spend another user's quota.
- Standard response envelope (`shared/response`) for every non-stream response and every pre-stream error.

## Internal Architecture
- `main.go`: config/logger/telemetry, DB connect + migrate, NATS (publish-only, **non-fatal**: analytics is disabled with a warning rather than failing AI answers), Redis denylist, gin + metrics, graceful shutdown. Port `8092`, `streaming=true` server timeouts.
- `handlers/ops.go`: `Ops.Run` validates, builds the prompt and streams. `RequireUser` gates `/api/ai`.
- `internal/ops/`: the op catalog (op → model tier + task template), input limits, `FormatDocument` (1-based `<<page N>>` markers) and `Build`.
- `internal/llm/`: the `Client` interface, `Usage`, and the classified `Error{Code}`. `Unconfigured` (the current client) always returns `AI_UNAVAILABLE`.
- `internal/sse/`: frame writer with **lazy header commit**, so an error before the first delta is still a normal JSON error with its HTTP status.
- `internal/models/`, `internal/migrations/`: the `ai_usage` ledger.

**No NATS consumer.** The backend publishes no user or account lifecycle event a consumer could react to (e.g. purging ledger rows on account deletion). Adding an empty consumer to satisfy a checklist would be dead code; one lands when such an event exists.

### Prompt layout
`[system: preamble + <document>…</document>]` → `[ask history]` → `[user: op task]`. The document sits in the shared prefix for **every** op, so a provider's automatic prefix caching can reuse it across ops and Ask turns. The preamble tells the model to treat document content as data and ignore instructions inside it (prompt-injection posture for user-supplied documents).

### Ops
| Op | Tier | Required input |
|----|------|----------------|
| summarize, keyPoints, actionItems, notes, flashcards | fast | `pages` |
| translate | fast | `pages`, `targetLang` (≤40 chars, single line) |
| explain | smart | `pages` |
| ask | smart | `pages`, `question`; optional `history` (user/assistant turns) |
| rewrite | smart | `passage`; `pages` optional context |

### Limits
At most 50 pages (mirrors the app's `AI_MAX_PAGES`). At most 250,000 document characters, which keeps a worst-case 3-byte-UTF-8 document under the gateway's 1 MiB proxy body limit. Question ≤2,000 chars, passage ≤20,000, history ≤20 turns of ≤8,000 chars. The service applies the same 1 MiB body cap itself, for mesh callers. Size failures are `413 AI_REQUEST_TOO_LARGE`; shape failures are `400 INVALID_INPUT`.

## Routes
Health: `GET /healthz`, `GET /readyz` (Postgres only: the provider is deliberately not a readiness dependency, because pulling the pod would turn a specific `AI_UNAVAILABLE` into a generic gateway 502).

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/ai/ops/:op` | Run an op. `Accept: text/event-stream` streams SSE frames; otherwise the answer is collected into the envelope. |

SSE frames (the shape of the app's `AiEvent`, `fyredocs_app/src/lib/ai/provider.ts`): `{"type":"delta","text"}`, then `{"type":"done","usage":{inputTokens,outputTokens,cachedTokens}}`, or `{"type":"error","code","message"}` if the provider fails mid-stream.

## Error Flows
| Situation | Response |
|-----------|----------|
| guest / anonymous | 401 `AUTH_UNAUTHORIZED` |
| unknown op | 404 `NOT_FOUND` |
| bad JSON / missing field | 400 `INVALID_INPUT` |
| over limits, or the model's context | 413 `AI_REQUEST_TOO_LARGE` |
| provider unconfigured, down, or unclassified | 503 `AI_UNAVAILABLE` (the cause is logged, **never** sent: provider error bodies can echo request content or account details) |
| failure after the first delta | SSE `error` frame (the 200 is already on the wire) |

## DB Schema (own Postgres)
- **ai_usage**: `subject` (text, `user:<id>` today), `period` (UTC YYYYMM), `input_tokens`, `output_tokens`, `cached_tokens`, `reserved_tokens`, `updated_at`; PK `(subject, period)`. `subject` is text so per-org quota (`org:<id>`) needs no schema change once the gateway carries an org identity. Versioned migrations (`goose_db_version_ai_service`) are the authority; see [schema-migrations.md](../architecture/schema-migrations.md).

## Analytics
Each completed op publishes `analytics.events.ai.op.completed` (`toolType` = op, `planName`, metadata `{tier, inputTokens, outputTokens, cachedTokens, durationMs}`). **It never carries document text or answers.** A publish failure is logged and never fails the op.

## Environment Variables
| Variable | Default | Description |
|----------|---------|-------------|
| PORT | 8092 | Service port |
| DATABASE_URL / DATABASE_URL_DIRECT | — | PostgreSQL (required) / direct connection for migrations |
| NATS_URL | nats://nats:4222 | Analytics publishing (optional at runtime) |
| REDIS_ADDR / REDIS_PASSWORD | redis:6379 | JWT denylist |
| JWT_HS256_SECRET, JWT_ALLOWED_ALGS, JWT_ISSUER, JWT_AUDIENCE, JWT_CLOCK_SKEW | — | Local token verification |
| AUTH_DENYLIST_ENABLED | true | Check revoked tokens |
| AUTH_TRUST_GATEWAY_HEADERS | false | **Keep false** — see Design Constraints |
| AI_MAX_OUTPUT_TOKENS | 4096 | Output cap per op |
| TRUSTED_PROXIES, OTEL_EXPORTER_OTLP_ENDPOINT | — | Standard |

## Gateway / Deployment
- The gateway routes `/api/ai` (+ `/api/v1/ai`) → `AI_SERVICE_URL` (`http://ai-service:8092`). The proxy's `FlushInterval -1` passes SSE through unbuffered, and the SSE writer sets `X-Accel-Buffering: no` for the edge.
- Compose: `ai-service` behind the **`ai` profile** (off by default, like notification-service). Enable with `--profile ai` or `./deployment/deploy.sh ai-service`. A standalone file is `deployment/docker-compose-ai-service.yml`.
- Not in any k6 scenario: every op spends real provider tokens.

## Scaling Constraints
Stateless apart from the ledger: replicas scale horizontally. Each op holds a DB connection only for quota reads and writes, never across the model stream, so the pool is small (10). Long streams hold an HTTP connection (WriteTimeout unset), so capacity is bound by concurrent streams and the provider's rate limits, not CPU.

## Roadmap (Phase 23)
- **Chunk C:** the OpenAI-compatible client (`AI_BASE_URL`, `AI_API_KEY`, `AI_MODEL_FAST`, `AI_MODEL_SMART`) behind `shared/circuitbreaker`, plus the reserve/settle quota ledger and `GET /api/ai/quota`.
- **Chunk D:** optional native-PDF input (`AI_PDF_INPUT`), cached-token accounting, and Ask session document cache.
