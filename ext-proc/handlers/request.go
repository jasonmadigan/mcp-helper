package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	basepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	eppb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const toolHeader = "x-mcp-toolname"

// extractMCPToolName safely extracts the tool name from MCP tool call request
func extractMCPToolName(data map[string]any) string {
	// Check if this is a JSON-RPC request
	jsonrpcVal, ok := data["jsonrpc"]
	if !ok {
		return ""
	}

	jsonrpcStr, ok := jsonrpcVal.(string)
	if !ok || jsonrpcStr != "2.0" {
		return ""
	}

	// Extract method field and check if it's tools/call
	methodVal, ok := data["method"]
	if !ok {
		return ""
	}

	methodStr, ok := methodVal.(string)
	if !ok {
		return ""
	}

	if methodStr != "tools/call" {
		return ""
	}

	// Extract params
	paramsVal, ok := data["params"]
	if !ok {
		log.Println("[EXT-PROC] MCP tool call missing params field")
		return ""
	}

	paramsMap, ok := paramsVal.(map[string]interface{})
	if !ok {
		log.Println("[EXT-PROC] MCP tool call params is not an object")
		return ""
	}

	// Extract tool name
	nameVal, ok := paramsMap["name"]
	if !ok {
		log.Println("[EXT-PROC] MCP tool call missing name field in params")
		return ""
	}

	nameStr, ok := nameVal.(string)
	if !ok {
		log.Println("[EXT-PROC] MCP tool call name is not a string")
		return ""
	}

	return nameStr
}

// stripServerPrefix removes server1- or server2- prefix from tool names
// Returns the stripped name and whether stripping was needed
func stripServerPrefix(toolName string) (string, bool) {
	if len(toolName) > 8 && toolName[:8] == "server1-" {
		return toolName[8:], true
	}
	if len(toolName) > 8 && toolName[:8] == "server2-" {
		return toolName[8:], true
	}
	return toolName, false
}

// extractSessionFromContext extracts mcp-session-id from the stored request headers
func (s *Server) extractSessionFromContext(ctx context.Context) string {
	if s.requestHeaders == nil || s.requestHeaders.Headers == nil {
		return ""
	}

	// Extract mcp-session-id from stored headers
	for _, header := range s.requestHeaders.Headers.Headers {
		if strings.ToLower(header.Key) == "mcp-session-id" {
			return string(header.RawValue)
		}
	}

	return ""
}

// HandleRequestBody handles request bodies for MCP tool calls.
func (s *Server) HandleRequestBody(ctx context.Context, data map[string]any) ([]*eppb.ProcessingResponse, error) {
	log.Println("[EXT-PROC] Processing request body for MCP tool calls...")

	// Extract tool name - only process tools/call
	toolName := extractMCPToolName(data)
	if toolName == "" {
		log.Println("[EXT-PROC] No MCP tool name found or not tools/call, continuing to gateway")
		return s.createEmptyBodyResponse(), nil
	}

	log.Printf("[EXT-PROC] Tool name: %s", toolName)

	// Determine routing based on tool prefix
	var routeTarget string
	if strings.HasPrefix(toolName, "server1-") {
		routeTarget = "server1"
	} else if strings.HasPrefix(toolName, "server2-") {
		routeTarget = "server2"
	} else {
		log.Println("[EXT-PROC] Tool name doesn't start with server1- or server2-, continuing to gateway")
		return s.createEmptyBodyResponse(), nil
	}

	log.Printf("[EXT-PROC] Routing to: %s", routeTarget)

	// Strip server prefix from tool name and modify request body
	strippedToolName, _ := stripServerPrefix(toolName)
	log.Printf("[EXT-PROC] Stripped tool name: %s", strippedToolName)

	// Create modified request body with stripped tool name
	modifiedData := make(map[string]any)
	for k, v := range data {
		modifiedData[k] = v
	}

	if params, ok := modifiedData["params"].(map[string]interface{}); ok {
		params["name"] = strippedToolName
		log.Printf("[EXT-PROC] ✅ Updated tool name in request body: %s", strippedToolName)
	}

	requestBodyBytes, err := json.Marshal(modifiedData)
	if err != nil {
		log.Printf("[EXT-PROC] Failed to marshal modified request body: %v", err)
		return s.createEmptyBodyResponse(), nil
	}

	// Get gateway session ID
	gatewaySession := s.extractSessionFromContext(ctx)
	if gatewaySession == "" {
		log.Println("[EXT-PROC] No mcp-session-id found in headers, using fallback")
		fallbackSession := fmt.Sprintf("%s-session-%s", routeTarget, "fallback")
		return s.createRoutingResponse(toolName, requestBodyBytes, routeTarget, fallbackSession), nil
	}

	log.Printf("[EXT-PROC] Gateway session: %s", gatewaySession)

	// Lookup session mapping directly from gateway (no HTTP call needed!)
	if s.gateway == nil {
		log.Println("[EXT-PROC] No gateway available for session lookup, using fallback")
		fallbackSession := fmt.Sprintf("%s-session-%s", routeTarget, gatewaySession)
		return s.createRoutingResponse(toolName, requestBodyBytes, routeTarget, fallbackSession), nil
	}

	sessionMapping, found := s.gateway.GetSessionMapping(gatewaySession)
	if !found {
		log.Printf("[EXT-PROC] Session mapping not found for %s, using fallback", gatewaySession)
		fallbackSession := fmt.Sprintf("%s-session-%s", routeTarget, gatewaySession)
		return s.createRoutingResponse(toolName, requestBodyBytes, routeTarget, fallbackSession), nil
	}

	// Use the correct backend session ID
	var backendSession string
	if routeTarget == "server1" {
		backendSession = sessionMapping.Server1SessionID
	} else {
		backendSession = sessionMapping.Server2SessionID
	}

	log.Printf("[EXT-PROC] Using gateway-provided session: %s", backendSession)

	return s.createRoutingResponse(toolName, requestBodyBytes, routeTarget, backendSession), nil
}

// createRoutingResponse creates a response with routing headers and session mapping
func (s *Server) createRoutingResponse(toolName string, bodyBytes []byte, routeTarget, backendSession string) []*eppb.ProcessingResponse {
	log.Printf("[EXT-PROC] 🔧 createRoutingResponse - streaming: %v, route: %s, session: %s", s.streaming, routeTarget, backendSession)

	headers := []*basepb.HeaderValueOption{
		{
			Header: &basepb.HeaderValue{
				Key:      toolHeader,
				RawValue: []byte(toolName),
			},
		},
		{
			Header: &basepb.HeaderValue{
				Key:      "x-mcp-server",
				RawValue: []byte(routeTarget),
			},
		},
	}

	// Add backend session header if we have one
	if backendSession != "" {
		headers = append(headers, &basepb.HeaderValueOption{
			Header: &basepb.HeaderValue{
				Key:      "mcp-session-id",
				RawValue: []byte(backendSession),
			},
		})
	}

	// Update content-length header to match the modified body
	contentLength := fmt.Sprintf("%d", len(bodyBytes))
	headers = append(headers, &basepb.HeaderValueOption{
		Header: &basepb.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(contentLength),
		},
	})

	if s.streaming {
		log.Printf("[EXT-PROC] 🚀 Using streaming mode - returning header response first")
		ret := []*eppb.ProcessingResponse{
			{
				Response: &eppb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &eppb.HeadersResponse{
						Response: &eppb.CommonResponse{
							ClearRouteCache: true,
							HeaderMutation: &eppb.HeaderMutation{
								SetHeaders: headers,
							},
						},
					},
				},
			},
		}
		ret = addStreamedBodyResponse(ret, bodyBytes)
		log.Printf("[EXT-PROC] Completed MCP processing with routing to %s (streaming)", routeTarget)
		return ret
	}

	// For non-streaming: Set headers in RequestBody response with ClearRouteCache
	log.Printf("[EXT-PROC] 📦 Using non-streaming mode - setting headers in body response")
	log.Printf("[EXT-PROC] Completed MCP processing with routing to %s", routeTarget)
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestBody{
				RequestBody: &eppb.BodyResponse{
					Response: &eppb.CommonResponse{
						// Necessary so that the new headers are used in the routing decision.
						ClearRouteCache: true,
						HeaderMutation: &eppb.HeaderMutation{
							SetHeaders: headers,
						},
						BodyMutation: &eppb.BodyMutation{
							Mutation: &eppb.BodyMutation_Body{
								Body: bodyBytes,
							},
						},
					},
				},
			},
		},
	}
}

func addStreamedBodyResponse(responses []*eppb.ProcessingResponse, requestBodyBytes []byte) []*eppb.ProcessingResponse {
	return append(responses, &eppb.ProcessingResponse{
		Response: &eppb.ProcessingResponse_RequestBody{
			RequestBody: &eppb.BodyResponse{
				Response: &eppb.CommonResponse{
					BodyMutation: &eppb.BodyMutation{
						Mutation: &eppb.BodyMutation_StreamedResponse{
							StreamedResponse: &eppb.StreamedBodyResponse{
								Body:        requestBodyBytes,
								EndOfStream: true,
							},
						},
					},
				},
			},
		},
	})
}

// createEmptyBodyResponse creates a response that doesn't modify the request
func (s *Server) createEmptyBodyResponse() []*eppb.ProcessingResponse {
	if s.streaming {
		return []*eppb.ProcessingResponse{
			{
				Response: &eppb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &eppb.HeadersResponse{},
				},
			},
		}
	}

	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestBody{
				RequestBody: &eppb.BodyResponse{},
			},
		},
	}
}

// HandleRequestHeaders handles request headers minimally.
func (s *Server) HandleRequestHeaders(headers *eppb.HttpHeaders) ([]*eppb.ProcessingResponse, error) {
	log.Printf("[EXT-PROC] 🔍 HandleRequestHeaders called - streaming: %v", s.streaming)
	if headers != nil && headers.Headers != nil {
		for _, header := range headers.Headers.Headers {
			if strings.ToLower(header.Key) == "content-type" || strings.ToLower(header.Key) == "mcp-session-id" {
				log.Printf("[EXT-PROC] 🔍 Header: %s = %s", header.Key, string(header.RawValue))
			}
		}
	}
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &eppb.HeadersResponse{},
			},
		},
	}, nil
}
