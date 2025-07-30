package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Backend server configuration
var (
	server1URL = getEnv("SERVER1_URL", "http://localhost:8081")
	server2URL = getEnv("SERVER2_URL", "http://localhost:8082")
)

// ClientBackendConnections holds the backend client connections for a specific client session
type ClientBackendConnections struct {
	ClientSessionID  string
	Server1Client    *client.Client
	Server2Client    *client.Client
	Server1SessionID string // Tracked session ID for server1
	Server2SessionID string // Tracked session ID for server2
	CreatedAt        time.Time
}

// SessionMapping holds the mapping between gateway session and backend sessions
type SessionMapping struct {
	GatewaySessionID string
	Server1SessionID string
	Server2SessionID string
	CreatedAt        time.Time
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

	// Session ID mapping - maps gateway session ID to backend session IDs
	sessionMappings map[string]*SessionMapping
	sessionLock     sync.RWMutex

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

	// Create a multiplexer to handle different routes
	mux := http.NewServeMux()

	// Handle session lookup endpoint
	mux.HandleFunc("/session-lookup", gateway.handleSessionLookup)

	// Handle all other requests as MCP requests
	mux.Handle("/", loggingHandler)

	if err := http.ListenAndServe(":"+*port, mux); err != nil {
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

		// Check if this is an initialize request
		if r.Method == "POST" && r.URL.Path == "/" {
			// Wrap the response writer to capture the session ID
			wrappedWriter := &sessionCapturingWriter{
				ResponseWriter: w,
				gateway:        g,
			}
			next.ServeHTTP(wrappedWriter, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// sessionCapturingWriter wraps http.ResponseWriter to capture session IDs from initialize responses
type sessionCapturingWriter struct {
	http.ResponseWriter
	gateway *MCPGateway
}

func (w *sessionCapturingWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *sessionCapturingWriter) Write(data []byte) (int, error) {
	// Check if a new session ID was set in the response headers
	if sessionID := w.Header().Get("mcp-session-id"); sessionID != "" {
		// This is likely a response to an initialize request
		go func() {
			// Create session mapping asynchronously
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := w.gateway.handleInitialization(ctx, sessionID); err != nil {
				log.Printf("❌ Failed to create session mapping for %s: %v", sessionID, err)
			}
		}()
	}

	return w.ResponseWriter.Write(data)
}

func (w *sessionCapturingWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

// NewMCPGateway creates a new MCP Gateway instance
func NewMCPGateway() *MCPGateway {
	gateway := &MCPGateway{
		aggregatedTools:   make([]mcp.Tool, 0),
		clientConnections: make(map[string]*ClientBackendConnections),
		sessionMappings:   make(map[string]*SessionMapping),
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

// handleInitialization creates backend sessions when a client initializes
func (g *MCPGateway) handleInitialization(ctx context.Context, gatewaySessionID string) error {
	log.Printf("🆕 Creating REAL backend sessions for gateway session: %s", gatewaySessionID)

	// Create real backend connections
	connections, err := g.createBackendConnectionsForSession(ctx, gatewaySessionID)
	if err != nil {
		return fmt.Errorf("failed to create backend connections: %w", err)
	}

	// Use the REAL session IDs from the connections we just created
	server1SessionID := connections.Server1SessionID
	server2SessionID := connections.Server2SessionID

	// Store REAL session mapping
	mapping := &SessionMapping{
		GatewaySessionID: gatewaySessionID,
		Server1SessionID: server1SessionID,
		Server2SessionID: server2SessionID,
		CreatedAt:        time.Now(),
	}

	g.sessionLock.Lock()
	g.sessionMappings[gatewaySessionID] = mapping
	g.sessionLock.Unlock()

	log.Printf("✅ REAL session mapping created: %s -> server1:%s, server2:%s",
		gatewaySessionID, server1SessionID, server2SessionID)

	return nil
}

// createBackendConnectionsForSession creates and initializes real backend connections
func (g *MCPGateway) createBackendConnectionsForSession(ctx context.Context, gatewaySessionID string) (*ClientBackendConnections, error) {
	log.Printf("🔗 Creating REAL backend connections for session: %s", gatewaySessionID)

	connections := &ClientBackendConnections{
		ClientSessionID: gatewaySessionID,
		CreatedAt:       time.Now(),
	}

	// Create and initialize server1 connection
	client1, sessionID, err := g.createClientBackendConnection(ctx, connections.ClientSessionID, "server1", server1URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create server1 connection: %w", err)
	}
	connections.Server1Client = client1
	connections.Server1SessionID = sessionID

	// Create and initialize server2 connection
	client2, sessionID, err := g.createClientBackendConnection(ctx, connections.ClientSessionID, "server1", server1URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create server2 connection: %w", err)
	}
	connections.Server2Client = client2
	connections.Server2SessionID = sessionID

	// Store the connections for later use
	g.connectionsLock.Lock()
	g.clientConnections[gatewaySessionID] = connections
	g.connectionsLock.Unlock()

	return connections, nil
}

// getSessionMapping returns the session mapping for a gateway session ID
func (g *MCPGateway) getSessionMapping(gatewaySessionID string) (*SessionMapping, bool) {
	g.sessionLock.RLock()
	defer g.sessionLock.RUnlock()
	mapping, exists := g.sessionMappings[gatewaySessionID]
	return mapping, exists
}

// SessionLookupResponse represents the response for session lookup
type SessionLookupResponse struct {
	Server1SessionID string `json:"server1_session_id"`
	Server2SessionID string `json:"server2_session_id"`
	Found            bool   `json:"found"`
}

// handleSessionLookup provides an HTTP endpoint for Envoy to lookup session mappings
func (g *MCPGateway) handleSessionLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gatewaySessionID := r.Header.Get("x-gateway-session-id")
	if gatewaySessionID == "" {
		http.Error(w, "Missing x-gateway-session-id header", http.StatusBadRequest)
		return
	}

	mapping, found := g.getSessionMapping(gatewaySessionID)

	response := SessionLookupResponse{
		Found: found,
	}

	if found {
		response.Server1SessionID = mapping.Server1SessionID
		response.Server2SessionID = mapping.Server2SessionID
		log.Printf("📋 Session lookup: %s -> server1:%s, server2:%s",
			gatewaySessionID, mapping.Server1SessionID, mapping.Server2SessionID)
	} else {
		log.Printf("❌ Session lookup failed: %s not found", gatewaySessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode session lookup response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
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

func (g *MCPGateway) routeToolCall(_ context.Context, toolName string, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("❌ Tool call reached gateway unexpectedly: %s (should be routed by Envoy)", toolName)
	return mcp.NewToolResultError(fmt.Sprintf("Tool call %s reached gateway - this should be handled by Envoy routing", toolName)), nil
}

// createClientBackendConnection creates and initializes a client connection to a backend server
func (g *MCPGateway) createClientBackendConnection(ctx context.Context, clientSessionID string, serverName string, serverURL string) (*client.Client, string, error) {
	log.Printf("🔗 Creating REAL dedicated %s connection for client %s", serverName, clientSessionID)

	// Create HTTP transport
	httpTransport, err := transport.NewStreamableHTTP(serverURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create HTTP transport for %s: %w", serverName, err)
	}

	// Create client
	mcpClient := client.NewClient(httpTransport)

	// Initialize with timeout
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    fmt.Sprintf("MCP Gateway (Client %s)", clientSessionID),
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := mcpClient.Initialize(initCtx, initRequest)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize %s: %w", serverName, err)
	}

	// Extract the REAL session ID from the initialized client
	sessionID := mcpClient.GetSessionId()
	if sessionID == "" {
		return nil, "", fmt.Errorf("failed to get real session ID from %s - session ID is empty", serverName)
	}

	log.Printf("✅ Client %s connected to %s: %s with REAL session ID: %s",
		clientSessionID, serverName, serverInfo.ServerInfo.Name, sessionID)

	return mcpClient, sessionID, nil
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
		"session_management": "per-client backend connections",
		"routing":            "handled by Envoy dynamic module",
	}

	return mcp.NewToolResultText(fmt.Sprintf("Gateway Info: %+v", info)), nil
}
