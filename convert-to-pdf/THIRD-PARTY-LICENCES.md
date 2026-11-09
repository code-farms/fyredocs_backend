# Third-party licences — convert-to-pdf image

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
| LibreOffice (Alpine `libreoffice`) | MPL-2.0, some parts LGPL/GPL secondary | separate process (unoserver daemons, cold fallback) |
| unoserver 3.7 (PyPI) | MIT | separate process |
| Python 3 (Alpine) | PSF-2.0 | interpreter for unoserver |
| Poppler utilities (Alpine `poppler-utils`) | GPL-2.0 / GPL-3.0 | installed by the base image; separate process when used |
| Liberation fonts (`ttf-liberation`), Noto Sans Devanagari (`font-noto-devanagari`) | SIL OFL-1.1 | fonts read by LibreOffice |
| fontconfig | MIT-style (HPND) | font discovery for LibreOffice |
| pdfcpu (Go module) | Apache-2.0 | linked into the Fyredocs binary (image-to-pdf) |
| Other Go modules | permissive (MIT, BSD, Apache-2.0) | linked into the Fyredocs binary; see the SBOM |

Licence texts: LibreOffice ships its own in `/usr/lib/libreoffice/`; Alpine packages
install theirs under `/usr/share/licenses/` where the package provides them.
