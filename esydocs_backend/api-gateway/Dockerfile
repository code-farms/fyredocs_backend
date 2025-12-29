FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/api-gateway .

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/bin/api-gateway ./api-gateway

EXPOSE 8080

CMD ["./api-gateway"]
