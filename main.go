package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Backend server configuration
const (
	server1URL = "http://localhost:8081"
	server2URL = "http://localhost:8082"
)

// ClientBackendConnections holds the backend client connections for a specific client session
type ClientBackendConnections struct {
	ClientSessionID string
	Server1Client   *client.Client
	Server2Client   *client.Client
	CreatedAt       time.Time
}

// MCPGateway represents the main MCP server that acts as both server and client
type MCPGateway struct {
	// Server side
	mcpServer *server.MCPServer

	// Tool aggregation
	aggregatedTools []mcp.Tool
	toolsLock       sync.RWMutex

	// Session management - maps client session ID to backend client connections
	clientConnections map[string]*ClientBackendConnections
	connectionsLock   sync.RWMutex

	// Startup clients (used only for initial tool discovery, then discarded)
	startupServer1Client *client.Client
	startupServer2Client *client.Client
}

func main() {
	var port = flag.String("port", "8080", "Port to listen on")
	flag.Parse()

	log.Println("Starting MCP Gateway...")

	gateway := NewMCPGateway()

	// Initialize backend connections and aggregate tools
	if err := gateway.initializeBackends(); err != nil {
		log.Fatalf("Failed to initialize backends: %v", err)
	}

	// Start the gateway server
	log.Printf("MCP Gateway listening on port %s", *port)
	log.Printf("MCP endpoint: http://localhost:%s", *port)
	log.Printf("Backend servers: %s, %s", server1URL, server2URL)

	streamableServer := server.NewStreamableHTTPServer(gateway.mcpServer)

	// Wrap the streamable server with logging middleware
	loggingHandler := gateway.loggingMiddleware(streamableServer)

	if err := http.ListenAndServe(":"+*port, loggingHandler); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// loggingMiddleware adds comprehensive logging for all HTTP requests
func (g *MCPGateway) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log all headers for debugging
		log.Printf("=== GATEWAY REQUEST ===")
		log.Printf("Method: %s, URL: %s", r.Method, r.URL.String())
		log.Printf("Headers:")
		for name, values := range r.Header {
			for _, value := range values {
				log.Printf("  %s: %s", name, value)
			}
		}

		// Specifically log session header
		sessionID := r.Header.Get("mcp-session-id")
		if sessionID != "" {
			log.Printf("🔑 MCP-SESSION-ID: %s", sessionID)
		} else {
			log.Printf("❌ No mcp-session-id header found")
		}

		log.Printf("======================")

		next.ServeHTTP(w, r)
	})
}

// NewMCPGateway creates a new MCP Gateway instance
func NewMCPGateway() *MCPGateway {
	gateway := &MCPGateway{
		aggregatedTools:   make([]mcp.Tool, 0),
		clientConnections: make(map[string]*ClientBackendConnections),
	}

	// Create MCP server with tool capabilities
	gateway.mcpServer = server.NewMCPServer(
		"MCP Gateway",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Setup gateway handlers
	gateway.setupHandlers()

	return gateway
}

// setupHandlers configures the MCP server handlers
func (g *MCPGateway) setupHandlers() {
	// Gateway info tool
	g.mcpServer.AddTool(mcp.NewTool("gateway_info",
		mcp.WithDescription("Get information about the MCP Gateway"),
	), g.handleGatewayInfo)
}

// initializeBackends connects to backend servers for initial tool discovery only
func (g *MCPGateway) initializeBackends() error {
	log.Println("Initializing backend server connections for tool discovery...")

	// Initialize startup clients (these will be discarded after tool discovery)
	if err := g.initializeStartupClients(); err != nil {
		return fmt.Errorf("failed to initialize startup clients: %w", err)
	}

	// Aggregate tools from both servers
	if err := g.aggregateTools(); err != nil {
		return fmt.Errorf("failed to aggregate tools: %w", err)
	}

	log.Printf("Successfully initialized. Aggregated %d tools from backend servers.", len(g.aggregatedTools))
	log.Println("Startup clients will be discarded - per-client sessions will be created on demand.")
	return nil
}

// initializeStartupClients creates temporary clients for tool discovery
func (g *MCPGateway) initializeStartupClients() error {
	// Initialize startup server1 client
	log.Printf("Creating startup connection to server1 at %s...", server1URL)
	httpTransport1, err := transport.NewStreamableHTTP(server1URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server1: %w", err)
	}
	g.startupServer1Client = client.NewClient(httpTransport1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initRequest1 := mcp.InitializeRequest{}
	initRequest1.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest1.Params.ClientInfo = mcp.Implementation{
		Name:    "MCP Gateway (Startup)",
		Version: "1.0.0",
	}
	initRequest1.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo1, err := g.startupServer1Client.Initialize(ctx, initRequest1)
	if err != nil {
		return fmt.Errorf("failed to initialize startup server1: %w", err)
	}
	log.Printf("Startup connection to server1: %s (version %s)", serverInfo1.ServerInfo.Name, serverInfo1.ServerInfo.Version)

	// Initialize startup server2 client
	log.Printf("Creating startup connection to server2 at %s...", server2URL)
	httpTransport2, err := transport.NewStreamableHTTP(server2URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server2: %w", err)
	}
	g.startupServer2Client = client.NewClient(httpTransport2)

	initRequest2 := mcp.InitializeRequest{}
	initRequest2.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest2.Params.ClientInfo = mcp.Implementation{
		Name:    "MCP Gateway (Startup)",
		Version: "1.0.0",
	}
	initRequest2.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo2, err := g.startupServer2Client.Initialize(ctx, initRequest2)
	if err != nil {
		return fmt.Errorf("failed to initialize startup server2: %w", err)
	}
	log.Printf("Startup connection to server2: %s (version %s)", serverInfo2.ServerInfo.Name, serverInfo2.ServerInfo.Version)

	return nil
}

// aggregateTools fetches and aggregates tools from both backend servers using startup clients
func (g *MCPGateway) aggregateTools() error {
	log.Println("Aggregating tools from backend servers using startup clients...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []mcp.Tool

	// Get tools from server1 using startup client
	server1Tools, err := g.startupServer1Client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list tools from server1: %w", err)
	}

	// Prefix server1 tools
	for _, tool := range server1Tools.Tools {
		prefixedTool := tool
		prefixedTool.Name = "server1-" + tool.Name
		allTools = append(allTools, prefixedTool)
	}
	log.Printf("Server1 contributed %d tools", len(server1Tools.Tools))

	// Get tools from server2 using startup client
	server2Tools, err := g.startupServer2Client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("failed to list tools from server2: %w", err)
	}

	// Prefix server2 tools
	for _, tool := range server2Tools.Tools {
		prefixedTool := tool
		prefixedTool.Name = "server2-" + tool.Name
		allTools = append(allTools, prefixedTool)
	}
	log.Printf("Server2 contributed %d tools", len(server2Tools.Tools))

	// Store aggregated tools
	g.toolsLock.Lock()
	g.aggregatedTools = allTools
	g.toolsLock.Unlock()

	// Register aggregated tools with the MCP server
	g.registerAggregatedTools()

	return nil
}

// registerAggregatedTools registers all aggregated tools with the MCP server
func (g *MCPGateway) registerAggregatedTools() {
	g.toolsLock.RLock()
	defer g.toolsLock.RUnlock()

	for _, tool := range g.aggregatedTools {
		// Create a closure to capture the tool name for routing
		toolName := tool.Name
		g.mcpServer.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.routeToolCall(ctx, toolName, req)
		})
	}

	log.Printf("Registered %d aggregated tools with MCP server", len(g.aggregatedTools))
}

// getOrCreateClientConnections gets existing backend connections or creates new ones for a client
func (g *MCPGateway) getOrCreateClientConnections(ctx context.Context, clientSessionID string) (*ClientBackendConnections, error) {
	g.connectionsLock.RLock()
	existing, exists := g.clientConnections[clientSessionID]
	g.connectionsLock.RUnlock()

	if exists {
		log.Printf("✅ Using existing backend connections for client %s", clientSessionID)
		return existing, nil
	}

	log.Printf("🆕 Creating new backend connections for client %s", clientSessionID)

	// Create new backend connections for this client
	connections := &ClientBackendConnections{
		ClientSessionID: clientSessionID,
		CreatedAt:       time.Now(),
	}

	// Initialize server1 connection for this client
	if err := g.createClientServer1Connection(ctx, connections); err != nil {
		return nil, fmt.Errorf("failed to create server1 connection for client %s: %w", clientSessionID, err)
	}

	// Initialize server2 connection for this client
	if err := g.createClientServer2Connection(ctx, connections); err != nil {
		return nil, fmt.Errorf("failed to create server2 connection for client %s: %w", clientSessionID, err)
	}

	// Store the connections
	g.connectionsLock.Lock()
	g.clientConnections[clientSessionID] = connections
	g.connectionsLock.Unlock()

	log.Printf("✅ Created backend connections for client %s", clientSessionID)

	return connections, nil
}

// createClientServer1Connection creates a dedicated server1 connection for a client
func (g *MCPGateway) createClientServer1Connection(ctx context.Context, connections *ClientBackendConnections) error {
	log.Printf("🔗 Creating dedicated server1 connection for client %s", connections.ClientSessionID)

	// Create HTTP transport for server1
	httpTransport, err := transport.NewStreamableHTTP(server1URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server1: %w", err)
	}

	// Create client
	connections.Server1Client = client.NewClient(httpTransport)

	// Initialize with timeout
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    fmt.Sprintf("MCP Gateway (Client %s)", connections.ClientSessionID),
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := connections.Server1Client.Initialize(initCtx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize server1: %w", err)
	}

	log.Printf("✅ Client %s connected to server1: %s (session maintained by client)",
		connections.ClientSessionID, serverInfo.ServerInfo.Name)
	return nil
}

// createClientServer2Connection creates a dedicated server2 connection for a client
func (g *MCPGateway) createClientServer2Connection(ctx context.Context, connections *ClientBackendConnections) error {
	log.Printf("🔗 Creating dedicated server2 connection for client %s", connections.ClientSessionID)

	// Create HTTP transport for server2
	httpTransport, err := transport.NewStreamableHTTP(server2URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server2: %w", err)
	}

	// Create client
	connections.Server2Client = client.NewClient(httpTransport)

	// Initialize with timeout
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    fmt.Sprintf("MCP Gateway (Client %s)", connections.ClientSessionID),
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := connections.Server2Client.Initialize(initCtx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize server2: %w", err)
	}

	log.Printf("✅ Client %s connected to server2: %s (session maintained by client)",
		connections.ClientSessionID, serverInfo.ServerInfo.Name)
	return nil
}

// routeToolCall routes tool calls to the appropriate backend server using per-client connections
func (g *MCPGateway) routeToolCall(ctx context.Context, toolName string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("🔧 Tool call started: %s", toolName)

	// Extract client session from context
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		log.Printf("❌ No client session found in context")
		return mcp.NewToolResultError("No active session"), nil
	}

	clientSessionID := session.SessionID()
	log.Printf("🔑 Client session ID: %s", clientSessionID)

	// Get or create backend connections for this client
	connections, err := g.getOrCreateClientConnections(ctx, clientSessionID)
	if err != nil {
		log.Printf("❌ Failed to get client connections: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("Connection error: %v", err)), nil
	}

	// Parse tool name to determine backend
	var backendClient *client.Client
	var originalToolName string

	if strings.HasPrefix(toolName, "server1-") {
		backendClient = connections.Server1Client
		originalToolName = strings.TrimPrefix(toolName, "server1-")
	} else if strings.HasPrefix(toolName, "server2-") {
		backendClient = connections.Server2Client
		originalToolName = strings.TrimPrefix(toolName, "server2-")
	} else {
		return mcp.NewToolResultError(fmt.Sprintf("Unknown tool prefix for %s", toolName)), nil
	}

	// Create call request with original tool name
	backendReq := mcp.CallToolRequest{}
	backendReq.Params.Name = originalToolName
	backendReq.Params.Arguments = req.Params.Arguments

	// Call backend server (client maintains its own session internally)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	log.Printf("🚀 Routing %s -> %s (client: %s, session maintained by backend client)",
		toolName, originalToolName, clientSessionID)

	result, err := backendClient.CallTool(callCtx, backendReq)
	if err != nil {
		log.Printf("❌ Backend call failed for %s: %v", toolName, err)
		return mcp.NewToolResultError(fmt.Sprintf("Backend call failed: %v", err)), nil
	}

	log.Printf("✅ Tool call %s completed successfully", toolName)
	return result, nil
}

// handleGatewayInfo handles the gateway_info tool
func (g *MCPGateway) handleGatewayInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g.toolsLock.RLock()
	toolCount := len(g.aggregatedTools)
	g.toolsLock.RUnlock()

	g.connectionsLock.RLock()
	connectionCount := len(g.clientConnections)
	g.connectionsLock.RUnlock()

	info := map[string]interface{}{
		"gateway_name":       "MCP Gateway",
		"version":            "1.0.0",
		"backend_servers":    []string{server1URL, server2URL},
		"aggregated_tools":   toolCount,
		"active_connections": connectionCount,
		"status":             "running",
		"session_management": "per-client backend connections (sessions maintained by clients)",
	}

	return mcp.NewToolResultText(fmt.Sprintf("Gateway Info: %+v", info)), nil
}
