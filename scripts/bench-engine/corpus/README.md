# Engine benchmark corpus

The corpus the engine benchmark harness (`scripts/bench-engine`, roadmap Q13)
runs every tool over. Design: `docs/developer/architecture/next-generation-document-engine/16-benchmarking-and-testing.md` §5.2.

## What is committed and what is not

- `manifest.json` (committed) holds **recipes**: series of files, each with a
  category, a generator, parameters, a count and the tools that run on them.
  It expands to 226 generated files today.
- `out/` (gitignored) holds the generated files and `out/corpus.lock`, the
  sha256 and size of each. Generated documents are not byte-stable
  (LibreOffice and pdfcpu write IDs and dates), so checksums are pinned per
  generation, not in git. `verify` (and every `run`) refuses a corpus that
  changed after `gen`.

## Commands

```bash
make bench-engine-gen                         # generate out/ and the lock (resumes; -force to redo)
make bench-engine BENCH_ARGS="-runs 3"        # verify, then run everything; writes a report
make bench-engine BENCH_ARGS="-series pdf-small,html -limit 2"   # a quick slice
make bench-compare A=base.json B=candidate.json                  # regressions vs a baseline
scripts/bench-engine/run.sh run -series scan-300dpi -limit 1 -keep scripts/bench-engine/corpus/out/inspect
                                                                  # keep outputs to inspect a finding
scripts/bench-engine/run.sh compare -noise a.json b.json          # noise floor of two same-code runs
```

`run` needs the stack up with the bench override (raised rate limits):

```bash
docker compose -f deployment/docker-compose.yml \
  -f scripts/bench-engine/compose.bench.yml --env-file .env up -d
```

Reports go to `docs/developer/architecture/next-generation-document-engine/benchmarks/results/`.

## Categories

| Series (prefix) | Category | Generator | Files | Tools |
|---|---|---|---|---|
| pdf-small/medium/large | pdf | text PDF padded to 0.8 / 8 / 40 MB | 15 | compress, text, image, pdfa, rotate, merge, split, protect, metadata, word |
| pdf-100pages, pdf-300pages | multipage | text PDF | 5 | compress, image, split, text |
| img-photo/noise/shapes | image-heavy | images imported with pdfcpu | 20 | compress, image |
| scan-300dpi, scan-200dpi | scans | rendered pages, ±2° skew, noise, JPEG (source text kept for OCR scoring) | 20 | ocr, compress |
| hindi-pdf, hindi-docx | hindi | Devanagari text through LibreOffice | 20 | compress, text, pdfa, image, word-to-pdf |
| table-pdf/docx/odt | tables | HTML tables through LibreOffice | 20 | excel, word, text, word/odt-to-pdf |
| columns-pdf/docx | layout | two-column HTML through LibreOffice | 20 | word, odt, text, word-to-pdf |
| sheet-50k-xlsx/ods | spreadsheets | 50 000-row CSV through LibreOffice | 5 | excel/ods-to-pdf |
| k6-* , office-* | office | k6 fixtures and LibreOffice conversions | 30 | *-to-pdf, word-to-odt, excel-to-ods |
| html | html | flex, grid, images, Devanagari, RTL, tables, long | 30 | html-to-pdf |
| enc-* | encrypted | pdfcpu RC4-128, AES-128, AES-256, owner-only | 6 | unlock |
| malformed | malformed | 10 corruption modes (4 recoverable by xref rebuild, 6 fatal) | 20 | repair, compress, image (clean failure is fine) |
| photo-jpeg, shapes-png | images | JPEG / PNG | 15 | image-to-pdf, scan-to-pdf |

**Pending** (listed in the manifest with `"status": "pending"`, no files yet):
real-world complex layouts, labelled tables, forms, presentations and scans.

## Known limitations

- The pdf-medium and pdf-large size classes get their size from padding no object references.
  compress-pdf drops it (40 MB → 16 KB), so they test transfer and parsing size, not engine
  work on large content. The image-heavy and scan series are the heavy-content cases.
- Peak RAM comes from Docker's stats stream (about one frame per second): a lower bound, and
  "n/a" for jobs that finish before the first frame.
- Benchmark runs need the workers' result cache off (`compose.bench.yml` sets
  `RESULT_CACHE_TTL_SECONDS=0`). Otherwise a file already seen comes back from Redis without
  running the engine. `run` refuses a cached worker unless given `-allow-cache`.

## Adding real-world files later

Real documents never go in git. Add a series with the files' category and tools,
put the files under `out/<category>/` from a private location, and record each
file's sha256 and licence in the lock (a `donated` generator that copies from
`BENCH_CORPUS_DIR` and checks the manifest's sha256 is the planned way; doc 16
§5.2). Every corpus file is untrusted input: the harness only hands it to the
tools under test and the oracles, inside containers.
