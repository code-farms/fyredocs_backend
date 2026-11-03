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

---

## POST /api/ai/extract
Classify a document, suggest a title, and extract typed fields and tables in one call. Body ≤4 MiB.

**Request**
```json
{
  "pages": [{ "index": 0, "text": "BIG BAZAAR … TOTAL 1,250.00" }],
  "images": ["data:image/jpeg;base64,/9j/4AAQ…"]
}
```
- At least one of `pages` or `images`. At most 2 images (JPEG/PNG/WebP data URLs, ≤1.5 MB each decoded); downscale to ~1600px first.
- With `AI_VISION=false`, or a text-only model, images are ignored and the text is used.

**200 OK**
```json
{
  "success": true,
  "data": {
    "docType": "receipt",
    "confidence": 0.94,
    "title": "Big Bazaar groceries March 2026",
    "language": "en",
    "fields": { "merchant": "Big Bazaar", "date": "2026-03-12", "total": 1250, "tax": 60, "currency": "INR", "category": "groceries" },
    "tables": [{ "header": ["Item", "Qty"], "rows": [["Rice", "2"]] }],
    "usage": { "inputTokens": 1900, "outputTokens": 120, "cachedTokens": 0 }
  }
}
```
`docType` ∈ `invoice, receipt, id_document, business_card, form, letter, contract, statement, other`; `subtype` (id_document only) ∈ `passport, aadhaar, pan, driver_license, voter_id, business_license, other_id`. Each type's fields are listed in `ai-service/internal/extract/schema.go`. A missing field means it was not found; values are never guessed (an ambiguous date is omitted).

**Errors:** 400 `INVALID_INPUT` (no input, a non-data-URL or unsupported image type, bad base64), 401, 413 `AI_REQUEST_TOO_LARGE` (over 2 images or 1.5 MB, or the page/char limits), 429 `AI_QUOTA_EXCEEDED`, 503 `AI_UNAVAILABLE`.

---

## POST /api/ai/transcribe
Handwriting (and printed) text from page images, verbatim. Body ≤4 MiB.

**Request**
```json
{ "images": ["data:image/jpeg;base64,/9j/4AAQ…"], "language": "eng+hin" }
```
- 1–2 images (JPEG/PNG/WebP data URLs, ≤1.5 MB each decoded). `language` is optional: `eng`, `hin` or `eng+hin`.

**200 OK**
```json
{ "success": true, "data": { "pages": ["Dear Asha,\nthanks for the [illegible] notes"], "usage": { "inputTokens": 1620, "outputTokens": 18, "cachedTokens": 0 } } }
```
One string per image, in order. An empty string means no text was found on that page.

**Errors:** 400 `INVALID_INPUT` (no images, bad data URL or type, unknown language), 401, 413 `AI_REQUEST_TOO_LARGE` (over 2 images or 1.5 MB), 429 `AI_QUOTA_EXCEEDED`, 503 `AI_UNAVAILABLE` (also when the configured model has no vision: transcription never falls back to text-only).
