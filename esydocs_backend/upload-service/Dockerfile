FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/upload-service .

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/bin/upload-service ./upload-service
COPY .env ./

RUN mkdir -p uploads
VOLUME /root/uploads

EXPOSE 8081

CMD ["./upload-service"]
