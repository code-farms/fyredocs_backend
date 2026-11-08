# Executive Summary — Next-Generation Document Engine (analysis phase, 2026-11)

> Status: ANALYSIS AND ARCHITECTURE ONLY. No production code, dependency or API was changed. Twenty design
> documents, a measured baseline and a reproducible measurement script are in this folder; the index is
> `README.md`. Everything stated below cites a repository path, a measured file under `benchmarks/`, or a
> dated source inside the numbered documents.

## 1. What was asked, and the honest answer

The brief asks for an independent, lightweight, high-throughput document platform that can eventually run
ordinary workloads without LibreOffice, delivered as a staged architecture with no rewrites. The brief's own
rules decide the shape of the answer: no custom engine without a measured advantage, fidelity over
dependency elimination, no new language without measured benefit, and Office compatibility only when tested.

Applied to this repository those rules give a clear verdict:

- **LibreOffice is partially replaceable and must be isolated, not eliminated.** It leaves `convert-from-pdf`
  entirely (Stage 3) and HTML→PDF (Stage 2). It stays, as an isolated and properly warmed `office-render`
  pool, for DOCX/XLSX/PPTX/ODF→PDF and Office→ODF for the whole roadmap. A custom office layout engine is an
  optional, gated Stage 6 fast path for a classified "simple document" class, never a core deliverable
  (`04-libreoffice-replacement-strategy.md`, `07-layout-and-rendering-engine.md`).
- **One custom engine is worth owning: E4, page-model reconstruction** (positioned words and images → paragraphs,
  columns, tables → DOCX/XLSX/PPTX/ODF/HTML/TXT writers), in Go inside `convert-from-pdf`. It removes
  pdf2docx, PyMuPDF (AGPL), pdfplumber, Python and LibreOffice from that image (`08-pdf-engine-design.md`,
  `05-document-core-architecture.md`).
- **Go-first, no team-written Rust for 12 months.** Every worker is a `CGO_ENABLED=0` static build; Rust enters
  only as an exec'd static binary (Typst, for Unicode/Devanagari stamps) or behind a measured gate
  (go-pdfium via WASM). Every Rust lead examined (office2pdf, dxpdf, krilla, hayro, HarfRust, cosmic-text)
  is recorded with its Go alternative and why not now (`13-language-and-technology-selection.md`).
- **Ghostscript and Tesseract: fewer calls, not zero.** Compress and repair move to pdfcpu-first Go paths;
  flatten, PDF/A and `extreme` stay on Ghostscript; no Go OCR engine exists (`08-…` §5.6, `11-…` §5).

## 2. The most important findings (measured, not assumed)

| # | Finding | Evidence | Consequence |
|---|---|---|---|
| 1 | **The unoserver warm pool has never started.** The entrypoint passes `--user-installation file:///tmp/uno-profile-<port>`; every unoserver 3.x release converts a *path* to a URI itself and dies on a `file://` string. A second defect would still stop the first daemon after that fix: unoserver defaults `--uno-port` to 2002 and refuses `--port 2002`. Readiness treats the pool as informational, so for five months every Office/HTML conversion ran on cold `libreoffice --headless`. | `convert-to-pdf/entrypoint.sh:43`; `benchmarks/raw/startlog-convert-to-pdf.txt`; `benchmarks/raw/unoserver-corrected-2002.log`; defects D23/D23b in `01-existing-system-audit.md` | The July capacity figure (9 jobs/min) was measured in this state. Fix is three lines plus a readiness rule — **Q0, day 1 of Stage 1**. |
| 2 | **Warm vs cold LibreOffice on the same host**: with corrected arguments, two daemons start in ≈ 10.8 s; a warm DOCX/XLSX/PPTX/HTML/ODT conversion of the k6 fixtures takes ≈ 160–260 ms versus ≈ 570–1,000 ms cold, and the cold path allocates a fresh 250–470 MB `soffice` per job while warm daemons stay resident at ≈ 160–230 MB for two. | `benchmarks/baseline-2026-11.md` §7 (3-run medians, arm64 8-CPU/4 GB VM; synthetic fixtures carry little real layout, so per-MB scaling is **not** exercised — real-document corpus is Stage 1 Q13) | The largest latency and memory win available needs no new engine. |
| 3 | **Cold start to readiness**: convert-to-pdf ≈ 42 s as shipped (waiting for daemons that cannot start) versus 0.6–1.2 s for the other three workers. Idle Go-only RSS: 11–59 MiB. | `benchmarks/baseline-2026-11.md` §5–§6 | Readiness truth (a dead pool must fail `/readyz`) is a Stage 1 rule. |
| 4 | **Images rebuilt from HEAD**: convert-from-pdf 1.76 GB, convert-to-pdf 1.17 GB (fyredocs-base 1.07 GB), optimize-pdf 327 MB, organize-pdf 217 MB, the eight scratch services 43–95 MB; stripped worker binaries ≈ 40 MB each, ai-service 68 MB. | `benchmarks/baseline-2026-11.md` §1–§3 | The size is LibreOffice/Python/apt, not Go; Stage 3 removes ≈ 400 MB+ from convert-from-pdf by design, the Alpine LO package split saves little (`18-…` §5.6). |
| 5 | **Version and licence exposure**: pdfcpu pinned at v0.9.1 vs upstream v0.16.4 (8 advisories fixed in 0.16.x); Alpine 3.19 EOL since 2025-11-01; Ghostscript 10.05.1 inside the CVE-2025-59798/59799 range; PyMuPDF (AGPL-3.0) in the convert-from-pdf image via pdf2docx (itself MIT); unoserver unpinned. | `benchmarks/raw/tool-versions.txt`; `03-heavy-dependency-analysis.md` §5; `15-security-and-reliability.md` §2 | Stage 1 includes the pdfcpu upgrade, base-image bump and licence notice; Stage 3 removes the AGPL dependency. |
| 6 | **Devanagari cannot be rendered by the stamping path**: pdfcpu parses no GSUB/GPOS tables, no image ships a Devanagari font, and OCR is eng/hin only. | `07-…` §2; `organize-pdf/processing/processing_edit.go:276,446,604` | E7: Typst-rendered stamps via pdfcpu's PDF-watermark mode; Noto Devanagari in the base images (Stage 1 Q6, Stage 2). |
| 7 | **Operational gaps that need no engine**: no process-group kill (grandchild `soffice.bin`/Python survive cancels), no cancellation state, pdfcpu ignores `ctx`, `DisableConfigDir` only in one worker, PDF/A invocation lacks `PDFA_def.ps`/ICC and is almost certainly non-conformant, image-to-pdf options from the app are silently ignored, 500 MB uploads vs a 10-minute job context. | `01-…` D1–D22; `20-…` §3 Q-table | All are Stage 1–2 quick wins Q1–Q12. |

## 3. Target architecture in one table

Engines by capability (not by file format), each owned by the worker that owns the tool (CLAUDE.md §1);
only `shared/procexec` (and possibly `safearchive`, open) is shared.

| Engine | Implementation | Stage |
|---|---|---|
| E1 PDF object engine | pdfcpu (upgrade to v0.16.x first) | exists → 1 |
| E2 PDF raster/text | Poppler now; go-pdfium WASM only if the throughput gate passes | 5 (gate) |
| E3 Office layout | LibreOffice/unoserver, warmed (Q0–Q2), later its own `office-render` container | 1 → 6 |
| E4 Page-model reconstruction | Go, inside convert-from-pdf; replaces pdf2docx/PyMuPDF/pdfplumber/LO/Python there | 3 |
| E5 Distiller/optimizer | pdfcpu-first image recompression + repair tiering; Ghostscript retained for flatten/PDF-A/extreme | 4 |
| E6 OCR | Tesseract, per-page pipeline fixes; PaddleOCR sidecar only on a ≥ 30 % CER win | 1 → 6 |
| E7 Typesetting | Typst static binary → PDF-mode stamps (Unicode, page numbers, templates) | 2 |
| E8 Imaging | pure Go with explicit budgets; libvips only if a benchmark justifies a sidecar | keep |
| E9 Signing | `pdfsign` (BSD-2) PAdES B-B/B-T; certificate model is a product decision; no LTV | 5 |
| Cross-cutting | `shared/procexec`; fidelity harness `make bench-engine`; worker profiles; cancellation | 1–2 |

The document model is layered (flow IR + fixed-page IR with explicitly lossy bridges and verbatim opaque
parts for unknown XML), so editing one element never deletes another (`05-…`, `06-…`). API contracts are
preserved: new capabilities are new slugs in `routing.ToolServiceMap`, cancellation is additive, profiles
split NATS subjects behind a flag (`14-…`).

## 4. Highest-value optimisation opportunities, in order

1. **Q0 — make the warm pool exist and be observable** (3 lines + readiness truth). Measured warm/cold ratio
   ≈ 3–5× on fixture conversions; removes a 250–470 MB allocation per job.
2. **Q1/Q2 — size-scaled timeout, daemon kill/respawn, recycling** using unoserver's own
   `--conversion-timeout` / `--stop-after`; the 30 s fixed timeout becomes visible only after Q0.
3. **Q3 — `shared/procexec`** at all 20 exec sites: no orphaned `soffice.bin`/`gs`/`tesseract`/Python.
4. **Q7 — OCR per-page rendering inside the pool** (today all pages are rasterised to tmpfs up front);
   Hindi at 300 DPI with `tessdata_best`.
5. **Stage 3 E4** — removes LibreOffice, Python and the AGPL dependency from convert-from-pdf and makes
   pdf→docx/xlsx/pptx/odf one in-process engine (image ≈ 1.76 GB → target ≤ 150 MB, to be measured).
6. **Stage 4 E5** — Go compress/repair; Ghostscript calls on compress −≥ 70 %.
7. **Stage 6 office-render isolation** — independent scaling of the only remaining heavy dependency.

## 5. Effort and the first milestone

Roughly **17 engineer-months over ~12 months with two engineers**, plus 4–6 EM only if the optional
fast path is ever justified (`20-implementation-roadmap.md` §4; per-stage figures are taken from the
engine documents and reconciled there).

**Recommended first milestone — Stage 1 "Foundation" (6 weeks, both engineers, ≈ 3 EM):** Q0 on day 1,
then Q1–Q8, Q12, the pdfcpu upgrade, the Alpine/Ghostscript base bump, licence notices, and Q13 — the
fidelity harness (`make bench-engine`, ≥ 200-file corpus, SSIM/text/page-count differential checks) that
every later gate depends on. Measure **before and after** with `benchmarks/measure-images.sh` and an
off-box k6 `convert-to-pdf` ladder; the gain must be measured, not assumed.

Acceptance (from `20-…` §3.1 and `16-…` §5.10): ≥ 1.5× sustained office jobs/min on the same hardware class;
zero cold-LibreOffice fallbacks in a `mixed-realistic` run; zero orphan processes after 100 forced cancels;
Hindi DOCX→PDF with no tofu and SSIM ≥ 0.95; veraPDF PDF/A-2b pass; 300-page scan OCR tmpfs peak ≤ 200 MB;
a committed baseline from the harness.

## 6. Where the brief is unrealistic, said plainly

- A custom DOCX/XLSX/PPTX→PDF layout engine as the core deliverable: fidelity is the product; LibreOffice's
  capacity problem is horizontal scaling of an isolated pool, not an 8–9 EM engineering problem.
- Team-written Rust: breaks the static-build pattern (cgo) or adds sidecars, with no measured benefit for
  this tool catalogue.
- Full Ghostscript removal and any Go replacement for Tesseract: not available; plan for fewer calls.
- hayro as a render engine: experimental; not on the roadmap.
- True content-stream redaction and PAdES LTV: deferred with the reasons in `08-…` and `19-…`.
- 500 MB Office uploads under a 10-minute job context: one of the two must change (Q12).

## 7. Decisions needed from the technical lead

1. Approve the architecture and Stage 1 scope (this phase changes nothing until approved).
2. `safearchive` zip-limit guard in `shared/` (infrastructure under CLAUDE.md §2) or one copy per worker.
3. Office input cap value vs size-scaled job context (Q12).
4. Whether JS-rendered HTML is a product requirement (WeasyPrint vs Chromium).
5. The production-class host and date for the before/after k6 ladders.

## 8. What this phase produced

- `01`–`20` design and analysis documents (≈ 7,000 lines), each with options tables, classification A–E,
  gates and dated sources; `README.md` index; `docs/developer/mermaid/next-generation-engine-overview.md`.
- `benchmarks/`: `rebuild-all.sh` (timed rebuild of all 15 images from HEAD, tagged `baseline-2026-11`),
  `measure-images.sh`, `render-baseline.py`, `raw/` measurements and `baseline-2026-11.md`.
- Two production defects found by measurement (D23/D23b) and one by review (non-conformant PDF/A), all
  documented with file:line, none fixed in this phase by instruction.
