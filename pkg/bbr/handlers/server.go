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
	"errors"
	"io"
	"log"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SessionMapper interface to access session mappings
type SessionMapper interface {
	GetSessionMapping(gatewaySessionID string) (*SessionMapping, bool)
}

// SessionMapping represents the mapping between gateway and backend sessions
type SessionMapping struct {
	GatewaySessionID string
	Server1SessionID string
	Server2SessionID string
}

func NewServer(streaming bool, gateway SessionMapper) *Server {
	return &Server{
		streaming: streaming,
		gateway:   gateway,
	}
}

// Server implements the Envoy external processing server.
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/ext_proc/v3/external_processor.proto
type Server struct {
	streaming      bool
	requestHeaders *extProcPb.HttpHeaders // Store headers for later use in body processing
	gateway        SessionMapper          // Direct access to session mappings
}

func (s *Server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	ctx := srv.Context()
	log.Println("Processing new request")

	streamedBody := &streamedBody{}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, recvErr := srv.Recv()
		if recvErr == io.EOF || errors.Is(recvErr, context.Canceled) {
			return nil
		}
		if recvErr != nil {
			log.Printf("Cannot receive stream request: %v", recvErr)
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", recvErr)
		}

		var responses []*extProcPb.ProcessingResponse
		var err error
		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			// Store headers for later use in body processing
			s.requestHeaders = req.GetRequestHeaders()

			if s.streaming && !req.GetRequestHeaders().GetEndOfStream() {
				// If streaming and the body is not empty, then headers are handled when processing request body.
				log.Println("Received headers, passing off header processing until body arrives...")
			} else {
				if requestId := ExtractHeaderValue(v, RequestIdHeaderKey); len(requestId) > 0 {
					log.Printf("Processing request with ID: %s", requestId)
				}
				responses, err = s.HandleRequestHeaders(req.GetRequestHeaders())
			}
		case *extProcPb.ProcessingRequest_RequestBody:
			log.Printf("Incoming body chunk: %s (EoS: %t)", string(v.RequestBody.Body), v.RequestBody.EndOfStream)
			responses, err = s.processRequestBody(ctx, req.GetRequestBody(), streamedBody)
		case *extProcPb.ProcessingRequest_ResponseHeaders:
			responses, err = s.HandleResponseHeaders(req.GetResponseHeaders())
		case *extProcPb.ProcessingRequest_ResponseBody:
			responses, err = s.HandleResponseBody(req.GetResponseBody())
		default:
			log.Printf("Unknown Request type: %T", v)
			return status.Error(codes.Unknown, "unknown request type")
		}

		if err != nil {
			log.Printf("Failed to process request: %v", err)
			return status.Errorf(status.Code(err), "failed to handle request: %v", err)
		}

		for _, resp := range responses {
			log.Printf("Response generated: %+v", resp)
			if err := srv.Send(resp); err != nil {
				log.Printf("Send failed: %v", err)
				return status.Errorf(codes.Unknown, "failed to send response back to Envoy: %v", err)
			}
		}
	}
}

type streamedBody struct {
	body []byte
}

func (s *Server) processRequestBody(ctx context.Context, body *extProcPb.HttpBody, streamedBody *streamedBody) ([]*extProcPb.ProcessingResponse, error) {

	var requestBody map[string]interface{}
	if s.streaming {
		streamedBody.body = append(streamedBody.body, body.Body...)
		// In the stream case, we can receive multiple request bodies.
		if body.EndOfStream {
			log.Println("Flushing stream buffer")
			err := json.Unmarshal(streamedBody.body, &requestBody)
			if err != nil {
				log.Printf("Error unmarshaling request body: %v", err)
			}
		} else {
			return nil, nil
		}
	} else {
		if err := json.Unmarshal(body.GetBody(), &requestBody); err != nil {
			return nil, err
		}
	}

	requestBodyResp, err := s.HandleRequestBody(ctx, requestBody)
	if err != nil {
		return nil, err
	}

	return requestBodyResp, nil
}
