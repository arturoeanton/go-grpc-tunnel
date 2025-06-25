# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based reverse TCP tunnel built with gRPC that enables secure access to services behind NAT/firewalls. The system consists of two main components:

- **Server** (`cmd/server/main.go`): Exposes public gRPC and TCP endpoints, handles authentication and data forwarding
- **Agent/Client** (`cmd/client/main.go`): Connects to local services (e.g. Firebird database) and maintains gRPC connection to server

## Architecture

The tunnel implements a bidirectional gRPC stream using Protocol Buffers for control messages and data multiplexing:

- **Control Flow**: Agent initiates gRPC connection to server with token authentication
- **Data Flow**: External clients connect to server's TCP port → tunneled via gRPC to agent → forwarded to local service
- **Multiplexing**: Multiple TCP connections are handled concurrently using unique connection IDs

## Common Commands

### Running the Application
```bash
# Start server (requires certificates and environment variables)
chmod +x run_server.sh
./run_server.sh

# Start client/agent (requires matching token and server address)  
chmod +x run_client.sh
./run_client.sh
```

### Go Operations
```bash
# Build server
go build -o bin/server ./cmd/server

# Build client
go build -o bin/client ./cmd/client  

# Run server directly
go run ./cmd/server/main.go

# Run client directly
go run ./cmd/client/main.go

# Download dependencies
go mod tidy

# Generate protobuf (if proto files change)
protoc --go_out=. --go-grpc_out=. proto/tunnel.proto
```

### Certificate Generation
```bash
# Generate self-signed certificates for TLS
mkdir -p certs
cd certs
openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt \
  -sha256 -days 365 -nodes \
  -subj "/CN=localhost" \
  -addext "subjectAltName = IP:127.0.0.1,DNS:localhost"
cp server.crt ca.crt
cd ..
```

## Configuration

Both server and client are configured via environment variables set in their respective shell scripts:

### Server (`run_server.sh`)
- `TUNNEL_AUTH_TOKEN`: Authentication token for agent connections
- `TUNNEL_GRPC_PORT`: Port for gRPC connections from agents (default: `:50051`)
- `TUNNEL_EXTERNAL_PORT`: Port for external TCP clients (default: `:8080`) 
- `TUNNEL_SERVER_CERT` / `TUNNEL_SERVER_KEY`: TLS certificate paths
- `TUNNEL_INSECURE`: Set to "true" to disable TLS (not recommended)

### Client (`run_client.sh`)
- `TUNNEL_AUTH_TOKEN`: Must match server token
- `TUNNEL_SERVER_ADDR`: Server gRPC address (e.g. `127.0.0.1:50051`)
- `FIREBIRD_LOCAL_ADDR`: Local service address (e.g. `127.0.0.1:3050`)
- `TUNNEL_CA_CERT`: Path to CA certificate
- `DANGEROUS_SKIP_VERIFY`: Set to "true" only for debugging (unsafe)

## Key Implementation Details

### gRPC Protocol (`proto/tunnel.proto`)
- Service: `TunnelService` with `ConnectControl` bidirectional streaming method
- Message: `Frame` with type enum, connection ID, payload bytes, and metadata
- Frame types: `CONTROL_INIT`, `START_DATA_TUNNEL`, `TUNNEL_READY`, `DATA`, `CLOSE_TUNNEL`, `ERROR`

### Connection Management
- Server maintains map of active tunnels (`connection_id -> tunnelInfo`)
- Agent maintains map of local connections (`connection_id -> localTunnelInfo`)
- Both sides use context cancellation for cleanup and goroutine coordination

### Security Features
- TLS encryption for gRPC communication
- Token-based authentication via gRPC metadata
- Self-signed certificates with proper SAN configuration
- Optional certificate verification skipping (debug only)

## Development Notes

- The codebase uses standard Go project structure with `cmd/` for executables
- Dependencies managed with Go modules (`go.mod`)
- Protocol Buffers generate code in `proto/` directory
- Shell scripts handle environment setup and execution
- Extensive logging for debugging connection issues
- Proper cleanup of resources using defer and context cancellation