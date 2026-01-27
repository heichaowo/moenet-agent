# Agent API Reference

## Overview

The agent exposes a minimal HTTP API for health checks and manual operations.

## Endpoints

### GET /status

Returns agent status and version information.

**Response:**

```json
{
  "status": "ok",
  "version": "1.2.0",
  "commit": "abc1234",
  "uptime": 3600,
  "node": {
    "id": 1,
    "name": "jp-edge",
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

**Response:**

```json
{
  "status": "ok",
  "synced": 15,
  "added": 2,
  "removed": 1,
  "updated": 0
}
```

### GET /health

Simple health check endpoint.

**Response:**

```text
OK
```

## Control Plane API

The agent communicates with the Control Plane using these endpoints:

### GET /agent/:router/sessions

Fetch all BGP sessions for this router.

**Headers:**

```http
Authorization: Bearer <token>
```

**Response:**

```json
{
  "sessions": [
    {
      "uuid": "abc-123",
      "asn": 4242421080,
      "status": 1,
      "ipv6": "fe80::1",
      "credential": {
        "publicKey": "..."
      }
    }
  ]
}
```

### POST /agent/:router/heartbeat

Report agent health and system metrics.

**Body:**

```json
{
  "version": "1.2.0",
  "uptime": 3600,
  "meshPublicKey": "..."
}
```

### POST /agent/:router/report

Report session metrics and statistics.

**Body:**

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

**Body:**

```json
{
  "uuid": "abc-123",
  "status": 1,
  "error": null
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
      "name": "hk-edge",
      "endpoint": "hk.moenet.work:23456",
      "publicKey": "..."
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
    "name": "jp-edge",
    "region": "ap-northeast",
    "asn": 4242420998,
    "loopbackIpv4": "172.23.105.177",
    "loopbackIpv6": "fd48:4242:420::1"
  }
}
```

## Error Handling

All endpoints return standard HTTP status codes:

| Code | Description         |
|------|---------------------|
| 200  | Success             |
| 400  | Bad request         |
| 401  | Unauthorized        |
| 404  | Not found           |
| 500  | Internal error      |
| 503  | Service unavailable |

Error response format:

```json
{
  "error": "error message",
  "code": "ERROR_CODE"
}
```
