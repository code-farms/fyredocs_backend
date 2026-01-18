FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/organize-pdf .

# Use alpine with PDF processing dependencies
FROM alpine:3.19

# Install dependencies for PDF operations
RUN apk add --no-cache \
        ca-certificates \
        poppler-utils \
        && adduser -D -u 10001 appuser 2>/dev/null || true

WORKDIR /app

COPY --from=builder /app/bin/organize-pdf ./organize-pdf

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8084

CMD ["./organize-pdf"]
