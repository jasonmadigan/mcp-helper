# MCP Gateway PoC

MCP Gateway with Rust Envoy dynamic module.

## Run

```bash
docker-compose up --build
```

Available at: http://localhost:8080

## Architecture

```
Client -> Envoy (Rust Dynamic Module) -> Gateway -> Server1/Server2
```