# Third-party licences — convert-from-pdf image

This image runs third-party programs and libraries. Each one listed here as a
separate process is executed unmodified, as a separate process, with arguments
only: it is not linked into the Fyredocs binary and has not been modified.
Licence identifications come from docs/developer/architecture/
next-generation-document-engine/03-heavy-dependency-analysis.md (engineering
analysis, not legal advice; a counsel review of the AGPL subprocess boundary is
still open). The authoritative per-package list for a built image is its SBOM
(`syft <image>`, produced by scripts/ci/08-vuln.sh).

| Component | Licence | How it is used |
|---|---|---|
| Ghostscript (Debian `ghostscript`) | **AGPL-3.0** (commercial licence available from Artifex) | separate process (pdf-to-pdfa) |
| PyMuPDF / MuPDF (dependency of pdf2docx) | **AGPL-3.0** (commercial licence available from Artifex) | loaded by the pdf2docx helper, a separate Python process |
| pdf2docx 0.5.13 | MIT (relicensed by Artifex) | separate Python process (pdf-to-word) |
| pdfplumber 0.11.10, pdfminer.six, openpyxl 3.1.5 | MIT | separate Python process (pdf-to-excel) |
| LibreOffice (Debian `libreoffice-core`, -writer, -calc, -impress) | MPL-2.0, some parts LGPL/GPL secondary | separate process |
| Poppler utilities (Debian `poppler-utils`) | GPL-2.0 / GPL-3.0 | separate process (pdftoppm, pdftotext, pdftohtml) |
| Python 3.15 (`python:3.15-slim-trixie` base, Debian 13) | PSF-2.0 | interpreter for the helpers |
| Liberation fonts, Noto fonts (`fonts-liberation`, `fonts-noto-core`) | SIL OFL-1.1 | fonts read by LibreOffice |
| pdfcpu (Go module) | Apache-2.0 | linked into the Fyredocs binary |
| Other Python and Go dependencies | see the SBOM | |

Licence texts: Debian packages install theirs in `/usr/share/doc/<package>/copyright`;
Python packages ship theirs in their `*.dist-info` directories.
