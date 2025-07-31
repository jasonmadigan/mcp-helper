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
	"log"

	eppb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

// HandleRequestBody handles request bodies for MCP tool calls.
func (s *Server) HandleRequestBody(ctx context.Context, data map[string]any) ([]*eppb.ProcessingResponse, error) {
	log.Println("[EXT-PROC] Processing request body...")

	return s.createEmptyBodyResponse(), nil
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
	return []*eppb.ProcessingResponse{
		{
			Response: &eppb.ProcessingResponse_RequestHeaders{
				RequestHeaders: &eppb.HeadersResponse{},
			},
		},
	}, nil
}
