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

	// Start the HTTP server with the streamable handler
	if err := http.ListenAndServe(":"+*port, streamableServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
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
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate random number 1-6
	roll := rand.Intn(6) + 1

	return mcp.NewToolResultText(fmt.Sprintf("🎲 You rolled: %d", roll)), nil
}

// handle8Ball handles the 8 ball tool
func handle8Ball(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	question, err := req.RequireString("question")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'question': %v", err)), nil
	}

	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Get random response
	response := eightBallResponses[rand.Intn(len(eightBallResponses))]

	return mcp.NewToolResultText(fmt.Sprintf("🎱 Question: %s\nAnswer: %s", question, response)), nil
}

// TODO: Add tool handlers
