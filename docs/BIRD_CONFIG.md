# BIRD Configuration Guide

## Overview

The MoeNet Agent automatically generates and maintains BIRD 3.x configuration files for DN42 routing. This includes filters, communities, Babel IGP, and cold potato routing.

## Generated Configuration Files

| File | Purpose |
|------|---------|
| `filters.conf` | DN42 import/export filters, prefix validation |
| `moenet_communities.conf` | MoeNet Large Community definitions for cold potato |
| `babel.conf` | Babel IGP configuration for mesh connectivity |
| `cold_potato.conf` | Cold potato routing functions |
| `ibgp.d/*.conf` | iBGP peer configurations |
| `peers/*.conf` | External BGP peer configurations |

## DN42 Communities

The agent sets DN42 standard communities (64511, xx) **only on self-originated routes**:

| Community | Value | Purpose |
|-----------|-------|---------|
| Region | (64511, 41-53) | Geographic region |
| Bandwidth | (64511, 21-25) | Link capacity |
| Latency | (64511, 1-9) | RTT tier |
| Crypto | (64511, 31-34) | Encryption status |

### Important: Export Filter Behavior

The export filter distinguishes between route sources:

```bird
filter dn42_export_filter {
    # Self-originated routes: add our communities
    if (source = RTS_STATIC || source = RTS_DEVICE) then {
        add_self_origin_communities();  # Only here!
        accept;
    }
    
    # BGP-learned routes: pass through unchanged
    if (source = RTS_BGP) then {
        accept;  # No community modification
    }
    
    reject;
}
```

> **Warning**: Never add region/country communities to foreign prefixes. This can cause routing anomalies for other networks.

## MoeNet Large Communities

Internal communities for cold potato routing within MoeNet backbone:

| Type | Format | Purpose |
|------|--------|---------|
| Origin Continent | (4242420998, 1, xx) | AS, NA, EU, OC, Other |
| Origin Subregion | (4242420998, 2, xxx) | AS-E, AS-SE, EU-W, etc |
| Origin Node | (4242420998, 3, nodeId) | Specific node ID |
| Link Characteristics | (4242420998, 4, x) | Intercontinental, high-lat |
| Bandwidth | (4242420998, 5, mbps) | Granular bandwidth |

## Cold Potato Routing

Cold potato routing keeps traffic inside the MoeNet backbone as long as possible:

1. **Same subregion**: +100 local_pref
2. **Same continent**: +50 local_pref
3. **Adjacent continent** (AS↔OC, NA↔EU): -50 local_pref
4. **Remote continent**: -200 local_pref

## Region Codes

| Code | Region | DN42 Community | MoeNet LC |
|------|--------|----------------|-----------|
| 101 | East Asia | (64511, 50) | LC_REGION_AS_E |
| 102 | Southeast Asia | (64511, 49) | LC_REGION_AS_SE |
| 103 | South Asia | (64511, 48) | LC_REGION_AS_S |
| 201 | NA East | (64511, 42) | LC_REGION_NA_E |
| 202 | NA Central | (64511, 43) | LC_REGION_NA_C |
| 203 | NA West | (64511, 44) | LC_REGION_NA_W |
| 301 | EU West | (64511, 41) | LC_REGION_EU_W |
| 302 | EU Central | (64511, 41) | LC_REGION_EU_C |
| 401 | Oceania | (64511, 51) | LC_REGION_OC |

## Babel IGP

Babel is used for IGP mesh connectivity between MoeNet nodes:

- Interface pattern: `dn42-wg-igp-*`
- Type: `tunnel` (for WireGuard P2P links)
- RTT cost enabled for path selection
- Only propagates loopback addresses (`/32`, `/128`)

## Configuration Sync

The `birdConfigSync` task runs every 300s and:

1. Fetches configuration from Control Plane API
2. Compares config hash for changes
3. Renders templates if changed
4. Reloads BIRD (`birdc configure`)

## Troubleshooting

### Check generated configs

```bash
cat /etc/bird/filters.conf
cat /etc/bird/moenet_communities.conf
cat /etc/bird/babel.conf
cat /etc/bird/cold_potato.conf
```

### Verify communities on routes

```bash
birdc show route for 172.23.0.0/24 all
```

### Check export filter

```bash
birdc show route export dn42_peer_xxx
```
