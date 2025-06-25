#!/bin/bash

# run_e2e_tests.sh - E2E test runner using built binaries
set -euo pipefail

# Build first
echo "🛠️ Building binaries..."
./build.sh
echo "✅ Build done."

# Config
PORT_GATEWAY=8080
PORT_SRV1=8081
PORT_SRV2=8082

URL_GATEWAY="http://localhost:$PORT_GATEWAY"
URL_SRV1="http://localhost:$PORT_SRV1"
URL_SRV2="http://localhost:$PORT_SRV2"

PID_GATEWAY=""
PID_SRV1=""
PID_SRV2=""

# Cleanup function
cleanup() {
    echo "🛑 [cleanup] Cleaning up servers..."
    for pid in "$PID_GATEWAY" "$PID_SRV1" "$PID_SRV2"; do
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo "🛑 [cleanup] Stopping PID $pid"
            kill "$pid" || true
            sleep 1
            kill -9 "$pid" || true
        else
            echo "🛑 [cleanup] No running process for PID '$pid'"
        fi
    done
    echo "✅ [cleanup] Done."
}

trap cleanup EXIT

# Wait for server
wait_for_server() {
    local url=$1
    local name=$2
    local max=30
    echo "🔎 [wait] Checking $name at $url with POST initialize"
    for i in $(seq 1 $max); do
        code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$url" \
            -H "Content-Type: application/json" \
            -d '{
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {},
                    "clientInfo": {
                        "name": "Test Client",
                        "version": "1.0.0"
                    }
                }
            }')
        echo "🔎 [wait] HTTP status for $name: $code"
        if [[ "$code" == "200" ]]; then
            echo "✅ [wait] $name ready."
            return 0
        fi
        echo "⏳ [wait] Waiting for $name ($i/$max)..."
        sleep 1
    done
    echo "❌ [wait] $name failed to start."
    return 1
}

# Start server
start_server() {
    local bin=$1
    local port=$2
    local name=$3
    echo "🚀 [start] Starting $name on port $port (bin: $bin)..."
    "./$bin" -port=$port >"/tmp/$name.log" 2>&1 &
    local spid=$!
    echo "🚀 [start] Started $name with PID $spid"
}

# ---- Run ----

echo "🧹 [pre-clean] Killing any processes on ports $PORT_GATEWAY, $PORT_SRV1, $PORT_SRV2..."
for p in $PORT_GATEWAY $PORT_SRV1 $PORT_SRV2; do
    echo "🧹 [pre-clean] Checking port $p"
    pids=$(lsof -ti tcp:$p 2>/dev/null || true)
    echo "🧹 [pre-clean] lsof result for port $p: '$pids'"
    if [ -n "$pids" ]; then
        echo "🧹 [pre-clean] Killing PIDs: $pids"
        kill -9 $pids || true
    else
        echo "🧹 [pre-clean] No process on port $p"
    fi
done

# Start servers
start_server "bin/server1" $PORT_SRV1 "server1"
PID_SRV1=$!

start_server "bin/server2" $PORT_SRV2 "server2"
PID_SRV2=$!

wait_for_server "$URL_SRV1" "server1" || exit 1
wait_for_server "$URL_SRV2" "server2" || exit 1

start_server "bin/gateway" $PORT_GATEWAY "gateway"
PID_GATEWAY=$!

wait_for_server "$URL_GATEWAY" "gateway" || exit 1

# Run tests
echo "🧪 [test] Running E2E tests..."
if go test -v ./e2e_test.go; then
    echo "✅ [test] E2E tests PASSED."
    exit 0
else
    echo "❌ [test] E2E tests FAILED."
    exit 1
fi
