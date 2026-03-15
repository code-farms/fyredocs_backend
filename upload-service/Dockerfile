# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY upload-service/go.mod upload-service/go.sum* ./upload-service/
RUN cd upload-service && go mod download

# Copy source code
COPY shared/ ./shared/
COPY upload-service/ ./upload-service/

# Build the binary
RUN cd upload-service && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/upload-service .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/upload-service .

USER appuser

EXPOSE 8081

VOLUME /app/uploads /app/outputs

CMD ["./upload-service"]
