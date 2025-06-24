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

	// Wrap with logging middleware
	loggingHandler := loggingMiddleware(streamableServer)

	// Start the HTTP server with the streamable handler
	if err := http.ListenAndServe(":"+*port, loggingHandler); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// loggingMiddleware adds comprehensive logging for all HTTP requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log all headers for debugging
		log.Printf("=== SERVER1 REQUEST ===")
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
			log.Printf("🔑 [SERVER1] MCP-SESSION-ID: %s", sessionID)
		} else {
			log.Printf("❌ [SERVER1] No mcp-session-id header found")
		}

		log.Printf("=======================")

		next.ServeHTTP(w, r)
	})
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
	log.Printf("🔧 [SERVER1] handleEcho called")
	message, err := req.RequireString("message")
	if err != nil {
		log.Printf("❌ [SERVER1] Echo error: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'message': %v", err)), nil
	}

	log.Printf("✅ [SERVER1] Echo returning: %s", message)
	return mcp.NewToolResultText(message), nil
}

// handleTimestamp handles the timestamp tool
func handleTimestamp(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("🔧 [SERVER1] handleTimestamp called")
	timestamp := time.Now().Format(time.RFC3339)
	log.Printf("✅ [SERVER1] Timestamp returning: %s", timestamp)
	return mcp.NewToolResultText(timestamp), nil
}
