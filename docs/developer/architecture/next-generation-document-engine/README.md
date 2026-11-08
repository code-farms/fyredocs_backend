# Next-Generation Document Engine — analysis and architecture (2026-11)

Status: **ANALYSIS AND ARCHITECTURE ONLY.** No production code, dependency or API was changed. Every
document cites the repository (`path:line`), a measured file under `benchmarks/`, or a dated external
source; anything else is marked "to be measured" or "unverified".

Start with `EXECUTIVE-SUMMARY.md`, then `20-implementation-roadmap.md`. The audience is Fyredocs backend
engineers and the technical lead deciding the roadmap.

| Doc | What it answers |
|---|---|
| `EXECUTIVE-SUMMARY.md` | The decisions, the biggest findings, effort, and the recommended first milestone |
| `01-existing-system-audit.md` | Architecture map of today's processing path, per-worker traces, defects D1–D23b |
| `02-document-capability-matrix.md` | Every tool (47 backend slugs) with implementation, limits, feasibility and class A–E |
| `03-heavy-dependency-analysis.md` | LibreOffice, Ghostscript, Poppler, Tesseract, pdf2docx/PyMuPDF, Python, heavy Go deps: cost, licence, replacement decision |
| `04-libreoffice-replacement-strategy.md` | All 14 LibreOffice operations ranked; verdict PARTIAL + ISOLATED; gates before any bypass |
| `05-document-core-architecture.md` | Layered IR (flow + fixed-page), opaque-part preservation, comparison, versioning |
| `06-parsing-engine-design.md` | Parser framework, streaming OOXML/ODF, limits, malformed-file handling |
| `07-layout-and-rendering-engine.md` | Shaping/layout/rendering separation; Typst for Unicode stamps; optional gated fast path |
| `08-pdf-engine-design.md` | pdfcpu-based PDF subsystem: distiller, repair, PDF/A, signing, rendering bake-off, compare/redact |
| `09-spreadsheet-engine-design.md` | excelize-based storage/writers; formula compatibility roadmap T0–T4 |
| `10-presentation-engine-design.md` | PPTX structure parsing vs rendering; text+image slide reconstruction |
| `11-image-and-ocr-subsystems.md` | Pure-Go imaging budgets; Tesseract pipeline fixes; optional OCR sidecar bake-off |
| `12-performance-architecture.md` | Measured baseline, where time/memory goes, the 17 techniques judged, SLOs per stage |
| `13-language-and-technology-selection.md` | Go-first stance; Rust leads with Go alternatives; the four integration contracts |
| `14-api-and-distributed-architecture.md` | API extensions that preserve contracts; cancellation; worker profiles; autoscaling signal |
| `15-security-and-reliability.md` | Threats, limits, sandboxing, sanitize-on-output, scanning, readiness truth |
| `16-benchmarking-and-testing.md` | `make bench-engine` harness, metrics and methods, differential testing, release gate |
| `17-custom-library-roadmap.md` | Every proposed internal library: API, structures, memory, tests, owner, stage |
| `18-deployment-optimization.md` | Profile images, package removal verdicts, reproducible builds, SBOM, hardening |
| `19-risk-and-compatibility-analysis.md` | Compatibility matrix per format, risk register, profiles, decision log |
| `20-implementation-roadmap.md` | Six stages with milestones, effort, gates, decision points, first 6 weeks |

## Benchmarks

| File | Purpose |
|---|---|
| `benchmarks/baseline-2026-11.md` | Measured baseline (rendered only from `raw/` by `render-baseline.py`) |
| `benchmarks/measure-images.sh` | Reproducible measurement: image/binary sizes, tool versions, idle RSS, cold start, conversion timings |
| `benchmarks/rebuild-all.sh` | Timed rebuild of every image from HEAD, tagged `baseline-2026-11` |
| `benchmarks/render-baseline.py` | Turns `raw/*.tsv` into the baseline document |
| `benchmarks/raw/` | The measurements themselves (TSV, logs) |
| `benchmarks/corpus/README.md` | Corpus categories and rules for the fidelity harness |

Diagram: `docs/developer/mermaid/next-generation-engine-overview.md`.

## Conventions used in these documents

Template headings: Purpose · Current state · Requirements · Options evaluated · Recommendation ·
Classification (A lightweight library / B custom engine / C specialised engine / D mature external /
E optional plugin) · Acceptance criteria and gates · Risks and open questions · Sources. Engine names
E1–E9, quick wins Q0–Q13, defects D1–D23b and gates G-* are shared across documents and defined in
`EXECUTIVE-SUMMARY.md`, `01-…`, `04-…` and `16-…`.
