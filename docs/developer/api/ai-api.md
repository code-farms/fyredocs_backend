# AI API (ai-service)

Base URL: `http://localhost` (the Caddy edge; the api-gateway is internal-only)

The gateway forwards `/api/ai/*` (and `/api/v1/ai/*`) to `ai-service:8092`. **Sign-in is required**: guests get `401 AUTH_UNAUTHORIZED`. Non-stream responses and every pre-stream error use the standard envelope `{success, message, data, error, meta}`.

---

## POST /api/ai/ops/:op
Run an AI op over document text. `:op` is one of `summarize`, `explain`, `ask`, `translate`, `rewrite`, `keyPoints`, `actionItems`, `notes`, `flashcards`. Prompt templates are server-owned; the body carries inputs only.

**Request**
```json
{
  "pages": [{ "index": 0, "text": "Page one text…" }, { "index": 1, "text": "…" }],
  "question": "What is the deadline?",
  "history": [{ "role": "user", "text": "…" }, { "role": "assistant", "text": "…" }],
  "targetLang": "Hindi",
  "passage": "Text to rewrite",
  "docRef": "9f2c…(64 hex)"
}
```
- `docRef` may replace `pages`: send the `docRef` returned by an earlier op on the same document (per user, sliding 30-minute TTL). If it has expired you get `410 AI_DOCUMENT_EXPIRED`; resend `pages`. `pages` win when both are sent.
- `pages[].index` is 0-based; the model sees and cites pages 1-based (`[p.1]`).
- `question` is required for `ask`; `history` is used only by `ask`.
- `targetLang` is required for `translate`; `passage` is required for `rewrite` (`pages` optional).
- Limits: ≤50 pages, ≤250,000 document chars, question ≤2,000, passage ≤20,000, ≤20 history turns of ≤8,000 chars, body ≤1 MiB.

**200 OK, streaming** (`Accept: text/event-stream`)
```
data: {"type":"delta","text":"The contract "}

data: {"type":"delta","text":"ends on 1 March [p.3]."}

data: {"type":"done","usage":{"inputTokens":1830,"outputTokens":14,"cachedTokens":1536},"docRef":"9f2c…"}
```
A provider failure after the first delta arrives as `data: {"type":"error","code":"AI_UNAVAILABLE","message":"…"}`.

**200 OK, JSON** (any other `Accept`)
```json
{
  "success": true,
  "message": "OK",
  "data": { "text": "The contract ends on 1 March [p.3].", "usage": { "inputTokens": 1830, "outputTokens": 14, "cachedTokens": 1536 }, "docRef": "9f2c…" }
}
```

**Errors**
| Status | `error.code` | When |
|--------|--------------|------|
| 400 | `INVALID_INPUT` | malformed body, missing op-specific field, bad history role |
| 401 | `AUTH_UNAUTHORIZED` | guest or not signed in |
| 404 | `NOT_FOUND` | unknown op |
| 410 | `AI_DOCUMENT_EXPIRED` | `docRef` no longer cached; resend `pages` |
| 413 | `AI_REQUEST_TOO_LARGE` | over the limits above, or the model's context |
| 429 | `AI_QUOTA_EXCEEDED` | this month's token quota is used (the provider is not called) |
| 503 | `AI_UNAVAILABLE` | provider not configured, down, or breaker open |

---

## GET /api/ai/quota
The caller's AI token quota for the current UTC month. Each op reserves its worst case (input estimate + output cap) before running, so `remainingTokens` already excludes in-flight holds.

**200 OK**
```json
{
  "success": true,
  "message": "OK",
  "data": {
    "plan": "free",
    "period": "2026-11",
    "limitTokens": 300000,
    "usedTokens": 41250,
    "reservedTokens": 0,
    "remainingTokens": 258750,
    "resetsAt": "2026-12-01T00:00:00Z"
  }
}
```
**401** `AUTH_UNAUTHORIZED` for guests; **503** `AI_UNAVAILABLE` if the ledger is unreachable.
