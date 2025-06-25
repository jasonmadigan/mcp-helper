# MCP Gateway Proof of Concept

This project implements an MCP (Model Context Protocol) Gateway that acts as a proxy/router for multiple MCP servers. The gateway itself is an MCP server that aggregates tools and manages per-client sessions to multiple backend MCP servers.

## Project Structure

```
mcp-gateway-poc/
├── main.go              # MCP Gateway server (main project)
├── go.mod               # Dependencies for gateway
├── go.sum               # Go module checksums
├── build.sh             # Build script for all servers
├── e2e_test.go          # End-to-end test suite
├── bin/                 # Built binaries
│   ├── gateway          # MCP Gateway binary
│   ├── server1          # Test Server 1 binary
│   └── server2          # Test Server 2 binary
├── server1/             # Test MCP Server 1
│   ├── main.go
│   ├── go.mod
│   └── go.sum
├── server2/             # Test MCP Server 2  
│   ├── main.go
│   ├── go.mod
│   └── go.sum
└── README.md            # This file
```

## Architecture

- **MCP Gateway** (port 8080): Main server that acts as both MCP server and MCP client, aggregating tools from backend servers with per-client session management
- **Test MCP Server 1** (port 8081): Simple MCP server with echo, timestamp, and echo_headers tools
- **Test MCP Server 2** (port 8082): Simple MCP server with dice roll, magic 8-ball, and echo_headers tools

### Session Management

The gateway implements **per-client backend connections**:
- Each client that connects to the gateway gets dedicated connections to each backend server
- Sessions are properly isolated between clients
- Backend connections maintain their own sessions internally via the mcp-go client library
- No manual session header management required

## Configuration (Hardcoded for PoC)

- Gateway: `localhost:8080` - HTTP transport with streamable HTTP MCP protocol
- Server 1: `localhost:8081` - HTTP transport with streamable HTTP MCP protocol 
- Server 2: `localhost:8082` - HTTP transport with streamable HTTP MCP protocol
- Transport: HTTP with streamable HTTP MCP protocol (not SSE)

## Launch Order

**⚠️ Important**: Launch the backend test servers first, then the gateway (the gateway connects to backends on startup).

### Method 1: Using Build Script

```bash
# Build all servers
./build.sh

# Start backend servers first
./bin/server1 -port=8081 &
./bin/server2 -port=8082 &

# Then start the gateway (it will connect to backends)
./bin/gateway -port=8080
```

### Method 2: Running from Source

```bash
# Terminal 1 - Start Server 1
cd server1
go run main.go -port=8081

# Terminal 2 - Start Server 2  
cd server2
go run main.go -port=8082

# Terminal 3 - Start Gateway (connects to servers 1 & 2)
go run main.go -port=8080
```

## Testing the MCP Gateway

Once all servers are running, test the gateway with proper MCP protocol commands:

### 1. Initialize Connection and Get Session ID

```bash
# Initialize connection - captures session ID from response headers
curl -i -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "capabilities": {},
      "clientInfo": {
        "name": "Test Client",
        "version": "1.0.0"
      }
    }
  }'

# Response includes headers like:
# Mcp-Session-Id: mcp-session-68153d74-6dcf-45f0-8491-08c2dee44f39
```

### 2. List Available Tools (Use Session ID from Above)

```bash
# Replace SESSION_ID with the actual ID from initialize response
export SESSION_ID="mcp-session-68153d74-6dcf-45f0-8491-08c2dee44f39"

curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }'

# Shows 7 aggregated tools:
# - gateway_info (gateway's own tool)
# - server1-echo (from server1)
# - server1-timestamp (from server1)
# - server1-echo_headers (from server1)
# - server2-8_ball (from server2)
# - server2-dice_roll (from server2)
# - server2-echo_headers (from server2)
```

### 3. Call Tools (Routes to Backend Servers)

```bash
# Test server1-echo tool
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "server1-echo",
      "arguments": {
        "message": "Hello from MCP Gateway!"
      }
    }
  }'

# Test server2-8_ball tool
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "server2-8_ball",
      "arguments": {
        "question": "Will this MCP Gateway work perfectly?"
      }
    }
  }'

# Test server1-timestamp tool (no parameters)
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 5,
    "method": "tools/call",
    "params": {
      "name": "server1-timestamp",
      "arguments": {}
    }
  }'

# Test server2-dice_roll tool (no parameters)
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 6,
    "method": "tools/call",
    "params": {
      "name": "server2-dice_roll",
      "arguments": {}
    }
  }'

# Test gateway's own tool
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{
    "jsonrpc": "2.0",
    "id": 7,
    "method": "tools/call",
    "params": {
      "name": "gateway_info",
      "arguments": {}
    }
  }'
```

## Automated End-to-End Testing

The project includes a comprehensive e2e test that automatically verifies all functionality:

```bash
# Run the complete end-to-end test
./run_e2e_tests.sh

# The test will:
# 1. Start all three servers (server1, server2, gateway)
# 2. Wait for servers to be ready  
# 3. Create two separate MCP client sessions
# 4. Verify tool aggregation works correctly
# 5. Test the echo_headers tool on both servers
# 6. Verify session isolation between clients
# 7. Confirm all session IDs are unique and properly managed
# 8. Clean up and stop all servers
```

### What the E2E Test Validates

- **Server Startup**: All three servers start in the correct order and become ready
- **Tool Aggregation**: Gateway correctly discovers and aggregates tools from backend servers
- **Session Management**: Each client gets unique session IDs at all levels:
  - Client-to-Gateway session IDs are unique per client
  - Gateway-to-Backend session IDs are unique per client
  - No session ID reuse between different clients
- **Request Routing**: Tool calls are properly routed to backend servers
- **Header Handling**: HTTP headers (including session IDs) are correctly processed and forwarded
- **Error Handling**: Proper error responses for invalid requests

The e2e test eliminates the need for manual testing via multiple browser windows and provides confidence that the session isolation is working correctly.

## Available Tools

### MCP Gateway (Port 8080) - Aggregated Tools
- **`gateway_info`** - Returns information about the gateway and backend servers
  - No parameters required
- **`server1-echo`** - [Routed to Server1] Echoes back the input message
  - Parameter: `message` (string, required) - Message to echo back
- **`server1-timestamp`** - [Routed to Server1] Returns the current timestamp in ISO 8601 format
  - No parameters required
- **`server1-echo_headers`** - [Routed to Server1] Returns HTTP headers from the request
  - No parameters required
- **`server2-dice_roll`** - [Routed to Server2] Roll a dice and return a random number from 1 to 6
  - No parameters required
- **`server2-8_ball`** - [Routed to Server2] Ask the magic 8 ball a question and get a random response
  - Parameter: `question` (string, required) - Your question for the magic 8 ball
- **`server2-echo_headers`** - [Routed to Server2] Returns HTTP headers from the request
  - No parameters required

### Test Server 1 (Port 8081) - Direct Access
- **`echo`** - Echoes back the input message
  - Parameter: `message` (string, required) - Message to echo back
- **`timestamp`** - Returns the current timestamp in ISO 8601 format
  - No parameters required
- **`echo_headers`** - Returns HTTP headers from the request
  - No parameters required

### Test Server 2 (Port 8082) - Direct Access
- **`dice_roll`** - Roll a dice and return a random number from 1 to 6
  - No parameters required
- **`8_ball`** - Ask the magic 8 ball a question and get a random response
  - Parameter: `question` (string, required) - Your question for the magic 8 ball
- **`echo_headers`** - Returns HTTP headers from the request
  - No parameters required

## Building

### Build All Servers (Recommended)

```bash
# Use the build script
./build.sh

# This creates:
# - bin/gateway (main MCP Gateway)
# - bin/server1 (Test Server 1)
# - bin/server2 (Test Server 2)
```

### Manual Build

```bash
# Build gateway
go build -o bin/gateway main.go

# Build test servers
cd server1 && go build -o ../bin/server1 main.go && cd ..
cd server2 && go build -o ../bin/server2 main.go && cd ..
```

## Development Commands

```bash
# Initialize all go modules (if starting fresh)
go mod init mcp-gateway-poc
cd server1 && go mod init server1 && cd ..
cd server2 && go mod init server2 && cd ..

# Download dependencies for all projects
go mod tidy
cd server1 && go mod tidy && cd ..
cd server2 && go mod tidy && cd ..

# Run end-to-end tests
go test -v -run TestE2E

# Clean build artifacts
rm -rf bin/
```

## Features

### Core Gateway Functionality
- **Tool Aggregation**: Automatically discovers and aggregates tools from multiple backend MCP servers
- **Per-Client Session Management**: Each client gets dedicated backend connections with proper session isolation
- **Request Routing**: Intelligently routes tool calls to appropriate backend servers based on tool name prefixes
- **Session Isolation**: Multiple clients can connect simultaneously with completely separate backend sessions
- **Comprehensive Logging**: Detailed logging of all HTTP requests, headers, and MCP session activity

### MCP Protocol Support
- **Full MCP Protocol Implementation**: Complete support for initialize, tools/list, and tools/call methods
- **JSON-RPC 2.0 Compliance**: Proper JSON-RPC 2.0 request/response handling
- **HTTP Session Headers**: Proper `mcp-session-id` header handling and forwarding
- **Streamable HTTP Transport**: Uses mcp-go's streamable HTTP transport (not SSE)

### Tool Management
- **Dynamic Tool Discovery**: Discovers tools from backend servers at startup
- **Tool Prefixing**: Automatically prefixes backend tools (`server1-echo`, `server2-dice_roll`) to avoid conflicts
- **Gateway Tools**: Provides its own tools (like `gateway_info`) alongside backend tools
- **Parameter Validation**: Proper parameter validation and error handling for all tools

### Error Handling & Reliability
- **Backend Connection Management**: Handles backend server connections and failures gracefully
- **Session Error Handling**: Proper error responses for missing or invalid sessions
- **Tool Routing Errors**: Clear error messages for unknown tools or routing failures
- **Timeout Management**: Configurable timeouts for backend server communications

### Development & Testing
- **Build System**: Simple build script that compiles all servers
- **Comprehensive Testing**: Complete curl-based testing examples for all functionality
- **Detailed Documentation**: Full API documentation with working examples
- **Logging & Debugging**: Extensive logging for troubleshooting and development

## Network Architecture

```
┌─────────────────┐    HTTP     ┌─────────────────┐
│   MCP Client    │◄──────────►│  MCP Gateway    │
│                 │   :8080     │   (Port 8080)   │
│                 │             │                 │
│                 │             │ ✅ Tool Aggregation │
│                 │             │ ✅ Per-Client Sessions │
│                 │             │ ✅ Request Routing │
└─────────────────┘             └─────────────────┘
                                         │
                                         │ Dedicated Connections
                                         │ Per Client
                                         ▼
                        ┌─────────────────────────────────┐
                        │         Backend Servers        │
                        │                                 │
                        │  ┌─────────────┐ ┌─────────────┐│
                        │  │ Test Server │ │ Test Server ││
                        │  │     1       │ │     2       ││
                        │  │  (:8081)    │ │  (:8082)    ││  
                        │  │             │ │             ││
                        │  │ echo        │ │ 8_ball      ││
                        │  │ timestamp   │ │ dice_roll   ││
                        │  └─────────────┘ └─────────────┘│
                        └─────────────────────────────────┘
```

## Dependencies

This project uses the [mcp-go](https://github.com/mark3labs/mcp-go) library v0.32.0 for MCP protocol implementation.

**Requirements:**
- Go 1.23+ (required by mcp-go v0.32.0)
- `github.com/mark3labs/mcp-go` library