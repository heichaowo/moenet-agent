package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
	"github.com/moenet/moenet-agent/internal/firewall"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// SessionSync handles synchronization of BGP sessions with Control Plane
type SessionSync struct {
	config     *config.Config
	httpClient *http.Client
	birdPool   *bird.Pool
	birdConfig *bird.ConfigGenerator
	wgExecutor *wireguard.Executor
	fwExecutor *firewall.Executor

	// Local session state
	mu       sync.RWMutex
	sessions map[string]*BgpSession // key: UUID

	// Callback invoked after each sync with the current session list
	onSessionsUpdated func([]*BgpSession)

	// orphanTracker tracks dn42_ interfaces with no matching session.
	// Key: interface name, Value: number of consecutive sync cycles seen as orphan.
	// An interface is only cleaned up after appearing as orphan for >= 2 cycles
	// (grace period to avoid deleting interfaces being created concurrently).
	orphanTracker map[string]int

	// remediatedThisCycle tracks session UUIDs that have already been remediated
	// in the current sync cycle. Limits each session to at most 1 fix attempt
	// per cycle to prevent restart loops.
	remediatedThisCycle map[string]bool

	// remediationCount tracks total remediations executed in the current cycle.
	// Capped at maxRemediationsPerCycle to avoid blocking the sync goroutine
	// with stacked time.Sleep calls when multiple sessions are broken.
	remediationCount int
}

// NewSessionSync creates a new session sync handler
func NewSessionSync(cfg *config.Config, birdPool *bird.Pool, birdConfig *bird.ConfigGenerator, wgExecutor *wireguard.Executor, fwExecutor *firewall.Executor) *SessionSync {
	return &SessionSync{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		birdPool:        birdPool,
		birdConfig:      birdConfig,
		wgExecutor:      wgExecutor,
		fwExecutor:      fwExecutor,
		sessions:        make(map[string]*BgpSession),
		orphanTracker:   make(map[string]int),
	}
}

// SetOnSessionsUpdated registers a callback that fires after each sync cycle.
func (s *SessionSync) SetOnSessionsUpdated(fn func([]*BgpSession)) {
	s.onSessionsUpdated = fn
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
	// Reset per-cycle remediation tracker
	s.remediatedThisCycle = make(map[string]bool)
	s.remediationCount = 0

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

	// Find and clean up deleted sessions (in local but not in remote)
	needBirdReload := false
	s.mu.RLock()
	for uuid, localSession := range s.sessions {
		if _, exists := remoteMap[uuid]; !exists {
			slog.Info("session removed from CP, cleaning up",
				"uuid", uuid, "asn", localSession.ASN)

			// Remove BIRD config
			peerName := fmt.Sprintf("dn42_%d", localSession.ASN)
			if err := s.birdConfig.RemoveSession(peerName); err != nil {
				slog.Warn("failed to remove BIRD config for orphan",
					"peer", peerName, "error", err)
			} else {
				needBirdReload = true
			}

			// Remove WireGuard interface
			if localSession.Interface != "" {
				if err := s.wgExecutor.DeleteInterface(localSession.Interface); err != nil {
					slog.Warn("failed to delete WG interface for orphan",
						"interface", localSession.Interface, "error", err)
				}
			}
		}
	}
	s.mu.RUnlock()

	// Reload BIRD once if any orphan configs were removed
	if needBirdReload {
		if err := s.birdPool.Configure(); err != nil {
			slog.Warn("BIRD reconfigure failed after orphan cleanup", "error", err)
		}
	}

	// Scan for stale dn42_ interfaces with no matching session (grace period: 2 cycles)
	s.scanOrphanInterfaces(remoteMap)

	// Update local session map
	s.mu.Lock()
	s.sessions = remoteMap
	s.mu.Unlock()

	// Sync firewall ports
	if s.fwExecutor != nil {
		var expectedPorts []int
		for _, session := range sessions {
			if session.Port > 0 && (session.Status == StatusEnabled || session.Status == StatusQueuedForSetup) {
				expectedPorts = append(expectedPorts, session.Port)
			}
		}
		if added, removed, err := s.fwExecutor.SyncPorts(expectedPorts); err != nil {
			log.Printf("[SessionSync] Firewall sync error: %v", err)
		} else if added > 0 || removed > 0 {
			log.Printf("[SessionSync] Firewall synced: %d added, %d removed", added, removed)
		}
	}

	// Notify probe (or other subscribers) of updated session list
	if s.onSessionsUpdated != nil {
		allSessions := s.GetAllSessions()
		s.onSessionsUpdated(allSessions)
	}

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
	case StatusDisabled:
		// Disabled sessions: ensure config is removed, don't report error
		return s.cleanupDisabledSession(ctx, session)
	case StatusPendingApproval:
		// Pending sessions: skip silently, waiting for admin approval
		return nil
	default:
		log.Printf("[SessionSync] Unknown status %d for session %s", session.Status, session.UUID)
		return nil
	}
}

// setupSession configures a new peering session
func (s *SessionSync) setupSession(ctx context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Setting up session AS%d (%s)", session.ASN, session.Name)

	// 1. Create WireGuard interface
	if session.Type == "wireguard" && session.Credential != "" {
		// Parse credential JSON to extract WireGuard parameters
		var cred struct {
			PublicKey    string `json:"public_key"`
			PresharedKey string `json:"preshared_key"`
			ListenPort   *int   `json:"listen_port"`
			Endpoint     string `json:"endpoint"`
			MTU          int    `json:"mtu"`
		}
		peerKey := session.Credential // fallback: treat as raw key
		if err := json.Unmarshal([]byte(session.Credential), &cred); err == nil && cred.PublicKey != "" {
			peerKey = cred.PublicKey
		}

		// Determine listen port from credential
		listenPort := 0
		if cred.ListenPort != nil {
			listenPort = *cred.ListenPort
		}

		// Standard DN42 allowed IPs (matching existing working sessions)
		allowedIPs := []string{"0.0.0.0/0", "fd00::/8", "fe80::/64"}

		// Use endpoint from credential if session endpoint is empty
		endpoint := session.Endpoint
		if endpoint == "" && cred.Endpoint != "" {
			endpoint = cred.Endpoint
		}

		if err := s.wgExecutor.CreateInterface(
			session.Interface,
			listenPort,        // Listen port from credential
			peerKey,           // Peer public key extracted from credential
			cred.PresharedKey, // Preshared key from credential
			endpoint,
			allowedIPs,
			25, // Keepalive
		); err != nil {
			return fmt.Errorf("failed to create WireGuard interface: %w", err)
		}

		// Set MTU
		mtu := session.MTU
		if mtu == 0 {
			mtu = 1420
		}
		if err := s.wgExecutor.SetMTU(session.Interface, mtu); err != nil {
			log.Printf("[SessionSync] Warning: failed to set MTU: %v", err)
		}

		// Assign local link-local address for BGP neighbor communication
		linkLocalAddr := deriveLLAFromLoopback(s.config.WireGuard.DN42IPv6)
		if linkLocalAddr != "" {
			if err := s.wgExecutor.AddAddress(session.Interface, linkLocalAddr); err != nil {
				log.Printf("[SessionSync] Warning: failed to add link-local address to %s: %v", session.Interface, err)
			}
		}
	}

	// 2. Generate BIRD configuration
	// Derive source address from loopback (strip /64 prefix len for BIRD)
	sourceAddr := deriveLLAFromLoopback(s.config.WireGuard.DN42IPv6)
	if idx := strings.Index(sourceAddr, "/"); idx > 0 {
		sourceAddr = sourceAddr[:idx]
	}

	cfg := &bird.SessionConfig{
		Name:          fmt.Sprintf("dn42_%d", session.ASN),
		Description:   session.Description,
		Interface:     session.Interface,
		ASN:           session.ASN,
		IPv4:          session.IPv4,
		IPv6:          session.IPv6,
		IPv6LinkLocal: session.IPv6LinkLocal,
		SourceAddress: sourceAddr,
		Extensions:    session.Extensions,
		Policy:        session.Policy,
	}

	if err := s.birdConfig.GenerateSession(cfg); err != nil {
		return fmt.Errorf("failed to generate BIRD config: %w", err)
	}

	// 3. Reload BIRD
	if err := s.birdPool.Configure(); err != nil {
		log.Printf("[SessionSync] Warning: BIRD reconfigure failed: %v", err)
	}

	// 4. Report success to CP
	if err := s.reportStatus(ctx, session.UUID, StatusEnabled, ""); err != nil {
		return fmt.Errorf("failed to report status: %w", err)
	}

	log.Printf("[SessionSync] Session AS%d setup complete", session.ASN)
	return nil
}

// verifySession checks if an existing session is healthy by inspecting
// BIRD protocol state and WireGuard handshake recency.
// If either check indicates a problem, it delegates to handleProblemSession.
// Errors are logged but never returned — a single unhealthy session must not
// block the rest of the sync cycle.
func (s *SessionSync) verifySession(ctx context.Context, session *BgpSession) error {
	protocolName := fmt.Sprintf("dn42_%d", session.ASN)
	var problems []string

	// --- BIRD protocol state check ---
	birdResult, err := s.birdPool.Execute("show protocols all \"" + protocolName + "\"")
	if err != nil {
		// BIRD socket may be unavailable (restart, etc.) — skip, don't cascade
		slog.Warn("BIRD query failed during health check, skipping",
			"peer", protocolName, "error", err)
	} else {
		state, found := parseBIRDProtocolState(birdResult)
		if found && state != "Established" {
			problems = append(problems, fmt.Sprintf("BGP state is %s", state))
		}
	}

	// --- WireGuard handshake check ---
	if session.Interface != "" {
		wgResult, err := s.wgExecutor.GetStatus(session.Interface)
		if err != nil {
			// Interface may not exist (manual removal) — skip
			slog.Warn("WG status query failed during health check, skipping",
				"interface", session.Interface, "error", err)
		} else {
			age, hasHandshake := parseHandshakeAge(wgResult)
			if !hasHandshake {
				problems = append(problems, "WG has no handshake (never connected)")
			} else if age > 5*time.Minute {
				problems = append(problems, fmt.Sprintf("WG last handshake %s ago", age))
			}
		}
	}

	// If any problems detected, attempt remediation
	if len(problems) > 0 {
		slog.Info("session health check failed",
			"asn", session.ASN, "problems", strings.Join(problems, "; "))
		session.LastError = strings.Join(problems, "; ")
		return s.handleProblemSession(ctx, session)
	}

	return nil
}

// deleteSession removes a peering session
func (s *SessionSync) deleteSession(ctx context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Deleting session AS%d (%s)", session.ASN, session.Name)

	// 1. Remove BIRD configuration
	peerName := fmt.Sprintf("dn42_%d", session.ASN)
	if err := s.birdConfig.RemoveSession(peerName); err != nil {
		log.Printf("[SessionSync] Warning: failed to remove BIRD config: %v", err)
	}

	// 2. Reload BIRD
	if err := s.birdPool.Configure(); err != nil {
		log.Printf("[SessionSync] Warning: BIRD reconfigure failed: %v", err)
	}

	// 3. Remove WireGuard interface
	if session.Type == "wireguard" && session.Interface != "" {
		if err := s.wgExecutor.DeleteInterface(session.Interface); err != nil {
			log.Printf("[SessionSync] Warning: failed to delete WireGuard interface: %v", err)
		}
	}

	// 4. Report deletion to CP
	if err := s.reportStatus(ctx, session.UUID, StatusDeleted, ""); err != nil {
		return fmt.Errorf("failed to report status: %w", err)
	}

	log.Printf("[SessionSync] Session AS%d deleted", session.ASN)
	return nil
}

// handleProblemSession attempts to fix a problematic session by restarting
// the BIRD protocol and/or bouncing the WireGuard interface.
// Each session is limited to 1 remediation attempt per sync cycle to prevent
// restart loops. If remediation fails, the session is reported as StatusProblem
// to the Control Plane.
func (s *SessionSync) handleProblemSession(ctx context.Context, session *BgpSession) error {
	// Rate limit: max 1 remediation per session per sync cycle
	if s.remediatedThisCycle[session.UUID] {
		slog.Debug("skipping remediation, already attempted this cycle",
			"asn", session.ASN, "uuid", session.UUID)
		return nil
	}

	// Cap total remediations per cycle to avoid blocking the sync goroutine
	// with stacked time.Sleep calls (each remediation sleeps 5s for recovery).
	const maxRemediationsPerCycle = 1
	if s.remediationCount >= maxRemediationsPerCycle {
		slog.Info("skipping remediation, cycle budget exhausted (will retry next cycle)",
			"asn", session.ASN, "uuid", session.UUID,
			"budget", maxRemediationsPerCycle)
		return nil
	}
	s.remediatedThisCycle[session.UUID] = true
	s.remediationCount++

	protocolName := fmt.Sprintf("dn42_%d", session.ASN)
	slog.Info("attempting auto-remediation", "asn", session.ASN, "protocol", protocolName)

	// --- Attempt BGP restart ---
	_, err := s.birdPool.Execute("restart \"" + protocolName + "\"")
	if err != nil {
		slog.Warn("BIRD restart failed", "protocol", protocolName, "error", err)
	}

	// --- Attempt WG interface bounce ---
	if session.Interface != "" {
		if err := bounceInterface(session.Interface); err != nil {
			slog.Warn("WG interface bounce failed", "interface", session.Interface, "error", err)
		}
	}

	// Wait briefly for services to recover
	time.Sleep(5 * time.Second)

	// --- Re-check health after remediation ---
	stillBroken := false
	var lastError string

	birdResult, err := s.birdPool.Execute("show protocols all \"" + protocolName + "\"")
	if err != nil {
		slog.Warn("BIRD re-check failed after remediation", "protocol", protocolName, "error", err)
		stillBroken = true
		lastError = "BIRD unreachable after restart"
	} else {
		state, found := parseBIRDProtocolState(birdResult)
		if found && state != "Established" {
			stillBroken = true
			lastError = fmt.Sprintf("BGP stuck in %s after restart", state)
		}
	}

	if session.Interface != "" {
		wgResult, err := s.wgExecutor.GetStatus(session.Interface)
		if err != nil {
			slog.Warn("WG re-check failed after remediation", "interface", session.Interface, "error", err)
			if !stillBroken {
				stillBroken = true
				lastError = "WG interface unavailable after bounce"
			}
		} else {
			age, hasHandshake := parseHandshakeAge(wgResult)
			if !hasHandshake || age > 5*time.Minute {
				if !stillBroken {
					stillBroken = true
					lastError = "WG no handshake after interface bounce"
				}
			}
		}
	}

	// Report to CP if still unhealthy
	if stillBroken {
		slog.Warn("remediation failed, reporting problem to CP",
			"asn", session.ASN, "lastError", lastError)
		if err := s.reportStatus(ctx, session.UUID, StatusProblem, lastError); err != nil {
			slog.Error("failed to report problem status to CP",
				"uuid", session.UUID, "error", err)
		}
	} else {
		slog.Info("remediation successful", "asn", session.ASN)
	}

	return nil
}

// scanOrphanInterfaces detects dn42_ WireGuard interfaces that have no
// matching session in the remote map. Interfaces are tracked across cycles
// and only cleaned up after appearing as orphans for >= 2 consecutive sync
// cycles (grace period to avoid deleting interfaces being created concurrently).
func (s *SessionSync) scanOrphanInterfaces(remoteMap map[string]*BgpSession) {
	systemIfaces := listDN42Interfaces()
	if len(systemIfaces) == 0 {
		return
	}

	// Build set of known interfaces from remote sessions
	knownIfaces := make(map[string]bool, len(remoteMap))
	for _, session := range remoteMap {
		if session.Interface != "" {
			knownIfaces[session.Interface] = true
		}
	}

	// Track new orphans and age existing ones
	currentOrphans := make(map[string]bool)
	for _, iface := range systemIfaces {
		if knownIfaces[iface] {
			continue
		}
		currentOrphans[iface] = true
		s.orphanTracker[iface]++

		if s.orphanTracker[iface] >= 2 {
			slog.Warn("cleaning up orphaned interface (grace period expired)",
				"interface", iface, "cycles_seen", s.orphanTracker[iface])
			if err := s.wgExecutor.DeleteInterface(iface); err != nil {
				slog.Warn("failed to delete orphaned interface",
					"interface", iface, "error", err)
			}
			delete(s.orphanTracker, iface)
		} else {
			slog.Info("orphaned interface detected, tracking",
				"interface", iface, "cycles_seen", s.orphanTracker[iface])
		}
	}

	// Clean stale entries from tracker (interfaces that disappeared)
	for iface := range s.orphanTracker {
		if !currentOrphans[iface] {
			delete(s.orphanTracker, iface)
		}
	}
}

// cleanupDisabledSession removes config for a disabled session
// Unlike deleteSession, it doesn't report back to CP (session stays disabled in DB)
func (s *SessionSync) cleanupDisabledSession(_ context.Context, session *BgpSession) error {
	log.Printf("[SessionSync] Cleaning up disabled session AS%d", session.ASN)

	// 1. Remove BIRD configuration
	peerName := fmt.Sprintf("dn42_%d", session.ASN)
	if err := s.birdConfig.RemoveSession(peerName); err != nil {
		log.Printf("[SessionSync] Warning: failed to remove BIRD config for disabled session: %v", err)
	}

	// 2. Reload BIRD
	if err := s.birdPool.Configure(); err != nil {
		log.Printf("[SessionSync] Warning: BIRD reconfigure failed: %v", err)
	}

	// 3. Remove WireGuard interface if exists
	if session.Type == "wireguard" && session.Interface != "" {
		if err := s.wgExecutor.DeleteInterface(session.Interface); err != nil {
			log.Printf("[SessionSync] Warning: failed to delete WireGuard interface: %v", err)
		}
	}

	// Note: Don't report to CP - session remains disabled until admin action
	return nil
}

// reportStatus reports session status change to Control Plane
func (s *SessionSync) reportStatus(ctx context.Context, sessionUUID string, status int, lastError string) error {
	url := fmt.Sprintf("%s/api/v1/agent/%s/modify", s.config.ControlPlane.URL, s.config.Node.Name)

	payload := map[string]interface{}{
		"uuid":   sessionUUID,
		"status": status,
	}
	if lastError != "" {
		payload["lastError"] = lastError
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
