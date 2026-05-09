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

# 2. Download dependencies (cached via BuildKit)
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 3. Copy source code
COPY shared/ ./shared/
COPY convert-from-pdf/ ./convert-from-pdf/

# 4. Build binary with Go build cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd convert-from-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p 2 -trimpath -ldflags="-s -w" -o /app/bin/convert-from-pdf .

# ---- Runtime Stage ----
# Runtime uses Python (pdf2docx for pdf→word), LibreOffice (pdf→xlsx/odt/ods/odp +
# pdf→word fallback), poppler (pdftoppm/pdftohtml/pdftotext), and Ghostscript
# (pdf→pdfa). Base is python:3.11-slim-bookworm because pdf2docx's deps
# (PyMuPDF, opencv-python-headless) do not publish musllinux wheels for aarch64
# and would otherwise have to compile MuPDF from source.
FROM python:3.11-slim-bookworm

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    libreoffice-core libreoffice-writer libreoffice-calc libreoffice-impress \
    poppler-utils \
    ghostscript \
    fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install --no-cache-dir pdf2docx==0.5.8

RUN useradd -m -u 10001 appuser

WORKDIR /app

# 3. Create folders and set proper permissions
RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

# 4. Copy the binary from builder
COPY --from=builder /app/bin/convert-from-pdf .
RUN chown appuser:appuser convert-from-pdf

USER appuser

EXPOSE 8082

VOLUME /app/uploads /app/outputs

CMD ["./convert-from-pdf"]