package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	extProc "mcp-helper/ext-proc"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/grpc"
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

// SessionMapping holds the mapping between helper session and backend sessions
type SessionMapping struct {
	HelperSessionID  string
	Server1SessionID string
	Server2SessionID string
	CreatedAt        time.Time
}

// MCPHelper represents the main MCP server that acts as both server and client
type MCPHelper struct {
	// Server side
	mcpServer *server.MCPServer

	// Tool aggregation
	aggregatedTools []mcp.Tool
	toolsLock       sync.RWMutex

	// Session management - maps client session ID to backend client connections
	clientConnections map[string]*ClientBackendConnections
	connectionsLock   sync.RWMutex

	// Session ID mapping - maps helper session ID to backend session IDs
	sessionMappings map[string]*SessionMapping
	sessionLock     sync.RWMutex

	// Startup clients (used only for initial tool discovery, then discarded)
	startupServer1Client *client.Client
	startupServer2Client *client.Client
}

func main() {
	var port = flag.String("port", "8080", "Port to listen on")
	flag.Parse()

	log.Println("Starting MCP Helper...")

	helper := NewMCPHelper()

	// Initialize backend connections and aggregate tools
	if err := helper.initializeBackends(); err != nil {
		log.Fatalf("Failed to initialize backends: %v", err)
	}

	// Setup signal handling for graceful shutdown
	var gracefulStop = make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)

	// Start the HTTP MCP Helper server in a goroutine
	go func() {
		log.Printf("MCP Helper listening on port %s", *port)
		log.Printf("MCP endpoint: http://localhost:%s", *port)
		log.Printf("Backend servers: %s, %s", server1URL, server2URL)

		streamableServer := server.NewStreamableHTTPServer(helper.mcpServer)

		// Wrap the streamable server with logging middleware
		loggingHandler := helper.loggingMiddleware(streamableServer)

		// Create a multiplexer to handle different routes
		mux := http.NewServeMux()

		// Handle all MCP requests
		mux.Handle("/", loggingHandler)

		if err := http.ListenAndServe(":"+*port, mux); err != nil {
			log.Fatalf("HTTP Server error: %v", err)
		}
	}()

	// Start the gRPC ext-proc filter server
	log.Println("Starting ext-proc filter")

	// grpc server init
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, extProc.NewServer(false, helper))

	log.Println("Starting ext-proc gRPC server on :50051")

	// Start gRPC server in a goroutine
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC Server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := <-gracefulStop
	log.Printf("Caught signal: %+v", sig)
	log.Println("Shutting down servers...")

	// Graceful shutdown
	s.GracefulStop()
	log.Println("Servers stopped")

	log.Println("Wait for 1 second to finish processing")
	time.Sleep(1 * time.Second)
}

// loggingMiddleware adds comprehensive logging for all HTTP requests
func (h *MCPHelper) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log all headers for debugging
		log.Printf("=== Helper REQUEST ===")
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
				helper:         h,
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
	helper *MCPHelper
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

			if err := w.helper.handleInitialization(ctx, sessionID); err != nil {
				log.Printf("❌ Failed to create session mapping for %s: %v", sessionID, err)
			}
		}()
	}

	return w.ResponseWriter.Write(data)
}

func (w *sessionCapturingWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

// NewMCPHelper creates a new MCP Helper instance
func NewMCPHelper() *MCPHelper {
	helper := &MCPHelper{
		aggregatedTools:   make([]mcp.Tool, 0),
		clientConnections: make(map[string]*ClientBackendConnections),
		sessionMappings:   make(map[string]*SessionMapping),
	}

	// Create MCP server with tool capabilities
	helper.mcpServer = server.NewMCPServer(
		"MCP Helper",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Setup helper handlers
	helper.setupHandlers()

	return helper
}

// setupHandlers configures the MCP server handlers
func (h *MCPHelper) setupHandlers() {
	// helper info tool
	h.mcpServer.AddTool(mcp.NewTool("helper_info",
		mcp.WithDescription("Get information about the MCP Helper"),
	), h.handleHelperInfo)
}

// handleInitialization creates backend sessions when a client initializes
func (h *MCPHelper) handleInitialization(ctx context.Context, helperSessionID string) error {
	log.Printf("🆕 Creating backend sessions for helper session: %s", helperSessionID)

	// Create backend connections
	connections, err := h.createBackendConnectionsForSession(ctx, helperSessionID)
	if err != nil {
		return fmt.Errorf("failed to create backend connections: %w", err)
	}

	// Store session mapping
	mapping := &SessionMapping{
		HelperSessionID:  helperSessionID,
		Server1SessionID: connections.Server1SessionID,
		Server2SessionID: connections.Server2SessionID,
		CreatedAt:        time.Now(),
	}

	h.sessionLock.Lock()
	h.sessionMappings[helperSessionID] = mapping
	h.sessionLock.Unlock()

	log.Printf("✅ session mapping created: %s -> server1:%s, server2:%s",
		helperSessionID, connections.Server1SessionID, connections.Server2SessionID)

	return nil
}

// createBackendConnectionsForSession creates and initializes backend connections
func (h *MCPHelper) createBackendConnectionsForSession(ctx context.Context, helperSessionID string) (*ClientBackendConnections, error) {
	log.Printf("🔗 Creating backend connections for session: %s", helperSessionID)

	connections := &ClientBackendConnections{
		ClientSessionID: helperSessionID,
		CreatedAt:       time.Now(),
	}

	// Create and initialize server1 connection
	client1, sessionID1, err := h.createClientBackendConnection(ctx, connections.ClientSessionID, "server1", server1URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create server1 connection: %w", err)
	}
	connections.Server1Client = client1
	connections.Server1SessionID = sessionID1

	// Create and initialize server2 connection
	client2, sessionID2, err := h.createClientBackendConnection(ctx, connections.ClientSessionID, "server2", server2URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create server2 connection: %w", err)
	}
	connections.Server2Client = client2
	connections.Server2SessionID = sessionID2

	// Store the connections for later use
	h.connectionsLock.Lock()
	h.clientConnections[helperSessionID] = connections
	h.connectionsLock.Unlock()

	return connections, nil
}

// GetSessionMapping returns the session mapping for a helper session ID (implements SessionMapper interface)
func (g *MCPHelper) GetSessionMapping(helperSessionID string) (*extProc.SessionMapping, bool) {
	g.sessionLock.RLock()
	defer g.sessionLock.RUnlock()

	mapping, exists := g.sessionMappings[helperSessionID]
	if !exists {
		return nil, false
	}

	// Convert to extProc.SessionMapping
	return &extProc.SessionMapping{
		HelperSessionID:  mapping.HelperSessionID,
		Server1SessionID: mapping.Server1SessionID,
		Server2SessionID: mapping.Server2SessionID,
	}, true
}

// initializeBackends connects to backend servers for initial tool discovery only
func (g *MCPHelper) initializeBackends() error {
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
func (g *MCPHelper) initializeStartupClients() error {
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
		Name:    "MCP Helper (Startup)",
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
		Name:    "MCP Helper (Startup)",
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
func (g *MCPHelper) aggregateTools() error {
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
func (g *MCPHelper) registerAggregatedTools() {
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

func (g *MCPHelper) routeToolCall(_ context.Context, toolName string, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("❌ Tool call reached helper unexpectedly: %s (should be routed by Envoy)", toolName)
	return mcp.NewToolResultError(fmt.Sprintf("Tool call %s reached helper - this should be handled by Envoy routing", toolName)), nil
}

// createClientBackendConnection creates and initializes a client connection to a backend server
func (g *MCPHelper) createClientBackendConnection(ctx context.Context, clientSessionID string, serverName string, serverURL string) (*client.Client, string, error) {
	log.Printf("🔗 Creating dedicated %s connection for client %s", serverName, clientSessionID)

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
		Name:    fmt.Sprintf("MCP Helper (Client %s)", clientSessionID),
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := mcpClient.Initialize(initCtx, initRequest)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize %s: %w", serverName, err)
	}

	// Extract the session ID from the initialized client
	sessionID := mcpClient.GetSessionId()
	if sessionID == "" {
		return nil, "", fmt.Errorf("failed to get session ID from %s - session ID is empty", serverName)
	}

	log.Printf("✅ Client %s connected to %s: %s with session ID: %s",
		clientSessionID, serverName, serverInfo.ServerInfo.Name, sessionID)

	return mcpClient, sessionID, nil
}

// handleHelperInfo handles the helper_info tool
func (g *MCPHelper) handleHelperInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g.toolsLock.RLock()
	toolCount := len(g.aggregatedTools)
	g.toolsLock.RUnlock()

	g.connectionsLock.RLock()
	connectionCount := len(g.clientConnections)
	g.connectionsLock.RUnlock()

	info := map[string]interface{}{
		"helper_name":        "MCP Helper",
		"version":            "1.0.0",
		"backend_servers":    []string{server1URL, server2URL},
		"aggregated_tools":   toolCount,
		"active_connections": connectionCount,
		"status":             "running",
		"session_management": "per-client backend connections",
		"routing":            "handled by Envoy dynamic module",
	}

	return mcp.NewToolResultText(fmt.Sprintf("Helper Info: %+v", info)), nil
}
