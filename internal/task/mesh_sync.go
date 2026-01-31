package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/config"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// MeshSync handles WireGuard mesh tunnel synchronization
type MeshSync struct {
	config     *config.Config
	httpClient *http.Client
	wgExecutor *wireguard.Executor

	mu             sync.RWMutex
	peers          map[int]*MeshPeer // key: node ID
	onPeersUpdated func(map[int]*MeshPeer)
}

// NewMeshSync creates a new mesh sync handler
func NewMeshSync(cfg *config.Config, wgExecutor *wireguard.Executor) *MeshSync {
	return &MeshSync{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		wgExecutor: wgExecutor,
		peers:      make(map[int]*MeshPeer),
	}
}

// SetOnPeersUpdated sets a callback that's invoked when mesh peers are updated
func (m *MeshSync) SetOnPeersUpdated(callback func(map[int]*MeshPeer)) {
	m.onPeersUpdated = callback
}

// Run starts the mesh sync task
func (m *MeshSync) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(120 * time.Second) // 2 minutes
	defer ticker.Stop()

	// Initial sync
	log.Println("[MeshSync] Performing initial sync...")
	if err := m.Sync(ctx); err != nil {
		log.Printf("[MeshSync] Initial sync failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[MeshSync] Task stopped")
			return
		case <-ticker.C:
			if err := m.Sync(ctx); err != nil {
				log.Printf("[MeshSync] Sync failed: %v", err)
			}
		}
	}
}

// Sync fetches mesh configuration and applies changes
func (m *MeshSync) Sync(ctx context.Context) error {
	meshConfig, err := m.fetchMeshConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch mesh config: %w", err)
	}

	log.Printf("[MeshSync] Received %d peers from CP", len(meshConfig.Peers))

	// Build new peer map and track status
	newPeers := make(map[int]*MeshPeer)
	peerStatus := make(map[int]string)

	for i := range meshConfig.Peers {
		peer := &meshConfig.Peers[i]
		newPeers[peer.NodeID] = peer

		// Skip self
		if peer.NodeID == m.config.Node.ID {
			continue
		}

		// Create or update mesh tunnel
		if err := m.ensureMeshTunnel(peer); err != nil {
			log.Printf("[MeshSync] Failed to configure tunnel to %s: %v", peer.NodeName, err)
			peerStatus[peer.NodeID] = fmt.Sprintf("error: %v", err)
		} else {
			peerStatus[peer.NodeID] = "configured"
		}
	}

	// Find and remove stale tunnels
	m.mu.RLock()
	for nodeID, oldPeer := range m.peers {
		if _, exists := newPeers[nodeID]; !exists {
			log.Printf("[MeshSync] Removing stale tunnel to %s", oldPeer.NodeName)
			m.removeMeshTunnel(oldPeer)
		}
	}
	m.mu.RUnlock()

	// Update peer map
	m.mu.Lock()
	m.peers = newPeers
	m.mu.Unlock()

	// Notify RTT of updated peers
	if m.onPeersUpdated != nil {
		m.onPeersUpdated(newPeers)
	}

	// Report status to CP (non-blocking)
	if len(peerStatus) > 0 {
		go func() {
			if err := m.reportMeshStatus(ctx, peerStatus); err != nil {
				log.Printf("[MeshSync] Failed to report status: %v", err)
			}
		}()
	}

	return nil
}

// fetchMeshConfig retrieves mesh configuration from Control Plane
func (m *MeshSync) fetchMeshConfig(ctx context.Context) (*MeshConfig, error) {
	url := fmt.Sprintf("%s/api/v1/agent/%s/mesh", m.config.ControlPlane.URL, m.config.Node.Name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+m.config.ControlPlane.Token)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    MeshConfig `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result.Data, nil
}

// ensureMeshTunnel creates or updates a mesh tunnel to a peer
func (m *MeshSync) ensureMeshTunnel(peer *MeshPeer) error {
	ifname := fmt.Sprintf("dn42-wg-igp-%d", peer.NodeID)

	// Build allowed IPs - allow all traffic through mesh for IGP routing
	allowedIPs := []string{
		"0.0.0.0/0", // All IPv4
		"fd00::/8",  // DN42 IPv6 ULA
		"fe80::/64", // Link-local
	}

	// Create interface
	// Use port based on PEER node ID (51820 + peerNodeID) so each interface has unique port
	listenPort := 51820 + peer.NodeID
	if err := m.wgExecutor.CreateInterface(
		ifname,
		listenPort,
		peer.PublicKey,
		peer.Endpoint,
		allowedIPs,
		25, // Keepalive
	); err != nil {
		return fmt.Errorf("failed to create interface: %w", err)
	}

	// Set MTU
	mtu := peer.MTU
	if mtu == 0 {
		mtu = 1420
	}
	if err := m.wgExecutor.SetMTU(ifname, mtu); err != nil {
		log.Printf("[MeshSync] Warning: failed to set MTU for %s: %v", ifname, err)
	}

	// Assign IPv6 link-local address for Babel IGP
	// Format: fe80:{region}:{local_index}::1 derived from loopback fd00:4242:7777:{region}:{local_index}::1
	linkLocalAddr := deriveLLAFromLoopback(m.config.WireGuard.DN42IPv6)
	if linkLocalAddr != "" {
		if err := m.wgExecutor.AddAddress(ifname, linkLocalAddr); err != nil {
			log.Printf("[MeshSync] Warning: failed to add link-local address to %s: %v", ifname, err)
		}
	}

	log.Printf("[MeshSync] Configured tunnel to %s (%s)", peer.NodeName, peer.Endpoint)
	return nil
}

// removeMeshTunnel removes a mesh tunnel
func (m *MeshSync) removeMeshTunnel(peer *MeshPeer) {
	ifname := fmt.Sprintf("dn42-wg-igp-%d", peer.NodeID)
	if err := m.wgExecutor.DeleteInterface(ifname); err != nil {
		log.Printf("[MeshSync] Warning: failed to delete interface %s: %v", ifname, err)
	}
}

// reportMeshStatus reports mesh tunnel status to CP
func (m *MeshSync) reportMeshStatus(ctx context.Context, status map[int]string) error {
	url := fmt.Sprintf("%s/api/v1/agent/%s/mesh/status", m.config.ControlPlane.URL, m.config.Node.Name)

	body, err := json.Marshal(map[string]interface{}{
		"node_id":   m.config.Node.Name,
		"timestamp": time.Now().Unix(),
		"peers":     status,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// deriveLLAFromLoopback derives link-local address from loopback IPv6
// Loopback format: fd00:4242:7777:{region}:{local_index}::1
// LLA format: fe80:{region}:{local_index}::1/64
func deriveLLAFromLoopback(loopback string) string {
	if loopback == "" {
		return ""
	}
	// Parse loopback like "fd00:4242:7777:302:1::1"
	// Split by ":" and extract region (index 3) and local_index (index 4)
	parts := splitIPv6(loopback)
	if len(parts) < 5 {
		return ""
	}
	// parts[0:3] = "fd00", "4242", "7777"
	// parts[3] = region (e.g., "302")
	// parts[4] = local_index (e.g., "1")
	region := parts[3]
	localIndex := parts[4]
	return fmt.Sprintf("fe80:%s:%s::1/64", region, localIndex)
}

// splitIPv6 splits an IPv6 address by colon, expanding :: if present
func splitIPv6(addr string) []string {
	// Remove any CIDR suffix
	if idx := len(addr) - 1; idx > 0 {
		for i := len(addr) - 1; i >= 0; i-- {
			if addr[i] == '/' {
				addr = addr[:i]
				break
			}
		}
	}
	// Simple split - for our loopback format fd00:4242:7777:XXX:Y::1
	// We just need the first 5 parts before the ::
	parts := []string{}
	current := ""
	for _, c := range addr {
		if c == ':' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
