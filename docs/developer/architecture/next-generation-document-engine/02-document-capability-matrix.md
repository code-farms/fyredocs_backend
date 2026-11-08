# 02 — Document Capability Matrix

> Part of the Next-Generation Document Engine analysis (2026-11). Status: ANALYSIS — no code changed.

## 1. Purpose and scope

One row per capability the backend routes today, keyed on the 47 slugs in
`job-service/internal/routing/routing.go:20-73` (aliases collapsed, 39 rows), cross-referenced with the
web registry (`fyredocs_frontend/src/config/tools.ts:11`, 37 tool keys) and the app registry
(`fyredocs_app/src/config/tools.ts:11`, 39 tool keys; adds `flatten-pdf` and `pdf-metadata`). Then the
non-tool document features (reader, AI ops, extract, transcribe, library, browser-side pdf-lib tools) and the
features the roadmap wants that do not exist. Every row gets the classification defined in §6. Nothing is
classified "remove": the brief forbids removing a tool, and every row has a keep-or-replace path.

Columns use these abbreviations: **Surface** W = web, A = app, W+A = both. **Perf known** states what the
repository actually fixes: 10-min job context (`convert-to-pdf/internal/worker/worker.go:546` and
siblings), UI cap 50 MB per file (`fyredocs_frontend/src/config/tools.ts:24`), images 20 MB (`:111`),
k6 fixtures small ≈0.8 MB / medium ≈8 MB / large ≈40 MB (`scripts/k6/fixtures/gen.py:8,22`), sustained
9 jobs/min mixed (`scripts/k6/results/capacity-jobs-20260727-234123.md`). Per-tool timings are **to be
measured** by `benchmarks/measure-images.sh` → `benchmarks/baseline-2026-11.md`.

## 2. Current state

### 2.1 Table A — identity and implementation

| # | Slug (aliases) | Surface | Implementation (file:line) | Library / binary | Input → output | Options exposed |
|---|---|---|---|---|---|---|
| 1 | pdf-to-word (pdf-to-docx) | W+A | `convert-from-pdf/processing/processing.go:48-58` → `processing_free.go:463-481` (`pdf2docx convert`), fallback `:239-277` (`libreoffice --infilter=writer_pdf_import`) | pdf2docx 0.5.8 (PyMuPDF, opencv) → LibreOffice | PDF → DOCX | none |
| 2 | pdf-to-excel (pdf-to-xlsx) | W+A | `processing.go:60-64` → `processing_free.go:340-352` (`python3 pdf_to_xlsx.py`) | pdfplumber 0.11.4 + openpyxl 3.1.5 (`convert-from-pdf/scripts/pdf_to_xlsx.py`) | PDF → XLSX | none |
| 3 | pdf-to-powerpoint (pdf-to-ppt, pdf-to-pptx) | W+A | `processing.go:65-67` → `processing_free.go:282-319` (`pdftoppm -png`) + `pptx_builder.go:16` | Poppler + hand-written OOXML (pure Go) | PDF → PPTX (one image per slide) | none |
| 4 | pdf-to-image (pdf-to-img) | W+A | `processing_free.go:154-202` (`pdfinfo`, `pdftoppm -r DPI`) | Poppler | PDF → PNG/JPG/TIFF (1 page) or ZIP | web: none (PNG, `tools.ts:214-231`); app: format png/jpg/tiff, dpi 150/300/600 (`fyredocs_app/src/components/options/ToolOptionsPanel.tsx:216-239`); backend clamps 72–600 (`:38-42`) |
| 5 | pdf-to-html | W+A | `processing_free.go:493-512` (`pdftohtml -s -noframes`) | Poppler | PDF → ZIP(HTML + images) | none |
| 6 | pdf-to-text (pdf-to-txt) | W+A | `processing_free.go:517-530` (`pdftotext -layout`) | Poppler | PDF → TXT | none |
| 7 | pdf-to-pdfa | W+A | `processing_free.go:209-232` (`gs -dPDFA=2 … pdfwrite`) | Ghostscript | PDF → PDF (claimed PDF/A-2b, `fyredocs_app/src/config/tools.ts:315`) | none |
| 8 | pdf-to-odt | W+A | `processing.go:74-76` → `processing_free.go:239-277` | LibreOffice `writer_pdf_import` | PDF → ODT | none |
| 9 | pdf-to-ods | W+A | `processing_free.go:368-376` (pdf_to_xlsx.py → `libreoffice --convert-to ods`) | pdfplumber + LibreOffice | PDF → ODS | none |
| 10 | pdf-to-odp | W+A | `processing_free.go:389-397` (image PPTX → `libreoffice --convert-to odp`) | Poppler + pure Go + LibreOffice | PDF → ODP | none |
| 11 | word-to-pdf | W+A | `convert-to-pdf/processing/processing.go:49` → `processing_free.go:59-81` (`unoconvert` 30 s → `libreoffice --headless`) | LibreOffice via unoserver | DOC/DOCX → PDF | none |
| 12 | excel-to-pdf | W+A | `processing.go:55` → same | LibreOffice | XLS/XLSX → PDF | none |
| 13 | powerpoint-to-pdf (ppt-to-pdf) | W+A | `processing.go:52` → same | LibreOffice | PPT/PPTX → PDF | none |
| 14 | html-to-pdf | W+A | `processing.go:58` → same | LibreOffice (Writer/Web import) | HTML → PDF | none |
| 15 | image-to-pdf (img-to-pdf) | W+A | `processing.go:61-63` → `processing_free.go:112-115` (`api.ImportImagesFile(…, nil, nil)`) | pdfcpu | PNG/JPG/WEBP ×≤20 → PDF (`job-service/handlers/jobs.go:62,1291-1293`) | app sends pageSize/margin/orientation/quality (`fyredocs_app/src/config/tools.ts:383`) — **ignored by the worker** |
| 16 | odt-to-pdf | W+A | `processing.go:64` → `officeToPDF` | LibreOffice | ODT → PDF | none |
| 17 | ods-to-pdf | W+A | `processing.go:67` | LibreOffice | ODS → PDF | none |
| 18 | odp-to-pdf | W+A | `processing.go:70` | LibreOffice | ODP → PDF | none |
| 19 | word-to-odt | W+A | `processing.go:73` → `processing_free.go:85-107` | LibreOffice | DOC/DOCX → ODT | none |
| 20 | excel-to-ods | W+A | `processing.go:76` | LibreOffice | XLS/XLSX → ODS | none |
| 21 | powerpoint-to-odp | W+A | `processing.go:79` | LibreOffice | PPT/PPTX → ODP | none |
| 22 | merge-pdf | W+A (+browser pdf-lib) | `organize-pdf/processing/processing_free.go:19-24` (`MergeCreateFile`) | pdfcpu | PDF ×2–20 → PDF | order = upload order |
| 23 | split-pdf | W+A | `:29-110` (`CollectFile`/`SplitFile`/`ExtractPagesFile`) | pdfcpu | PDF → ZIP of PDFs | mode all / range / every-N / into-N |
| 24 | organize-pdf (reorder) | W+A (+browser) | `:164-183` (`CollectFile`) | pdfcpu | PDF → PDF | page order string |
| 25 | remove-pages | W+A (+browser) | `:115-134` (`RemovePagesFile`) | pdfcpu | PDF → PDF | pages |
| 26 | extract-pages | W+A (+browser) | `:139-158` (`CollectFile`) | pdfcpu | PDF → PDF | pages |
| 27 | rotate-pdf | W+A (+browser) | `:188-201` (`RotateFile`) | pdfcpu | PDF → PDF | 90/180/270; all/odd/even (`processing.go:85`) |
| 28 | scan-to-pdf | W+A (app camera) | `processing_scan.go:444-490` (imaging + `ImportImagesFile`), OCR `:498-551` (`tesseract` sequential) | pure Go `internal/imaging` + pdfcpu + Tesseract | images ×≤20 → PDF (searchable if OCR) | OCR on/off, lang eng/hin/eng+hin, page size auto/a4/letter/id/card, margins none/small/normal/large, per-page corners/cleanup/enhance (`job-service/handlers/tool_options.go:368-448`) |
| 29 | watermark-pdf | W+A | `processing_edit.go:211-284` (text), `:295-353` (image data-URL) | pdfcpu (`AddWatermarksFile`) | PDF → PDF | text/image, position center/diagonal/tiled, opacity, font size 12–120, colour, scale |
| 30 | protect-pdf | W+A | `:120-191` (`EncryptFile`) | pdfcpu | PDF → PDF | open + permissions password, 5 permission toggles, AES-256/AES-128/RC4-128 (`tool_options.go:518`) |
| 31 | unlock-pdf | W+A | `:197-206` (`DecryptFile`) | pdfcpu | PDF → PDF | password |
| 32 | sign-pdf | W+A | `:475-545` (image watermark on selected pages) | pdfcpu | PDF → PDF | drawn/uploaded PNG/JPG, 7 anchors, page selection (`tool_options.go:192`) |
| 33 | edit-pdf | W (app stub, `fyredocs_app/src/config/tools.ts:623`, `src/app/tool/[toolId].tsx:383`) | `:551-619` (text watermark per annotation) | pdfcpu | PDF → PDF | annotations[{type, content, page, position, fontSize}] |
| 34 | add-page-numbers | W+A | `:381-460` (per-page `AddWatermarksFile`) | pdfcpu | PDF → PDF | 6 anchors, start number, size 8–48, 4 formats |
| 35 | pdf-metadata | A only | `processing_metadata.go:34-68` (`AddPropertiesFile`) | pdfcpu | PDF → PDF | title/author/subject/keywords… (`tool_options.go:326`) |
| 36 | compress-pdf | W+A | `optimize-pdf/processing/processing_free.go:54-103`, args `:106-165` | Ghostscript pdfwrite | PDF → PDF | quality low/medium/high/extreme (web still sends legacy `ebook`, `fyredocs_frontend/src/hooks/useJob.ts:124`, which hits the default branch = medium) |
| 37 | repair-pdf | W+A | `:174-211` (gs `/prepress`) | Ghostscript | PDF → PDF | none |
| 38 | ocr-pdf | W+A | `:474-632` (`pdftoppm` all pages → `tesseract` errgroup) | Poppler + Tesseract (+pdfcpu merge) | PDF → searchable PDF + text ≤512 KB in metadata | lang eng/hin/eng+hin (`:367-402`), dpi auto/150/300/400/600 (`:499-509`) |
| 39 | flatten-pdf | A only | `:218-266` (gs `-dPreserveAnnots=false -dPrinted`) | Ghostscript | PDF → PDF | none |

### 2.2 Table B — requirements, limitations and feasibility

Replacement feasibility (R) = can an existing Go/static-binary engine do this today; custom feasibility (C) =
can the team own an implementation inside the 12-month, two-engineer roadmap. Scale: high / medium / low /
none, each with the reason.

| # | Slug | Functional requirement | Perf known | Compatibility requirement | Current limitations | R | C | Class |
|---|---|---|---|---|---|---|---|---|
| 1 | pdf-to-word | editable DOCX with paragraphs, lists, tables, images in reading order | 10-min ctx; 50 MB; synthetic progress 30→80 % (`processing_free.go:427-456`) | opens in Word/LibreOffice; text recall ≈ pdftotext | PyMuPDF AGPL in SBOM; scanned PDFs yield empty DOCX; LO fallback produces text frames; two interpreters in image | medium — no Go PDF→DOCX exists; E4 must be built | high — words+bbox from `pdftotext -bbox-layout` now, PDFium later; OOXML writer like `pptx_builder.go` | B |
| 2 | pdf-to-excel | one sheet per page, detected tables as cells, text fallback | same | opens in Excel/Calc | heuristic table detection; pure-Python speed; no merged cells/formulas | medium — excelize writes XLSX; table detection is the E4 work | high (shares E4) | B |
| 3 | pdf-to-powerpoint | one slide per page | same; pdftoppm default DPI | opens in PowerPoint/Impress | slides are bitmaps: no text, no selection; DPI fixed | high — already pure Go writer; render engine is the only external piece | high (E4 adds text + image slides) | B |
| 4 | pdf-to-image | raster every page at chosen DPI/format | same; DPI ≤600; ≤10 000 px edge; all pages before zip | pixel-identical page count to the source | web exposes PNG only though backend does JPG/TIFF/ZIP; whole document rendered up front | medium — go-pdfium WASM (no cgo) candidate; gate in `01-existing-system-audit.md` §7 | low — writing a PDF rasteriser is out of scope | C (D interim) |
| 5 | pdf-to-html | HTML + images zip | same | browser-renderable | pdftohtml absolute-positioned divs; no semantic structure | medium | high after E4 (HTML writer) | B (D interim) |
| 6 | pdf-to-text | layout-preserving text | same | — | no OCR detection for scanned input (`:514-516`) | high — pdfcpu `ExtractContent` is not layout-aware; pdftotext is the interim extractor | high (E4 TXT writer) | B (D interim) |
| 7 | pdf-to-pdfa | ISO 19005-2 conformant output | same | veraPDF 2b pass | invocation is wrong (D3 in `01-existing-system-audit.md` §8.1) | low — no Go PDF/A converter; gs stays | none in roadmap | D |
| 8 | pdf-to-odt | editable ODT | same | opens in Writer | LibreOffice PDF import = one frame per line | medium (falls out of E4 + ODF writer) | high after E4 (1 EM) | B |
| 9 | pdf-to-ods | ODS tables | same | opens in Calc | two engines, two temp files | medium | high after E4 | B |
| 10 | pdf-to-odp | ODP slides | same | opens in Impress | bitmap slides via LO second stage | medium | high after E4 | B |
| 11 | word-to-pdf | print-faithful PDF | 2 s + 1.5 s/MB (`production-readiness.md:104`); 30 s unoconvert then cold; 10-min ctx | SSIM ≥0.95 vs LibreOffice reference on the corpus; Devanagari shaped | no Devanagari fonts (D14); fixed timeout (D1); ghost conversions | low — no Go DOCX layout engine; optional gated fast path only (`04-libreoffice-replacement-strategy.md`) | low (4–6 EM, simple-doc subset only) | D |
| 12 | excel-to-pdf | print layout with page breaks, formulas evaluated | same | matches Excel pagination | excelize reads but has no print-layout engine | none | none (8+ EM) | D |
| 13 | powerpoint-to-pdf | slide rendering with effects | same | matches PowerPoint rendering | — | none | none (8+ EM) | D |
| 14 | html-to-pdf | CSS layout, images | same | flex/grid/Devanagari/RTL rendered | LibreOffice HTML import is Writer/Web, poor CSS; no JS (also a security feature) | high — WeasyPrint sidecar (Python, no JS) or Chromium if JS required | medium (sidecar integration 0.5–1 EM) | D |
| 15 | image-to-pdf | one page per image, optional page size/margins | same; 20 MB/image; ≤20 | — | options ignored (D17); gif/bmp rejected at the boundary | high — pdfcpu `Import` already supports form/pos/scale (`processing_scan.go:415-429`) | high (reuse scan-to-pdf config) | A |
| 16–18 | odt/ods/odp-to-pdf | as 11–13 | same | — | — | none | none | D |
| 19–21 | word-to-odt, excel-to-ods, powerpoint-to-odp | lossless-as-possible ODF | same | round-trip in LibreOffice | — | low — would need full OOXML↔ODF model (4+ EM) | low | D |
| 22 | merge-pdf | concatenate with bookmarks/forms preserved | 10-min; 20 files × 50 MB | output opens everywhere | pdfcpu no ctx; in-memory contexts for all inputs (`tmpfs.go` budget sums inputs) | already Go | n/a | A |
| 23 | split-pdf | page-range splitting | same | — | zip output only; no per-file naming options | already Go | n/a | A |
| 24–27 | reorder / remove / extract / rotate | page-level ops | same | — | rotation only 90/180/270 | already Go | n/a | A |
| 28 | scan-to-pdf | dewarp, clean, paginate, optional OCR | same; 50 MP per image (`decode.go:17`) | searchable text layer when OCR on | sequential OCR (D10); OCR via Tesseract only | imaging already Go; OCR has no Go engine | n/a (imaging); OCR = Tesseract (C) | A (+C) |
| 29 | watermark-pdf | text/image stamps, tiling, opacity | same | Unicode text | Helvetica only (D13); data-URL unbounded (D12) | high — pdfcpu PDF-mode watermark + Typst-rendered page (Stage 2) | high | A |
| 30 | protect-pdf | AES-256/128, RC4, permission bits | same | opens in Acrobat with correct permission UI | pdfcpu config-dir hazard in other workers (D4) | already Go | n/a | A |
| 31 | unlock-pdf | remove owner/user password | same | — | — | already Go | n/a | A |
| 32 | sign-pdf | visible signature image; **product asks for e-signature** | same | — | not a cryptographic signature; no PAdES; no certificate model | high — `digitorus/pdfsign` (Go, BSD-2-Clause) for PAdES-B-B/B-T (Stage 5) | medium — needs certificate decision first | A |
| 33 | edit-pdf | text annotations | same | — | Helvetica only; stub on app; unbounded count | high (Typst→pdfcpu) | high | A |
| 34 | add-page-numbers | numbered pages with formats | same; N rewrites per N pages (D11) | Unicode digits (Devanagari numerals) | Helvetica only; O(N²) I/O | high | high | A |
| 35 | pdf-metadata | XMP/Info dictionary edit | same | — | app only; no web UI | already Go | n/a | A |
| 36 | compress-pdf | smaller file, readable text, image downsampling | same | ≥80 % of gs size reduction on image-heavy corpus at same DPI; text-only never grows | gs AGPL exec; `extreme` grayscale; no SMask/ICC handling in a Go path yet | high for DCT/Flate image downsampling via pdfcpu + x/image; gs stays for extreme/flatten-like cases | high (E5, 2 EM) | B |
| 37 | repair-pdf | open a damaged file | same | — | lossy re-distill; no tiering | high — pdfcpu relaxed read + rewrite as tier 1 | high (0.5 EM) | A (D tier 2) |
| 38 | ocr-pdf | searchable layer + text, eng/hin | same; ≤4 parallel; all pages rendered first (D9) | CER on Hindi scans to be measured (Stage 6 bake-off) | tmpfs peak; `tessdata` fast models only; 150 DPI default too low for Devanagari | none — no Go OCR engine | none | C |
| 39 | flatten-pdf | bake annotations/form appearances | same | selectable text retained | app only; gs only | low — pdfcpu cannot bake appearance streams; rasterising unacceptable | none in roadmap | D |

### 2.3 Non-tool document features

| Feature | Where | Implementation | Limits | Class |
|---|---|---|---|---|
| Reader | web `/reader` (`fyredocs_frontend/src/pages/ReaderPage.tsx`) | pdf.js: search, outline, bookmarks, read-aloud, XFDF/JSON annotations | non-WinAnsi annotation text → `?` (`src/lib/annotate/pdfAnnots.ts:101-115`); no server rendering | A (browser lib) |
| AI ops | `POST /api/ai/ops/:op` | OpenAI-compatible chat, 9 ops (`ai-service/internal/ops/ops.go:71-81`), 50 pages / 250 k chars (`:26-34`), 1 MiB body | text comes from the client; server never reads MinIO | D (external model) |
| AI extract | `POST /api/ai/extract` | vision model; ≤2 images × 1.5 MB (`ai-service/handlers/extract.go:22-29,51,106`); 4 MiB gateway cap | — | D |
| Transcribe (handwriting) | `POST /api/ai/transcribe` | vision model, eng+hin (`ai-service/internal/transcribe/transcribe.go:1-5`) | — | D |
| Library | document-service (`document-service/routes/routes.go:22-68`) | CRUD, folders, tags, exports CSV/JSON in memory (`handlers/exports.go:169-251`), SHA-256 of stored object (`handlers/hashing.go:25-50`) | no thumbnails (`models/document.go:39` never set), no page count, no server text extraction | A |
| Browser-side tools | web "Process in browser" | pdf-lib for rotate/reorder/remove/extract/merge/image-to-pdf (`src/lib/localTools.ts:18`), `WRITER_MAX_BYTES` cap | no job record; no password PDFs (`:84`) | A |
| Edge detection | `POST /internal/v1/detect-edges` | pure Go (`organize-pdf/internal/httpapi/detect.go:60-71`), ≤30 MiB (`:23`) | internal only | A |
| App-only | camera scanner, Skia annotation editor with redact/blur, local FTS, app lock, cloud backup | client code in `fyredocs_app` | redaction is **visual only on the client**; nothing server-side | A |

### 2.4 Features the vision wants that do not exist today

| Feature | Exists? | One-line feasibility |
|---|---|---|
| Compare two PDFs | no (grep of routing/tools: no slug) | medium — E4 word stream diff + pdftoppm render diff; Stage 5, ≈1 EM after E4 |
| Server-side redaction | no (app redact is a visual overlay) | low for true content-stream redaction in Go (2+ EM, liability); medium for rasterise-region (render page, paint box, re-import image) — Stage 5 |
| Templates / certificates (fill + render) | no | medium — Typst static binary gives layout + Unicode; data model and UI are the cost; Stage 2 unlocks it |
| EPUB input/output | no | medium — EPUB 3.3 is zipped XHTML+CSS: output falls out of E4's HTML writer; input needs an HTML→PDF path (WeasyPrint) |
| Markdown → PDF / DOCX | no | high — goldmark (Go, MIT) → HTML → WeasyPrint, or → Typst; 0.5 EM once html-to-pdf exists |
| PAdES signing | no (sign-pdf is a stamp) | medium — `digitorus/pdfsign` in-process; blocked on the certificate-model decision (whose key, TSA, revocation) |
| Thumbnails | no (`thumbnail_path` never populated) | high — `pdftoppm -f 1 -l 1 -scale-to 320` on `document.uploaded`, MinIO `thumbs/` prefix; Q10, 1 wk |
| Page count at upload / in results | no (only inside split/reorder/pdf-to-image via `ReadContextFile`) | high — `api.PageCountFile`; Q9, 2 d |
| Job cancellation | no (`00001_baseline.sql:50` has no state; D21) | medium — `jobs.cancel.<id>` subject + per-worker cancel map + procexec kill; touches state machine, swagger, postman; Q11, 1.5 wk |
| Batch across formats (one job, mixed inputs) | no — one tool per job; MIME category per tool (`jobs.go:1312`) | medium — needs a job-group model in job-service and per-input routing; not in the 12-month roadmap |
| Unicode / Devanagari stamps | no (Helvetica only) | high — Typst → PDF page → pdfcpu PDF-mode watermark; Stage 2 |
| Fonts for Hindi office conversion | no fonts installed | high — `font-noto`, `font-noto-devanagari`, `fontconfig` + `fc-cache` in the base image; Q6, 1 d |

## 3. Requirements

Aggregated from Table B; each row's specific requirement lives there.

- **Functional:** all 39 rows keep their input/output contract; aliases keep resolving
  (`job-service/handlers/jobs.go:98` `normalizeToolType`); app-only tools (flatten-pdf, pdf-metadata) stay
  routed even without a web UI.
- **Performance:** every job completes inside the 10-min context on the medium fixture; the office rows
  must stop depending on the 30 s unoconvert timeout; throughput target ≥1.5× the k6 ladder on the same
  hardware after Stage 1 (roadmap acceptance).
- **Compatibility:** no row may claim more than its gate proves. Office→PDF fidelity is defined relative to
  LibreOffice output (SSIM on the harness corpus), never relative to Microsoft Office until a test says so.
  PDF/A must pass veraPDF. Encrypted output must open in Acrobat with the chosen permissions.
- **Security:** options reaching a command line stay allowlisted (`optimize-pdf/processing/
  processing_free.go:367-402`; `convert-from-pdf/processing/processing_free.go:28-34`;
  `job-service/handlers/tool_options.go:130-160`); data-URL images get a size cap and sniff; every exec site
  gets process-group kill.

## 4. Options evaluated

Engine options are evaluated per capability family, not per row; the detailed tables are in the sibling
documents. Summary:

| Option | Language | Licence | Maturity (2026-11) | Fidelity/coverage | Integration | Effort (EM) | Verdict |
|---|---|---|---|---|---|---|---|
| Keep pdfcpu for E1 ops (rows 15, 22–27, 29–35, 37) | Go | Apache-2.0 | v0.16.4 (2026-10-27); pinned v0.9.1 | full for page/object ops; no appearance-stream baking | in-process | 0.5 (upgrade + ctx checks) | keep, extend |
| E4 page-model reconstruction (rows 1–3, 5–6, 8–10) | Go | — | to build | target pdf2docx parity | in-process in convert-from-pdf | 4–5 | build, Stage 3 |
| LibreOffice via unoserver (rows 11–14, 16–21, fallback 1, 8–10) | C++ / Python | MPL-2.0 / MIT | LO 7.6.7 (Alpine 3.19), 7.4.7 (bookworm); upstream 26.2.x; unoserver 3.7 | reference fidelity | daemon pool → isolated office-render container | 1 (lifecycle) + 2 (Stage 6) | keep for the roadmap |
| WeasyPrint sidecar (row 14) | Python | BSD-3 | current (not pinned yet) | CSS Paged Media, no JS | HTTP sidecar | 0.5–1 | adopt, Stage 2, gated |
| Ghostscript (rows 7, 36–39) | C | AGPL-3.0 | 10.08.0 (2026-09-08); images ship 10.05.1 / 10.0.0 | reference distiller | exec | 0 | keep for flatten/PDF-A/extreme; fewer calls after E5 |
| E5 Go optimiser (row 36) | Go | — | to build | ≥80 % of gs reduction gate | in-process | 2 | build, Stage 4 |
| Poppler CLIs (rows 3–6, 38 aux) | C++ | GPL-2/3 | 26.11.0 (2026-11-05); images ship 23.10.0 / 22.12.0 | fast, batched | exec | 0 | keep until the E2 bake-off |
| go-pdfium WASM (rows 4, E4 extractor) | Go + WASM | MIT / Apache-2.0 (PDFium) | v1.19.7 (2026-04) | PDFium rendering | in-process WASM (wazero, no cgo) | 1 (bake-off) | Stage 5 decision |
| Tesseract (rows 28, 38) | C++ | Apache-2.0 | 5.5.4 (2026-10-28); images ship 5.3.3 | eng/hin | exec | 0.5 (tuning) | keep; PaddleOCR sidecar only on ≥30 % CER gain |
| Typst static binary (rows 29, 33, 34, templates) | Rust (tool, no team Rust) | Apache-2.0 | current | Unicode shaping incl. Devanagari | exec | 0.5 | adopt, Stage 2; Go alternative `go-text/typesetting` → PNG stamp (raster, lower quality) |
| digitorus/pdfsign (row 32) | Go | BSD-2-Clause | PAdES B-B/B-T with RFC 3161 TSA, no LTV (`08-…` §4) | PAdES-B-B/B-T | in-process | 1 | Stage 5 after certificate decision |

## 5. Recommendation

Classify and sequence by the engine that owns each row, not by file format: pdfcpu rows stay and gain ctx
checks and Unicode stamping; the eight PDF→editable rows become one Go engine (E4) that removes pdf2docx,
pdfplumber, LibreOffice and Python from convert-from-pdf; office→PDF/ODF rows stay on LibreOffice behind a
corrected daemon lifecycle and later an isolated office-render pool; compress gets a Go path with gs as
fallback; flatten, PDF/A and OCR stay on their mature external engines. For every Rust/native item the Go
alternative is named in §4. No row is removed; app-only rows (flatten-pdf, pdf-metadata) should get a web UI
rather than being dropped, and the web should expose the JPG/TIFF/DPI options the backend already accepts
for pdf-to-image.

## 6. Classification

Definitions (from the analysis template): **A** lightweight library in-process (pdfcpu, x/image, pdf.js,
pdf-lib); **B** custom engine the team owns (E4 page model, E5 optimiser); **C** specialised engine for one
task, external but tightly integrated (Tesseract, PDFium WASM); **D** mature external program kept as-is
(LibreOffice, Ghostscript, Poppler, WeasyPrint, LLM API); **E** optional plugin that ships only if a gate
passes (PaddleOCR sidecar, Chromium, simple-DOCX fast path — none is a row today).

### 6.1 Rows per classification (target state)

| Class | Count | Rows |
|---|---|---|
| A | 16 | 15, 22–27 (6), 28, 29, 30, 31, 32, 33, 34, 35, 37 |
| B | 9 | 1, 2, 3, 5, 6, 8, 9, 10, 36 |
| C | 2 | 4, 38 |
| D | 12 | 7, 11, 12, 13, 14, 16, 17, 18, 19, 20, 21, 39 |
| E | 0 | — (E candidates attach to rows 11, 14, 38 as optional plugins) |
| **Total** | **39** | = 47 slugs with aliases collapsed |

### 6.2 Rows per current primary engine

| Engine today | Count | Rows | Secondary use |
|---|---|---|---|
| LibreOffice | 11 | 8, 11–14, 16–21 | second stage of 9, 10; fallback of 1 |
| Ghostscript | 4 | 7, 36, 37, 39 | — |
| Poppler | 5 | 3, 4, 5, 6, 10 | `pdfinfo`/`pdftoppm` inside 38 |
| Tesseract | 1 | 38 | optional OCR inside 28 |
| pdf2docx (PyMuPDF) | 1 | 1 | — |
| pdfplumber (+openpyxl) | 2 | 2, 9 | — |
| pdfcpu | 15 | 15, 22–27, 29–35 | merge step of 38, fallback pages of 28/38 |
| pure Go (imaging, pptx_builder) | — | inside 3, 10, 28 | — |
| **Total** | **39** | | |

After Stage 3 the pdf2docx and pdfplumber columns go to zero and LibreOffice drops to 10 primary rows (row 8
moves to E4); after Stage 4 Ghostscript stays at 4 rows but is the *fallback* on row 36; after Stage 5 Poppler
may drop to zero if the PDFium gate passes.

## 7. Acceptance criteria and gates

| Row group | Gate | Proof |
|---|---|---|
| 1, 2, 5, 6, 8–10 (E4) | labelled 200-PDF set: text recall ≥99 % vs `pdftotext`; table cell F1 ≥ pdfplumber; DOCX→PDF(LibreOffice) round-trip SSIM ≥ pdf2docx on 90 %; image ≤150 MB; no AGPL in SBOM | `make bench-engine` report |
| 3 | PPTX opens in PowerPoint and Impress; slide count = page count; text slides where E4 finds text | harness |
| 4 | PDFium WASM ≥0.7× pdftoppm throughput at 150 DPI; identical page counts on the fuzz corpus; no cross-job crash — else keep Poppler | bake-off script in `benchmarks/` |
| 7 | veraPDF 2b pass on the corpus (veraPDF added to test-binaries) | CI job |
| 11–14, 16–21 | ≥1.5× sustained office jobs/min on the k6 ladder; zero cold-LibreOffice fallbacks in a mixed-realistic run (`convert_fallback_total` = 0); Hindi DOCX→PDF SSIM ≥0.95, no tofu | k6 + harness |
| 14 (WeasyPrint) | 30 HTML fixtures SSIM ≥0.9 vs Chrome reference; egress blocked | harness |
| 15 | app options honoured; output page size verified by `pdfcpu info` | unit + harness |
| 29, 33, 34 (Typst) | Devanagari stamp shaped and selectable (`pdftotext` round-trip) | harness |
| 32 (PAdES) | signature validates in Acrobat and `pdfsig` | harness |
| 36 (E5) | ≥80 % of gs size reduction on image-heavy corpus at same DPI; text-only never grows; ≤50 % gs wall time; SSIM ≥0.9 at medium | harness |
| 37 | tier-1 pdfcpu repairs ≥X % of the broken corpus (X to be set after the baseline run) | harness |
| 38 | 300-page scan OCR tmpfs peak ≤200 MB; Hindi CER measured on 50 scans before/after 300 DPI + `tessdata_best` | harness |
| all | zero orphan `soffice.bin`/`gs`/`tesseract`/`python3` after 100 forced cancels | `ps` sweep |

## 8. Risks and open questions

1. **Row count drift between surfaces.** Web 37, app 39, backend 39 — the web lacks flatten-pdf and
   pdf-metadata; the app stubs edit-pdf. The matrix treats backend routing as truth.
2. **Office compatibility is unproven in both directions.** No committed test compares output to Microsoft
   Office; the brief forbids claiming it. The harness corpus must include real customer-like files.
3. **E4 scope creep.** "pdf2docx parity" is the gate, not Acrobat parity; table/column detection on real
   layouts is the risk.
4. **PDF/A today is a false claim** until D3 is fixed and veraPDF runs.
5. **Row 32 depends on a product decision** (certificate ownership) before any engineering starts.
6. **Classification C for pdf-to-image is conditional** on the PDFium bake-off; if it fails the row stays D
   (Poppler) with no loss of function.
7. **Hindi OCR quality is unmeasured**; Stage 6 bake-off decides whether an E plugin (PaddleOCR) is justified.

## Sources

- Repository files cited inline (read 2026-11-07): `job-service/internal/routing/routing.go`,
  `fyredocs_frontend/src/config/tools.ts`, `fyredocs_frontend/src/lib/toolApi.ts`,
  `fyredocs_frontend/src/lib/localTools.ts`, `fyredocs_app/src/config/tools.ts`,
  `fyredocs_app/src/components/options/ToolOptionsPanel.tsx`, worker `processing/` packages.
- `scripts/k6/results/capacity-jobs-20260727-234123.md` — only committed job-throughput number.
- `docs/developer/architecture/production-readiness.md:104` — LibreOffice cost model.
- pdfcpu releases: https://github.com/pdfcpu/pdfcpu/releases (v0.16.4, 2026-10-27; accessed 2026-11-07).
- Ghostscript release dates: https://ghostscript.readthedocs.io/en/latest/ReleaseDates.html (10.08.0,
  2026-09-08; accessed 2026-11-07).
- Poppler: https://poppler.freedesktop.org/ (26.11.0, 2026-11-05; accessed 2026-11-07).
- Tesseract releases: https://github.com/tesseract-ocr/tesseract/releases (5.5.4, 2026-10-28; accessed
  2026-11-07).
- LibreOffice release notes: https://www.libreoffice.org/download/release-notes/ (26.2.5 listed as latest
  branch on the cached page; newer 26.8.x reported by third parties — unverified; accessed 2026-11-07).
- go-pdfium: https://pkg.go.dev/github.com/klippa-app/go-pdfium (v1.19.7, 2026-04; accessed 2026-11-07).
- digitorus/pdfsign, WeasyPrint, Typst, goldmark: PAdES level, pinning and shaping coverage still to be
  verified — marked "to verify" above.
