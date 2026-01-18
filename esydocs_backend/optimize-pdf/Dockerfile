FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/optimize-pdf .

# Runtime image with PDF processing dependencies
FROM alpine:3.19

# Install all required dependencies for optimize-pdf
# - ca-certificates: SSL/TLS support
# - poppler-utils: pdftoppm for PDF to image conversion (OCR workflow)
# - ghostscript: PDF repair and rebuilding
# - tesseract-ocr: OCR engine for text recognition
# - tesseract-ocr-data-eng: English language data (default)
RUN apk add --no-cache \
        ca-certificates \
        poppler-utils \
        ghostscript \
        tesseract-ocr \
        tesseract-ocr-data-eng \
    && adduser -D -u 10001 appuser 2>/dev/null || true

WORKDIR /app

COPY --from=builder /app/bin/optimize-pdf ./optimize-pdf
COPY .env.example ./.env

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8085

CMD ["./optimize-pdf"]
