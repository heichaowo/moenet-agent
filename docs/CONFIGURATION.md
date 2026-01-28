# Agent Configuration Guide

## Overview

The MoeNet Agent can be configured in two ways:

1. **Bootstrap Mode** (Recommended) - Fetch configuration from Control Plane
2. **Full Configuration** - All settings in local config file

## Bootstrap Mode

Minimal configuration that fetches settings from Control Plane at startup:

```json
{
  "bootstrap": {
    "apiUrl": "https://api.moenet.work",
    "nodeName": "jp-edge",
    "token": "your-agent-token"
  },
  "server": {
    "listen": ":24368"
  }
}
```

### What Bootstrap Fetches

| Setting        | Description            |
|----------------|------------------------|
| `nodeId`       | Unique node ID (1-62)  |
| `region`       | Geographic region      |
| `loopbackIpv4` | DN42 loopback IPv4     |
| `loopbackIpv6` | DN42 loopback IPv6     |
| `asn`          | Local ASN              |
| `meshPeers`    | IGP mesh peer list     |
| `ibgpPeers`    | iBGP peer list         |

## Full Configuration

See [config.example.json](../configs/config.example.json) for complete example.

### Sections

#### node

```json
{
  "node": {
    "name": "jp-edge",
    "id": 1,
    "region": "ap-northeast",
    "asn": 4242420998,
    "loopbackIpv4": "172.23.105.177",
    "loopbackIpv6": "fd48:4242:420::1"
  }
}
```

#### controlPlane

```json
{
  "controlPlane": {
    "url": "https://api.moenet.work",
    "token": "your-agent-token",
    "maxRetries": 3,
    "retryInitialDelay": 1000
  }
}
```

#### bird

```json
{
  "bird": {
    "controlSocket": "/run/bird/bird.ctl",
    "peerConfDir": "/etc/bird/peers",
    "ibgpConfDir": "/etc/bird/ibgp"
  }
}
```

#### wireguard

```json
{
  "wireguard": {
    "interfacePrefix": "wg_",
    "listenPortBase": 24000,
    "privateKeyFile": "/etc/wireguard/private.key",
    "mtu": 1420
  }
}
```

#### mesh

```json
{
  "mesh": {
    "enabled": true,
    "interfacePrefix": "mesh_",
    "listenPort": 23456
  }
}
```

#### server

```json
{
  "server": {
    "listen": ":24368"
  }
}
```

## Environment Variables

These can override config file settings:

| Variable           | Config Path        | Description        |
|--------------------|--------------------|--------------------||
| `MOENET_NODE_NAME` | `node.name`        | Node name          |
| `MOENET_NODE_ID`   | `node.id`          | Node ID            |
| `MOENET_CP_URL`    | `controlPlane.url` | Control Plane URL  |
| `MOENET_CP_TOKEN`  | `controlPlane.token` | Agent token      |

## Configuration Locations

The agent searches for config in order:

1. Command line: `./moenet-agent -config /path/to/config.json`
2. Current directory: `./config.json`
3. System: `/etc/moenet-agent/config.json`
4. User: `~/.config/moenet-agent/config.json`

## Validation

The agent validates configuration at startup:

```bash
./moenet-agent -validate
```

Required fields:

- `node.name` or `bootstrap.nodeName`
- `controlPlane.url` or `bootstrap.apiUrl`
- `controlPlane.token` or `bootstrap.token`
