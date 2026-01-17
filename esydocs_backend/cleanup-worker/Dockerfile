FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/cleanup-worker .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/cleanup-worker ./cleanup-worker
COPY .env ./

USER appuser

CMD ["./cleanup-worker"]
