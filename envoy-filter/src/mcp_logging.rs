use envoy_proxy_dynamic_modules_rust_sdk::*;
use envoy_proxy_dynamic_modules_rust_sdk::{EnvoyBuffer};
use serde::{Deserialize, Serialize};

/// Configuration for the body-based routing filter.
#[derive(Serialize, Deserialize, Debug)]
pub struct FilterConfig {
    #[serde(default)]
    debug: bool,
}

/// Response from the gateway session lookup endpoint
#[derive(Deserialize, Debug)]
struct SessionLookupResponse {
    server1_session_id: String,
    server2_session_id: String,
    found: bool,
}

impl FilterConfig {
    /// Creates a new FilterConfig from JSON configuration.
    pub fn new(filter_config: &str) -> Self {
        if filter_config.trim().is_empty() {
            FilterConfig { debug: false }
        } else {
            serde_json::from_str::<FilterConfig>(filter_config)
                .unwrap_or_else(|_| FilterConfig { debug: false })
        }
    }
}

impl<EC: EnvoyHttpFilterConfig, EHF: EnvoyHttpFilter> HttpFilterConfig<EC, EHF> for FilterConfig {
    fn new_http_filter(&mut self, _envoy: &mut EC) -> Box<dyn HttpFilter<EHF>> {
        Box::new(Filter::new())
    }
}

// Helper function to find a header value by name
fn find_header_value(headers: &[(EnvoyBuffer, EnvoyBuffer)], name: &str) -> String {
    for (header_name, header_value) in headers {
        if let Ok(name_str) = std::str::from_utf8(header_name.as_slice()) {
            if name_str.eq_ignore_ascii_case(name) {
                if let Ok(value_str) = std::str::from_utf8(header_value.as_slice()) {
                    return value_str.to_string();
                }
            }
        }
    }
    String::new()
}

/// Body-based routing filter that analyzes request bodies and sets routing headers.
/// 
/// MEMORY CONSIDERATIONS:
/// - Buffers complete request bodies in memory during analysis
/// - Memory usage scales with request body size
/// - Consider implementing body size limits for production use
/// 
/// LATENCY CONSIDERATIONS:
/// - Pauses request processing until complete body is available
/// - JSON parsing adds computational overhead
/// - Route cache clearing forces re-evaluation (small cost)
pub struct Filter {
    // Store the session lookup response while processing
    pending_session_lookup: Option<SessionLookupResponse>,
    // Store the routing decision while waiting for session lookup
    pending_route_decision: Option<String>,
    // Store the stripped tool name for tools/call
    stripped_tool_name: Option<String>,
    // Store the current request body for modification
    current_request_body: Option<String>,
}

impl Filter {
    pub fn new() -> Self {
        Filter {
            pending_session_lookup: None,
            pending_route_decision: None,
            stripped_tool_name: None,
            current_request_body: None,
        }
    }

    // Helper method to handle fallback session creation
    fn handle_fallback_session<EHF: EnvoyHttpFilter>(&mut self, envoy_filter: &mut EHF, route_decision: &str) {
        let headers = envoy_filter.get_request_headers();
        let gateway_session = find_header_value(&headers, "mcp-session-id");
        let backend_session = format!("{}-session-{}", route_decision, gateway_session);
        envoy_filter.set_request_header("mcp-session-id", backend_session.as_bytes());
        envoy_filter.set_request_header("x-mcp-server", route_decision.as_bytes());
        
        envoy_filter.clear_route_cache();
        envoy_filter.continue_decoding();
        
        // Clear the pending state
        self.pending_route_decision = None;
        self.stripped_tool_name = None;
    }

    // Extract and store request body data for later modification
    fn extract_request_body<EHF: EnvoyHttpFilter>(&mut self, envoy_filter: &mut EHF) -> Option<String> {
        if let Some(body_buffers) = envoy_filter.get_request_body() {
            let mut body_data = Vec::new();
            for buffer in body_buffers {
                body_data.extend_from_slice(buffer.as_slice());
            }
            
            if let Ok(body_str) = std::str::from_utf8(&body_data) {
                self.current_request_body = Some(body_str.to_string());
                return Some(body_str.to_string());
            }
        }
        None
    }
}

impl<EHF: EnvoyHttpFilter> HttpFilter<EHF> for Filter {
    fn on_request_headers(
        &mut self,
        _envoy_filter: &mut EHF,
        end_of_stream: bool,
    ) -> abi::envoy_dynamic_module_type_on_http_filter_request_headers_status {
        
        // CRITICAL: For requests with bodies, we must pause header processing here.
        // If we don't pause, Envoy will make routing decisions before we can analyze
        // the body content and set our routing header. StopIteration prevents
        // upstream connection establishment until body analysis is complete.
        if !end_of_stream {
            return abi::envoy_dynamic_module_type_on_http_filter_request_headers_status::StopIteration;
        }
        
        // No body expected - continue with default routing
        abi::envoy_dynamic_module_type_on_http_filter_request_headers_status::Continue
    }

    fn on_request_body(&mut self, envoy_filter: &mut EHF, end_of_stream: bool) -> abi::envoy_dynamic_module_type_on_http_filter_request_body_status {
        eprintln!("[MCP_FILTER] Body received (end_of_stream={})", end_of_stream);
        
        if !end_of_stream {
            return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::StopIterationAndBuffer;
        }

        // Extract the request body
        let body_str = match self.extract_request_body(envoy_filter) {
            Some(body) => body,
            None => {
                eprintln!("[MCP_FILTER] Failed to extract request body");
                return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue;
            }
        };

        eprintln!("[MCP_FILTER] Body content: {}", body_str);

        // Parse JSON to extract method and params
        let parsed: serde_json::Value = match serde_json::from_str(&body_str) {
            Ok(v) => v,
            Err(e) => {
                eprintln!("[MCP_FILTER] Failed to parse JSON: {}", e);
                return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue;
            }
        };

        let method = parsed.get("method")
            .and_then(|m| m.as_str())
            .unwrap_or("");

        eprintln!("[MCP_FILTER] Extracted method: {}", method);

        // Only process tools/call - let initialize and tools/list go to gateway
        if method != "tools/call" {
            eprintln!("[MCP_FILTER] Method is not tools/call, continuing to gateway");
            return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue;
        }

        // Extract tool name from params
        let tool_name = parsed.get("params")
            .and_then(|p| p.get("name"))
            .and_then(|n| n.as_str())
            .unwrap_or("");

        eprintln!("[MCP_FILTER] Tool name: {}", tool_name);

        // Determine routing based on tool prefix
        let (route_to, stripped_tool_name) = if tool_name.starts_with("server1-") {
            ("server1", &tool_name[8..]) // Strip "server1-" prefix
        } else if tool_name.starts_with("server2-") {
            ("server2", &tool_name[8..]) // Strip "server2-" prefix  
        } else {
            eprintln!("[MCP_FILTER] Tool name doesn't start with server1- or server2-, continuing to gateway");
            return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue;
        };

        eprintln!("[MCP_FILTER] Routing to: {}, stripped tool name: {}", route_to, stripped_tool_name);

        // IMMEDIATELY modify the request body with stripped tool name
        if let Ok(mut json_value) = serde_json::from_str::<serde_json::Value>(&body_str) {
            if let Some(params) = json_value.get_mut("params") {
                if let Some(params_obj) = params.as_object_mut() {
                    params_obj.insert("name".to_string(), serde_json::Value::String(stripped_tool_name.to_string()));
                    eprintln!("[MCP_FILTER] Updated tool name from server-prefixed to: {}", stripped_tool_name);

                    if let Ok(modified_body) = serde_json::to_string(&json_value) {
                        let new_body_bytes = modified_body.as_bytes();
                        
                        // Replace the entire request body using Envoy API
                        if let Some(body_buffers) = envoy_filter.get_request_body() {
                            let current_body_size: usize = body_buffers.iter().map(|b| b.as_slice().len()).sum();
                            
                            if envoy_filter.drain_request_body(current_body_size) {
                                if envoy_filter.append_request_body(new_body_bytes) {
                                    // Update content-length header
                                    let new_length = new_body_bytes.len().to_string();
                                    envoy_filter.set_request_header("content-length", new_length.as_bytes());
                                    
                                    eprintln!("[MCP_FILTER] ✅ Successfully replaced request body with stripped tool name: {}", stripped_tool_name);
                                } else {
                                    eprintln!("[MCP_FILTER] ❌ Failed to append new request body");
                                }
                            } else {
                                eprintln!("[MCP_FILTER] ❌ Failed to drain request body");
                            }
                        }
                        
                        // Store the modified body for later use
                        self.current_request_body = Some(modified_body);
                    }
                }
            }
        }

        // Get gateway session from headers
        let headers = envoy_filter.get_request_headers();
        let gateway_session = find_header_value(&headers, "mcp-session-id");

        if gateway_session.is_empty() {
            eprintln!("[MCP_FILTER] No mcp-session-id header found");
            return abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue;
        }

        eprintln!("[MCP_FILTER] Gateway session: {}", gateway_session);

        // Store routing decision and stripped tool name for later use
        self.pending_route_decision = Some(route_to.to_string());
        self.stripped_tool_name = Some(stripped_tool_name.to_string());

        // Initiate HTTP callout to gateway for session lookup
        eprintln!("[MCP_FILTER] Making HTTP callout to gateway for session: {}", gateway_session);

        let headers = vec![
            (":method", b"GET".as_slice()),
            (":path", b"/session-lookup".as_slice()),
            (":authority", b"gateway:8080".as_slice()),
            ("content-length", b"0".as_slice()),
            ("x-gateway-session-id", gateway_session.as_bytes()),
        ];

        let result = envoy_filter.send_http_callout(
            1234,
            "gateway_cluster",
            headers,
            Some(b""),
            5000
        );

        match result {
            abi::envoy_dynamic_module_type_http_callout_init_result::Success => {
                eprintln!("[MCP_FILTER] HTTP callout initiated successfully");
                eprintln!("[MCP_FILTER] HTTP callout initiated for {} session lookup", route_to);
                abi::envoy_dynamic_module_type_on_http_filter_request_body_status::StopIterationAndBuffer
            }
            _ => {
                eprintln!("[MCP_FILTER] Failed to initiate HTTP callout");
                eprintln!("[MCP_FILTER] HTTP callout failed, using placeholder");
                
                // Fallback to placeholder session
                let backend_session = format!("{}-session-{}", route_to, gateway_session);
                eprintln!("[MCP_FILTER] Mapping to {} session: {}", route_to, backend_session);
                
                // Set headers and continue
                envoy_filter.set_request_header("mcp-session-id", backend_session.as_bytes());
                envoy_filter.set_request_header("x-mcp-server", route_to.as_bytes());
                envoy_filter.clear_route_cache();
                
                eprintln!("[MCP_FILTER] Routing decision: {}", route_to);
                abi::envoy_dynamic_module_type_on_http_filter_request_body_status::Continue
            }
        }
    }

    fn on_response_headers(&mut self, envoy_filter: &mut EHF, end_of_stream: bool) -> abi::envoy_dynamic_module_type_on_http_filter_response_headers_status {
        eprintln!("[MCP_FILTER] Response headers received (end_of_stream={})", end_of_stream);
        
        // Check if we have a backend session ID that needs to be mapped back
        let headers = envoy_filter.get_response_headers();
        let backend_session_id = find_header_value(&headers, "mcp-session-id");
        
        if !backend_session_id.is_empty() {
            eprintln!("[MCP_FILTER] Response backend session: {}", backend_session_id);
            
            // Check if this is a server1 or server2 session that needs mapping back to gateway session
            if backend_session_id.starts_with("server1-session-") || backend_session_id.starts_with("server2-session-") {
                // Extract the original gateway session ID by removing the prefix
                let gateway_session = if backend_session_id.starts_with("server1-session-") {
                    &backend_session_id[16..] // Remove "server1-session-" prefix
                } else {
                    &backend_session_id[16..] // Remove "server2-session-" prefix  
                };
                
                eprintln!("[MCP_FILTER] Mapping backend session back to gateway session: {}", gateway_session);
                envoy_filter.set_response_header("mcp-session-id", gateway_session.as_bytes());
            }
        }
        
        abi::envoy_dynamic_module_type_on_http_filter_response_headers_status::Continue
    }

    fn on_http_callout_done(
        &mut self,
        envoy_filter: &mut EHF,
        callout_id: u32,
        result: abi::envoy_dynamic_module_type_http_callout_result,
        _headers: Option<&[(EnvoyBuffer, EnvoyBuffer)]>,
        body: Option<&[EnvoyBuffer]>,
    ) {
        eprintln!("[MCP_FILTER] HTTP callout {} completed with result: {:?}", callout_id, result);
        
        // Clone route_decision to avoid borrowing issues
        let route_decision = self.pending_route_decision.clone();
        
        if let Some(route_decision_str) = route_decision {
            match result {
                abi::envoy_dynamic_module_type_http_callout_result::Success => {
                    if let Some(body_buffers) = body {
                        let mut response_data = Vec::new();
                        for buffer in body_buffers {
                            response_data.extend_from_slice(buffer.as_slice());
                        }
                        
                        let response_str = match std::str::from_utf8(&response_data) {
                            Ok(s) => s,
                            Err(_) => {
                                eprintln!("[MCP_FILTER] Failed to parse HTTP callout response as UTF-8");
                                self.handle_fallback_session(envoy_filter, &route_decision_str);
                                return;
                            }
                        };

                        eprintln!("[MCP_FILTER] Session lookup successful for gateway session");
                        eprintln!("[MCP_FILTER] Response body: {}", response_str);

                        // Parse the JSON response
                        let parsed: SessionLookupResponse = match serde_json::from_str(response_str) {
                            Ok(resp) => resp,
                            Err(e) => {
                                eprintln!("[MCP_FILTER] Failed to parse session lookup response: {}", e);
                                self.handle_fallback_session(envoy_filter, &route_decision_str);
                                return;
                            }
                        };

                        if !parsed.found {
                            eprintln!("[MCP_FILTER] Session mapping not found, using fallback");
                            self.handle_fallback_session(envoy_filter, &route_decision_str);
                            return;
                        }

                        // Use the correct session ID based on routing decision
                        let backend_session = if route_decision_str == "server1" {
                            parsed.server1_session_id
                        } else {
                            parsed.server2_session_id
                        };

                        eprintln!("[MCP_FILTER] Using gateway-provided session: {}", backend_session);

                        // Set the correct session header
                        envoy_filter.set_request_header("mcp-session-id", backend_session.as_bytes());
                        
                        // Set routing header
                        envoy_filter.set_request_header("x-mcp-server", route_decision_str.as_bytes());
                        eprintln!("[MCP_FILTER] Setting routing header: {}", route_decision_str);

                        // Clear route cache and continue
                        envoy_filter.clear_route_cache();
                        eprintln!("[MCP_FILTER] Resuming request processing after session lookup");
                        envoy_filter.continue_decoding();

                        // Clear the pending state
                        self.pending_route_decision = None;
                        self.stripped_tool_name = None;
                    } else {
                        eprintln!("[MCP_FILTER] No response body, using fallback");
                        self.handle_fallback_session(envoy_filter, &route_decision_str);
                    }
                }
                _ => {
                    eprintln!("[MCP_FILTER] HTTP callout failed, using fallback session");
                    self.handle_fallback_session(envoy_filter, &route_decision_str);
                }
            }
        }
    }
}
