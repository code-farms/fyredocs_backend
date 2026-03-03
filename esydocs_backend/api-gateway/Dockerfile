# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY api-gateway/go.mod api-gateway/go.sum* ./api-gateway/
RUN cd api-gateway && go mod download

# Copy source code
COPY shared/ ./shared/
COPY api-gateway/ ./api-gateway/

# Build the binary
RUN cd api-gateway && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/api-gateway .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/api-gateway .

USER appuser

EXPOSE 8080

CMD ["./api-gateway"]
