# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY convert-from-pdf/ ./convert-from-pdf/

RUN cd convert-from-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/convert-from-pdf .

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