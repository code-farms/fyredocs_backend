# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY optimize-pdf/go.mod optimize-pdf/go.sum* ./optimize-pdf/
RUN cd optimize-pdf && go mod download

# Copy source code
COPY shared/ ./shared/
COPY optimize-pdf/ ./optimize-pdf/

# Build the binary
RUN cd optimize-pdf && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/optimize-pdf .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN apk add --no-cache \
    poppler-utils \
    ghostscript \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/optimize-pdf .

USER appuser

EXPOSE 8085

VOLUME /app/uploads /app/outputs

CMD ["./optimize-pdf"]
