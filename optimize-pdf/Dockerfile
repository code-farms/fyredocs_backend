# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY optimize-pdf/ ./optimize-pdf/

RUN cd optimize-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/optimize-pdf .

# ---- Runtime Stage ----
# This service uses Ghostscript, Tesseract, and pdftoppm — no LibreOffice needed.
FROM alpine:3.19

RUN apk add --no-cache \
    ca-certificates \
    poppler-utils \
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