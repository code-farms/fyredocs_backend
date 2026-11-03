# AI Service

## Service Responsibility
The **server-side proxy for every AI feature** (mobile Phase 23). It owns the op catalog, the prompt templates, input limits and the per-user token quota. It talks to the model provider over the **OpenAI-compatible Chat Completions API**, so the provider and the models are configuration, not code. No client ever holds a provider key: a key in a mobile bundle or device keystore is an extractable key.

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
- `internal/llm/`: the `Client` interface, `Usage`, and the classified `Error{Code}`. `Compat` (`compat.go`) is the only implementation: streaming Chat Completions through `github.com/openai/openai-go/v3`, pointed at whatever `AI_BASE_URL` names. It uses `stream_options.include_usage` and falls back to a chars/4 estimate when a provider omits usage. Errors are classified **by HTTP status and wording, never by vendor type**: 413, or a 400 mentioning the context window → `AI_REQUEST_TOO_LARGE`; everything else → `AI_UNAVAILABLE`. A `shared/circuitbreaker` TwoStep trips on provider failures only (not oversize input or client disconnects). With no endpoint or model configured, `New` returns `Unconfigured`.
- `internal/quota/`: the reserve/settle ledger (below).
- `internal/doccache/`: the per-user document cache behind `docRef` (below).
- `internal/extract/`: document understanding (below): doc types, per-type field schemas, the extraction prompt, and a tolerant parser and normalizers.
- `handlers/extract.go`: `POST /api/ai/extract`.
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
| POST | `/api/ai/extract` | Classify + title + typed fields + tables from page text and/or ≤2 page images (see Document understanding). JSON only, not streamed. |
| GET | `/api/ai/quota` | Caller's plan, month, limit, used, reserved and remaining tokens, and `resetsAt`, for the app's pre-run cost sheet. |

SSE frames (the shape of the app's `AiEvent`, `fyredocs_app/src/lib/ai/provider.ts`): `{"type":"delta","text"}`, then `{"type":"done","usage":{inputTokens,outputTokens,cachedTokens}}`, or `{"type":"error","code","message"}` if the provider fails mid-stream.

## Error Flows
| Situation | Response |
|-----------|----------|
| guest / anonymous | 401 `AUTH_UNAUTHORIZED` |
| unknown op | 404 `NOT_FOUND` |
| bad JSON / missing field | 400 `INVALID_INPUT` |
| over limits, or the model's context | 413 `AI_REQUEST_TOO_LARGE` |
| `docRef` no longer cached | 410 `AI_DOCUMENT_EXPIRED`: resend `pages` |
| month's quota used | 429 `AI_QUOTA_EXCEEDED` (the provider is never called) |
| quota ledger unreachable | 503 `AI_UNAVAILABLE`: **fails closed**, since answering unmetered spends money no limit accounts for |
| provider unconfigured, down, or unclassified | 503 `AI_UNAVAILABLE` (the cause is logged, **never** sent: provider error bodies can echo request content or account details) |
| failure after the first delta | SSE `error` frame (the 200 is already on the wire) |

## DB Schema (own Postgres)
- **ai_usage**: `subject` (text, `user:<id>` today), `period` (UTC YYYYMM), `input_tokens`, `output_tokens`, `cached_tokens`, `reserved_tokens`, `updated_at`; PK `(subject, period)`. `subject` is text so per-org quota (`org:<id>`) needs no schema change once the gateway carries an org identity. Versioned migrations (`goose_db_version_ai_service`) are the authority; see [schema-migrations.md](../architecture/schema-migrations.md).

### Document reuse and prompt caching
Every successful op with `pages` returns a `docRef` (the sha256 of the page list) in its `done` frame or JSON result. The next request may send `docRef` **instead of** `pages`: a multi-turn Ask stops re-uploading up to ~1 MiB of page text per turn from a phone. Entries live in Redis under `ai:doc:<userId>:<ref>` with a sliding `AI_DOC_CACHE_TTL` (30m). They are scoped per user, because a content hash alone would let one user read another's document by hashing common text. A miss (expired, evicted, another user's ref, or Redis down) is `410 AI_DOCUMENT_EXPIRED`, and the client resends the pages. Pages in the body always win over a ref.

The cache also pins the document **byte-identically** across turns, which is the condition for provider-side automatic prefix caching: the document is the stable system prefix of every op. Hits come back as `cached_tokens` in usage and are recorded in the ledger. Only this portable mechanism is used, with no vendor cache API.

Native PDF input (sending the PDF file itself) is **deliberately not built**. The app already extracts digital PDFs' text on-device and offline, so it removes no pipeline stage. File-input support also differs across OpenAI-compatible providers, which would break the configuration-only provider swap.

### Document understanding (`POST /api/ai/extract`)
One call does classification, a suggested title and typed field extraction, which is cheaper than three ops. **Input:** page text (≤50 pages, the same limits as ops) and/or up to **2 page images** (JPEG/PNG/WebP data URLs, ≤1.5 MB each, downscaled by the app). Images travel as the standard `image_url` content part.
- **Doc types:** `invoice, receipt, id_document (subtype passport/aadhaar/pan/driver_license/voter_id/business_license/other_id), business_card, form, letter, contract, statement, other`.
- **Schemas live in Go** (`internal/extract/schema.go`). The model is asked for JSON in plain text (no JSON mode, for portability). Everything it returns is untrusted:
  - fields outside the doc type's schema are dropped;
  - dates become ISO, and an ambiguous `03/04/2026` is **dropped, never guessed**;
  - amounts accept `₹1,23,456.50` and `1.234,50`;
  - currency becomes ISO 4217 (`¥` stays unmapped: JPY or CNY);
  - emails, phones and URLs are validated;
  - tables are padded or truncated to the header width.
  - An unparseable reply returns `docType: "other"` with 200, never a 500.
- **Vision is env-gated:** with `AI_VISION=false`, images are stripped. A provider that rejects image parts with a 400 is retried **once on the text alone**, so a text-only model still extracts from OCR text with no code change. Each image is reserved at a flat 1,500 tokens; settle records the real usage.
- **PII posture:** extracted values (ID numbers, names, addresses) are never logged, cached or persisted server-side. The `ai.op.completed` event for extract carries the **doc type only**, and the result goes back to the device.
- **Gateway:** the route has its own 4 MiB body cap. The `/api/ai/extract` prefix is more specific than `/api/ai`, so every other AI op keeps 1 MiB.

## Quota
Per user and per UTC month. Plan limits are `AI_QUOTA_FREE_TOKENS` / `AI_QUOTA_PRO_TOKENS`; unknown plans get the free limit, as the gateway rate limiter does, and 0 disables a plan. Each op **reserves its worst case** (estimated input + the output cap) in a single `INSERT … ON CONFLICT … DO UPDATE … WHERE used + held + new <= limit` statement. The check and the increment are atomic, so concurrent requests cannot jointly overrun. After the stream the op **settles** to the provider's real usage (input + output count; cached tokens are recorded for visibility) on a context detached from the client, so a disconnect cannot leak the hold. A failure before any output settles at zero; a failure after output was streamed is charged by estimate. Known gap: a process crash between reserve and settle leaks that hold until the month rolls over.

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
| AI_BASE_URL | — | OpenAI-compatible endpoint (empty → every op `AI_UNAVAILABLE`) |
| AI_API_KEY | — | Provider key (may be empty for local servers like Ollama) |
| AI_MODEL_FAST | — | Model for summarize/translate/keyPoints/actionItems/notes/flashcards |
| AI_MODEL_SMART | = FAST | Model for ask/explain/rewrite |
| AI_MAX_TOKENS_PARAM | max_tokens | `max_completion_tokens` for newer OpenAI reasoning models |
| AI_REQUEST_TIMEOUT | 90s | Per provider request |
| AI_MAX_RETRIES | 1 | Provider retries (429/5xx) |
| AI_MAX_OUTPUT_TOKENS | 4096 | Output cap per op |
| AI_QUOTA_FREE_TOKENS / AI_QUOTA_PRO_TOKENS | 300000 / 5000000 | Monthly per-user token limits |
| AI_DOC_CACHE_TTL | 30m | Sliding TTL of the `docRef` document cache |
| AI_VISION | true | Allow image input for extract; false for text-only models |
| TRUSTED_PROXIES, OTEL_EXPORTER_OTLP_ENDPOINT | — | Standard |

## Gateway / Deployment
- The gateway routes `/api/ai` (+ `/api/v1/ai`) → `AI_SERVICE_URL` (`http://ai-service:8092`). The proxy's `FlushInterval -1` passes SSE through unbuffered, and the SSE writer sets `X-Accel-Buffering: no` for the edge.
- Compose: `ai-service` behind the **`ai` profile** (off by default, like notification-service). Enable with `--profile ai` or `./deployment/deploy.sh ai-service`. A standalone file is `deployment/docker-compose-ai-service.yml`.
- Not in any k6 scenario: every op spends real provider tokens.

## Scaling Constraints
Stateless apart from the ledger: replicas scale horizontally. Each op holds a DB connection only for quota reads and writes, never across the model stream, so the pool is small (10). Long streams hold an HTTP connection (WriteTimeout unset), so capacity is bound by concurrent streams and the provider's rate limits, not CPU.

## Roadmap (Phase 23)
