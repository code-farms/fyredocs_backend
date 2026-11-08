# Base image with LibreOffice pre-installed for PDF processing services.
# Built once by deployment/build-base-image.sh (or deploy.sh when missing) as
# fyredocs-base:latest; convert-to-pdf runs FROM it.
#   docker build -f deployment/base-alpine-libreoffice.Dockerfile -t fyredocs-base:latest .
#
# Alpine 3.24: LibreOffice and Tesseract come from Alpine's community repo,
# which is supported only until the next stable release, so this tracks the
# newest branch. 3.19 was end-of-life on 2025-11-01 and is no longer patched.
# NOTE: keep the Alpine version in sync with organize-pdf/Dockerfile,
# optimize-pdf/Dockerfile and deployment/test-binaries.Dockerfile.

FROM alpine:3.24

LABEL maintainer="fyredocs"
LABEL description="Alpine Linux with LibreOffice and PDF tools pre-installed"
LABEL version="3.24"

# Install LibreOffice, Python, fonts and dependencies.
# Note: Java is NOT needed for headless PDF conversion via LibreOffice/unoserver.
# Fonts: ttf-liberation is metric-compatible with Arial/Times/Courier, so Latin
# Office documents keep their layout; font-noto-devanagari covers Hindi, which
# otherwise renders as missing-glyph boxes (finding in docs/developer/
# architecture/next-generation-document-engine/07-layout-and-rendering-engine.md).
RUN apk add --no-cache \
    ca-certificates \
    poppler-utils \
    libreoffice \
    ttf-liberation \
    font-noto-devanagari \
    fontconfig \
    python3 \
    py3-pip && \
    fc-cache -f

# Install unoserver for persistent LibreOffice daemon mode
# unoserver provides: unoserver (daemon) + unoconvert (client CLI) + unoping.
# Pinned: the daemon's CLI contract (--user-installation takes a path,
# --uno-port must differ from --port) is what the convert-to-pdf daemon
# supervisor (convert-to-pdf/processing/daemons.go) is written against.
# NOTE: keep in sync with deployment/test-binaries.Dockerfile.
RUN pip3 install --break-system-packages unoserver==3.7

# Verify installations, including that fontconfig sees the Devanagari font.
RUN libreoffice --version && \
    pdftoppm -v && \
    unoconvert --help > /dev/null 2>&1 && \
    fc-list :lang=hi family | grep -qi "Noto Sans Devanagari"

# Clean up
RUN rm -rf /tmp/* /var/cache/apk/*
