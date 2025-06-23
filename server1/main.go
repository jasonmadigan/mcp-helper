package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var port = flag.String("port", "8081", "Port to listen on")
	flag.Parse()

	log.Println("Starting MCP Test Server 1...")

	// Create MCP server instance with only tool capabilities
	mcpServer := server.NewMCPServer(
		"Test Server 1",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Setup the two tools
	setupTools(mcpServer)

	// Create streamable HTTP server and start it
	log.Printf("Test Server 1 listening on port %s", *port)
	log.Printf("MCP endpoint: http://localhost:%s", *port)

	streamableServer := server.NewStreamableHTTPServer(mcpServer)

	// Start the HTTP server with the streamable handler
	if err := http.ListenAndServe(":"+*port, streamableServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// setupTools configures the two tools for Server 1
func setupTools(s *server.MCPServer) {
	// Echo tool - echoes back the input string
	s.AddTool(mcp.NewTool("echo",
		mcp.WithDescription("Echoes back the input message"),
		mcp.WithString("message",
			mcp.Description("Message to echo back"),
			mcp.Required(),
		),
	), handleEcho)

	// Timestamp tool - returns current time in ISO 8601 format
	s.AddTool(mcp.NewTool("timestamp",
		mcp.WithDescription("Returns the current timestamp in ISO 8601 format"),
	), handleTimestamp)
}

// handleEcho handles the echo tool
func handleEcho(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	message, err := req.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'message': %v", err)), nil
	}

	return mcp.NewToolResultText(message), nil
}

// handleTimestamp handles the timestamp tool
func handleTimestamp(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	timestamp := time.Now().Format(time.RFC3339)
	return mcp.NewToolResultText(timestamp), nil
}
