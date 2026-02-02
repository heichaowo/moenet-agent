# MoeNet Agent

[![Go Version](https://img.shields.io/badge/Go-1.25%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A Go-based daemon for automated BGP peering on [DN42](https://dn42.dev). Manages WireGuard tunnels, BIRD routing configuration, and real-time metrics—all orchestrated by a central Control Plane.

## Table of Contents

- [Features](#features)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Architecture](#architecture)
- [Background Tasks](#background-tasks)
- [API Reference](#api-reference)
- [BGP Communities](#bgp-communities)
- [Development](#development)
- [Documentation](#documentation)
- [License](#license)

## Features

- **Automated BGP Session Management** - Complete lifecycle from creation to teardown
- **BIRD 3.x Integration** - Connection pool with template-based config generation
- **WireGuard Management** - Direct kernel interface control (no wg-quick)
- **P2P Mesh IGP** - WireGuard-based underlay with Babel for internal routing
- **Cold Potato Routing** - Keep traffic inside the backbone via Large Communities
- **Real-time Metrics** - RTT measurement, route statistics, traffic monitoring
- **Auto-update** - GitHub release-based self-update (optional)
- **Graceful Shutdown** - Context-based cancellation with 30s timeout

## Quick Start

### Bootstrap Mode (Recommended)

The easiest way to deploy is via bootstrap script from the Control Plane:

```bash
# 1. Generate bootstrap script via Telegram Bot
#    Use /addnode and /bootstrap commands

# 2. Run the generated script on your server
curl -fsSL "https://api.moenet.work/bootstrap/YOUR_TOKEN" | bash

# Agent starts automatically and connects to Control Plane
```

### Manual Installation

```bash
# Download binary
curl -L -o moenet-agent \
  https://github.com/moenet/moenet-agent/releases/latest/download/moenet-agent-linux-amd64
chmod +x moenet-agent

# Create minimal config
cat > config.json << 'EOF'
{
  "bootstrap": {
    "apiUrl": "https://api.moenet.work",
    "nodeName": "your-node-name",
    "token": "your-agent-token"
  },
  "server": { "listen": ":24368" }
}
EOF

# Run
./moenet-agent -c config.json
```

### Systemd Service

```bash
# Copy service file
sudo cp moenet-agent.service /etc/systemd/system/

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now moenet-agent

# Check status
sudo systemctl status moenet-agent
sudo journalctl -u moenet-agent -f
```

## Configuration

### Bootstrap Mode

Agent fetches configuration from Control Plane at startup:

```json
{
  "bootstrap": {
    "apiUrl": "https://api.moenet.work",
    "nodeName": "jp1",
    "token": "your-agent-token"
  },
  "server": { "listen": ":24368" }
}
```

This automatically retrieves: `nodeId`, `region`, `loopback IPs`, `ASN`, and other settings.

### Full Configuration

For complete customization, see [configs/config.example.json](configs/config.example.json).

| Section | Key | Description |
|---------|-----|-------------|
| `node.name` | string | Node hostname (e.g., `jp1`) |
| `node.id` | int | Unique node ID (1-62) |
| `controlPlane.url` | string | Control Plane API URL |
| `controlPlane.token` | string | Agent authentication token |
| `bird.controlSocket` | string | BIRD control socket path |
| `bird.peerConfDir` | string | Directory for peer configs |
| `autoUpdate.enabled` | bool | Enable auto-update from GitHub |

### Environment Variables

| Variable | Config Path | Description |
|----------|-------------|-------------|
| `MOENET_NODE_NAME` | `node.name` | Node name |
| `MOENET_CP_URL` | `controlPlane.url` | Control Plane URL |
| `MOENET_CP_TOKEN` | `controlPlane.token` | Agent token |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         moenet-agent                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │   Task       │  │  HTTP Client │  │  HTTP Server │           │
│  │  Scheduler   │  │  (CP comms)  │  │  (status)    │           │
│  └──────┬───────┘  └──────┬───────┘  └──────────────┘           │
│         │                 │                                     │
│  ┌──────▼─────────────────▼──────┐                              │
│  │         Core Engine           │                              │
│  │   • Session Sync              │                              │
│  │   • Config Rendering          │                              │
│  │   • iBGP Mesh Sync            │                              │
│  └──────┬─────────────────┬──────┘                              │
│         │                 │                                     │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────────────┐           │
│  │ BIRD Manager │  │  WG Manager  │  │   Firewall   │           │
│  │  (birdc)     │  │  (wg/ip)     │  │   (nftables) │           │
│  └──────────────┘  └──────────────┘  └──────────────┘           │
└─────────────────────────────────────────────────────────────────┘
         │                           │                    │
         ▼                           ▼                    ▼
    BIRD 3.x                   WireGuard              nftables
```

### Session Lifecycle

| Status | Code | Description |
|--------|------|-------------|
| PENDING_REVIEW | 3 | Awaiting admin approval |
| QUEUED_FOR_SETUP | 4 | Approved, agent will configure |
| ACTIVE | 1 | Running normally |
| ERROR | 2 | Configuration or connectivity issue |
| QUEUED_FOR_DELETE | 5 | Marked for removal |

## Background Tasks

| Task | Interval | Purpose |
|------|----------|---------|
| `heartbeat` | 30s | Report health, version, system metrics |
| `sessionSync` | 60s | Sync BGP sessions, configure WG+BIRD |
| `birdConfigSync` | 300s | Sync BIRD filters and communities |
| `metricCollector` | 60s | Collect BGP stats, report to CP |
| `rttMeasurement` | 300s | Measure RTT to peers |
| `meshSync` | 120s | Sync P2P WireGuard IGP mesh |
| `ibgpSync` | 120s | Sync iBGP peer configurations |

## API Reference

### Agent Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Agent status and version |
| `/sync` | GET | Trigger manual session sync |
| `/metrics` | GET | Prometheus metrics |
| `/maintenance` | GET | Maintenance mode status |
| `/maintenance/start` | POST | Enable maintenance mode |
| `/maintenance/stop` | POST | Disable maintenance mode |
| `/restart` | POST | Restart specific WG interface |

### Control Plane Communication

The agent polls these Control Plane endpoints:

| Endpoint | Interval | Purpose |
|----------|----------|---------|
| `GET /agent/:router/sessions` | 60s | Fetch BGP sessions |
| `GET /agent/:router/bird-config` | 300s | Fetch BIRD config |
| `GET /agent/:router/mesh` | 120s | Fetch mesh peers |
| `POST /agent/:router/heartbeat` | 30s | Report health |
| `POST /agent/:router/modify` | On change | Update session status |

## BGP Communities

### Self-Originated Routes Only

The agent tags **only self-originated routes** (static/device) with DN42 communities:

| Community | Description |
|-----------|-------------|
| `(64511, 1-9)` | Latency tier |
| `(64511, 21-25)` | Bandwidth tier |
| `(64511, 31-34)` | Encryption type |
| `(64511, 41-53)` | Region code |

> **Important**: BGP-learned routes pass through unchanged to preserve upstream communities.

### Cold Potato Routing

MoeNet Large Communities for internal routing optimization:

| Type | Format | Purpose |
|------|--------|---------|
| Origin Node | `(4242420998, 3, nodeId)` | Ingress node |
| Bandwidth | `(4242420998, 5, mbps)` | Link capacity |

## Development

### Build

```bash
# Clone
git clone https://github.com/moenet/moenet-agent.git
cd moenet-agent

# Build
go build -o moenet-agent ./cmd/moenet-agent

# Build with version info
go build -ldflags="-X main.Version=1.0.0 -X main.Commit=$(git rev-parse --short HEAD)" \
  -o moenet-agent ./cmd/moenet-agent
```

### Test

```bash
go test ./...
```

### Lint

```bash
golangci-lint run
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - Internal design and components
- [API Reference](docs/API.md) - Detailed endpoint documentation
- [Configuration](docs/CONFIGURATION.md) - All configuration options
- [BIRD Config](docs/BIRD_CONFIG.md) - BIRD template rendering
- [Troubleshooting](docs/TROUBLESHOOTING.md) - Common issues and solutions

## License

MIT License - see [LICENSE](LICENSE)
