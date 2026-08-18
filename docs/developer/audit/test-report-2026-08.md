# Backend Test Report — August 2026

Final report of the production testing program (2026-08-17 → 2026-08-19).
Companion documents: `docs/developer/architecture/testing-strategy.md` (the
living contract: taxonomy, runners, conventions, wave log) and
`docs/developer/architecture/ci-pipeline.md` (vendor-neutral CI stages + Argo
wiring).

## 1. Before / after

| Metric | Before (2026-08-17) | After |
|---|---:|---:|
| Test files | 151 | 190 |
| Test functions | 790 | 1,042 |
| Benchmarks | 0 | 8 |
| Fuzz targets | 0 | 10 (+ seed corpora, incl. one kept crasher) |
| `t.Parallel` tests | 0 | 75 (surgical, proven at `-race -count=2 -shuffle=on`) |
| Integration-tagged files | 0 (2 ad-hoc `t.Skip` gates) | 13 (both legacy gates migrated) |
| DB-path tests | 0 | every DB-backed service (real Postgres via `shared/database/dbtest`) |
| Real-binary exec tests | 0 (1 skip-gated) | 16 fns dockerized (`make test-binaries`) |
| Runner flags | none | `-race -count=1 -shuffle=on -timeout=10m` everywhere |
| Coverage tooling | none | `make cover` (+ `COVER_TAGS=integration`), merged profile |
| Lint | none | `make lint` (gofmt + vet) + pinned `.golangci.yml` |
| CI | none | `scripts/ci/00–07` stages + Argo WorkflowTemplate doc |

## 2. Production bugs found by testing and FIXED (regression-tested)

1. **Validation bypass for 5 routable tools** — `pdf-to-docx/xlsx/pptx/txt`
   and `html-to-pdf` were in `routing.ToolServiceMap` but missing from
   `validateFileType`/`mimeCategory`; the deliberate MIME fail-open meant jobs
   under those names skipped ALL extension and content checks
   (`job-service/handlers/jobs.go`). Guarded forever by
   `TestEveryRoutedToolIsValidated`.
2. **Dot-segment traversal in backup registration** — `storagePathAllowed`
   was prefix-only, so `backups/<me>/../<other>/f.pdf` passed ownership
   (`document-service/handlers/documents.go`); now rejects `..` segments.
3. **Password-reset lockout at the gateway** — `publicAuthPaths()` omitted
   forgot/reset-password, so a stale `access_token` cookie 401'd exactly the
   users needing a reset (`api-gateway/main.go`). Also corrected doc drift
   claiming `/api/v1/share/` was public (it must not be — revoke group).
4. **Upload-session IDOR at consumption** — finding F4's owner binding was
   enforced on the multipart endpoints but never at `consumeUpload`, so any
   caller who learned a live uploadId could pull another user's bytes into
   their own job/share/backup. Consumption now takes the caller identity;
   denials answer "upload not found" (no probing oracle).
5. **Tagged documents permanently unpurgeable** — `PurgeDocument` deleted the
   row before its `document_tags` rows; `fk_document_tags_document` made every
   purge of a tagged document a 500. Associations now go first.
6. **In-use tags undeletable** — same FK ordering in `DeleteTag`; fixed with a
   scoped ownership fetch first (a blind association delete would have let
   non-owners strip foreign tags).
7. **DSN corruption on re-application** — found by fuzzing in its first
   seconds: `ApplyPostgresDSNDefaults("postgres://")` collapsed the URI shape
   and duplicated params on the second pass (`shared/config`); host-less URIs
   now pass through. Crasher kept as a regression seed.
8. **Nil-JetStream panic after job commit** — `queue.PublishJobEvent` (and
   `PublishRaw`/`PublishAnalyticsEvent`) dereferenced a nil handle; a NATS
   outage panicked AFTER the job row committed → stranded row + duplicate on
   retry. All three publishers now return errors; the job handler's own
   controlled 500 preserves the upload for retry.

Also fixed: a genuine ~5% flake (JWT tamper test flipped a base64url padding
bit the decoder ignores), the four sleep-based worker drain tests (now
`testing/synctest`), and `parsePageRange` comment rot.

## 3. Pinned gaps — recommended follow-ups (each carries a NOTE-annotated test)

| # | Gap | Where | Risk |
|---|---|---|---|
| 1 | `VerifyRefreshToken` accepts an access token (no token-type claim) | `auth-service/internal/token/issuer.go` | Low today — stored-hash matching blocks use; close at the token layer |
| 2 | `Content-Disposition` built from unescaped document name reaches the presign params | `document-service/handlers/documents.go` (download) | Header/param injection candidate; sanitize like job-service's backup path |
| 3 | CSV export emits `=`/`+`/`-`/`@`-prefixed names verbatim (spreadsheet formula injection) | `document-service/handlers/exports.go` `docsToCSV` | Neutralize with a leading `'` on risky prefixes |
| 4 | `storagePathAllowed` validates only the `backups/` prefix; other shapes pass unvalidated | document-service | Documented compatibility choice — revisit |
| 5 | Plan-quota headers trusted from any caller reaching job-service directly | job-service | Identity is safe; quota is spoofable off-gateway (documented residual) |
| 6 | `MarkRead` returns 200 for a foreign/nonexistent notification id (no-op masquerading as success) | notification-service | Cosmetic/API honesty |
| 7 | Malformed `sourceId` silently skips notification dedupe → duplicates possible | notification-service `CreateInternal` | Low |
| 8 | `RedriveDLQ` authorizes off the raw `X-User-Role` header | job-service | Safe only behind the gateway's (tested) header stripping |
| 9 | Refresh reuse revokes the replayed session row only; model doc says "family" | auth-service | Session==family in this schema; fix the doc or widen the revoke — decide |
| 10 | `RoleAtLeast(known, unknownMin)` returns true | user-service models | Trap if a minimum ever comes from request data |
| 11 | Move-to-root via PATCH is unreachable (binding rejects `""` before the handler's branch) | document-service | Dead feature path |
| 12 | Org scope-move returns a zero-value document payload (reload runs under the old scope) | document-service | Confusing 200 |
| 13 | `DeleteFolder` reparents documents but leaves child folders dangling | document-service | Data hygiene |
| 14 | gs failure leaves a ~2.4KB stub at outputPath (nothing uploads; disk noise only) | optimize-pdf | Cosmetic |
| 15 | `tryUnoconvert` burns its full 30s timeout per conversion when no unoserver daemon runs | convert-to-pdf | Perf under the documented pool/daemon mismatch |
| 16 | `Retry-After` always advertises the full window | api-gateway ratelimit | Over-conservative |
| 17 | `classifyError(context.Canceled)` tells the user the file "may be corrupted" | workers | Message quality |

## 4. Coverage (statements)

**Unit tier** (`make cover`, offline): merged total **46.0%**.

| Module | Unit | Notes |
|---|---:|---|
| shared | 64.8% | best-covered; utilities |
| organize-pdf | 60.7% | imaging is pure Go |
| api-gateway | 60.1% | routing/CORS/limits |
| convert-to-pdf | 59.1% | worker plumbing |
| job-service | 49.3% | +DB paths in integration tier |
| document-service | 43.0% | was near-zero handler coverage |
| notification-service | 36.9% | |
| optimize-pdf | 35.4% | exec paths live in binaries tier |
| user-service | 32.4% | |
| auth-service | 30.4% | handlers are DB-heavy → integration tier |
| convert-from-pdf | 30.3% | exec paths live in binaries tier |
| analytics-service | 27.9% | dashboards are DB-heavy → integration tier |

**Integration tier** (`COVER_TAGS=integration scripts/coverage.sh`, real
Postgres/Redis/MinIO): merged total **53.3%** — the meaningful number for the
handler-heavy services above (auth, analytics, document, job DB paths all
exercised over real rows).

The exec-heavy conversion paths are covered functionally by the dockerized
binaries tier (not instrumented for coverage). Percentages are gap-finding
data, not targets (per the program mandate: no coverage chasing).

## 5. Security coverage delivered

IDOR/authorization matrices over real rows for jobs, uploads, documents,
folders, tags, exports, orgs, notifications, activity; refresh-token rotation/
grace/reuse detection end-to-end; forged identity-header stripping at the
gateway; public-path pins (share redeem public, share revoke not); internal
endpoints fail closed without their token; path traversal (upload names,
worker input keys, backup storage paths); extension/MIME spoofing incl. the
routed-tool drift guard; OCR language allowlists against command injection;
cache-key collision resistance (cross-user output leak vector); guest/user
ownership isolation; fail-closed-on-outage pins (authz under Redis loss) and
fail-open pins where designed (rate limiter, analytics, notify).

## 6. Fuzzing, benchmarks, race, parallel

- **Fuzz** (`make fuzz-short`, 10s/target; long sessions manual): filename +
  part-number parsers, DSN/env parsers, JWT scope decoder, ingest sanitizers,
  page-range parsers, signature data-URL decoder. One real bug found (fix #7).
- **Benchmarks** (`make bench`): cache key 1.9µs, JWT verify 4.0µs, envelope
  2.8µs, sanitizeFileName 31ns/0 allocs, page-range 663ns, part numbers 215ns,
  rate-limit middleware 643µs (incl. miniredis round-trip), document quad
  detection 12.5ms/3.8MB (the imaging hot path — the number to watch).
- **Race**: every runner passes `-race`; the whole workspace is race-clean
  including the concurrency tests (upload claims, SSE, TTL caches, drains,
  same-key job creation over real Postgres).
- **Flaky tests**: two found, both fixed (JWT padding-bit tamper, worker drain
  sleeps). `-shuffle=on` everywhere guards order dependence.

## 7. N/A register (brief items with no corresponding backend feature)

AI/LLM tests (no AI integration exists) · payment tests (none) · OAuth (JWT
only) · favorites/pin (no feature) · push/email provider matrix (Resend
password-reset only, covered via `Mailer` fakes) · zip-bomb handling (no
archive extraction in the backend) · OpenAPI contract tooling (envelope+matrix
assertions cover the contract; Swagger/Postman stay human-maintained) ·
GitHub Actions (user chose Argo; vendor-neutral stages shipped instead).

## 8. Remaining gaps (honest list)

- convert-from-pdf's `pdf2docx` / `pdf_to_xlsx.py` paths: need that service's
  debian+pip runtime as a test image (the alpine binaries image can't host
  them). Extend `deployment/test-binaries.Dockerfile`'s approach with a second
  bookworm-based image when worth it.
- Live-NATS end-to-end (publish → JetStream → worker consume → status event):
  covered piecewise (real publishes recorded, consumer logic under fakes,
  subscriber suites); a full-stack path test would need the worker binaries +
  NATS together — the k6 suite covers this shape at the system level.
- golangci-lint is configured but not installed on this machine; stage 2
  warns loudly. Bake a pinned version into the CI image.
- Integration suites require serial packages (`-p 1`, shared test DB); a
  per-package schema would allow parallelism if suite time ever hurts.
- The 17 pinned gaps above are deliberate non-fixes awaiting their own
  changes.

## 9. How to run everything

```bash
make test                  # unit, all modules, race+shuffle (offline)
make test-<svc>            # one module
make test-infra-up         # start Postgres/Redis/NATS/MinIO (loopback)
make test-integration      # tagged suites vs real infra (-p 1, sources .env)
make test-binaries         # dockerized gs/libreoffice/tesseract exec paths
make fuzz-short            # every fuzz target, FUZZTIME=10s each
make bench                 # all benchmarks (-benchmem)
make cover                 # unit coverage → coverage/merged.out
COVER_TAGS=integration sh scripts/coverage.sh   # integration-tier coverage
make lint                  # gofmt gate + vet (golangci-lint via scripts/ci/02)
scripts/ci/0*.sh           # the CI stages, runnable anywhere
```
