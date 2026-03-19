# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

# Prepare system files and non-root user
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

# 1. Copy workspace blueprints for global caching
COPY go.work go.work.sum* ./
COPY shared/go.mod shared/go.sum* ./shared/
COPY api-gateway/go.mod* ./api-gateway/
COPY auth-service/go.mod* ./auth-service/
COPY cleanup-worker/go.mod* ./cleanup-worker/
COPY convert-from-pdf/go.mod* ./convert-from-pdf/
COPY convert-to-pdf/go.mod* ./convert-to-pdf/
COPY optimize-pdf/go.mod* ./optimize-pdf/
COPY organize-pdf/go.mod* ./organize-pdf/
COPY job-service/go.mod* ./job-service/
COPY analytics-service/go.mod* ./analytics-service/

# 2. Download dependencies once (cached via BuildKit)
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 3. Copy source code
COPY shared/ ./shared/
COPY optimize-pdf/ ./optimize-pdf/

# 4. Build binary with Go build cache and module cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd optimize-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p 2 -trimpath -ldflags="-s -w" -o /app/bin/optimize-pdf .

# ---- Runtime Stage ----
# 1. Grab our pre-built image (contains Poppler-utils)
FROM esydocs-base:latest

# 2. Install ONLY Ghostscript and Tesseract OCR (takes seconds)
RUN apk add --no-cache \
    ghostscript \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

# 3. Create folders and set proper permissions for OCR processing
RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

# 4. Copy the binary from builder
COPY --from=builder /app/bin/optimize-pdf .
RUN chown appuser:appuser optimize-pdf

USER appuser

EXPOSE 8085

VOLUME /app/uploads /app/outputs

CMD ["./optimize-pdf"]