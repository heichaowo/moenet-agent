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

	mu    sync.RWMutex
	peers map[int]*MeshPeer // key: node ID
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

	// Build new peer map
	newPeers := make(map[int]*MeshPeer)
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
	ifname := fmt.Sprintf("wg_mesh_%d", peer.NodeID)

	// Build allowed IPs (peer's loopback addresses)
	allowedIPs := []string{}
	if peer.LoopbackIPv4 != "" {
		allowedIPs = append(allowedIPs, peer.LoopbackIPv4+"/32")
	}
	if peer.LoopbackIPv6 != "" {
		allowedIPs = append(allowedIPs, peer.LoopbackIPv6+"/128")
	}

	// Create interface
	if err := m.wgExecutor.CreateInterface(
		ifname,
		0, // Dynamic port
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

	log.Printf("[MeshSync] Configured tunnel to %s (%s)", peer.NodeName, peer.Endpoint)
	return nil
}

// removeMeshTunnel removes a mesh tunnel
func (m *MeshSync) removeMeshTunnel(peer *MeshPeer) {
	ifname := fmt.Sprintf("wg_mesh_%d", peer.NodeID)
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
