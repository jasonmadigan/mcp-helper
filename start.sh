#!/bin/bash

# run_e2e_tests.sh - E2E test runner using built binaries (manual start)
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
TAIL_PID=""

# Colours
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # no colour

# Cleanup function
cleanup() {
    echo -e "${RED}🛑 [cleanup] Cleaning up servers...${NC}"
    for pid in "$PID_GATEWAY" "$PID_SRV1" "$PID_SRV2"; do
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo -e "${RED}🛑 [cleanup] Stopping PID $pid${NC}"
            kill "$pid" || true
            sleep 1
            kill -9 "$pid" || true
        else
            echo -e "${RED}🛑 [cleanup] No running process for PID '$pid'${NC}"
        fi
    done
    if [ -n "$TAIL_PID" ] && kill -0 "$TAIL_PID" 2>/dev/null; then
        kill "$TAIL_PID" || true
    fi
    echo -e "${GREEN}✅ [cleanup] Done.${NC}"
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

# Tail logs
echo "📝 [logs] Tailing logs..."
tail -F /tmp/server1.log /tmp/server2.log /tmp/gateway.log | \
while IFS= read -r line; do
    case "$line" in
        */server1.log*)
            echo -e "${GREEN}[server1]${NC} ${line#*/server1.log: }"
            ;;
        */server2.log*)
            echo -e "${BLUE}[server2]${NC} ${line#*/server2.log: }"
            ;;
        */gateway.log*)
            echo -e "${RED}[gateway]${NC} ${line#*/gateway.log: }"
            ;;
        *)
            echo "$line"
            ;;
    esac
done &
TAIL_PID=$!

echo "✅ Servers running."
echo "Press Ctrl+C to stop."

# Wait for servers
wait
