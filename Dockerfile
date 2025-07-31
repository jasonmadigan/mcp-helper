# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy Go files
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY ext-proc ./ext-proc

# Build for Linux AMD64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mcp_helper main.go

# Final image
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/mcp_helper .

RUN chmod +x mcp_helper

EXPOSE 8081

CMD ["./mcp_helper", "-port=8080"]
