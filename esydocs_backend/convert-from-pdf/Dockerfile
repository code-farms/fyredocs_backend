FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/convert-from-pdf .

FROM alpine:3.19

# Install dependencies for PDF processing
# - poppler-utils: for pdftoppm (PDF to images), pdftotext, pdftohtml
# - ghostscript: for PDF/A conversion
# - libreoffice-writer: for PDF to Word (minimal, not full suite)
# - libreoffice-calc: for PDF to Excel
# - libreoffice-impress: for PDF to PowerPoint
# - ttf-liberation: fonts for better rendering
RUN apk add --no-cache \
        ca-certificates \
        poppler-utils \
        ghostscript \
        libreoffice-writer \
        libreoffice-calc \
        libreoffice-impress \
        libreoffice-common \
        ttf-liberation \
    && rm -rf /usr/lib/libreoffice/share/gallery \
    && rm -rf /usr/lib/libreoffice/share/template \
    && rm -rf /usr/lib/libreoffice/share/wizards \
    && rm -rf /usr/lib/libreoffice/share/config/images_*.zip \
    && rm -rf /var/cache/apk/* \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/convert-from-pdf ./convert-from-pdf

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8082

CMD ["./convert-from-pdf"]
