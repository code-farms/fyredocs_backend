# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Download shared module dependencies
COPY shared/go.mod shared/go.sum* ./shared/
RUN cd shared && go mod download

# Download service dependencies
COPY organize-pdf/go.mod organize-pdf/go.sum* ./organize-pdf/
RUN cd organize-pdf && go mod download

# Copy source code
COPY shared/ ./shared/
COPY organize-pdf/ ./organize-pdf/

# Build the binary
RUN cd organize-pdf && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/organize-pdf .

# ---- Runtime Stage ----
FROM alpine:3.19

RUN apk add --no-cache \
    poppler-utils \
    && rm -rf /var/cache/apk/*

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs

COPY --from=builder /app/bin/organize-pdf .

USER appuser

EXPOSE 8084

VOLUME /app/uploads /app/outputs

CMD ["./organize-pdf"]
