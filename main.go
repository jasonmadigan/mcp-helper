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

// MCPGateway represents the main MCP server that acts as both server and client
type MCPGateway struct {
	// Server side
	mcpServer *server.MCPServer

	// Client side - connections to backend servers
	server1Client *client.Client
	server2Client *client.Client

	// Tool aggregation
	aggregatedTools []mcp.Tool
	toolsLock       sync.RWMutex

	// Session management
	sessionMap  map[string]*GatewaySession // clientSessionID -> GatewaySession
	sessionLock sync.RWMutex
}

// GatewaySession tracks a client session and its corresponding backend sessions
type GatewaySession struct {
	ClientSessionID string
	Server1Session  string // session ID for server1
	Server2Session  string // session ID for server2
	CreatedAt       time.Time
}

// Implement server.Session interface for GatewaySession
func (gs *GatewaySession) SessionID() string {
	return gs.ClientSessionID
}

// Implement server.ClientSession interface for session notifications
func (gs *GatewaySession) OnNotification(notification mcp.JSONRPCNotification) error {
	// Forward notifications to client if needed
	log.Printf("Session %s received notification: %s", gs.ClientSessionID, notification.Method)
	return nil
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

	if err := http.ListenAndServe(":"+*port, streamableServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// NewMCPGateway creates a new MCP Gateway instance
func NewMCPGateway() *MCPGateway {
	gateway := &MCPGateway{
		sessionMap:      make(map[string]*GatewaySession),
		aggregatedTools: make([]mcp.Tool, 0),
	}

	// Create MCP server with session capabilities
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

	// Setup session management
	g.setupSessionHandlers()
}

// setupSessionHandlers configures session management for the gateway
func (g *MCPGateway) setupSessionHandlers() {
	// For now, we'll implement basic session management without initialization hooks
	// The gateway will create sessions dynamically when needed
	log.Println("Session handlers configured")
}

// initializeGatewaySession creates sessions with backend servers for a gateway session
func (g *MCPGateway) initializeGatewaySession(ctx context.Context, gatewaySession *GatewaySession) error {
	// Note: For HTTP transport, sessions are typically managed through headers
	// The actual session IDs would be returned from the Initialize calls to backends
	// For simplicity in this implementation, we'll generate session IDs for tracking

	gatewaySession.Server1Session = fmt.Sprintf("server1-session-%d", time.Now().UnixNano())
	gatewaySession.Server2Session = fmt.Sprintf("server2-session-%d", time.Now().UnixNano())

	log.Printf("Initialized backend sessions for gateway session %s", gatewaySession.ClientSessionID)
	return nil
}

// getOrCreateSession gets or creates a gateway session for the current context
func (g *MCPGateway) getOrCreateSession(ctx context.Context) (*GatewaySession, error) {
	// For now, create a simple session - in production this would be more sophisticated
	sessionID := fmt.Sprintf("gateway-session-%d", time.Now().UnixNano())

	gatewaySession := &GatewaySession{
		ClientSessionID: sessionID,
		CreatedAt:       time.Now(),
	}

	// Initialize backend sessions
	if err := g.initializeGatewaySession(ctx, gatewaySession); err != nil {
		return nil, fmt.Errorf("failed to initialize gateway session: %w", err)
	}

	// Store session
	g.sessionLock.Lock()
	g.sessionMap[sessionID] = gatewaySession
	g.sessionLock.Unlock()

	log.Printf("Created gateway session %s with backends [server1: %s, server2: %s]",
		sessionID, gatewaySession.Server1Session, gatewaySession.Server2Session)

	return gatewaySession, nil
}

// initializeBackends connects to backend servers and aggregates tools
func (g *MCPGateway) initializeBackends() error {
	log.Println("Initializing backend server connections...")

	// Initialize server1 client
	if err := g.initializeServer1(); err != nil {
		return fmt.Errorf("failed to initialize server1: %w", err)
	}

	// Initialize server2 client
	if err := g.initializeServer2(); err != nil {
		return fmt.Errorf("failed to initialize server2: %w", err)
	}

	// Aggregate tools from both servers
	if err := g.aggregateTools(); err != nil {
		return fmt.Errorf("failed to aggregate tools: %w", err)
	}

	log.Printf("Successfully initialized. Aggregated %d tools from backend servers.", len(g.aggregatedTools))
	return nil
}

// initializeServer1 creates and initializes connection to server1
func (g *MCPGateway) initializeServer1() error {
	log.Printf("Connecting to server1 at %s...", server1URL)

	// Create HTTP transport for server1
	httpTransport, err := transport.NewStreamableHTTP(server1URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server1: %w", err)
	}

	// Create client
	g.server1Client = client.NewClient(httpTransport)

	// Initialize with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "MCP Gateway",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := g.server1Client.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize server1: %w", err)
	}

	log.Printf("Connected to server1: %s (version %s)", serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	return nil
}

// initializeServer2 creates and initializes connection to server2
func (g *MCPGateway) initializeServer2() error {
	log.Printf("Connecting to server2 at %s...", server2URL)

	// Create HTTP transport for server2
	httpTransport, err := transport.NewStreamableHTTP(server2URL)
	if err != nil {
		return fmt.Errorf("failed to create HTTP transport for server2: %w", err)
	}

	// Create client
	g.server2Client = client.NewClient(httpTransport)

	// Initialize with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize the connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "MCP Gateway",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	serverInfo, err := g.server2Client.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize server2: %w", err)
	}

	log.Printf("Connected to server2: %s (version %s)", serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	return nil
}

// aggregateTools fetches and aggregates tools from both backend servers
func (g *MCPGateway) aggregateTools() error {
	log.Println("Aggregating tools from backend servers...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []mcp.Tool

	// Get tools from server1
	server1Tools, err := g.server1Client.ListTools(ctx, mcp.ListToolsRequest{})
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

	// Get tools from server2
	server2Tools, err := g.server2Client.ListTools(ctx, mcp.ListToolsRequest{})
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

// routeToolCall routes tool calls to the appropriate backend server
func (g *MCPGateway) routeToolCall(ctx context.Context, toolName string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get or create session for this request
	gatewaySession, err := g.getOrCreateSession(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Session error: %v", err)), nil
	}

	// Parse tool name to determine backend
	var backendClient *client.Client
	var backendSessionID string
	var originalToolName string

	if strings.HasPrefix(toolName, "server1-") {
		backendClient = g.server1Client
		backendSessionID = gatewaySession.Server1Session
		originalToolName = strings.TrimPrefix(toolName, "server1-")
	} else if strings.HasPrefix(toolName, "server2-") {
		backendClient = g.server2Client
		backendSessionID = gatewaySession.Server2Session
		originalToolName = strings.TrimPrefix(toolName, "server2-")
	} else {
		return mcp.NewToolResultError(fmt.Sprintf("Unknown tool prefix for %s", toolName)), nil
	}

	// Create call request with original tool name
	backendReq := mcp.CallToolRequest{}
	backendReq.Params.Name = originalToolName
	backendReq.Params.Arguments = req.Params.Arguments

	// Call backend server
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	log.Printf("Routing tool call %s -> %s (session: %s)", toolName, originalToolName, backendSessionID)

	result, err := backendClient.CallTool(ctx, backendReq)
	if err != nil {
		log.Printf("Backend call failed for %s: %v", toolName, err)
		return mcp.NewToolResultError(fmt.Sprintf("Backend call failed: %v", err)), nil
	}

	log.Printf("Tool call %s completed successfully", toolName)
	return result, nil
}

// handleGatewayInfo handles the gateway_info tool
func (g *MCPGateway) handleGatewayInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g.toolsLock.RLock()
	toolCount := len(g.aggregatedTools)
	g.toolsLock.RUnlock()

	g.sessionLock.RLock()
	sessionCount := len(g.sessionMap)
	g.sessionLock.RUnlock()

	info := map[string]interface{}{
		"gateway_name":     "MCP Gateway",
		"version":          "1.0.0",
		"backend_servers":  []string{server1URL, server2URL},
		"aggregated_tools": toolCount,
		"active_sessions":  sessionCount,
		"status":           "running",
	}

	return mcp.NewToolResultText(fmt.Sprintf("Gateway Info: %+v", info)), nil
}
