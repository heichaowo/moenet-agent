# MoeNet Agent Architecture

## Overview

The MoeNet Agent is a Go application that manages BGP peering sessions on DN42 nodes. It communicates with the Control Plane to synchronize configuration and report status.

## Components

```text
┌─────────────────────────────────────────────────────────────────┐
│                         moenet-agent                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │   Scheduler  │  │  HTTP Client │  │  HTTP Server │           │
│  │  (cron jobs) │  │  (CP comms)  │  │  (status)    │           │
│  └──────┬───────┘  └──────┬───────┘  └──────────────┘           │
│         │                 │                                     │
│  ┌──────▼─────────────────▼──────┐                              │
│  │         Core Engine           │                              │
│  │   • Session Manager           │                              │
│  │   • Config Renderer           │                              │
│  │   • State Tracker             │                              │
│  └──────┬─────────────────┬──────┘                              │
│         │                 │                                     │
│  ┌──────▼───────┐  ┌──────▼───────┐                             │
│  │ BIRD Manager │  │  WG Manager  │                             │
│  │  (birdc)     │  │  (wg/ip)     │                             │
│  └──────────────┘  └──────────────┘                             │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         │                           │
         ▼                           ▼
┌─────────────────┐         ┌─────────────────┐
│   BIRD 3.x      │         │   WireGuard     │
│   (BGP daemon)  │         │   (tunnels)     │
└─────────────────┘         └─────────────────┘
```

## Directory Structure

```text
moenet-agent/
├── cmd/
│   └── moenet-agent/     # Entry point
├── internal/
│   ├── agent/            # Core agent logic
│   ├── bird/             # BIRD control socket
│   ├── config/           # Configuration loading
│   ├── wireguard/        # WG interface management
│   ├── mesh/             # P2P mesh IGP
│   ├── httpclient/       # HTTP client with retry
│   ├── circuitbreaker/   # Circuit breaker pattern
│   └── scheduler/        # Background task scheduler
├── configs/              # Example configurations
├── docs/                 # Documentation
└── templates/            # BIRD/WG config templates
```

## Background Tasks

| Task | Interval | Purpose |
|------|----------|---------|
| `heartbeat` | 30s | Report health to Control Plane |
| `sessionSync` | 60s | Sync BGP sessions from CP |
| `metrics` | 60s | Collect and report BGP stats |
| `rtt` | 300s | Measure RTT to peers |
| `meshSync` | 120s | Sync P2P WireGuard mesh |
| `ibgpSync` | 120s | Sync iBGP configurations |

## Session Lifecycle

```text
┌─────────────┐
│   PENDING   │  ← User creates peer
└──────┬──────┘
       │ Admin approves
       ▼
┌─────────────┐
│   QUEUED    │  ← Agent will pick up
└──────┬──────┘
       │ Agent configures
       ▼
┌─────────────┐     ┌─────────────┐
│   ACTIVE    │ ←→  │   ERROR     │
└──────┬──────┘     └─────────────┘
       │ User deletes
       ▼
┌─────────────┐
│   DELETING  │  ← Agent removes config
└──────┬──────┘
       │
       ▼
    (removed)
```

## Resilience Features

### HTTP Retry with Backoff

```go
client := httpclient.New(nil, httpclient.RetryConfig{
    MaxRetries:   3,
    InitialDelay: time.Second,
    MaxDelay:     30 * time.Second,
    Multiplier:   2.0,
})
```

### Circuit Breaker

Prevents cascading failures when Control Plane is unavailable:

| State | Behavior |
|-------|----------|
| Closed | Normal operation |
| Open | Fail fast, skip CP calls |
| Half-Open | Test with single request |

### Graceful Shutdown

- Context-based cancellation
- Complete in-progress operations
- Save state before exit

## Configuration Modes

### Bootstrap Mode (Recommended)

Agent fetches configuration from Control Plane at startup:

```json
{
  "bootstrap": {
    "controlPlaneUrl": "https://api.moenet.work",
    "nodeName": "jp-edge",
    "token": "..."
  }
}
```

### Full Configuration

All settings in local config file. See [CONFIGURATION.md](./CONFIGURATION.md).

## State Management

### Runtime State

- In-memory session cache
- Updated on each sync cycle

### Persistent State

- `last_state.json` - Last known good configuration
- Used for disaster recovery

## Logging

Structured JSON logging:

```json
{
  "time": "2024-01-01T00:00:00Z",
  "level": "info",
  "msg": "Session configured",
  "session": "abc-123",
  "asn": 4242421080
}
```

Log levels: `debug`, `info`, `warn`, `error`
