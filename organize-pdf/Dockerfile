# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY organize-pdf/ ./organize-pdf/

RUN cd organize-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/organize-pdf .

# ---- Runtime Stage ----
# This service only uses pdfcpu (Go library) and Tesseract — no LibreOffice needed.
FROM alpine:3.19

# Tesseract language packs match the scan-to-pdf `language` whitelist
# (eng/deu/fra/spa/hin) enforced in job-service and processing_scan.go.
RUN apk add --no-cache \
    ca-certificates \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    tesseract-ocr-data-deu \
    tesseract-ocr-data-fra \
    tesseract-ocr-data-spa \
    tesseract-ocr-data-hin \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

# 2. Create folders and set proper permissions
RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

# 3. Copy the binary from builder
COPY --from=builder /app/bin/organize-pdf .
RUN chown appuser:appuser organize-pdf

USER appuser

EXPOSE 8084

VOLUME /app/uploads /app/outputs

CMD ["./organize-pdf"]