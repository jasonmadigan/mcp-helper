#!/bin/bash

# Docker build script for MCP Gateway PoC
# Usage: ./docker-build.sh [push]

set -e

# Image names
GATEWAY_IMAGE="quay.io/dmartin/mcp-gateway-poc"
SERVER1_IMAGE="quay.io/dmartin/mcp-gateway-poc-server1"
SERVER2_IMAGE="quay.io/dmartin/mcp-gateway-poc-server2"

echo "Building MCP Gateway PoC Docker images..."

# Build Gateway image
echo "Building Gateway image..."
docker build -t $GATEWAY_IMAGE .

# Build Server1 image
echo "Building Server1 image..."
docker build -t $SERVER1_IMAGE ./server1

# Build Server2 image
echo "Building Server2 image..."
docker build -t $SERVER2_IMAGE ./server2

echo "✅ All images built successfully!"

# Push images if requested
if [ "$1" = "push" ]; then
    echo ""
    echo "Pushing images to registry..."
    
    echo "Pushing Gateway image..."
    docker push $GATEWAY_IMAGE
    
    echo "Pushing Server1 image..."
    docker push $SERVER1_IMAGE
    
    echo "Pushing Server2 image..."
    docker push $SERVER2_IMAGE
    
    echo "✅ All images pushed successfully!"
else
    echo ""
    echo "To push images to registry, run: $0 push"
fi

echo ""
echo "To run with docker-compose: docker-compose up"
echo "To run individual containers:"
echo "  docker run -p 8081:8081 $SERVER1_IMAGE"
echo "  docker run -p 8082:8082 $SERVER2_IMAGE"
echo "  docker run -p 8080:8080 $GATEWAY_IMAGE" 