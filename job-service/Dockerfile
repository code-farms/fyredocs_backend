# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY job-service/go.mod job-service/go.sum* ./job-service/
RUN cd job-service && go mod download

# Copy source code
COPY shared/ ./shared/
COPY job-service/ ./job-service/

# Build the binary
RUN cd job-service && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/job-service .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/job-service .

USER appuser

EXPOSE 8081

VOLUME /app/uploads /app/outputs

CMD ["./job-service"]
