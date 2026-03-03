# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY cleanup-worker/go.mod cleanup-worker/go.sum* ./cleanup-worker/
RUN cd cleanup-worker && go mod download

# Copy source code
COPY shared/ ./shared/
COPY cleanup-worker/ ./cleanup-worker/

# Build the binary
RUN cd cleanup-worker && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/cleanup-worker .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/cleanup-worker .

USER appuser

CMD ["./cleanup-worker"]
