# MCP Helper PoC

MCP Helper with Go [Envoy external processor](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter).

## Run

```bash
docker-compose up --build
```

A streamable HTTP MCP server will now be available at: http://localhost:8080/sse

## Test with MCP Inspector

```bash
# Start the inspector
DANGEROUSLY_OMIT_AUTH=true npx @modelcontextprotocol/inspector
```

1. Open http://127.0.0.1:6274/
2. Select **Streamable HTTP** transport
3. Enter URL: `http://localhost:8080/sse`
4. Click **Connect**
5. Click **List Tools**

You'll see federated tools from both backend MCP servers (server1 and server2), automatically prefixed to avoid conflicts:

![alt text](mcp-inspector.png)
