# Measurements 2026-11-09 (M1.4 verification)

Environment: Docker Desktop VM, 8 CPUs, 4 GB RAM (arm64); `fyredocs-test-binaries` image
(LibreOffice 25.8, unoserver 3.7); container capped at 3 GB.

## Daemon RSS soak (`soak-rss-run*.tsv`)

300 warm conversions through `tryUnoconvert` on 2 daemons, 2 concurrent workers, recycling
disabled (`UNOSERVER_MAX_CONVERSIONS=0`, `UNOSERVER_MAX_RSS_MB=0`). Inputs rotate the k6 fixtures
`small.docx`, `small.xlsx`, `small.pptx`, `small.odt`, with a `medium.*` (8 MB) every 20th job.
RSS is the sum over each daemon's process group from `/proc` (`groupRSS`), sampled every 25 jobs.

- Run 2 (clean): both daemons ~125 MB idle → ~280 MB after the first 25 jobs → 283–287 MB at job
  300. No growth trend after warm-up. 0 cold fallbacks. 300 jobs in 20 s.
- Run 1: daemon 2002 behaved the same (127 → 280 → 284 MB). Daemon 2003 stayed at 33 MB (unoserver
  alone, no LibreOffice in its group) until about job 250, then rose to 275 MB; 1 cold fallback.
  Not explained and not reproduced in run 2; the supervisor log of run 1 was not captured.

Consequence for the defaults: the 600 MB RSS limit sits about 2× above the observed plateau, so it
only fires on a real outlier. 300 conversions showed no leak to justify the 200-conversion recycle
on these inputs; it is kept as hygiene (a recycle costs a 7–8 s restart with the port held, no
fallback), pending a longer soak with real-world documents.

## Concurrent cold starts

Two `libreoffice --headless --convert-to pdf` at once, each with its own profile:

| Profile at start | Trials | Failures |
|---|---|---|
| brand-new (empty dir) | 10 | 1 (`WrappedTargetRuntimeException`, no output) |
| brand-new with hardened registry | 10 | 0 (1 of 3 in an earlier smaller run) |
| copy of a LibreOffice-initialised profile | 20 | 0 |
| copy of a `--terminate_after_init` template + hardened registry rewritten | 20 | 0 |

## Macro sentinel

ODT with a Basic macro on the document-open event (`dom:load`), writing a sentinel file:
- permissive profile (macros enabled, level 0), headless `--convert-to`: macro did NOT run;
- hardened profile, cold path: did not run; hardened daemon via unoconvert: did not run.
Headless conversion does not fire the open event here; the hardened settings are a second safeguard.

Fixture notes: LibreOffice rejects the package ("source file could not be loaded") if the
`mimetype` entry has a zip data descriptor or the manifest comes after the Basic files.

## M1.5 runtime check (read-only stack)

All 12 Fyredocs services started from images rebuilt at commit `34d6289` and ran with
`ReadonlyRootfs=true`, `CapDrop=[ALL]`, `no-new-privileges`, `User=10001:10001` (`docker inspect`).
Through the gateway: word-to-pdf, excel-to-pdf, word-to-odt, image-to-pdf, pdf-to-word, pdf-to-odt,
pdf-to-ods, pdf-to-pdfa, pdf-to-image, pdf-to-excel, merge-pdf, scan-to-pdf, pdf-metadata,
compress-pdf, ocr-pdf all completed; detect-edges returned corners. A cold `libreoffice` run inside
the read-only convert-to-pdf container (profile in `/tmp`) converted. No container logged
`read-only file system`, `permission denied` or `operation not permitted`.

## Trivy 0.76.2, HIGH/CRITICAL (worker images, built 2026-11-09)

| Image | CRITICAL | HIGH | Fixed version available | Where |
|---|---|---|---|---|
| convert-to-pdf | 0 | 4 | 4 | Go binary only (Alpine 3.24.4 packages, LibreOffice included: none); needed `--timeout 30m` on the first run |
| organize-pdf | 0 | 4 | 4 | Go binary: quic-go 0.54.0, golang.org/x/net 0.60.0, grpc 1.82.1 |
| optimize-pdf | 0 | 4 | 4 | Go binary: same three modules |
| convert-from-pdf | 4 | 108 | 8 | Debian 12.15 packages (glib, libxml2, curl, sqlite, expat, libtiff, util-linux, ghostscript, …) mostly without a Debian fix yet; Python: pdfminer.six 20231228, jaraco.context, wheel; Go binary as above |

All four Go binaries share the same HIGH findings: quic-go 0.54.0 (fixed 0.54.1), golang.org/x/net
0.60.0 (fixed 0.62.0), grpc 1.82.1 (fixed 1.83.1). One dependency bump clears them in every image;
govulncheck (CI stage 8) reports the same modules plus otel. The Debian base of convert-from-pdf
carries most of the remaining findings; moving it to Alpine (with the rest of the workers) or to a
newer Debian is the larger fix.

### After the dependency bump (same day)

Go 1.26.10 (`toolchain` in go.work, pinned builder images), otel 1.47.0, grpc 1.85.0,
golang.org/x/net 0.62.0, quic-go 0.65.0: govulncheck reports 0 reachable vulnerabilities in all
13 modules; Trivy on the rebuilt organize-pdf image: 0 HIGH/CRITICAL (alpine 3.24.4 and the Go
binary). The Debian findings in convert-from-pdf are unaffected by this change.
