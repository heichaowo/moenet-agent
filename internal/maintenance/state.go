// Package maintenance implements the maintenance mode functionality for the agent.
// When in maintenance mode, the agent signals BIRD to gracefully shutdown all eBGP sessions.
package maintenance

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
)

// State represents the current maintenance state of the node.
type State struct {
	mu            sync.RWMutex
	enabled       bool
	enteredAt     time.Time
	birdPool      *bird.Pool
	disabledPeers []string // List of peers that were disabled
}

// NewState creates a new maintenance state manager.
func NewState(birdPool *bird.Pool) *State {
	return &State{
		birdPool: birdPool,
	}
}

// IsEnabled returns whether maintenance mode is currently enabled.
func (s *State) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// EnteredAt returns when maintenance mode was entered.
func (s *State) EnteredAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enteredAt
}

// Enter enables maintenance mode by gracefully shutting down all eBGP sessions.
func (s *State) Enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		return nil // Already in maintenance mode
	}

	log.Println("[Maintenance] Entering maintenance mode...")

	// Get list of all eBGP peers
	peers, err := s.getEBGPPeers()
	if err != nil {
		return err
	}

	// Disable each peer
	s.disabledPeers = make([]string, 0, len(peers))
	for _, peer := range peers {
		if err := s.disablePeer(peer); err != nil {
			log.Printf("[Maintenance] Warning: failed to disable peer %s: %v", peer, err)
			continue
		}
		s.disabledPeers = append(s.disabledPeers, peer)
		log.Printf("[Maintenance] Disabled peer: %s", peer)
	}

	s.enabled = true
	s.enteredAt = time.Now()

	log.Printf("[Maintenance] Maintenance mode enabled, %d peers disabled", len(s.disabledPeers))
	return nil
}

// Exit disables maintenance mode by re-enabling all previously disabled eBGP sessions.
func (s *State) Exit() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return nil // Not in maintenance mode
	}

	log.Println("[Maintenance] Exiting maintenance mode...")

	// Re-enable each previously disabled peer
	for _, peer := range s.disabledPeers {
		if err := s.enablePeer(peer); err != nil {
			log.Printf("[Maintenance] Warning: failed to enable peer %s: %v", peer, err)
			continue
		}
		log.Printf("[Maintenance] Enabled peer: %s", peer)
	}

	s.enabled = false
	s.enteredAt = time.Time{}
	s.disabledPeers = nil

	log.Println("[Maintenance] Maintenance mode disabled")
	return nil
}

// getEBGPPeers returns a list of all eBGP peer names (protocols starting with "dn42_").
func (s *State) getEBGPPeers() ([]string, error) {
	output, err := s.birdPool.ShowProtocols()
	if err != nil {
		return nil, err
	}

	return parseEBGPPeers(output), nil
}

// parseEBGPPeers extracts eBGP peer names from BIRD protocol output.
func parseEBGPPeers(output string) []string {
	var peers []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		// Skip header and empty lines
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			continue
		}

		// Parse protocol name (first field)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := fields[0]
		// eBGP peers start with "dn42_"
		if strings.HasPrefix(name, "dn42_") {
			peers = append(peers, name)
		}
	}

	return peers
}

// disablePeer disables a BGP peer using BIRD.
func (s *State) disablePeer(name string) error {
	_, err := s.birdPool.Execute("disable " + name)
	return err
}

// enablePeer enables a BGP peer using BIRD.
func (s *State) enablePeer(name string) error {
	_, err := s.birdPool.Execute("enable " + name)
	return err
}
