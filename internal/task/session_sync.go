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
)

// SessionSync handles synchronization of BGP sessions with Control Plane
type SessionSync struct {
	config     *config.Config
	httpClient *http.Client

	// Local session state
	mu       sync.RWMutex
	sessions map[string]*BgpSession // key: UUID
}

// NewSessionSync creates a new session sync handler
func NewSessionSync(cfg *config.Config) *SessionSync {
	return &SessionSync{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		sessions: make(map[string]*BgpSession),
	}
}

// Run starts the session sync task
func (s *SessionSync) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(s.config.ControlPlane.SyncInterval) * time.Second)
	defer ticker.Stop()

	// Initial sync
	log.Println("[SessionSync] Performing initial sync...")
	if err := s.Sync(ctx); err != nil {
		log.Printf("[SessionSync] Initial sync failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[SessionSync] Task stopped")
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				log.Printf("[SessionSync] Sync failed: %v", err)
			}
		}
	}
}

// Sync fetches sessions from CP and applies changes
func (s *SessionSync) Sync(ctx context.Context) error {
	// Fetch sessions from Control Plane
	sessions, err := s.fetchSessions(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch sessions: %w", err)
	}

	log.Printf("[SessionSync] Received %d sessions from CP", len(sessions))

	// Build current session map
	remoteMap := make(map[string]*BgpSession)
	for i := range sessions {
		remoteMap[sessions[i].UUID] = &sessions[i]
	}

	// Process sessions
	for _, session := range sessions {
		if err := s.processSession(ctx, &session); err != nil {
			log.Printf("[SessionSync] Failed to process session %s (AS%d): %v",
				session.UUID, session.ASN, err)
		}
	}

	// Find deleted sessions (in local but not in remote)
	s.mu.RLock()
	for uuid, localSession := range s.sessions {
		if _, exists := remoteMap[uuid]; !exists {
			log.Printf("[SessionSync] Session %s (AS%d) removed from CP, cleaning up",
				uuid, localSession.ASN)
			// TODO: Remove WireGuard interface and BIRD config
		}
	}
	s.mu.RUnlock()

	// Update local session map
	s.mu.Lock()
	s.sessions = remoteMap
	s.mu.Unlock()

	return nil
}

// fetchSessions retrieves sessions from Control Plane
func (s *SessionSync) fetchSessions(ctx context.Context) ([]BgpSession, error) {
	url := fmt.Sprintf("%s/api/v1/agent/%s/sessions", s.config.ControlPlane.URL, s.config.Node.Name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+s.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			BgpSessions []BgpSession `json:"bgpSessions"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Data.BgpSessions, nil
}

// processSession handles a single session based on its status
func (s *SessionSync) processSession(ctx context.Context, session *BgpSession) error {
	switch session.Status {
	case StatusQueuedForSetup:
		return s.setupSession(ctx, session)
	case StatusEnabled:
		return s.verifySession(ctx, session)
	case StatusQueuedForDelete:
		return s.deleteSession(ctx, session)
	case StatusProblem:
		return s.handleProblemSession(ctx, session)
	default:
		log.Printf("[SessionSync] Unknown status %d for session %s", session.Status, session.UUID)
		return nil
	}
}

// setupSession configures a new peering session
func (s *SessionSync) setupSession(ctx context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Setting up session AS%d (%s)", session.ASN, session.Name)

	// 1. Create WireGuard interface
	// TODO: Call wireguard.CreateInterface()

	// 2. Generate BIRD configuration from template
	// TODO: Call bird.GenerateConfig()

	// 3. Reload BIRD
	// TODO: Call bird.Configure()

	// 4. Report success to CP
	if err := s.reportStatus(ctx, session.UUID, "active", ""); err != nil {
		return fmt.Errorf("failed to report status: %w", err)
	}

	log.Printf("[SessionSync] Session AS%d setup complete", session.ASN)
	return nil
}

// verifySession checks if an existing session is working
func (s *SessionSync) verifySession(ctx context.Context, session *BgpSession) error {
	// TODO: Check WireGuard handshake
	// TODO: Check BIRD protocol state
	return nil
}

// deleteSession removes a peering session
func (s *SessionSync) deleteSession(ctx context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Deleting session AS%d (%s)", session.ASN, session.Name)

	// 1. Remove BIRD configuration
	// TODO: Call bird.RemovePeer()

	// 2. Reload BIRD
	// TODO: Call bird.Configure()

	// 3. Remove WireGuard interface
	// TODO: Call wireguard.DeleteInterface()

	// 4. Report deletion to CP
	if err := s.reportStatus(ctx, session.UUID, "deleted", ""); err != nil {
		return fmt.Errorf("failed to report status: %w", err)
	}

	log.Printf("[SessionSync] Session AS%d deleted", session.ASN)
	return nil
}

// handleProblemSession attempts to fix a problematic session
func (s *SessionSync) handleProblemSession(ctx context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Handling problem session AS%d", session.ASN)
	// TODO: Attempt to reconfigure
	return nil
}

// reportStatus reports session status change to Control Plane
func (s *SessionSync) reportStatus(ctx context.Context, uuid, status, lastError string) error {
	url := fmt.Sprintf("%s/api/v1/agent/%s/modify", s.config.ControlPlane.URL, s.config.Node.Name)

	payload := map[string]string{
		"peer_id": uuid,
		"status":  status,
	}
	if lastError != "" {
		payload["last_error"] = lastError
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+s.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetSession returns a session by UUID
func (s *SessionSync) GetSession(uuid string) *BgpSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[uuid]
}

// GetAllSessions returns all current sessions
func (s *SessionSync) GetAllSessions() []*BgpSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*BgpSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	return result
}
