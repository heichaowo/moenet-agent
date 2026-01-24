# Migration Guide: Python Agent → Go Agent

## Overview

This guide covers migrating from the Python-based `moenet-dn42-agent` to the new Go-based `moenet-agent`.

## Prerequisites

- Ansible control machine with access to nodes
- Existing Python agent running on nodes
- Updated `moenet-dn42-infra` with Go agent support

## Migration Steps

### 1. Update Inventory (Optional)

If you want to migrate specific nodes first, set `agent_type` per host:

```yaml
# host_vars/jp-edge.yml
agent_type: "go"  # Use new Go agent
```

Or globally in `group_vars/all.yml`:

```yaml
agent_type: "go"  # Default for all nodes
```

### 2. Deploy to Single Canary Node

```bash
# Test on single node first
ansible-playbook -i inventory.yml site.yml -l jp-edge --tags agent
```

### 3. Verify Canary Node

```bash
# Check service status
ssh jp-edge systemctl status moenet-agent

# Check agent logs
ssh jp-edge journalctl -u moenet-agent -f

# Verify CP connectivity
ssh jp-edge curl -s http://localhost:8080/status
```

### 4. Roll Out to All Nodes

```bash
# Deploy to all nodes
ansible-playbook -i inventory.yml site.yml --tags agent
```

## Rollback Procedure

If issues occur, set `agent_type: "python"` and redeploy:

```yaml
# group_vars/all.yml
agent_type: "python"
```

```bash
ansible-playbook -i inventory.yml site.yml --tags agent
```

## Configuration Changes

### Python Config → Go Config

| Python Field | Go Field |
|-------------|----------|
| `control_plane_url` | `controlPlane.url` |
| `control_plane_token` | `controlPlane.token` |
| `sync_interval` | `controlPlane.syncInterval` |
| `heartbeat_interval` | `controlPlane.heartbeatInterval` |
| `bird_config_dir` | `bird.peerConfDir` |
| `bird_ctl` | `bird.controlSocket` |

### New Go-only Features

- Connection pooling for BIRD socket (`bird.poolSize`)
- HTTP API on port 8080 (`/status`, `/sync`)
- Graceful shutdown with connection draining

## Troubleshooting

### Agent Not Starting

```bash
# Check config syntax
/opt/moenet-agent/moenet-agent -c /opt/moenet-agent/config.json -version

# Check BIRD socket permissions
ls -la /var/run/bird/run/bird.ctl
```

### No Sessions Syncing

```bash
# Check CP connectivity
curl -H "Authorization: Bearer $TOKEN" \
  https://cp.moenet.work/api/v1/agent/$(hostname)/sessions

# Check agent logs for errors
journalctl -u moenet-agent --since "5 minutes ago"
```

### WireGuard Issues

```bash
# Check WireGuard interfaces
wg show

# Check WireGuard keys
cat /etc/wireguard/publickey
```

## Timeline

1. **Week 1**: Deploy to canary node, monitor
2. **Week 2**: Roll out to 50% of edge nodes
3. **Week 3**: Complete rollout
4. **Week 4**: Deprecate Python agent repo
