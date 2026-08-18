# Testing Strategy

This document is the contract for how fyredocs_backend is tested: the test
taxonomy, the runner targets, the conventions every new test must follow, and
the audit that drives the test roadmap. It is extended in the same change as
any test-infrastructure work (CLAUDE.md §8).

## Test taxonomy

Exactly three tiers, selected by Go build tags:

| Tier | Tag | Needs | When it runs |
|---|---|---|---|
| Unit | *(none)* | Nothing — fully offline | Every `make test`, every PR |
| Integration | `//go:build integration` | The essentials infra stack (Postgres, Redis, NATS, MinIO on loopback) | `make test-integration`, after `make test-infra-up` |
| Binaries | `//go:build integration && binaries` | Additionally gs / libreoffice / tesseract / poppler / python3 | `make test-binaries` (dockerized; added with the first binaries-tagged tests) |

Untagged tests must never dial the network, touch a real database, or shell
out to external binaries. The historical ad-hoc gates (`S3_TEST_ENDPOINT` in
`shared/storage`, the ghostscript-presence skip in `optimize-pdf`) predate
this taxonomy and migrate to tags as those packages are touched.

There is no `e2e` tier: API tests run real gin routers in-process under the
`integration` tag, and full-stack load/E2E traffic is owned by the existing
k6 suite (`scripts/k6/`, see `load-testing.md`).

## Runners

All runners loop per module — root-relative `./...` does not work in this
workspace (CLAUDE.md §10 Step 4).

```
make test                 # unit, all modules: -race -count=1 -shuffle=on -timeout=10m
make test-<svc>           # one module, same flags (e.g. make test-job-service)
make test-infra-up        # start db/redis/nats/minio from docker-compose.essentials.yml
make test-integration     # integration-tagged, adds -p 1 (shared test database)
make test-infra-down      # stop the infra containers (volumes kept)
make cover                # per-module coverprofiles merged into coverage/merged.out
make bench                # all benchmarks, -benchmem, -run='^$'
make fuzz-short           # every Fuzz* target for FUZZTIME (default 10s) each
make lint                 # gofmt -l gate + per-module go vet
./test.sh [-v] [svc ...]  # same flags as make test (test.bat on Windows)
```

Flag rationale (`TESTFLAGS` in the Makefile): `-count=1` defeats the result
cache so a rerun actually executes; `-shuffle=on` surfaces test-order
dependencies before they calcify; `-race` is mandatory because the repo has
real concurrency tests (upload claims, SSE, TTL caches, worker drains) that
are worthless without it; `-timeout=10m` is a hang backstop sized for the
slowest module under race overhead. `test-integration` adds `-p 1` because
integration packages share the `fyredocs_test` database — parallel packages
would truncate each other's rows mid-test.

## Conventions

- **Stack**: stdlib `testing` + `net/http/httptest` + miniredis. No testify,
  gomock, sqlmock, or testcontainers — the existing hand-rolled-fake pattern
  (`job-service/handlers/fakestore_test.go`, the worker `fakeStorage`/
  `fakeCache`) is the template for new doubles.
- **Package globals** (`models.DB`, `redisstore.Client`, `natsconn.JS`,
  `objStore`): capture the old value, swap, restore in `t.Cleanup`. See
  `withMiniRedis` in `job-service/handlers/jobs_test.go` and `withFakeStore`
  in `fakestore_test.go` for the canonical shape. A test that mutates a global
  without a Cleanup restore blocks `t.Parallel` for the whole package.
- **`t.Parallel()`** only in packages whose tests touch no package globals;
  integration packages stay serial.
- **Timing**: never assert on real sleeps. Channel/timer logic uses
  `testing/synctest` (see the worker `drain_test.go` files); durations come
  from env or parameters so tests can shrink them.
- **Fixtures**: generate inputs in-code into `t.TempDir()` (pdfcpu
  `api.ImportImagesFile` is the established pattern — see
  `organize-pdf/processing/processing_edit_test.go`). Commit binary files to a
  package-local `testdata/` only when generation is impossible (malformed
  PDFs, extension-spoofed magic bytes), 50 KB per file at most, always
  synthetic — never production or copyrighted documents. Fuzz corpora live in
  Go's native `testdata/fuzz/<FuzzName>/`.
- **Helpers**: keep them in the package's `_test.go` files; promote to
  `<svc>/internal/testutil` only once ≥2 packages in the same service need
  them. Cross-service test helpers are forbidden except utility-grade packages
  under `shared/` (CLAUDE.md §1/§2). The four PDF workers' test suites are
  duplicated by design — do not unify them.
- **Environment**: `t.Setenv` only (it fails fast if the test forgot to be
  serial); never `os.Setenv` in tests.
- **Security gaps discovered by tests are pinned, not silently fixed**: the
  test asserts current behavior with a `NOTE:` comment citing
  `backend-hardening.md`, and the fix is proposed separately with its own
  §8/§9 fan-out.

## Database integration harness

`shared/database/dbtest` (integration-tagged) is the one shared piece of DB
test infrastructure — it fits the CLAUDE.md-allowed "database" shared-utility
category and contains no business logic:

- `Open(t, service, migrationsFS)` connects to the dedicated test database
  (default `<POSTGRES_DB>_test` on `localhost:5432` with the stack's
  `POSTGRES_USER`/`POSTGRES_PASSWORD`; `FYREDOCS_TEST_DATABASE_URL` overrides),
  creates it if missing, and applies the service's own embedded goose
  migrations via `database.RunMigrations` — the production boot path. It
  refuses any database whose name does not end in `_test`, blanks
  `DATABASE_URL_DIRECT` so migrations cannot be redirected, and closes the
  handle in Cleanup.
- `Tx(t, db)` returns a rolled-back-in-Cleanup transaction — swap it into the
  service's package-global `models.DB` for per-test isolation. **Not usable**
  when the code under test commits, holds the handle in goroutines, or must
  see rows from another connection (ON CONFLICT dedupe, Redis-claimed
  idempotency, cleanup sweeps): those tests use `Truncate(t, db, tables...)`
  cleanup instead. Misapplying Tx to such a test produces phantom failures —
  check this first.
- Each service's `TestMain` (in an integration-tagged file) opens the DB once
  and sets `models.DB`; suites from different services share the database
  safely because table sets are disjoint, but packages must run serially —
  `make test-integration` passes `-p 1` and sources `.env` (set -a, as
  deploy.sh does) for the credentials.

Run: `make test-infra-up && make test-integration`.

## Coverage

`make cover` writes one profile per module to `coverage/<svc>.out` and merges
them into `coverage/merged.out` (`scripts/coverage.sh`; set
`COVER_TAGS=integration` to include tagged tests). Coverage is a gap-finding
tool, not a target: the roadmap prioritizes authorization, file access, job
lifecycle, and money-adjacent logic over raw percentages.

## Phase-0 audit (2026-08-17 baseline)

Workspace totals at the start of the testing program: **151 test files,
790 test functions, 0 benchmarks, 0 fuzz targets, 0 `t.Parallel`**, no
database-touching tests, and no runner passed `-race`/`-count=1`/`-shuffle`
until this document's Wave 1 landed.

| Module | Tests | Strongest existing coverage | Biggest gaps | Risk | Recommended additions |
|---|---:|---|---|---|---|
| shared | 196 | authverify, response, config, logger, queue | ~~redisstore (empty stub)~~ *(fixed Wave 1)*, storage integration untagged | Med | tag storage integration; dbtest harness |
| job-service | 117 | upload lifecycle, tool options, claims (miniredis) | `authorizeJobAccess`, backups/dlq/storage handlers, DB paths | **High** | security-pure unit; DB + API-matrix integration |
| organize-pdf | 117 | imaging (pure math), processing round-trips | worker exec paths, cache-key security framing | Med | drift/backoff tests; binaries-tagged exec |
| analytics-service | 103 | activity ingest validation, promscrape | most handlers are route-exists smoke only; DB dedupe untested | **High** | ingest DB integration; admin authz matrix |
| auth-service | 57 | token issuer, cookie/normalization helpers | `ClassifyRefresh` reuse, login timing equalizer, internal_api, DB sessions | **High** | security-pure unit; refresh-rotation integration |
| convert-to-pdf | 53 | unopool, worker cache/storage/tmpfs | exec conversion paths | Med | binaries-tagged conversions |
| optimize-pdf | 51 | OCR guards, ghostscript args | exec gs/tesseract paths | Med | binaries-tagged; adversarial OCR language |
| convert-from-pdf | 42 | worker plumbing (cloned) | exec paths incl. python pdf_to_xlsx | Med | binaries-tagged conversions |
| api-gateway | 30 | alias collisions, CORS, proxy streaming | public-path pins, ratelimit fail-open breadth | **High** | security pins; ratelimit tables |
| document-service | 10 | storagePathAllowed (1 test), route smoke | documents/exports/folders/tags handlers ≈ untested; ownership scoping | **Critical** | guard-path unit; ownership DB/API matrix |
| user-service | 9 | slugify/rank, pagination | org membership mutations (privilege escalation) | **High** | escalation unit + DB integration |
| notification-service | 5 | requireUser, route smoke | subscriber, create/read paths | Med | subscriber unit; DB integration |

Cross-cutting gaps driving the roadmap: no fuzzing of the parser surface
(page ranges, filenames, DSNs, scope claims, metadata JSON); no benchmarks;
three tests that silently skip when no database is present
(`auth-service/handlers/plans_test.go`, `analytics-service/handlers/`
`api_trends_test.go` ×2) — these convert to `integration`-tagged tests that
actually run; and CI, which is delivered as vendor-neutral stage scripts under
`scripts/ci/` (see `ci-pipeline.md` once it lands).

## Wave log

- **Wave 1 (2026-08-17)**: hardened all runners with `TESTFLAGS`; added
  `test-integration`/`test-infra-up`/`test-infra-down`/`cover`/`bench`/
  `fuzz-short`/`lint` targets plus `scripts/coverage.sh` and
  `scripts/fuzz-short.sh`; converted the four worker `drain_test.go` files to
  `testing/synctest` (real-sleep flake shape); replaced the empty
  `shared/redisstore` test stub with a real miniredis suite; verified the
  whole workspace green under `-race -count=1 -shuffle=on`.
- **Wave 2 (2026-08-17)**: security-critical pure-function tests across
  job-service (authorizeJobAccess IDOR matrix, upload ownership,
  `TestEveryRoutedToolIsValidated` drift guard), auth-service (refresh reuse
  classification, role/impersonation matrices, login timing equalizer, issuer
  edges), api-gateway (public-path matrix, forged-identity-header stripping,
  CORS adversarial rows, rate-limit header/fail-open cases), the four workers
  (backoff-vs-consumer drift guard, cache-key ambiguity/security rows,
  fetchInputs traversal containment), OCR language guards, and the analytics
  alert-receiver placement pin. **Three production bugs found and fixed** with
  regression tests: (1) job-service validation bypass — the routable aliases
  `pdf-to-docx`/`pdf-to-xlsx`/`pdf-to-pptx`/`pdf-to-txt` and `html-to-pdf`
  were missing from `validateFileType`/`mimeCategory`, so those tool names
  skipped all extension and content checks; (2) document-service
  `storagePathAllowed` accepted `..` segments under an owned `backups/`
  prefix (dot-segment traversal); (3) api-gateway `publicAuthPaths()` omitted
  forgot/reset-password, so a stale access_token cookie 401'd the password
  reset flow. **Pinned gaps for follow-up** (NOTE-annotated tests, no code
  change): `VerifyRefreshToken` accepts an access token (token-type
  confusion; defense-in-depth holds via stored-hash matching);
  `classifyError(context.Canceled)` reports "file may be corrupted";
  rate-limit `Retry-After` always advertises the full window.
- **Wave 3 (2026-08-17)**: filled the near-untested services — document-service
  (guard paths for documents/exports/folders/tags/hints, orgclient/notifyclient
  wire contracts over httptest with the resolveOrg role matrix and fail-open
  notify pin, route-table drift guard), user-service (role matrices, org guard
  paths, internal-token chain), notification-service (handler guards,
  subscriber filter table with ack/nak/term semantics and backoff drift guard),
  plus auth internal_api/routes, job-service backups/dlq/storage, and
  shared/healthcheck (re-exec pattern). **Fourth production fix**: upload
  sessions are now owner-checked at CONSUMPTION (`consumeUpload` takes the
  caller identity) — previously only the multipart endpoints enforced finding
  F4's owner binding, so any caller who learned a live uploadId could pull
  another user's bytes into their own job, share, or backup; denials answer
  "upload not found" so uploadIds stay unprobeable. Also fixed a real ~5%
  flake: the tampered-JWT test flipped the LAST base64url signature character,
  whose low 2 bits are decoder-ignored padding — it now tampers the first.
  Fake-consolidation into a job-service testutil package was deliberately
  skipped: handlers.ObjectStore and cleanup.ObjectStore are different narrow
  interfaces by design, so separate fakes stay. Migrated shared/storage's
  S3_TEST_ENDPOINT skip-gate to the integration tag (runs against the stack's
  app-scoped credentials and real buckets).
- **Waves 4–5 (2026-08-18)**: `shared/database/dbtest` harness landed (see the
  harness section above) and every DB-backed service gained an
  integration-tagged suite over real Postgres: analytics (ingest dedupe/scrub/
  identity, trend SQL), user (privilege-escalation matrix over real rows),
  notification (dedupe, IDOR isolation, subscriber mapping/idempotency),
  document (CRUD lifecycle, ownership/org matrices, exports, delta-sync,
  constraints), job (creation end-to-end, both idempotency layers, concurrent
  SetNX single-winner, authz over HTTP, cleanup ordering), auth (signup/login/
  refresh rotation, grace vs reuse, revocation, internal endpoints). **Three
  more production fixes** with regression tests: PurgeDocument and DeleteTag
  both deleted the parent row before its `document_tags` associations —
  `fk_document_tags_*` made every tagged document unpurgeable and every in-use
  tag undeletable (500s); associations now go first, DeleteTag behind a scoped
  ownership fetch. And `shared/queue` publishers gained nil-JetStream guards:
  `PublishJobEvent` panicked on a nil handle AFTER the job row committed
  (stranded row + duplicate on client retry); now the handler's own controlled
  500 with the upload session preserved. Also fixed a genuine ~5% flake (JWT
  tamper test flipped a base64url padding bit) and migrated the ghost
  S3 skip-gate. Session-limit note: heavy integration agents must run one at
  a time.
- **Wave 7 (2026-08-18)**: 9 fuzz targets with seed corpora — filename/part
  parsers (job-service), DSN/env parsers (shared/config), scope-claim decoder
  (shared/authverify), ingest sanitizers (analytics), page-range parsers and
  the signature data-URL decoder (organize-pdf). First fuzz session found and
  fixed a real bug: `ApplyPostgresDSNDefaults` corrupted host-less URIs into
  keyword-form duplicates on re-application (crasher kept in `testdata/fuzz/`
  as a regression seed). `make fuzz-short` runs each target 10s.
- **Waves 8–9 (2026-08-18)**: 8 benchmarks (`make bench`) with recorded
  baselines; 64 top-level tests parallelized surgically (packages proven free
  of global mutation, stability shown at `-race -count=2 -shuffle=on`);
  resilience additions — circuit-breaker full lifecycle under
  `testing/synctest`, ForgotPassword masks (200) even when the mailer errors,
  worker storeOutput failure is recoverable→NAK with correct backoff and
  terminal-ACK at MaxDeliver, SSE handler exits on client disconnect without
  goroutine leaks. CI plumbing: `.golangci.yml` (correctness linters only),
  `scripts/ci/00–07` vendor-neutral stages, `ci-pipeline.md` with the Argo
  WorkflowTemplate, `make test-binaries` + `deployment/test-binaries.Dockerfile`
  (Go toolchain + gs/poppler/tesseract/libreoffice/unoserver).
