# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy Go files
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./

# Build for Linux AMD64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server1 main.go

# Final image
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/server1 .

RUN chmod +x server1

EXPOSE 8081

CMD ["./server1", "-port=8081"]
