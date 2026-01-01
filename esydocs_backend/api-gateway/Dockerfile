FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/api-gateway .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/api-gateway ./api-gateway

RUN chown -R appuser:appuser /app

USER appuser

EXPOSE 8080

CMD ["./api-gateway"]
