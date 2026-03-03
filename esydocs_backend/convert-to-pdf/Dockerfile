# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY convert-to-pdf/go.mod convert-to-pdf/go.sum* ./convert-to-pdf/
RUN cd convert-to-pdf && go mod download

# Copy source code
COPY shared/ ./shared/
COPY convert-to-pdf/ ./convert-to-pdf/

# Build the binary
RUN cd convert-to-pdf && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/convert-to-pdf .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN apk add --no-cache \
    libreoffice \
    ttf-liberation \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/convert-to-pdf .

USER appuser

EXPOSE 8083

VOLUME /app/uploads /app/outputs

CMD ["./convert-to-pdf"]
