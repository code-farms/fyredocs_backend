FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy shared module
COPY shared/go.mod shared/go.sum ./shared/
RUN cd shared && go mod download

# Copy auth-service module
COPY auth-service/go.mod auth-service/go.sum ./auth-service/
RUN cd auth-service && go mod download

# Copy source code
COPY shared/ ./shared/
COPY auth-service/ ./auth-service/

# Build
RUN cd auth-service && CGO_ENABLED=0 GOOS=linux go build -o /auth-service .

FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /auth-service /auth-service

EXPOSE 8086

CMD ["/auth-service"]
