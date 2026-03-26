# Base image with LibreOffice pre-installed for PDF processing services
# This image should be built once and pushed to a container registry
# Build: docker build -f docker/base-alpine-libreoffice.Dockerfile -t your-registry/alpine-libreoffice:3.19 .
# Push: docker push your-registry/alpine-libreoffice:3.19

FROM alpine:3.19

LABEL maintainer="esydocs"
LABEL description="Alpine Linux with LibreOffice and PDF tools pre-installed"
LABEL version="3.19"

# Install LibreOffice, Python, and dependencies
# Note: Java is NOT needed for headless PDF conversion via LibreOffice/unoserver
RUN apk add --no-cache \
    ca-certificates \
    poppler-utils \
    libreoffice \
    ttf-liberation \
    python3 \
    py3-pip

# Install unoserver for persistent LibreOffice daemon mode
# unoserver provides: unoserver (daemon) + unoconvert (client CLI)
RUN pip3 install --break-system-packages unoserver

# Verify installations
RUN libreoffice --version && \
    pdftoppm -v && \
    unoconvert --help > /dev/null 2>&1

# Clean up
RUN rm -rf /tmp/* /var/cache/apk/*
