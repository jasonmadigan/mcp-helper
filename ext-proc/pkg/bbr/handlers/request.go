/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handlers

import (
	"context"
	"encoding/json"
	"log"

	basepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	eppb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const toolHeader = "x-mcp-tool"

// MCPToolCall represents an MCP tool call request structure
type MCPToolCall struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"params"`
}

// HandleRequestBody handles request bodies for MCP tool calls.
func (s *Server) HandleRequestBody(ctx context.Context, data map[string]any) ([]*eppb.ProcessingResponse, error) {
	log.Println("[EXT-PROC] Processing request body for MCP tool calls...")

	var ret []*eppb.ProcessingResponse

	requestBodyBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[EXT-PROC] Failed to marshal request body: %v", err)
		return s.createEmptyBodyResponse(), nil
	}

	// Try to extract MCP tool name
	toolName := extractMCPToolName(data)

	if toolName == "" {
		log.Println("[EXT-PROC] No MCP tool name found, proceeding without header")
		return s.createEmptyBodyResponse(), nil
	}

	if s.streaming {
		ret = append(ret, &eppb.ProcessingResponse{
			Response: &eppb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &eppb.HeadersResponse{
					Response: &eppb.CommonResponse{
						ClearRouteCache: true,
						HeaderMutation: &eppb.HeaderMutation{
							SetHeaders: []*basepb.HeaderValueOption{
								{
									Header: &basepb.HeaderValue{
										Key:      toolHeader,
										RawValue: []byte(toolName),
									},
								},
							},
						},
					},
				},
			},
		})
		ret = addStreamedBodyResponse(ret, requestBodyBytes)
		log.Println("[EXT-PROC] Completed MCP processing (streaming)")
		return ret, nil
	}

	log.Println("[EXT-PROC] Completed MCP processing")
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestBody{
				RequestBody: &eppb.BodyResponse{
					Response: &eppb.CommonResponse{
						ClearRouteCache: true,
						HeaderMutation: &eppb.HeaderMutation{
							SetHeaders: []*basepb.HeaderValueOption{
								{
									Header: &basepb.HeaderValue{
										Key:      toolHeader,
										RawValue: []byte(toolName),
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

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

func addStreamedBodyResponse(responses []*eppb.ProcessingResponse, requestBodyBytes []byte) []*eppb.ProcessingResponse {
	return append(responses, &extProcPb.ProcessingResponse{
		Response: &extProcPb.ProcessingResponse_RequestBody{
			RequestBody: &extProcPb.BodyResponse{
				Response: &extProcPb.CommonResponse{
					BodyMutation: &extProcPb.BodyMutation{
						Mutation: &extProcPb.BodyMutation_StreamedResponse{
							StreamedResponse: &extProcPb.StreamedBodyResponse{
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

// HandleRequestHeaders handles request headers minimally.
func (s *Server) HandleRequestHeaders(headers *eppb.HttpHeaders) ([]*eppb.ProcessingResponse, error) {
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &eppb.HeadersResponse{},
			},
		},
	}, nil
}
