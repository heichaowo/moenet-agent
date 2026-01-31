package task

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
)

// IBGPSync handles iBGP peer configuration synchronization
type IBGPSync struct {
	config       *config.Config
	birdPool     *bird.Pool
	ibgpConfDir  string
	ibgpTemplate *template.Template

	mu    sync.RWMutex
	peers map[int]*MeshPeer // key: node ID
}

// NewIBGPSync creates a new iBGP sync handler
func NewIBGPSync(cfg *config.Config, birdPool *bird.Pool) (*IBGPSync, error) {
	confDir := cfg.Bird.IBGPConfDir
	if confDir == "" {
		confDir = "/etc/bird/ibgp"
	}

	// Ensure directory exists
	if err := os.MkdirAll(confDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create iBGP conf dir: %w", err)
	}

	tmpl, err := template.New("ibgp").Parse(ibgpTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse iBGP template: %w", err)
	}

	sync := &IBGPSync{
		config:       cfg,
		birdPool:     birdPool,
		ibgpConfDir:  confDir,
		ibgpTemplate: tmpl,
		peers:        make(map[int]*MeshPeer),
	}

	// Compile-time references to silence unused method warnings
	// These methods are reserved for future use
	_ = sync.removePeerConfig
	_ = sync.cleanupStaleConfigs

	return sync, nil
}

// Run starts the iBGP sync task
func (i *IBGPSync) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(120 * time.Second) // 2 minutes
	defer ticker.Stop()

	// Initial sync
	log.Println("[iBGP] Performing initial sync...")
	if err := i.Sync(ctx); err != nil {
		log.Printf("[iBGP] Initial sync failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[iBGP] Task stopped")
			return
		case <-ticker.C:
			if err := i.Sync(ctx); err != nil {
				log.Printf("[iBGP] Sync failed: %v", err)
			}
		}
	}
}

// Sync updates iBGP peer configurations based on mesh peers
func (i *IBGPSync) Sync(ctx context.Context) error {
	i.mu.RLock()
	peers := make([]*MeshPeer, 0, len(i.peers))
	peerMap := make(map[int]*MeshPeer)
	for id, peer := range i.peers {
		peers = append(peers, peer)
		peerMap[id] = peer
	}
	i.mu.RUnlock()

	if len(peers) == 0 {
		log.Println("[iBGP] No peers to configure")
		return nil
	}

	changed := false

	for _, peer := range peers {
		// Skip self
		if peer.NodeID == i.config.Node.ID {
			continue
		}

		// Generate iBGP config file
		filename := filepath.Join(i.ibgpConfDir, fmt.Sprintf("ibgp_%d.conf", peer.NodeID))
		if err := i.generateConfig(peer, filename); err != nil {
			log.Printf("[iBGP] Failed to generate config for %s: %v", peer.NodeName, err)
			continue
		}
		changed = true
	}

	// Clean up stale configs (files not matching current peers)
	if err := i.cleanupStaleConfigs(peerMap); err != nil {
		log.Printf("[iBGP] Warning: cleanup failed: %v", err)
	}

	// Reload BIRD if configs changed
	if changed {
		if err := i.birdPool.Configure(); err != nil {
			log.Printf("[iBGP] Warning: BIRD reconfigure failed: %v", err)
		} else {
			log.Printf("[iBGP] Configured %d iBGP peers", len(peers)-1)
		}
	}

	return nil
}

// UpdatePeers updates the peer list (called by MeshSync)
func (i *IBGPSync) UpdatePeers(peers map[int]*MeshPeer) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.peers = peers
}

// UpdatePeersFromAPI updates the peer list from API response
func (i *IBGPSync) UpdatePeersFromAPI(apiPeers []BirdIBGPPeer) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Convert API peers to internal format
	newPeers := make(map[int]*MeshPeer)
	for _, p := range apiPeers {
		newPeers[p.NodeID] = &MeshPeer{
			NodeID:       p.NodeID,
			NodeName:     p.NodeName,
			LoopbackIPv4: p.LoopbackIPv4,
			LoopbackIPv6: p.LoopbackIPv6,
			IsRR:         p.IsRR,
		}
	}
	i.peers = newPeers

	log.Printf("[iBGP] Received %d peers from API", len(apiPeers))
}

// generateConfig generates iBGP configuration for a peer
func (i *IBGPSync) generateConfig(peer *MeshPeer, filename string) error {
	// Check if already exists with same content
	if i.configUnchanged(peer, filename) {
		return nil
	}

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Determine local node type from config
	localIsRR := strings.Contains(strings.ToLower(i.config.Node.Name), "-rr")

	// RR client logic: only add "rr client" when local is RR and peer is NOT RR
	// This makes the peer a client of this RR, receiving reflected routes
	markAsRRClient := localIsRR && !peer.IsRR

	data := map[string]interface{}{
		"NodeID":         peer.NodeID,
		"NodeName":       peer.NodeName,
		"LoopbackIPv6":   peer.LoopbackIPv6,
		"LoopbackIPv4":   peer.LoopbackIPv4,
		"IsRR":           peer.IsRR,
		"MarkAsRRClient": markAsRRClient, // true = add "rr client" directive
		"LocalLoopback":  i.config.WireGuard.DN42IPv6,
	}

	return i.ibgpTemplate.Execute(f, data)
}

// configUnchanged checks if the config file exists and is unchanged
//
//nolint:unused,unparam // peer reserved for future config comparison
func (i *IBGPSync) configUnchanged(_ *MeshPeer, filename string) bool {
	// Simple check: if file exists and peer hasn't changed, skip
	if _, err := os.Stat(filename); err != nil {
		return false // File doesn't exist
	}
	return false // For now, always regenerate
}

// removePeerConfig removes the iBGP config for a peer
//
//nolint:unused // Reserved for future use
func (i *IBGPSync) removePeerConfig(nodeID int) error {
	filename := filepath.Join(i.ibgpConfDir, fmt.Sprintf("ibgp_%d.conf", nodeID))
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ibgpTemplate is the BIRD 3 template for iBGP peers
const ibgpTemplate = `# iBGP peer: {{.NodeName}} (Node {{.NodeID}})
# Auto-generated by moenet-agent

protocol bgp ibgp_{{.NodeID}} from ibgp_peers {
    neighbor {{.LoopbackIPv6}} as 4242420216;
    description "iBGP to {{.NodeName}}";
    {{- if .MarkAsRRClient}}
    rr client;
    {{- end}}
    
    ipv4 {
        import all;
        export all;
        next hop self;
    };
    
    ipv6 {
        import all;
        export all;
        next hop self;
    };
}
`

// cleanupStaleConfigs removes configs for peers that no longer exist
func (i *IBGPSync) cleanupStaleConfigs(currentPeers map[int]*MeshPeer) error {
	files, err := os.ReadDir(i.ibgpConfDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "ibgp_") || !strings.HasSuffix(file.Name(), ".conf") {
			continue
		}

		var nodeID int
		_, err := fmt.Sscanf(file.Name(), "ibgp_%d.conf", &nodeID)
		if err != nil {
			continue
		}

		if _, exists := currentPeers[nodeID]; !exists {
			path := filepath.Join(i.ibgpConfDir, file.Name())
			os.Remove(path)
			log.Printf("[iBGP] Removed stale config for node %d", nodeID)
		}
	}

	return nil
}
