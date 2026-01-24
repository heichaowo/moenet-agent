# MoeNet Agent

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue.svg)](https://golang.org)

A Go agent for automated BGP peering session management on MoeNet DN42 infrastructure nodes. Communicates with the MoeNet Control Plane to orchestrate BGP session lifecycle, WireGuard tunnel configuration, and real-time performance metrics.

## Features

- **Session Lifecycle Management**: Automated setup, monitoring, and teardown of BGP peering sessions
- **BIRD Integration**: Connection pool for efficient BIRD control socket communication
- **WireGuard Management**: Direct interface management without wg-quick
- **P2P Mesh IGP**: WireGuard-based IGP underlay with Babel
- **Real-time Metrics**: RTT measurement, route statistics, traffic monitoring
- **Graceful Shutdown**: Context-based cancellation with proper resource cleanup

## Architecture

```mermaid
graph TB
    subgraph "Control Plane"
        CP[FastAPI Server]
        DB[(PostgreSQL)]
    end
    
    subgraph "Agent Node"
        Agent[Go Agent]
        BIRD[BIRD 3.x]
        WG[WireGuard]
    end
    
    Agent -->|GET /sessions| CP
    Agent -->|POST /heartbeat| CP
    Agent -->|POST /modify| CP
    Agent -->|POST /report| CP
    
    Agent -->|birdc configure| BIRD
    Agent -->|wg set| WG
```

## Session Lifecycle

```mermaid
stateDiagram-v2
    [*] --> PENDING: User creates peer
    PENDING --> QUEUED_FOR_SETUP: Admin approves
    QUEUED_FOR_SETUP --> ENABLED: Agent configures
    ENABLED --> PROBLEM: Config/connectivity issue
    PROBLEM --> ENABLED: Issue resolved
    ENABLED --> QUEUED_FOR_DELETE: User/admin deletes
    QUEUED_FOR_DELETE --> DELETED: Agent removes config
    DELETED --> [*]
```

| Status | Description |
|--------|-------------|
| `PENDING` | Awaiting admin approval |
| `QUEUED_FOR_SETUP` | Approved, waiting for agent to configure |
| `ENABLED` | Active and configured |
| `PROBLEM` | Configuration or connectivity issue |
| `QUEUED_FOR_DELETE` | Marked for removal |
| `DELETED` | Removed from system |
| `TEARDOWN` | Emergency teardown (invalid config) |

## Background Tasks

| Task | Interval | Purpose |
|------|----------|---------|
| `heartbeatTask` | 30s | Report node health, version, system metrics |
| `sessionSyncTask` | 60s | Sync BGP sessions from CP, apply changes |
| `metricTask` | 60s | Collect BGP stats, report to CP |
| `rttTask` | 300s | Measure RTT to peers, update latency tier |
| `meshSyncTask` | 120s | Sync P2P WireGuard IGP mesh |
| `ibgpSyncTask` | 120s | Sync iBGP peer configurations |

## Installation

### Binary Installation

```bash
# Download latest release
curl -L -o moenet-agent https://github.com/moenet/moenet-agent/releases/latest/download/moenet-agent-linux-amd64
chmod +x moenet-agent

# Create directories
mkdir -p /opt/moenet-agent/logs

# Copy configuration
cp config.example.json /opt/moenet-agent/config.json
# Edit config.json with your settings
```

### Building from Source

```bash
git clone https://github.com/moenet/moenet-agent.git
cd moenet-agent
go build -o moenet-agent ./cmd/moenet-agent
```

### Systemd Service

```bash
cp moenet-agent.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable moenet-agent
systemctl start moenet-agent
```

## Configuration

See [configs/config.example.json](configs/config.example.json) for a complete example.

| Section | Key | Description |
|---------|-----|-------------|
| `node.name` | string | Node hostname (e.g., `hk-edge`) |
| `node.id` | int | Unique node ID (1-62) |
| `controlPlane.url` | string | Control Plane API URL |
| `controlPlane.token` | string | Agent authentication token |
| `bird.controlSocket` | string | BIRD control socket path |
| `bird.peerConfDir` | string | Directory for peer configs |

## API Endpoints

The agent exposes a minimal HTTP API:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Agent status and version |
| `/sync` | GET | Trigger manual session sync |

## Development

```bash
# Run tests
go test ./...

# Build with version info
go build -ldflags="-X main.Version=1.0.0 -X main.Commit=$(git rev-parse --short HEAD)" ./cmd/moenet-agent
```

## License

GPL-3.0
