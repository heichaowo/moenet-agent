---
title: MoeNet Agent Architecture
description: Internal architecture of the Go-based DN42 node agent
---

# MoeNet Agent Architecture

## Overview

The MoeNet Agent is a Go application that manages BGP peering sessions on DN42 nodes. It communicates with the Control Plane to synchronize configuration, manage WireGuard tunnels, and report status.

> [!IMPORTANT]
> **BIRD 3.2.0 Syntax Required** - All BIRD configurations MUST use BIRD 3.2.0 syntax.
> **No wg-quick** - Direct WireGuard management only, never use wg-quick.

## Architecture Diagram

<!-- Diagram: MoeNet Agent internal architecture showing task scheduler, HTTP clients, BIRD and WireGuard integration -->

```text
┌─────────────────────────────────────────────────────────────────┐
│                         moenet-agent                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
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
│  │   • Metric Collection         │                              │
│  └──────┬─────────────────┬──────┘                              │
│         │                 │                                     │
│  ┌──────▼───────┐  ┌──────▼───────┐  ┌──────────────┐           │
│  │ BIRD Manager │  │  WG Manager  │  │   Firewall   │           │
│  │  (birdc)     │  │  (wg/ip)     │  │   (nftables) │           │
│  └──────────────┘  └──────────────┘  └──────────────┘           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         │                           │                    │
         ▼                           ▼                    ▼
┌─────────────────┐         ┌─────────────────┐  ┌─────────────────┐
│   BIRD 3.x      │         │   WireGuard     │  │    nftables     │
│   (BGP daemon)  │         │   (tunnels)     │  │   (firewall)    │
└─────────────────┘         └─────────────────┘  └─────────────────┘
```

## Directory Structure

```text
moenet-agent/
├── cmd/
│   └── moenet-agent/        # Entry point (main.go)
├── internal/
│   ├── api/                 # HTTP API server (/status, /sync, /metrics)
│   ├── bird/                # BIRD connection pool & config generator
│   ├── circuitbreaker/      # Circuit breaker for CP resilience
│   ├── config/              # Configuration loading with bootstrap
│   ├── firewall/            # nftables rule management
│   ├── httpclient/          # HTTP client with retry & backoff
│   ├── loopback/            # dummy0 interface management
│   ├── maintenance/         # Maintenance mode state
│   ├── mesh/                # P2P WireGuard IGP mesh
│   ├── metrics/             # Prometheus metrics collection
│   ├── task/                # Background task system
│   │   ├── heartbeat.go     # Health reporting
│   │   ├── session_sync.go  # eBGP session sync
│   │   ├── bird_config.go   # BIRD policy sync
│   │   ├── ibgp_sync.go     # iBGP mesh sync
│   │   ├── mesh_sync.go     # WireGuard mesh sync
│   │   ├── metric.go        # BGP metrics
│   │   └── rtt.go           # RTT measurement
│   ├── updater/             # Auto-update from GitHub
│   └── wireguard/           # WireGuard interface management
├── configs/                 # Example configurations
├── docs/                    # Documentation
└── templates/               # BIRD config templates
```

## Background Tasks

| Task | Interval | Purpose |
|------|----------|---------|
| `heartbeat` | 30s | Report health, version, system metrics to CP |
| `sessionSync` | 60s | Sync eBGP sessions from CP, configure WG+BIRD |
| `birdConfigSync` | 300s | Sync BIRD filters, communities from CP |
| `metricCollector` | 60s | Collect BGP stats, report to CP |
| `rttMeasurement` | 300s | Measure RTT to peers, update latency tier |
| `meshSync` | 120s | Sync P2P WireGuard IGP mesh peers |
| `ibgpSync` | 120s | Sync iBGP peer configurations |
| `updater` | config | Auto-update agent binary (if enabled) |

### Task Pattern

All background tasks follow the same pattern:

```go
func (t *SomeTask) Run(ctx context.Context, wg *sync.WaitGroup) {
    defer wg.Done()
    ticker := time.NewTicker(t.interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            log.Println("[Task] Shutting down")
            return
        case <-ticker.C:
            if err := t.execute(ctx); err != nil {
                log.Printf("[Task] Error: %v", err)
            }
        }
    }
}
```

## Session Lifecycle

```text
┌─────────────────┐
│ PENDING_REVIEW  │  ← User creates peer via Bot
│      (3)        │
└────────┬────────┘
         │ Admin approves
         ▼
┌─────────────────┐
│ QUEUED_FOR_SETUP│  ← Agent will pick up
│      (4)        │
└────────┬────────┘
         │ Agent configures WG + BIRD
         ▼
┌─────────────────┐     ┌─────────────────┐
│     ACTIVE      │ ←→  │     ERROR       │
│      (1)        │     │      (2)        │
└────────┬────────┘     └─────────────────┘
         │ User/Admin deletes
         ▼
┌─────────────────┐
│QUEUED_FOR_DELETE│  ← Agent removes config
│      (5)        │
└────────┬────────┘
         │
         ▼
      (removed)
```

| Status | Code | Description |
|--------|------|-------------|
| DISABLED | 0 | Session manually disabled |
| ACTIVE | 1 | Running normally |
| ERROR | 2 | Configuration or connectivity issue |
| PENDING_REVIEW | 3 | Awaiting admin approval |
| QUEUED_FOR_SETUP | 4 | Approved, agent will configure |
| QUEUED_FOR_DELETE | 5 | Marked for removal |
| SETUP_FAILED | 6 | Agent failed to configure |

## Resilience Features

### HTTP Retry with Backoff

All Control Plane requests use exponential backoff:

```go
client := httpclient.New(nil, httpclient.RetryConfig{
    MaxRetries:   3,
    InitialDelay: time.Second,
    MaxDelay:     30 * time.Second,
    Multiplier:   2.0,
})
```

Features:

- Configurable max retries and delays
- Jitter to prevent thundering herd
- Context-aware cancellation
- Automatic 5xx and 429 retry

### Circuit Breaker

Prevents cascading failures when Control Plane is unavailable:

| State | Behavior |
|-------|----------|
| Closed | Normal operation, all requests pass through |
| Open | Fail fast, skip CP calls for `OpenDuration` |
| Half-Open | Test with single request, success closes circuit |

```go
cb := circuitbreaker.New(circuitbreaker.Config{
    FailureThreshold: 5,
    SuccessThreshold: 3,
    OpenDuration:     30 * time.Second,
})
```

### Graceful Shutdown

- Context-based cancellation propagated to all tasks
- Complete in-progress operations within 30s timeout
- Close BIRD connection pool

## Configuration Modes

### Bootstrap Mode (Recommended)

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

The agent fetches `nodeId`, `region`, `loopback IPs`, and other settings from the database via `/agent/:router/config` endpoint.

### Full Configuration

All settings in local config file. See [CONFIGURATION.md](./CONFIGURATION.md).

## BIRD Integration

### Connection Pool

The agent maintains a pool of connections to the BIRD control socket:

```go
pool, err := bird.NewPool(
    "/run/bird/bird.ctl",  // Socket path
    5,                     // Initial pool size
    10,                    // Max pool size
)
```

### Config Generation

BIRD configurations are rendered from templates:

- `peers/*.conf` - Per-peer eBGP sessions
- `ibgp/*.conf` - iBGP mesh peers
- `moenet_communities.conf` - DN42 community definitions
- `filters.conf` - Import/export filters

## WireGuard Management

Direct kernel interface management (NO wg-quick):

```go
executor, err := wireguard.NewExecutor(
    "/etc/wireguard",           // Config directory
    "/etc/wireguard/private.key", // Private key path
)

// Add peer
executor.AddPeer(ctx, config)

// Remove peer
executor.RemovePeer(ctx, interfaceName)
```

## Loopback Interface

The agent manages the `dummy0` interface for DN42 loopback addresses:

```go
lbExecutor := loopback.NewExecutor(slog.Default())
lbExecutor.SetupLoopbackWithIPs("172.22.68.198/32", "fd28:cb8f:4c92::1/128")
```

## Firewall Management

Dynamic nftables rule management for peer ports:

```go
fwExecutor := firewall.NewExecutor(slog.Default())
fwExecutor.OpenPort(ctx, 51821)  // Open WireGuard port
fwExecutor.ClosePort(ctx, 51821) // Close port after peer removal
```

## Logging

Structured logging using `log/slog`:

```go
slog.Info("Session configured",
    "session", sessionID,
    "asn", 4242421080,
    "node", "jp1",
)
```

Output:

```json
{
  "time": "2026-01-01T00:00:00Z",
  "level": "INFO",
  "msg": "Session configured",
  "session": "abc-123",
  "asn": 4242421080,
  "node": "jp1"
}
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Agent status, version, node info |
| `/sync` | GET | Trigger manual session sync |
| `/metrics` | GET | Prometheus metrics |
| `/maintenance` | GET | Maintenance mode status |
| `/maintenance/start` | POST | Enable maintenance mode |
| `/maintenance/stop` | POST | Disable maintenance mode |
| `/restart` | POST | Restart specific WG interface |

## Related Documentation

- [API Reference](./API.md)
- [Configuration](./CONFIGURATION.md)
- [BIRD Config Rendering](./BIRD_CONFIG.md)
- [Troubleshooting](./TROUBLESHOOTING.md)
