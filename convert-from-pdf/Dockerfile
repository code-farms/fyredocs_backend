# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY convert-from-pdf/go.mod convert-from-pdf/go.sum* ./convert-from-pdf/
RUN cd convert-from-pdf && go mod download

# Copy source code
COPY shared/ ./shared/
COPY convert-from-pdf/ ./convert-from-pdf/

# Build the binary
RUN cd convert-from-pdf && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/convert-from-pdf .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN apk add --no-cache \
    poppler-utils \
    ghostscript \
    libreoffice-writer \
    libreoffice-calc \
    libreoffice-impress \
    libreoffice-common \
    ttf-liberation \
    && rm -rf /usr/lib/libreoffice/share/gallery/* \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/convert-from-pdf .

USER appuser

EXPOSE 8082

VOLUME /app/uploads /app/outputs

CMD ["./convert-from-pdf"]
