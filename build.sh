#!/bin/bash

# Build script for MCP Gateway PoC

set -e

echo "Building MCP Gateway PoC..."

# Create bin directory if it doesn't exist
mkdir -p bin

# Build gateway
echo "Building MCP Gateway..."
go build -o bin/gateway main.go

# Build test servers
echo "Building Test Server 1..."
cd server1 && go build -o ../bin/server1 main.go && cd ..

echo "Building Test Server 2..."
cd server2 && go build -o ../bin/server2 main.go && cd ..

echo "Build complete!"
echo ""
echo "To run the servers:"
echo "1. Start test servers:"
echo "   ./bin/server1 &"
echo "   ./bin/server2 &"
echo "2. Start gateway:"
echo "   ./bin/gateway"
echo ""
echo "Or run from source:"
echo "1. Start test servers:"
echo "   cd server1 && go run main.go &"
echo "   cd server2 && go run main.go &"
echo "2. Start gateway:"
echo "   go run main.go" 