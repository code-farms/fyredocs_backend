# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 1. Copy the master workspace files first
COPY go.work go.work.sum* ./

# 2. Copy only the dependency blueprints for the required modules
COPY shared/go.mod shared/go.sum* ./shared/
COPY api-gateway/go.mod* ./api-gateway/
COPY auth-service/go.mod* ./auth-service/
COPY cleanup-worker/go.mod* ./cleanup-worker/
COPY convert-from-pdf/go.mod* ./convert-from-pdf/
COPY convert-to-pdf/go.mod* ./convert-to-pdf/
COPY optimize-pdf/go.mod* ./optimize-pdf/
COPY organize-pdf/go.mod* ./organize-pdf/
COPY upload-service/go.mod* ./upload-service/
COPY job-service/go.mod* ./job-service/

# 3. Download dependencies ONCE, using Docker's permanent BuildKit cache
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 4. Now copy the actual changing source code
COPY shared/ ./shared/
COPY convert-to-pdf/ ./convert-to-pdf/

# 5. Build the binary using both the module cache AND the Go build cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd convert-to-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p 2 -trimpath -ldflags="-s -w" -o /app/bin/convert-to-pdf .

# ---- Runtime Stage ----
FROM esydocs-base:latest

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

COPY --from=builder /app/bin/convert-to-pdf .
RUN chown appuser:appuser convert-to-pdf

USER appuser

EXPOSE 8083

VOLUME /app/uploads /app/outputs

CMD ["./convert-to-pdf"]