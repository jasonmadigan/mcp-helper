package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	testGatewayURL = "http://localhost:8080"
	testServer1URL = "http://localhost:8081"
	testServer2URL = "http://localhost:8082"
)

// TestE2E is the main end-to-end test
func TestE2E(t *testing.T) {
	log.Println("🚀 Starting E2E Test")

	// Step 1: Start all three servers
	servers := startServers(t)
	defer stopServers(servers)

	// Step 2: Wait for servers to be ready
	waitForServers(t)

	// Step 3: Test first MCP client session
	log.Println("📋 Testing first MCP client session...")
	session1Results := testMCPClient(t, 1)

	// Step 4: Test second MCP client session
	log.Println("📋 Testing second MCP client session...")
	session2Results := testMCPClient(t, 2)

	// Step 5: Verify session isolation
	verifySessionIsolation(t, session1Results, session2Results)

	log.Println("✅ E2E Test completed successfully!")
}

// ServerProcess holds process information for a server
type ServerProcess struct {
	Name    string
	Process *exec.Cmd
}

// startServers starts all three servers and returns their process information
func startServers(t *testing.T) []ServerProcess {
	log.Println("🔧 Starting servers...")

	var servers []ServerProcess

	// Start server1
	log.Println("Starting server1...")
	server1Cmd := exec.Command("go", "run", "main.go", "-port=8081")
	server1Cmd.Dir = "server1"
	server1Cmd.Stdout = os.Stdout
	server1Cmd.Stderr = os.Stderr
	if err := server1Cmd.Start(); err != nil {
		t.Fatalf("Failed to start server1: %v", err)
	}
	servers = append(servers, ServerProcess{"server1", server1Cmd})

	// Start server2
	log.Println("Starting server2...")
	server2Cmd := exec.Command("go", "run", "main.go", "-port=8082")
	server2Cmd.Dir = "server2"
	server2Cmd.Stdout = os.Stdout
	server2Cmd.Stderr = os.Stderr
	if err := server2Cmd.Start(); err != nil {
		t.Fatalf("Failed to start server2: %v", err)
	}
	servers = append(servers, ServerProcess{"server2", server2Cmd})

	// Wait for upstream servers to be ready before starting gateway
	log.Println("Waiting for upstream servers to be ready...")
	waitForServer(t, testServer1URL, "server1 (before gateway)")
	waitForServer(t, testServer2URL, "server2 (before gateway)")

	// Start gateway
	log.Println("Starting gateway...")
	gatewayCmd := exec.Command("go", "run", "main.go", "-port=8080")
	gatewayCmd.Stdout = os.Stdout
	gatewayCmd.Stderr = os.Stderr
	if err := gatewayCmd.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	servers = append(servers, ServerProcess{"gateway", gatewayCmd})

	return servers
}

// stopServers stops all servers
func stopServers(servers []ServerProcess) {
	log.Println("🛑 Stopping servers...")
	for _, server := range servers {
		if server.Process != nil {
			log.Printf("Stopping %s...", server.Name)
			server.Process.Process.Kill()
		}
	}
}

// waitForServers waits for all servers to be ready
func waitForServers(t *testing.T) {
	log.Println("⏳ Waiting for servers to be ready...")

	// Wait for server1
	waitForServer(t, testServer1URL, "server1")

	// Wait for server2
	waitForServer(t, testServer2URL, "server2")

	// Wait for gateway (this should be last since it depends on the others)
	waitForServer(t, testGatewayURL, "gateway")

	log.Println("✅ All servers are ready!")
}

// waitForServer waits for a specific server to be ready
func waitForServer(t *testing.T, url, name string) {
	for i := 0; i < 30; i++ { // Wait up to 30 seconds
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			log.Printf("✅ %s is ready", name)
			return
		}
		log.Printf("⏳ Waiting for %s... (%d/30)", name, i+1)
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("❌ %s failed to start within 30 seconds", name)
}

// SessionResults holds the results from testing a MCP client session
type SessionResults struct {
	SessionID            string
	ToolsList            []mcp.Tool
	Server1HeadersResult string
	Server2HeadersResult string
	GatewaySessionID     string
}

// testMCPClient tests a single MCP client session
func testMCPClient(t *testing.T, sessionNum int) SessionResults {
	log.Printf("🔗 Creating MCP client %d...", sessionNum)

	// Create HTTP transport
	httpTransport, err := transport.NewStreamableHTTP(testGatewayURL)
	if err != nil {
		t.Fatalf("Failed to create HTTP transport: %v", err)
	}

	// Create client
	mcpClient := client.NewClient(httpTransport)

	// Initialize client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    fmt.Sprintf("E2E Test Client %d", sessionNum),
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	log.Printf("🤝 Initializing client %d...", sessionNum)
	serverInfo, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		t.Fatalf("Failed to initialize client %d: %v", sessionNum, err)
	}

	log.Printf("✅ Client %d connected to: %s (version %s)",
		sessionNum, serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)

	// Test tools list
	log.Printf("📋 Listing tools for client %d...", sessionNum)
	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		t.Fatalf("Failed to list tools for client %d: %v", sessionNum, err)
	}

	log.Printf("✅ Client %d found %d tools", sessionNum, len(toolsResult.Tools))

	// Verify expected tools are present
	expectedTools := []string{"server1-echo_headers", "server2-echo_headers", "gateway_info"}
	for _, expectedTool := range expectedTools {
		found := false
		for _, tool := range toolsResult.Tools {
			if tool.Name == expectedTool {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Expected tool '%s' not found for client %d", expectedTool, sessionNum)
		}
	}

	// Test echo_headers tool on server1
	log.Printf("🔧 Testing server1-echo_headers for client %d...", sessionNum)
	server1CallRequest := mcp.CallToolRequest{}
	server1CallRequest.Params.Name = "server1-echo_headers"
	server1CallRequest.Params.Arguments = make(map[string]interface{})

	server1Result, err := mcpClient.CallTool(ctx, server1CallRequest)
	if err != nil {
		t.Fatalf("Failed to call server1-echo_headers for client %d: %v", sessionNum, err)
	}

	server1HeadersText := extractTextFromResult(server1Result)
	log.Printf("✅ Server1 headers for client %d: %s", sessionNum, server1HeadersText)

	// Test echo_headers tool on server2
	log.Printf("🔧 Testing server2-echo_headers for client %d...", sessionNum)
	server2CallRequest := mcp.CallToolRequest{}
	server2CallRequest.Params.Name = "server2-echo_headers"
	server2CallRequest.Params.Arguments = make(map[string]interface{})

	server2Result, err := mcpClient.CallTool(ctx, server2CallRequest)
	if err != nil {
		t.Fatalf("Failed to call server2-echo_headers for client %d: %v", sessionNum, err)
	}

	server2HeadersText := extractTextFromResult(server2Result)
	log.Printf("✅ Server2 headers for client %d: %s", sessionNum, server2HeadersText)

	gatewaySessionID := httpTransport.GetSessionId()

	// Close the client
	mcpClient.Close()

	return SessionResults{
		SessionID:            fmt.Sprintf("client-%d", sessionNum),
		ToolsList:            toolsResult.Tools,
		Server1HeadersResult: server1HeadersText,
		Server2HeadersResult: server2HeadersText,
		GatewaySessionID:     gatewaySessionID,
	}
}

// extractTextFromResult extracts text content from a CallToolResult
func extractTextFromResult(result *mcp.CallToolResult) string {
	if len(result.Content) > 0 {
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}

// extractSessionID extracts a session ID from headers text using regex
func extractSessionID(headersText, headerName string) string {
	// Look for patterns like "mcp-session-id: some-value"
	pattern := fmt.Sprintf(`%s:\s*([^\s\n]+)`, regexp.QuoteMeta(headerName))
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(headersText)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// verifySessionIsolation verifies that different client sessions have different session IDs
func verifySessionIsolation(t *testing.T, session1, session2 SessionResults) {
	log.Println("🔍 Verifying session isolation...")

	// Check that both sessions got tools
	if len(session1.ToolsList) == 0 {
		t.Fatal("Session 1 got no tools")
	}
	if len(session2.ToolsList) == 0 {
		t.Fatal("Session 2 got no tools")
	}

	// Verify sessions have the same tools (should be identical)
	if len(session1.ToolsList) != len(session2.ToolsList) {
		t.Fatalf("Sessions have different number of tools: %d vs %d",
			len(session1.ToolsList), len(session2.ToolsList))
	}

	// Check that gateway session IDs are different
	if session1.GatewaySessionID == "" {
		t.Fatal("Session 1 gateway session ID not found")
	}
	if session2.GatewaySessionID == "" {
		t.Fatal("Session 2 gateway session ID not found")
	}
	if session1.GatewaySessionID == session2.GatewaySessionID {
		t.Fatalf("Gateway session IDs should be different but both are: %s",
			session1.GatewaySessionID)
	}

	log.Printf("✅ Gateway Session isolation verified:")
	log.Printf("  Gateway Session 1: %s", session1.GatewaySessionID)
	log.Printf("  Gateway Session 2: %s", session2.GatewaySessionID)

	// Also verify that backend session IDs are different for each session
	server1SessionID1 := extractSessionID(session1.Server1HeadersResult, "Mcp-Session-Id")
	server1SessionID2 := extractSessionID(session2.Server1HeadersResult, "Mcp-Session-Id")

	if server1SessionID1 != "" && server1SessionID2 != "" && server1SessionID1 == server1SessionID2 {
		t.Fatalf("Server1 session IDs should be different but both are: %s",
			server1SessionID1)
	}
	log.Printf("✅  Server 1 Session isolation verified:")
	log.Printf("  Server 1 Session 1: %s", server1SessionID1)
	log.Printf("  Server 1 Session 2: %s", server1SessionID2)

	server2SessionID1 := extractSessionID(session1.Server2HeadersResult, "Mcp-Session-Id")
	server2SessionID2 := extractSessionID(session2.Server2HeadersResult, "Mcp-Session-Id")

	if server2SessionID1 != "" && server2SessionID2 != "" && server2SessionID1 == server2SessionID2 {
		t.Fatalf("Server2 session IDs should be different but both are: %s",
			server2SessionID1)
	}
	log.Printf("✅  Server 2 Session isolation verified:")
	log.Printf("  Server 2 Session 1: %s", server2SessionID1)
	log.Printf("  Server 2 Session 2: %s", server2SessionID2)

	log.Println("✅ All session IDs are properly isolated!")
}
