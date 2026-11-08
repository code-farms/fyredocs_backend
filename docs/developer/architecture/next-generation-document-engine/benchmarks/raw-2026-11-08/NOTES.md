# Re-measure after the Alpine 3.24 + Devanagari font change (2026-11-08)

Raw files here were written by `../measure-images.sh` with
`RAW_DIR=…/raw-2026-11-08 TAG=latest STEPS=1,3`. The 2026-11-07 baseline in
`../raw/` is unchanged. Host: macOS arm64, Docker VM 8 CPU / 4 GB.

## Tool versions (`tool-versions.txt` here vs `../raw/tool-versions.txt`)

| Image | Before | After |
|---|---|---|
| convert-to-pdf (fyredocs-base) | Alpine 3.19.9, LibreOffice 7.6.7.2, Poppler 23.10.0, Python 3.11.14 | Alpine 3.24.4, LibreOffice 25.8.7.3, Poppler 25.12.0, Python 3.14.10 |
| optimize-pdf | Alpine 3.19.9, Ghostscript 10.05.1, Tesseract 5.3.3 | Alpine 3.24.4, Ghostscript 10.08.0, Tesseract 5.5.2 |
| organize-pdf | Alpine 3.19.9, Tesseract 5.3.3 | Alpine 3.24.4, Tesseract 5.5.2 |
| convert-from-pdf | Debian 12.15, unchanged tools | unchanged; fonts-noto-core added |

## Image sizes (`image-sizes.tsv` here vs `../raw/image-sizes.tsv`)

| Image | Before | After | Change |
|---|---:|---:|---:|
| fyredocs-base | 1065.2 MB | 1536.0 MB | +470.8 MB |
| fyredocs-convert-to-pdf | 1172.1 MB | 1643.0 MB | +470.9 MB |
| fyredocs-convert-from-pdf | 1764.8 MB | 1828.9 MB | +64.1 MB |
| fyredocs-organize-pdf | 216.6 MB | 222.2 MB | +5.6 MB |
| fyredocs-optimize-pdf | 326.9 MB | 326.9 MB | +0.03 MB |

Why fyredocs-base grew: in Alpine 3.24 `libreoffice-common` itself depends on
Qt 6, GTK 4 and GTK 3, which pull mesa and llvm22-libs. Measured installed sizes
in the new image: llvm22-libs 167 MB, qt6-qtdeclarative 58 MB, mesa 35 MB,
qt6-qtbase(-x11) 35 MB, gtk4.0 + gtk+3.0 22 MB, flite 20 MB, gstreamer plugins
about 25 MB — about 377 MB a headless server never loads. Installing only
libreoffice-common/-writer/-calc/-impress still pulls all of them (checked in
a clean `alpine:3.24`). Fonts are 26.5 MB (`/usr/share/fonts/noto`, 108 files).

## Memory (what the 1 GB container limit actually constrains)

Two unoserver daemons in a 1 GB container, `docker stats --no-stream`:

| Image | Idle | After 3 conversions of `docx/medium.docx` |
|---|---:|---:|
| convert-to-pdf:baseline-2026-11 (Alpine 3.19, LO 7.6) | 193.3 MiB | 298.7 MiB |
| convert-to-pdf:latest (Alpine 3.24, LO 25.8) | 122.3 MiB | 229.3 MiB |

The size growth is on disk only; resident memory went down.
