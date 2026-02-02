---
title: Agent API Reference
description: HTTP API endpoints for MoeNet Agent and Control Plane communication
---

# Agent API Reference

## Overview

The agent exposes a minimal HTTP API for health checks and manual operations. Default port: `24368`.

## Table of Contents

- [Local Endpoints](#local-endpoints)
- [Control Plane Endpoints](#control-plane-endpoints)
- [Error Handling](#error-handling)
- [Examples](#examples)

## Local Endpoints

These endpoints are exposed by the agent for local monitoring and operations.

### GET /status

Returns agent status, version, and session counts.

**Request:**

```bash
curl http://localhost:24368/status
```

**Response:**

```json
{
  "status": "ok",
  "version": "1.2.0",
  "commit": "abc1234",
  "uptime": 3600,
  "node": {
    "id": 1,
    "name": "jp1",
    "region": "ap-northeast"
  },
  "sessions": {
    "total": 15,
    "active": 12,
    "error": 1,
    "pending": 2
  }
}
```

### GET /sync

Triggers an immediate session sync from Control Plane.

**Request:**

```bash
curl http://localhost:24368/sync
```

**Response:**

```json
{
  "status": "sync_triggered"
}
```

### GET /metrics

Prometheus-format metrics for monitoring.

**Request:**

```bash
curl http://localhost:24368/metrics
```

**Response:**

```text
# HELP moenet_agent_sessions_total Total BGP sessions
# TYPE moenet_agent_sessions_total gauge
moenet_agent_sessions_total{status="active"} 12
moenet_agent_sessions_total{status="error"} 1
```

### GET /maintenance

Check maintenance mode status.

**Request:**

```bash
curl http://localhost:24368/maintenance
```

**Response:**

```json
{
  "enabled": false,
  "since": null
}
```

### POST /maintenance/start

Enable maintenance mode (drains BGP sessions).

**Request:**

```bash
curl -X POST http://localhost:24368/maintenance/start
```

**Response:**

```json
{
  "status": "ok",
  "message": "Maintenance mode enabled"
}
```

### POST /maintenance/stop

Disable maintenance mode and restore sessions.

**Request:**

```bash
curl -X POST http://localhost:24368/maintenance/stop
```

### POST /restart

Restart a specific WireGuard interface.

**Request:**

```bash
curl -X POST http://localhost:24368/restart \
  -H "Content-Type: application/json" \
  -d '{"interface": "wg_4242421080"}'
```

**Response:**

```json
{
  "status": "ok",
  "interface": "wg_4242421080"
}
```

---

## Control Plane Endpoints

The agent communicates with the Control Plane using these endpoints.

### Authentication

All requests require a Bearer token:

```bash
curl -H "Authorization: Bearer $AGENT_TOKEN" \
  https://api.moenet.work/agent/jp1/sessions
```

### GET /agent/:router/sessions

Fetch all BGP sessions for this router.

**Response:**

```json
{
  "sessions": [
    {
      "uuid": "abc-123",
      "asn": 4242421080,
      "status": 1,
      "ipv6": "fe80::1",
      "ipv6LinkLocal": "fe80::2",
      "mtu": 1420,
      "interface": "wg_4242421080",
      "endpoint": "example.com:51820",
      "credential": {
        "publicKey": "abc123..."
      }
    }
  ]
}
```

**Status Codes:**

| Status | Name | Description |
|--------|------|-------------|
| 0 | DISABLED | Manually disabled |
| 1 | ACTIVE | Running normally |
| 2 | ERROR | Configuration issue |
| 3 | PENDING_REVIEW | Awaiting approval |
| 4 | QUEUED_FOR_SETUP | Ready for agent |
| 5 | QUEUED_FOR_DELETE | Marked for removal |

### POST /agent/:router/heartbeat

Report agent health and system metrics.

**Request:**

```json
{
  "version": "1.2.0",
  "uptime": 3600,
  "meshPublicKey": "publickey..."
}
```

**Response:**

```json
{
  "status": "ok"
}
```

### POST /agent/:router/report

Report session metrics and statistics.

**Request:**

```json
{
  "sessions": [
    {
      "uuid": "abc-123",
      "rtt_ms": 25,
      "routes_imported": 150,
      "routes_exported": 10,
      "state": "established"
    }
  ]
}
```

### POST /agent/:router/modify

Update session status after configuration.

**Request:**

```json
{
  "uuid": "abc-123",
  "status": 1,
  "error": null
}
```

**Error Example:**

```json
{
  "uuid": "abc-123",
  "status": 2,
  "error": "BIRD configuration failed: syntax error"
}
```

### GET /agent/:router/mesh

Fetch mesh IGP peer list.

**Response:**

```json
{
  "peers": [
    {
      "nodeId": 2,
      "name": "hk1",
      "endpoint": "hk.moenet.work:23456",
      "publicKey": "publickey...",
      "loopbackIpv4": "172.23.105.178",
      "loopbackIpv6": "fd48:4242:420::2"
    }
  ]
}
```

### GET /agent/:router/config

Fetch full bootstrap configuration.

**Response:**

```json
{
  "node": {
    "id": 1,
    "name": "jp1",
    "region": "ap-northeast",
    "asn": 4242420998,
    "loopbackIpv4": "172.23.105.177",
    "loopbackIpv6": "fd48:4242:420::1"
  }
}
```

### GET /agent/:router/bird-config

Fetch BIRD routing policy and community configurations.

**Response:**

```json
{
  "configHash": "abc123",
  "node": {
    "id": 1,
    "name": "jp1",
    "type": "edge",
    "bandwidth": "1G",
    "regionCode": 101,
    "continentLc": "LC_ORIGIN_AS",
    "subregionLc": "LC_REGION_AS_E",
    "regionCommunity": "DN42_REGION_AS_E",
    "bandwidthCommunity": "DN42_BW_1G_PLUS"
  },
  "policy": {
    "dn42As": "4242420998",
    "dn42Ipv4Prefix": "172.23.105.176/28",
    "dn42Ipv6Prefix": "fd48:4da8:420::/48",
    "rpkiServers": [
      {
        "host": "rpki.burble.dn42",
        "port": 8283
      }
    ],
    "ebgpImportLimit": 10000,
    "ebgpExportLimit": 100,
    "asPathMaxLen": 10
  },
  "ibgpPeers": [...]
}
```

---

## Error Handling

### HTTP Status Codes

| Code | Description |
|------|-------------|
| 200 | Success |
| 400 | Bad request (invalid JSON, missing fields) |
| 401 | Unauthorized (invalid or missing token) |
| 404 | Not found (unknown router) |
| 429 | Rate limited |
| 500 | Internal server error |
| 503 | Service unavailable (maintenance mode) |

### Error Response Format

```json
{
  "error": "Detailed error message",
  "code": "ERROR_CODE"
}
```

### Common Error Codes

| Code | Description |
|------|-------------|
| `INVALID_TOKEN` | Agent token is invalid or expired |
| `ROUTER_NOT_FOUND` | Router name not registered |
| `SESSION_NOT_FOUND` | Session UUID doesn't exist |
| `RATE_LIMITED` | Too many requests |
| `MAINTENANCE_MODE` | Agent in maintenance mode |

---

## Examples

### Check All Sessions via CLI

```bash
#!/bin/bash
# Get all sessions and their status

curl -s http://localhost:24368/status | jq '.sessions'
```

### Manual Session Sync

```bash
# Trigger sync and wait for completion
curl -s http://localhost:24368/sync

# Check logs
journalctl -u moenet-agent | grep -i sync
```

### Debug Control Plane Connection

```bash
# Test connectivity to Control Plane
curl -v -H "Authorization: Bearer $TOKEN" \
  "https://api.moenet.work/agent/jp1/sessions"

# Check for circuit breaker state
curl -s http://localhost:24368/status | jq '.circuitBreaker'
```
