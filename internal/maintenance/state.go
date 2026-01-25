// Package maintenance implements the maintenance mode functionality for the agent.
// When in maintenance mode, the agent sets BIRD's MAINTENANCE_MODE to true,
// which triggers GRACEFUL_SHUTDOWN (RFC 8326) community on route exports.
package maintenance

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
)

const (
	// maintenanceConfPath is where BIRD looks for the MAINTENANCE_MODE variable
	maintenanceConfPath = "/etc/bird/maintenance.conf"
)

// State represents the current maintenance state of the node.
type State struct {
	mu        sync.RWMutex
	enabled   bool
	enteredAt time.Time
	birdPool  *bird.Pool
}

// NewState creates a new maintenance state manager.
func NewState(birdPool *bird.Pool) *State {
	s := &State{
		birdPool: birdPool,
	}
	// Read current state from file
	s.readCurrentState()
	return s
}

// readCurrentState reads the maintenance.conf file to determine current state
func (s *State) readCurrentState() {
	data, err := os.ReadFile(maintenanceConfPath)
	if err != nil {
		return
	}
	content := string(data)
	if content == "define MAINTENANCE_MODE = true;\n" {
		s.enabled = true
		s.enteredAt = time.Now() // Approximate
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

// Enter enables maintenance mode by setting MAINTENANCE_MODE = true in BIRD.
// This causes BIRD to add GRACEFUL_SHUTDOWN community to all exported routes,
// signaling peers to prefer alternative paths before this node goes down.
func (s *State) Enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		return nil // Already in maintenance mode
	}

	log.Println("[Maintenance] Entering maintenance mode (Graceful Shutdown)...")

	// Write MAINTENANCE_MODE = true to config file
	content := "define MAINTENANCE_MODE = true;\n"
	if err := os.WriteFile(maintenanceConfPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write maintenance.conf: %w", err)
	}

	// Reconfigure BIRD to apply the change
	if err := s.birdPool.Configure(); err != nil {
		// Rollback
		os.WriteFile(maintenanceConfPath, []byte("define MAINTENANCE_MODE = false;\n"), 0644)
		return fmt.Errorf("failed to reconfigure BIRD: %w", err)
	}

	s.enabled = true
	s.enteredAt = time.Now()

	log.Println("[Maintenance] Maintenance mode enabled - GRACEFUL_SHUTDOWN community active")
	return nil
}

// Exit disables maintenance mode by setting MAINTENANCE_MODE = false in BIRD.
// This removes the GRACEFUL_SHUTDOWN community from exports, restoring normal routing.
func (s *State) Exit() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return nil // Not in maintenance mode
	}

	log.Println("[Maintenance] Exiting maintenance mode...")

	// Write MAINTENANCE_MODE = false to config file
	content := "define MAINTENANCE_MODE = false;\n"
	if err := os.WriteFile(maintenanceConfPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write maintenance.conf: %w", err)
	}

	// Reconfigure BIRD to apply the change
	if err := s.birdPool.Configure(); err != nil {
		log.Printf("[Maintenance] Warning: BIRD reconfigure failed: %v", err)
		// Don't rollback - file is already written
	}

	s.enabled = false
	s.enteredAt = time.Time{}

	log.Println("[Maintenance] Maintenance mode disabled - normal routing restored")
	return nil
}
