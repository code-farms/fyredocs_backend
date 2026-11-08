# 13 — Language and Technology Selection

> Part of the Next-Generation Document Engine analysis (2026-11). Status: ANALYSIS — no code changed.

## 1. Purpose and scope

Decide, per subsystem, which language and integration mechanism the next-generation engines use, under two
hard constraints: every worker is a `CGO_ENABLED=0` static build from `fyredocs-go-builder`, and CLAUDE.md
§1–§4 (independent services, no shared business logic, REST/gRPC/NATS between services). The document fixes
the Go-first stance, lists every Rust lead with its Go or exec'd alternative and why it is not adopted now,
and defines the four communication contracts that every engine must use: in-process Go interfaces, the
`shared/procexec` exec contract, the sidecar HTTP contract, and the WASM contract. Engines themselves are
designed in `04-libreoffice-replacement-strategy.md` and docs `05`–`10`; stages follow `20-implementation-roadmap.md`.

## 2. Current state

- **Toolchain**: Go 1.26.0 workspace of 13 modules (`go.work`); `deployment/base-go-builder.Dockerfile:18`
  `FROM golang:1.26-alpine`; warm compile at `:60-66` uses `CGO_ENABLED=0 GOOS=linux go build -trimpath`;
  every service Dockerfile repeats it (`convert-to-pdf/Dockerfile:19-20`, `convert-from-pdf/Dockerfile:19-20`,
  `-ldflags="-s -w"`). The build cache keys depend on this; a cgo build would miss the warm cache
  (`base-go-builder.Dockerfile:55-58`) and need a C toolchain in the builder.
- **Runtime images**: eight services `FROM scratch` (audit §Images); `convert-to-pdf` on `fyredocs-base`
  (Alpine + LibreOffice, `deployment/base-alpine-libreoffice.Dockerfile`); `convert-from-pdf` on
  `python:3.11-slim-bookworm` (`convert-from-pdf/Dockerfile:28`) — the only Python runtime, for pdf2docx and
  `convert-from-pdf/scripts/pdf_to_xlsx.py` (`processing_free.go:342`); `optimize-pdf`/`organize-pdf` Alpine + tesseract/gs/poppler.
- **Languages in production code**: Go, Python (one script + two pip tools). (The shell entrypoint
  `convert-to-pdf/entrypoint.sh` was removed on 2026-11-08; the Go worker supervises unoserver.) No Rust, no C/C++ written by the team; C/C++ only as exec'd binaries (LibreOffice, Ghostscript,
  Poppler, Tesseract).
- **Integration today**: in-process Go (pdfcpu, `organize-pdf/internal/imaging`, `x/image`); `exec.CommandContext`
  at 20 sites (audit table) with no `Setpgid`/`cmd.Cancel`/`WaitDelay`; daemon over XML-RPC (unoserver via the
  `unoconvert` CLI, `convert-to-pdf/processing/processing_free.go:40`); no gRPC, no WASM, no shared memory, no
  HTTP sidecar. Inter-service: REST through api-gateway, NATS JetStream (`shared/natsconn/natsconn.go:69-112`).
- **Shared utilities allowed** (CLAUDE.md §2): logging, tracing, metrics, config, queue clients, authverify,
  database. `shared/` today: authverify, circuitbreaker, clientip, config, database, discord, healthcheck, logger,
  metrics, natsconn, queue, redisstore, response, storage, telemetry. A `shared/procexec` fits this list (infra,
  no business logic); a "shared document engine" package does **not** (§1 — stated as unrealistic in
  `EXECUTIVE-SUMMARY.md`).
- **Local measurement host** (audit): macOS arm64, Docker VM 8 CPU / 4 GB, Go 1.26, **no Rust toolchain**.

## 3. Requirements

- **Build**: no change to `CGO_ENABLED=0`; new binaries in runtime images must be static (musl) and pinned by
  version + sha256 in the Dockerfile; builder image stays Go-only.
- **Isolation**: a crash or runaway allocation in an engine must not take the worker's NATS consumer down
  (today a panic in a job is recovered, `convert-to-pdf/internal/worker/worker.go:369-384`, but a native crash
  would not be).
- **Cancellation**: every engine call must stop within 2 s of job-context cancel (Stage 2 acceptance, Q11), including
  grandchildren.
- **Observability**: every mechanism reports duration, outcome and resource use through `shared/metrics`
  (`shared/metrics/metrics.go:23-62`) and OTel spans (`shared/telemetry`).
- **Team**: two engineers, Go; no Rust written in the first 12 months (binding decision, `EXECUTIVE-SUMMARY.md`).
- **Security**: exec'd tools run with resource limits; sidecars have no egress; WASM instances are memory-capped.

## 4. Options evaluated

### 4.1 Languages per subsystem

| Subsystem | Go (existing) | Rust | C/C++ | Python | Decision |
|---|---|---|---|---|---|
| Orchestration / API / workers | all services; gin, nats.go, gorm | — | — | — | **Go** (no question) |
| E1 PDF object engine | pdfcpu v0.9.1 (Apache-2, in use) | pdf-writer, krilla (output only) | qpdf (not installed) | pypdf | **Go** pdfcpu |
| E2 PDF raster/text | Poppler CLIs (exec); go-pdfium **WASM** (MIT; PDFium Apache-2) | hayro 0.7 "experimental" | PDFium, MuPDF (AGPL) | PyMuPDF (AGPL) | **exec Poppler now → go-pdfium WASM after Stage 5 bake-off** |
| E4 page-model reconstruction | own code in convert-from-pdf | — | — | pdf2docx (MIT; depends on PyMuPDF, AGPL) today | **Go** (the one custom engine) |
| E5 distiller/optimiser | pdfcpu + `x/image/draw`; Ghostscript exec for flatten/PDF-A | — | Ghostscript (AGPL, exec'd unmodified) | — | **Go + exec gs** |
| E6 OCR | none exists in Go | ocrs (no results), faster-paddle (ONNX; unverified) | Tesseract 5 (exec) | PaddleOCR | **exec Tesseract; optional PaddleOCR HTTP sidecar after CER bake-off** |
| E7 typesetting | go-text/typesetting (shaping; no page layout) | Typst 0.15.1 (static musl CLI) | — | — | **exec Typst**; Go alt = go-text → PNG stamp |
| E8 imaging | `x/image`, `organize-pdf/internal/imaging` (pure Go) | image-rs | libvips (cgo via govips/bimg) | Pillow | **Go**; libvips only on measured need (and only as an exec'd `vips` CLI, never cgo) |
| E9 signing | digitorus/pdfsign v1.0.0 (BSD-2; PAdES B-B/B-T, RFC 3161, visible appearance; no LT/LTA) | — | — | pyHanko | **Go** pdfsign |
| html→pdf | none with CSS paged media | — | Chromium (chromedp/rod exec) | WeasyPrint 70.0 | **Python sidecar (WeasyPrint)**; Chromium only if JS required |
| Office layout (E3) | none | office2pdf 0.10 (Typst-based, young) | LibreOffice | — | **LibreOffice in `office-render`** (see `04-…`) |

### 4.2 Integration mechanisms under the static-build constraint

| Mechanism | Works with `CGO_ENABLED=0`? | Crash isolation | Call overhead | Cancellation | Build/ops cost | Where used |
|---|---|---|---|---|---|---|
| In-process Go | yes | none (panic recover only) | ~0 | cooperative (ctx between stages; pdfcpu ignores ctx) | none | E1, E4, E5, E8, E9 |
| FFI / cgo | **no** — breaks the builder pattern (`base-go-builder.Dockerfile:64`), needs C toolchain, dynamic libs in `scratch` images | none (native crash kills the process) | lowest | hard | high | **rejected** for all workers |
| WASM (wazero) | yes — pure Go runtime, zero deps, no cgo (wazero 1.12.0, Apache-2) | per-instance (trap → Go error; memory sandboxed) | ~2× native per go-pdfium README | `WithCloseOnContextDone` (runtime overhead; module closed afterwards) | medium: ~1 s module compile at start (go-pdfium README), memory per instance | E2 candidate (go-pdfium) |
| exec'd static binary | yes | full (separate process) | process spawn (to be measured per tool) | needs process group (today missing) | low: binary + sha256 in Dockerfile | Poppler, gs, tesseract, LibreOffice, Typst, WeasyPrint CLI (option) |
| HTTP sidecar (same compose network) | yes | full | network + serialisation | request deadline + server-side kill | medium: another container, health, versioning | WeasyPrint, optional PaddleOCR, future `office-render` |
| gRPC | yes (grpc v1.82.1 already indirect) | as HTTP | lower than JSON | deadline propagation built in | medium; proto toolchain, codegen in CI | not needed in 12 months; allowed by §4 if a sidecar grows a typed API |
| NATS request/reply | yes (in use) | full | queue hop | header-propagated ctx + explicit cancel subject | low (infra exists) | job dispatch; **not** for synchronous render calls (payload cap 64 KiB `natsconn.go:69-112`) |
| Shared memory / mmap | yes (`syscall.Mmap`, `x/sys`) | partial | lowest for bulk data | none | high (protocol, lifecycle, security) | **rejected**: tmpfs files already give zero-copy handoff to exec'd tools |

### 4.3 Decision matrix (1 = best / lowest cost, 5 = worst) — team-written code

| Option | Measurable benefit (evidence) | Team skill cost | Build-pipeline cost | Security/sandbox cost | Ops cost | Total (lower better) | Decision |
|---|---|---|---|---|---|---|---|
| Go in-process | 3 (no raw-speed win, but zero spawn, zero copy) | 1 | 1 | 2 (no sandbox; panics recovered) | 1 | 8 | **default** |
| Rust library via cgo | 1 (claimed; unmeasured for our workloads) | 5 (no Rust on team or host) | 5 (breaks static build) | 3 | 3 | 17 | **rejected** |
| Rust as exec'd static binary (Typst) | 2 (feature we lack: shaping + layout) | 1 (we write none) | 2 (download, pin, sha256) | 2 (procexec limits) | 2 | 9 | **accepted** for Typst only |
| Rust sidecar (e.g. faster-paddle, office2pdf as service) | 2–3 (unmeasured) | 2 | 3 (separate image build) | 2 | 3 | 12–13 | **gated**: only behind a measured bake-off |
| C/C++ exec'd (Poppler, gs, Tesseract, LO) | 4 (mature; the baseline) | 1 | 1 | 3 (AGPL notice for gs/MuPDF; process limits) | 2 | 11 | **keep**; reduce call count, not to zero |
| Python sidecar (WeasyPrint, PaddleOCR) | 3 (capability we lack) | 2 | 3 | 3 (CVE cadence, pinning) | 3 | 14 | **accepted** where no Go/exec option exists (html→pdf; OCR bake-off) |
| WASM (go-pdfium) | 3 (PDFium quality; 2× slower than cgo per vendor) | 2 | 1 | 1 (memory-capped sandbox) | 2 | 9 | **bake-off** Stage 5 |

### 4.4 Every Rust lead, its Go/exec alternative, and why not now

| Rust lead | What it offers | Status (2026-11-07) | Go / exec alternative | Why not now |
|---|---|---|---|---|
| office2pdf | DOCX/XLSX/PPTX→PDF via Typst, PDF/A-2b | 0.10.0 (2026-10-28), Apache-2, single owner; README lists pagination/complex-script gaps | LibreOffice in `office-render`; optional Go simple-DOCX fast path (Stage 6+) | fidelity unproven on our corpus; Indic unverified; young project — evaluate only inside gate G-FAST (`04-…` §7) as an exec'd binary |
| dxpdf | DOCX→PDF via Skia | 0.8.1, MIT; "Indic reordering: not supported"; needs clang/Skia | same as above | Indic gap is disqualifying |
| krilla | PDF writer with fonts/PDF-A/UA (used by Typst) | 0.8.2, LaurenzV | pdfcpu for object ops; Typst (which embeds krilla) for typeset output | output-only library; we would have to write Rust to use it directly |
| hayro | PDF rasteriser | 0.7.0, "experimental, work-in-progress"; no knockout groups, no non-embedded CID fonts | Poppler exec now; go-pdfium WASM | not production-grade by its own README |
| HarfRust | HarfBuzz port in Rust (harfbuzz org, MIT, matches HarfBuzz 15.0.0, "<25 % slower") | no release list on repo page | **go-text/typesetting** `harfbuzz` package: direct port with Indic, USE, Arabic, Khmer, Myanmar, Hangul, Hebrew, Thai shapers (v0.3.5, 2026-09-04, BSD-3/Unlicense; maintained by Fyne + Gio + independent maintainer, two-of-three sign-off) | Go port exists with the same shaper set; Devanagari correctness to be verified on the Stage 1 Hindi corpus (`go test ./harfbuzz -run Indic` in the module plus visual check) |
| cosmic-text | layout + fallback + raster on HarfRust | 0.19.0 (2026-04-22), MIT/Apache-2 | go-text/typesetting + `x/image/font` for raster; Typst for paged output | would require team Rust; Typst covers the paged case |
| faster-paddle / ocrs | OCR reimplementations | self-reported numbers only; ocrs no results | Tesseract exec; PaddleOCR Python sidecar for the CER bake-off | unverified; OCR decision is a CER bake-off, not a language choice |

## 5. Recommendation

### 5.1 Final stance

**Go-first.** New engine code is Go, in-process, inside the worker that owns the tool (CLAUDE.md §1). **Rust only
as exec'd static binaries** (Typst now; office2pdf only if it wins gate G-FAST) **or as a sidecar candidate behind a
measured gate** (none planned in 12 months). **No team-written Rust in 12 months.** No cgo in any worker. C/C++
engines stay as exec'd processes with fewer calls; Python stays only where no Go/exec option exists (WeasyPrint;
PaddleOCR bake-off) and leaves `convert-from-pdf` in Stage 3.

### 5.2 Contract A — in-process Go interfaces (engines inside a worker)

Each worker defines its own interfaces (no shared DTOs, §3). Sketch of the shape every engine follows:

```go
// Engine stages take ctx and check it between units of work (pdfcpu ignores
// ctx, so the caller checks between pages/files). Inputs/outputs are paths in
// the job scratch dir, never []byte for whole documents (tmpfs budget,
// internal/worker/tmpfs.go).
type Renderer interface {
    RenderPage(ctx context.Context, in string, page int, dpi int) (image.Image, error)
}
type Extractor interface { // E4: pdftotext -bbox-layout now, PDFium later
    Extract(ctx context.Context, in string) (*PageModel, error)
}
type Writer interface {   // E4 writers: DOCX, XLSX, PPTX, ODT/ODS/ODP, HTML, TXT
    Write(ctx context.Context, m *PageModel, out string) (Meta, error)
}
```

Rules: deterministic output for the result cache key (`rescache:v1:…`, audit); errors wrap a stable code from the
worker's error-code map (`convert-to-pdf/internal/worker/worker.go:88-113`); every stage records a span and a
`job_duration_seconds` observation; no goroutine outlives ctx.

### 5.3 Contract B — `shared/procexec` for every exec'd tool

A utility package (allowed by §2: infra only; no tool knowledge). Replaces the 20 bare `exec.CommandContext` sites.

| Field / behaviour | Contract |
|---|---|
| Process group | `SysProcAttr{Setpgid: true}`; `cmd.Cancel = func() error { return syscall.Kill(-pgid, SIGKILL) }` so `soffice.bin`, `python→MuPDF`, `gs` children die with the parent |
| `WaitDelay` | 5 s default: after cancel, Wait gives up on inherited pipes instead of blocking (the `CombinedOutput` hang today) |
| Timeouts | per-call `Deadline` = `min(jobCtx deadline, base + perMB × inputMB)`; tool-specific defaults (LO: 2 s + 1.5 s/MB; gs/pdftoppm/tesseract: to be measured by the harness) |
| stderr capture | bounded ring (64 KiB) returned in the error; stdout to file when the tool streams output |
| Resource limits | `RLIMIT_AS`/`RLIMIT_CPU` via `prlimit`-style wrapper where the image provides it, else cgroup limits per container; `nice`/`ionice` optional for large jobs |
| Environment | explicit allow-list (`PATH`, `HOME`→scratch, `TMPDIR`→scratch, per-tool vars such as `-env:UserInstallation`); never inherit the worker env |
| Working dir | the job scratch dir (`os.MkdirTemp("", "job-<id>-")`, `worker.go:500`) |
| Result | `{ExitCode, Duration, PeakRSS (from `/proc/<pid>/status` sample or `Rusage`), Stderr, TimedOut, Killed}` |
| Metrics | `procexec_duration_seconds{tool}`, `procexec_exit_total{tool,outcome=ok|error|timeout|killed}`, `procexec_orphans_total` (post-run `pgrep -g pgid` check in tests) |
| Acceptance | 0 orphan `soffice.bin`/`gs`/`tesseract`/`python3` after 100 forced cancels (Stage 1); cancel → process gone ≤ 2 s (Stage 2) |

### 5.4 Contract C — sidecar HTTP (WeasyPrint now; optional PaddleOCR; future `office-render`)

| Element | Contract |
|---|---|
| Transport | HTTP/1.1 over the compose network, bound to the service name only, no published port; TLS not required inside `fyredocs_net`; `INTERNAL_API_TOKEN` header as `organize-pdf`'s internal route does (`internal/httpapi/detect.go`) |
| Request | `POST /v1/<op>` (`render-html`, `ocr`, `convert`); body = input bytes (`application/octet-stream` or multipart with `options` JSON part); headers `X-Job-ID`, `X-Request-ID`, `traceparent`, `X-Deadline-Seconds`, `X-Convert-To` / `X-Lang` as the op needs; size cap `SIDECAR_MAX_BODY_MB` (413) |
| Response | 200 + output bytes + `X-Pages`, `X-Engine-Version`, `X-Duration-Ms`; 408 deadline; 413 too large; 422 unrenderable input (permanent — no retry); 503 engine unhealthy (retry with backoff, counted in `jobs_failed_total` only after MaxDeliver) |
| Timeouts | client: `X-Deadline-Seconds` ≤ remaining jobCtx; server kills the engine process at deadline (procexec inside the sidecar, or WeasyPrint's own timeout); `http.Client` with `ResponseHeaderTimeout` = deadline + 5 s |
| Health | `GET /healthz` (process up), `GET /readyz` (engine warmed, fonts loaded, daemons pinged); `/metrics` Prometheus |
| Versioning | path `/v1`; `X-Engine-Version` (e.g. `weasyprint/70.0`) logged per job and included in the result-cache key so an engine upgrade invalidates cached outputs |
| Security | network egress blocked (compose `internal: true` network or iptables in image); WeasyPrint `url_fetcher` restricted to `data:` and bundled fonts; non-root; read-only FS except `/tmp`; CPU/memory limits in compose |
| Circuit breaker | reuse `shared/circuitbreaker` (gobreaker) as api-gateway does (`api-gateway/main.go:513-519`) |

### 5.5 Contract D — WASM (go-pdfium on wazero)

| Element | Contract |
|---|---|
| Runtime | wazero 1.12.0 compiler mode (amd64/arm64 only — interpreter elsewhere is "much, much slower"); `WithMemoryLimitPages` set explicitly (`PDFIUM_WASM_MAX_MB`, default to be chosen from the bake-off; 4 GB is the hard cap per instance); `WithCloseOnContextDone(true)` so a cancelled job traps the instance |
| Pooling | go-pdfium pool `MinIdle`/`MaxIdle`/`MaxTotal`; `MaxTotal = WORKER_CONCURRENCY`; one instance per job, never shared across jobs; `GetInstance(timeout)` bounded by jobCtx |
| Crash isolation | a trap or memory error returns a Go error and the instance is **closed and recreated** (memory is only returned on close per README); worker counts `pdfium_instance_recycled_total{reason}`; the job falls back to Poppler exec if `PDF_RENDER_FALLBACK=poppler` |
| Startup | module compile ≈ 1 s (vendor README) — done once at worker start (`WithCompilationWorkers`); readiness waits for it |
| Limits | single bitmap < 2 GB (≈536 MP) — enforce `IMAGE_MAX_IMAGE_DIM` (`convert-from-pdf/processing/processing_free.go:90-98`) before render; tile if exceeded |
| Build | pure Go, `go:embed`ded PDFium wasm → still `CGO_ENABLED=0`; binary size growth to be measured (`ls -l` before/after) |
| Gate (Stage 5) | go-pdfium WASM at 150 DPI ≥ 0.7× pdftoppm throughput on the harness corpus; identical page counts on the fuzz corpus; no cross-job crash in a 1 000-job soak; otherwise keep Poppler — do **not** switch the builder to cgo |

### 5.6 Sequence of language/toolchain additions by stage

| Stage | Addition | Mechanism | Toolchain impact |
|---|---|---|---|
| 1 | `shared/procexec`; daemon supervision in Go; pinned `unoserver==3.7`; Noto fonts | Go + exec | none |
| 2 | Typst 0.15.1 musl binary (sha256-pinned download in `fyredocs-base` and `organize-pdf` images); WeasyPrint 70.0 sidecar image (`python:3.12-slim`, pinned) | exec + HTTP sidecar | first non-Go binary **downloaded**, none compiled; first Python sidecar image |
| 3 | excelize v2.11.1 (Go ≥1.26 ok); possibly docxgo v2 (after round-trip test) | in-process | go.mod only |
| 4 | none (pdfcpu + `x/image`) | in-process | none |
| 5 | go-pdfium (wazero) bake-off; digitorus/pdfsign | in-process WASM | go.mod only; binary size |
| 6 | `office-render` container (Go shim + LO); optional PaddleOCR sidecar | HTTP sidecar | one more image; no new language in the team's code |
| 6+ (optional) | office2pdf static binary **only** if it wins G-FAST | exec | sha256-pinned download |

Never added in this roadmap: Rust toolchain in CI, cgo, JVM, gRPC codegen (allowed by §4; not needed).

### 5.7 Exit criteria to revisit Rust (any one triggers a written re-evaluation, not an adoption)

1. **E4 table/column detection in Go** exceeds **200 ms/page** median on the 200-PDF labelled set after one
   profiling pass (`go test -bench . -cpuprofile` in `convert-from-pdf/internal/pagemodel`), **and** a Rust
   candidate exists with a published benchmark on comparable input.
2. **go-text/typesetting** fails Devanagari shaping on the Hindi stamp corpus (conjuncts, matras, reph) where
   HarfBuzz/HarfRust passes, and the project does not fix it within one release cycle. (Typst already covers the
   paged case; this criterion concerns the raster-stamp fallback.)
3. **go-pdfium WASM** fails the Stage 5 gate (< 0.7× pdftoppm) **and** Poppler exec cannot meet the thumbnail SLO
   (Stage 2: 100 % of uploads thumbnailed) — then evaluate hayro/PDFium-in-Rust as a **sidecar**, still no cgo.
4. **OCR**: PaddleOCR wins the CER bake-off by ≥ 30 % but its Python sidecar exceeds the memory budget on the
   12 vCPU / 48 GB target (production-readiness.md) — then evaluate faster-paddle (Rust+ONNX) as a sidecar.
5. **Office fast path**: LO throughput remains the ceiling after `office-render` autoscaling **and** office2pdf
   passes G-FAST on ≥ 95 % of the simple-class corpus — then adopt it as an exec'd binary (still no team Rust).

## 6. Classification

| Item | Class |
|---|---|
| Go in-process engines (E1, E4, E5, E8, E9) | **A/B** (lightweight lib / custom in Go) |
| Exec'd C/C++ tools (Poppler, Ghostscript, Tesseract, LibreOffice) | **D** mature external |
| Typst, office2pdf (if gated in) | **D** exec'd static binary (Rust, zero team code) |
| WeasyPrint, PaddleOCR, `office-render` | **D** sidecar via Contract C; PaddleOCR **E** optional |
| go-pdfium WASM | **C** specialised engine behind Contract D, gated |
| cgo, shared memory, team-written Rust, JVM | **rejected** |

## 7. Acceptance criteria and gates

| Gate | Proof |
|---|---|
| Static-build invariant | CI step: `file bin/* \| grep -v "statically linked"` prints nothing; `ldd` fails on every worker binary (`scripts/ci/07-build.sh` extension) |
| procexec adoption | `grep -rn "exec.Command" --include=*.go` outside `shared/procexec` and tests returns 0 lines; orphan test 0/100 |
| Sidecar contract | contract test in each client worker against a stub server: deadline → 408 within deadline + 1 s; 413 at cap; `X-Engine-Version` present; egress test from inside the sidecar fails |
| WASM contract | soak 1 000 renders with 2 % synthetic traps: `pdfium_instance_recycled_total` increments, zero worker restarts, RSS plateau (to be measured: `docker stats`) |
| Rust exit criteria | each criterion has a harness number recorded in `benchmarks/` before any Rust discussion |
| Typst exec | `time typst compile stamp.typ` cold and warm inside the image — to be measured (expectation from asset size 17.5 MB compressed: well under 1 s; no sourced number) |

## 8. Risks and open questions

- **go-text/typesetting governance**: small project (161 stars on the repo page) with three-maintainer sign-off;
  a shaping bug could block Hindi stamps. Mitigation: Typst is the primary stamp path; go-text is the fallback.
- **Vendor-reported performance** (go-pdfium "2× slower than cgo", HarfRust "<25 % slower"): unverified; all gates
  use our harness numbers.
- **WeasyPrint CVE cadence** (three in 2026): sidecar must be rebuilt on advisories; egress block limits blast radius.
- **wazero compiler support**: amd64/arm64 only; if a future host is other (unlikely), WASM falls to interpreter and
  the Poppler fallback must stay.
- **gRPC** would reduce sidecar serialisation cost but adds codegen; revisit only if a sidecar grows beyond two ops.
- **Open**: who owns sidecar images in CI (`scripts/ci/07-build.sh` builds Go only today)? Is a Python sidecar
  acceptable to the security review given CVE cadence, or must html→pdf wait for a Go option that does not exist?

## Sources

(accessed 2026-11-07 unless noted)

- go-text/typesetting — https://github.com/go-text/typesetting ; `harfbuzz` package file list — https://pkg.go.dev/github.com/go-text/typesetting/harfbuzz and https://github.com/go-text/typesetting/tree/main/harfbuzz (v0.3.5, 2026-09-04, BSD-3-Clause/Unlicense; Indic, USE, Arabic, Khmer, Myanmar, Hangul, Hebrew, Thai shapers present)
- wazero — https://pkg.go.dev/github.com/tetratelabs/wazero (v1.12.0, 2026-05-28, Apache-2.0; zero deps, no cgo; `WithMemoryLimitPages`, `WithCloseOnContextDone`)
- go-pdfium — https://pkg.go.dev/github.com/klippa-app/go-pdfium (v1.21.4, 2026-10-21, MIT; WebAssembly mode via wazero, pool config, 4 GB cap, "~2× slower than cgo" — vendor claim)
- Typst release assets — https://api.github.com/repos/typst/typst/releases/latest (v0.15.1, 2026-07-17; `typst-x86_64-unknown-linux-musl.tar.xz` 17,462,992 B; aarch64 16,216,812 B); binary size on disk and startup: to be measured
- HarfRust — https://github.com/harfbuzz/harfrust (MIT; matches HarfBuzz v15.0.0; "<25 % slower" self-claim)
- cosmic-text — https://docs.rs/crate/cosmic-text/latest (0.19.0, 2026-04-22; shaping by HarfRust)
- krilla — https://docs.rs/crate/krilla/0.8.2 ; hayro — https://github.com/flxzt/hayro and https://docs.rs/crate/hayro/0.7.0 ("experimental, work-in-progress")
- office2pdf — https://docs.rs/crate/office2pdf/latest (0.10.0, 2026-10-28, Apache-2.0); dxpdf — https://docs.rs/crate/dxpdf/latest (0.8.1, MIT; no Indic reordering)
- digitorus/pdfsign — https://pkg.go.dev/github.com/digitorus/pdfsign (v1.0.0, 2026-10-06, BSD-2; PAdES B-B/B-T, no LT/LTA)
- excelize — https://pkg.go.dev/github.com/xuri/excelize/v2 (v2.11.1, 2026-11-02, BSD-3)
- WeasyPrint — https://pypi.org/project/weasyprint/ (70.0, 2026-09-08, BSD); Debian security tracker — https://tracker.debian.org/pkg/src:weasyprint
- chromedp — https://pkg.go.dev/github.com/chromedp/chromedp (v0.23.0, 2026-11-05, MIT)
- unoserver — https://pypi.org/project/unoserver/ (3.7, 2026-06-10, MIT)
- Internal: `deployment/base-go-builder.Dockerfile`, `convert-to-pdf/Dockerfile`, `convert-from-pdf/Dockerfile`, `CLAUDE.md` §1–§4, `docs/developer/architecture/production-readiness.md`
