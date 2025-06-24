package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var port = flag.String("port", "8082", "Port to listen on")
	flag.Parse()

	log.Println("Starting MCP Test Server 2...")

	// Create MCP server instance with only tool capabilities
	mcpServer := server.NewMCPServer(
		"Test Server 2",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Setup the two tools
	setupTools(mcpServer)

	// Create streamable HTTP server and start it
	log.Printf("Test Server 2 listening on port %s", *port)
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
		log.Printf("=== SERVER2 REQUEST ===")
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
			log.Printf("🔑 [SERVER2] MCP-SESSION-ID: %s", sessionID)
		} else {
			log.Printf("❌ [SERVER2] No mcp-session-id header found")
		}

		log.Printf("=======================")

		// Add HTTP headers to context for tool handlers to access
		ctx := context.WithValue(r.Context(), "http_headers", map[string][]string(r.Header))
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	})
}

// setupTools configures the two tools for Server 2
func setupTools(s *server.MCPServer) {
	// Dice roll tool - returns a random number 1 to 6
	s.AddTool(mcp.NewTool("dice_roll",
		mcp.WithDescription("Roll a dice and return a random number from 1 to 6"),
	), handleDiceRoll)

	// 8 ball tool - returns a random response
	s.AddTool(mcp.NewTool("8_ball",
		mcp.WithDescription("Ask the magic 8 ball a question and get a random response"),
		mcp.WithString("question",
			mcp.Description("Your question for the magic 8 ball"),
			mcp.Required(),
		),
	), handle8Ball)

	// Echo headers tool - returns all headers from the request
	s.AddTool(mcp.NewTool("echo_headers",
		mcp.WithDescription("Returns all headers received by the server"),
	), handleEchoHeaders)
}

// 8 ball responses
var eightBallResponses = []string{
	"Yes, definitely",
	"It is certain",
	"Without a doubt",
	"Ask again later",
	"Cannot predict now",
	"Don't count on it",
	"My reply is no",
	"Outlook not so good",
	"Signs point to yes",
	"Very doubtful",
}

// handleDiceRoll handles the dice roll tool
func handleDiceRoll(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("🔧 [SERVER2] handleDiceRoll called")
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate random number 1-6
	roll := rand.Intn(6) + 1

	log.Printf("✅ [SERVER2] Dice roll returning: %d", roll)
	return mcp.NewToolResultText(fmt.Sprintf("🎲 You rolled: %d", roll)), nil
}

// handle8Ball handles the 8 ball tool
func handle8Ball(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("🔧 [SERVER2] handle8Ball called")
	question, err := req.RequireString("question")
	if err != nil {
		log.Printf("❌ [SERVER2] 8-ball error: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'question': %v", err)), nil
	}

	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Get random response
	response := eightBallResponses[rand.Intn(len(eightBallResponses))]

	log.Printf("✅ [SERVER2] 8-ball question: %s, answer: %s", question, response)
	return mcp.NewToolResultText(fmt.Sprintf("🎱 Question: %s\nAnswer: %s", question, response)), nil
}

// handleEchoHeaders handles the echo_headers tool
func handleEchoHeaders(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("🔧 [SERVER2] handleEchoHeaders called")

	// Extract HTTP headers from context
	headers := make(map[string]interface{})
	headers["server"] = "server2"
	headers["timestamp"] = time.Now().Format(time.RFC3339)

	// Try to get the HTTP request from context - this depends on the server implementation
	// For now, we'll use a custom context key that we need to set in the middleware
	if httpHeaders, ok := ctx.Value("http_headers").(map[string][]string); ok {
		for name, values := range httpHeaders {
			if len(values) > 0 {
				headers[name] = values[0] // Take first value for simplicity
			}
		}
	} else {
		// If no headers are available, show the context keys for debugging
		headers["context_debug"] = "No HTTP headers found in context"
	}

	result := fmt.Sprintf("Server2 Headers:\n")
	for key, value := range headers {
		result += fmt.Sprintf("  %s: %v\n", key, value)
	}

	log.Printf("✅ [SERVER2] EchoHeaders returning headers")
	return mcp.NewToolResultText(result), nil
}
