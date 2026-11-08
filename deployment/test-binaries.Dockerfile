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
#
# Pinned to the runtime images' Alpine (3.24) so the tested LibreOffice,
# Ghostscript, Poppler and Tesseract versions are the shipped ones. NOTE: keep
# in sync with deployment/base-alpine-libreoffice.Dockerfile.
FROM golang:1.26-alpine3.24

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
    tesseract-ocr-data-hin \
    libreoffice \
    ttf-liberation \
    font-noto-devanagari \
    fontconfig \
    python3 \
    py3-pip \
 && fc-cache -f \
 && pip3 install --break-system-packages unoserver==3.7

WORKDIR /src
