# check=skip=FromPlatformFlagConstDisallowed
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

# veraPDF (the reference PDF/A validator) as a test-only oracle for pdf-to-pdfa;
# it never ships in a runtime image. The official image is amd64-only, but the
# application is plain Java, so its files are copied out and run on this image's
# own JRE on any architecture. Pinned: bump deliberately, it decides pass/fail.
# The constant --platform is deliberate ($BUILDPLATFORM would fail to pull on
# arm64), hence the build-check skip on the first line of this file.
FROM --platform=linux/amd64 verapdf/cli:v1.30.4 AS verapdf

# NOTE: Go patch release pinned to the `toolchain` line in go.work; bump both together.
FROM golang:1.27.3-alpine3.24

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
    openjdk21-jre-headless \
    python3 \
    py3-pip \
 && fc-cache -f \
 && pip3 install --break-system-packages unoserver==3.7

COPY --from=verapdf /opt/verapdf /opt/verapdf
RUN ln -s /opt/verapdf/verapdf /usr/local/bin/verapdf \
 && verapdf --version

WORKDIR /src
