# Test-runner image for the `binaries`-tagged suite: the Go toolchain plus
# every external binary the four PDF workers shell out to, so
# processing_free.go / processing_scan.go exec paths can run for real.
# Mirrors the runtime images' tool set (optimize/organize: alpine + poppler/
# ghostscript/tesseract; convert-to-pdf: fyredocs-base's libreoffice +
# unoserver). The pdf2docx / pdf-to-xlsx Python paths are NOT covered here —
# they need convert-from-pdf's debian/pip runtime and stay descoped to that
# image (see docs/developer/architecture/testing-strategy.md).
#
# Build + run via: make test-binaries
FROM golang:1.25-alpine

RUN apk add --no-cache \
    ca-certificates \
    bash \
    git \
    build-base \
    poppler-utils \
    ghostscript \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    tesseract-ocr-data-deu \
    libreoffice \
    ttf-liberation \
    python3 \
    py3-pip \
 && pip3 install --break-system-packages unoserver

WORKDIR /src
