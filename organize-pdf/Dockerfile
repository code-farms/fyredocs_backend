# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# The workspace blueprint must match the fresh source below, not the older snapshot
# the base image baked in — otherwise a bumped `go` directive or a new module in a
# go.mod fails against a stale go.work ("go.work lists go 1.25.0").
COPY go.work go.work.sum* ./

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY organize-pdf/ ./organize-pdf/

RUN cd organize-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/organize-pdf .

# ---- Runtime Stage ----
# This service only uses pdfcpu (Go library) and Tesseract — no LibreOffice needed.
# Alpine 3.24 (3.19 reached end-of-life on 2025-11-01). NOTE: keep in sync with
# deployment/base-alpine-libreoffice.Dockerfile and deployment/test-binaries.Dockerfile.
FROM alpine:3.24

# Tesseract language packs match the scan-to-pdf `language` whitelist
# (eng, hin, eng+hin) enforced in job-service and processing_scan.go.
# No system fonts: Tesseract's PDF renderer embeds its own glyphless font and
# pdfcpu stamps use built-in core fonts.
RUN apk add --no-cache \
    ca-certificates \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    tesseract-ocr-data-hin \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

# 2. Create folders and set proper permissions
RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

# 3. Copy the binary from builder
COPY --from=builder /app/bin/organize-pdf .
# Third-party licence notice for the programs this image runs (see the file).
COPY organize-pdf/THIRD-PARTY-LICENCES.md /licences/THIRD-PARTY-LICENCES.md
RUN chown appuser:appuser organize-pdf

USER appuser

EXPOSE 8084

CMD ["./organize-pdf"]