---
title: Agent Troubleshooting Guide
description: Common issues and solutions for MoeNet Agent
---

# Agent Troubleshooting Guide

## Common Issues

### Agent Not Starting

#### Symptom

Agent fails to start or exits immediately.

#### Check

```bash
journalctl -u moenet-agent -n 50
```

#### Common Causes

**1. Invalid configuration**

```bash
./moenet-agent -validate
```

**2. Missing permissions**

```bash
# BIRD socket
ls -la /run/bird/bird.ctl
chmod 660 /run/bird/bird.ctl
chown root:bird /run/bird/bird.ctl

# WireGuard
ls -la /etc/wireguard/private.key
chmod 600 /etc/wireguard/private.key
```

**3. Port already in use**

```bash
ss -tulnp | grep 24368
```

---

### Can't Connect to Control Plane

#### Symptom

```
[ERROR] Failed to fetch sessions: connection refused
```

#### Check

```bash
curl -H "Authorization: Bearer $TOKEN" https://api.moenet.work/health
```

#### Common Causes

**1. Network connectivity**

```bash
ping api.moenet.work
curl -v https://api.moenet.work/health
```

**2. Invalid token**
Check `controlPlane.token` in config.

**3. Firewall blocking**

```bash
iptables -L -n | grep 443
```

---

### Sessions Not Syncing

#### Symptom

Sessions exist in Control Plane but not configured on node.

#### Check

```bash
# Trigger manual sync
curl http://localhost:24368/sync

# Check agent logs
journalctl -u moenet-agent | grep -i sync
```

#### Common Causes

**1. Circuit breaker open**

```
[WARN] Circuit breaker open, skipping sync
```

Wait 30 seconds for circuit to reset.

**2. BIRD configuration error**

```bash
birdc configure check
```

---

### WireGuard Tunnel Not Working

#### Symptom

Tunnel created but no connectivity.

#### Check

```bash
# Interface exists?
ip link show | grep wg_

# Peer configured?
wg show

# Handshake happening?
wg show wg_4242421080
```

#### Common Causes

**1. Wrong endpoint**

```bash
# Check peer endpoint
wg show wg_4242421080 | grep endpoint
```

**2. Firewall blocking UDP**

```bash
iptables -L -n | grep 24000
```

**3. MTU issues**

```bash
ping -M do -s 1400 fe80::1%wg_4242421080
```

---

### BGP Session Not Established

#### Symptom

WireGuard up, but BGP not establishing.

#### Check

```bash
birdc show protocols | grep 4242421080
birdc show protocol all dn42_4242421080
```

#### Common Causes

**1. Wrong neighbor address**

```bash
birdc show protocol all dn42_4242421080 | grep Neighbor
```

**2. Missing route to neighbor**

```bash
ip -6 route get fe80::1
```

**3. Configuration syntax error**

```bash
birdc configure check
cat /etc/bird/peers/dn42_4242421080.conf
```

---

### High Memory Usage

#### Symptom

Agent consuming excessive memory.

#### Check

```bash
ps aux | grep moenet-agent
cat /proc/$(pgrep moenet-agent)/status | grep VmRSS
```

#### Common Causes

**1. Too many sessions**
Normal: ~5MB per 100 sessions

**2. Memory leak**
Restart agent and monitor:

```bash
systemctl restart moenet-agent
watch -n 60 'ps aux | grep moenet-agent'
```

---

## Diagnostic Commands

### Agent Status

```bash
curl http://localhost:24368/status | jq
```

### BIRD Status

```bash
birdc show status
birdc show protocols
birdc show route count
```

### WireGuard Status

```bash
wg show
wg show all dump
```

### System Resources

```bash
# CPU/Memory
top -p $(pgrep moenet-agent)

# Open files
lsof -p $(pgrep moenet-agent) | wc -l

# Network connections
ss -np | grep moenet-agent
```

## Log Locations

| Component | Location |
|-----------|----------|
| Agent | `journalctl -u moenet-agent` |
| BIRD | `/var/log/bird.log` |
| System | `/var/log/syslog` |

## Getting Help

1. Check logs: `journalctl -u moenet-agent -n 100`
2. Enable debug: Set `"logLevel": "debug"` in config
3. Contact: Telegram @heicha
