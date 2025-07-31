# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy Go files
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY health.go ./
COPY pkg/bbr ./pkg/bbr
COPY internal ./internal


# Build for Linux AMD64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o gateway main.go

# Final image
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/gateway .

RUN chmod +x gateway

EXPOSE 8081

CMD ["./gateway", "-port=8080"]
