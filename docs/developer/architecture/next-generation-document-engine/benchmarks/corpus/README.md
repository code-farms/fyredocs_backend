# Fidelity and performance corpus

This folder documents the corpus the engine harness (`make bench-engine`, designed in
`../../16-benchmarking-and-testing.md`) and the baseline script (`../measure-images.sh`) run against.
Per CLAUDE.md §2 the harness code and the corpus **manifest** live under `scripts/bench-engine/`
(proposed in doc 16); this doc folder keeps only baselines and results. Files are **not** committed:
the k6 generator produces the synthetic set, and real-world files are listed in the manifest with
checksums and kept in object storage (bucket `bench-corpus/`, to be created) so licensed or
customer-like material never enters git.

## Layout

```
scripts/bench-engine/corpus/            (proposed, Stage 1 — see 16-benchmarking-and-testing.md)
  manifest.tsv            # path  sha256  bytes  category  pages  language  source  licence
  synthetic/              # generated: bash scripts/k6/fixtures/generate.sh  →  scripts/k6/fixtures/out/
  real/<category>/        # fetched from bench-corpus by sha256
docs/…/benchmarks/        (this folder: baselines, raw measurements, results)
```

`synthetic/` is a symlink-free mirror of `scripts/k6/fixtures/out/<category>/{small,medium,large}.<ext>`
(docx, xlsx, pptx, odt/ods/odp small-only, pdf, scanned-pdf 1/4/12 pages, image png, html).
Sizes: small ≈ 0.8 MB, medium ≈ 8 MB, large ≈ 40 MB (`scripts/k6/fixtures/gen.py`).

## Categories required by the brief and the gates

| Category | Purpose | Minimum count (Stage 1 target) |
|---|---|---|
| small / medium / large | size scaling of every tool | 3 per format (synthetic) |
| complex-docx | styles, numbering, fields, footnotes, text boxes, floating images, columns, tracked changes | 50 real |
| simple-docx | paragraphs, runs, basic tables, images, headers/footers, lists — the fast-path class | 100 real |
| image-heavy-pdf | compress gate (E5) | 30 real |
| table-heavy-pdf | E4 table reconstruction gate | 30 real, with labelled cell ground truth |
| multi-page-pdf | 100–500 pages; OCR tmpfs budget; progress | 10 real |
| large-xlsx | 100k+ rows, shared strings, formulas | 10 real |
| complex-pptx | masters, charts, SmartArt, animations (preserved, not rendered) | 20 real |
| hindi-devanagari | DOCX/ODT/PDF with Devanagari; scans for OCR CER | 50 (25 DOCX, 25 scans) |
| scans | skewed, shadowed, multi-page; OCR bake-off | 50 English + 50 Hindi |
| forms | AcroForm fill/flatten | 10 real |
| encrypted | AES-128/256, RC4, owner-only | 10 synthetic (generated via pdfcpu) |
| malformed | fuzz crashers (`*/testdata/fuzz`), truncated xref, zip bombs, XML bombs, deep nesting | 50 (generated) |
| html | flex/grid, images, Devanagari, RTL, print CSS | 30 |

Ground truth per file where a gate needs it: extracted text (pdftotext reference), page count and
page sizes, table cells (labelled by hand or from the generating source), reference renders
(LibreOffice and Ghostscript/Poppler outputs stored next to the file as `*.ref.pdf` /
`*.ref-<page>.png`). Reference engines are development oracles only; they are never runtime
dependencies of the lightweight path.

## Rules

- Every file has a row in `manifest.tsv`; the harness refuses files not in the manifest.
- Real files must be redistributable (public-domain, CC, or company-owned). Record the licence.
- Never add customer files.
- A gate quotes the manifest revision (git sha of `manifest.tsv`) it was measured against.
