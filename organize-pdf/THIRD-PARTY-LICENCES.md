# Third-party licences — organize-pdf image

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
| Tesseract OCR and its eng/hin language data (Alpine) | Apache-2.0 | separate process (scan-to-pdf OCR) |
| Leptonica (Tesseract dependency) | BSD-2-Clause | loaded by Tesseract |
| pdfcpu (Go module) | Apache-2.0 | linked into the Fyredocs binary |
| Other Go modules | permissive (MIT, BSD, Apache-2.0) | linked into the Fyredocs binary; see the SBOM |
