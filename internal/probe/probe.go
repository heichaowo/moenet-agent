// Package probe provides MTU and latency probing for BGP peers.
package probe

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DN42 latency tier thresholds in milliseconds (upper bound for each tier).
// Reference: https://dn42.eu/howto/Bird-communities
var latencyThresholds = []float64{2.7, 7.3, 20, 55, 148, 403, 1097, 2981}

// LatencyToTier converts RTT in milliseconds to a DN42 latency tier (0-8).
func LatencyToTier(rttMs float64) int {
	for tier, threshold := range latencyThresholds {
		if rttMs < threshold {
			return tier
		}
	}
	return 8 // Highest tier (slowest)
}

// ProbeResult stores the result of a single latency probe.
type ProbeResult struct {
	Target      string  `json:"target"`
	ASN         uint32  `json:"asn"`
	RTTMs       float64 `json:"rtt_ms"`
	LatencyTier int     `json:"latency_tier"`
	Timestamp   int64   `json:"timestamp"` // Unix seconds
	Success     bool    `json:"success"`
	Error       string  `json:"error,omitempty"`
}

// PeerInfo tracks a peer being probed.
type PeerInfo struct {
	ASN        uint32  `json:"asn"`
	Endpoint   string  `json:"endpoint"`
	LastRTT    float64 `json:"last_rtt"`
	LastTier   int     `json:"last_tier"`
	LastProbe  int64   `json:"last_probe"` // Unix seconds
	ProbeCount int     `json:"probe_count"`
	FailCount  int     `json:"fail_count"`
}

// PeerStats contains statistics for a single peer.
type PeerStats struct {
	ASN        uint32          `json:"asn"`
	Endpoint   string          `json:"endpoint"`
	LastRTT    float64         `json:"last_rtt"`
	LastTier   int             `json:"last_tier"`
	LastProbe  int64           `json:"last_probe"`
	ProbeCount int             `json:"probe_count"`
	FailCount  int             `json:"fail_count"`
	Stats      *AggregateStats `json:"stats,omitempty"`
	History    []ProbeResult   `json:"history"`
}

// AggregateStats holds min/max/avg for a peer's probe history.
type AggregateStats struct {
	MinRTT  float64 `json:"min_rtt"`
	MaxRTT  float64 `json:"max_rtt"`
	AvgRTT  float64 `json:"avg_rtt"`
	Samples int     `json:"samples"`
}

// AllStats is the overview returned by GET /probe.
type AllStats struct {
	ProbeInterval int                  `json:"probe_interval"`
	PeerCount     int                  `json:"peer_count"`
	Running       bool                 `json:"running"`
	Paused        bool                 `json:"paused"`
	Peers         map[uint32]PeerStats `json:"peers"`
}

// LatencyProbe continuously measures RTT to peers and maps to DN42 latency tiers.
type LatencyProbe struct {
	// Configuration
	probeInterval time.Duration
	pingCount     int
	timeout       time.Duration
	ewmaAlpha     float64

	// State
	peers   map[uint32]*PeerInfo
	history map[uint32][]ProbeResult
	mu      sync.RWMutex

	// Lifecycle
	running bool
	paused  bool
	stopCh  chan struct{}

	maxHistory int
}

// NewLatencyProbe creates a new latency probe with default settings.
func NewLatencyProbe() *LatencyProbe {
	return &LatencyProbe{
		probeInterval: 300 * time.Second, // 5 minutes
		pingCount:     5,
		timeout:       10 * time.Second,
		ewmaAlpha:     0.3,
		peers:         make(map[uint32]*PeerInfo),
		history:       make(map[uint32][]ProbeResult),
		maxHistory:    100,
	}
}

// AddPeer registers a peer for probing.
func (lp *LatencyProbe) AddPeer(asn uint32, endpoint string) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	if _, exists := lp.peers[asn]; !exists {
		lp.peers[asn] = &PeerInfo{ASN: asn, Endpoint: endpoint}
		lp.history[asn] = nil
		slog.Info("added peer to latency probe", "asn", asn, "endpoint", endpoint)
	} else {
		lp.peers[asn].Endpoint = endpoint
	}
}

// RemovePeer unregisters a peer from probing.
func (lp *LatencyProbe) RemovePeer(asn uint32) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	if _, exists := lp.peers[asn]; exists {
		delete(lp.peers, asn)
		delete(lp.history, asn)
		slog.Info("removed peer from latency probe", "asn", asn)
	}
}

// Run starts the latency probe as a background task following the standard pattern.
func (lp *LatencyProbe) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	lp.mu.Lock()
	lp.running = true
	lp.stopCh = make(chan struct{})
	lp.mu.Unlock()

	ticker := time.NewTicker(lp.probeInterval)
	defer ticker.Stop()

	// Initial probe
	slog.Info("latency probe started", "interval", lp.probeInterval)
	lp.probeAll(ctx)

	for {
		select {
		case <-ctx.Done():
			lp.mu.Lock()
			lp.running = false
			lp.mu.Unlock()
			slog.Info("latency probe stopped")
			return
		case <-lp.stopCh:
			lp.mu.Lock()
			lp.running = false
			lp.mu.Unlock()
			slog.Info("latency probe stopped via API")
			return
		case <-ticker.C:
			lp.mu.RLock()
			isPaused := lp.paused
			lp.mu.RUnlock()
			if !isPaused {
				lp.probeAll(ctx)
			}
		}
	}
}

// Pause pauses the probe daemon without stopping the goroutine.
func (lp *LatencyProbe) Pause() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.paused = true
	slog.Info("latency probe paused")
}

// Resume resumes the probe daemon.
func (lp *LatencyProbe) Resume() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.paused = false
	slog.Info("latency probe resumed")
}

// IsRunning returns whether the probe daemon is running.
func (lp *LatencyProbe) IsRunning() bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.running
}

// IsPaused returns whether the probe daemon is paused.
func (lp *LatencyProbe) IsPaused() bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.paused
}

// ProbeNow immediately probes a specific peer (synchronous).
func (lp *LatencyProbe) ProbeNow(asn uint32) *ProbeResult {
	lp.mu.RLock()
	peer, exists := lp.peers[asn]
	if !exists {
		lp.mu.RUnlock()
		return nil
	}
	endpoint := peer.Endpoint
	lp.mu.RUnlock()

	rttMs, err := lp.ping(endpoint)
	now := time.Now().Unix()

	if err != nil {
		result := &ProbeResult{
			Target:      endpoint,
			ASN:         asn,
			RTTMs:       0,
			LatencyTier: 8,
			Timestamp:   now,
			Success:     false,
			Error:       err.Error(),
		}

		lp.mu.Lock()
		if p, ok := lp.peers[asn]; ok {
			p.FailCount++
		}
		lp.mu.Unlock()

		return result
	}

	tier := LatencyToTier(rttMs)
	result := &ProbeResult{
		Target:      endpoint,
		ASN:         asn,
		RTTMs:       math.Round(rttMs*100) / 100,
		LatencyTier: tier,
		Timestamp:   now,
		Success:     true,
	}

	lp.mu.Lock()
	if p, ok := lp.peers[asn]; ok {
		p.LastRTT = rttMs
		p.LastTier = tier
		p.LastProbe = now
		p.ProbeCount++
	}
	lp.appendHistory(asn, *result)
	lp.mu.Unlock()

	return result
}

// GetPeerStats returns stats for a single peer (thread-safe).
func (lp *LatencyProbe) GetPeerStats(asn uint32) *PeerStats {
	lp.mu.RLock()
	defer lp.mu.RUnlock()

	peer, exists := lp.peers[asn]
	if !exists {
		return nil
	}

	stats := &PeerStats{
		ASN:        peer.ASN,
		Endpoint:   peer.Endpoint,
		LastRTT:    peer.LastRTT,
		LastTier:   peer.LastTier,
		LastProbe:  peer.LastProbe,
		ProbeCount: peer.ProbeCount,
		FailCount:  peer.FailCount,
		History:    []ProbeResult{},
	}

	history := lp.history[asn]
	successful := filterSuccessful(history)

	if len(successful) > 0 {
		var minRTT, maxRTT, sumRTT float64
		minRTT = math.MaxFloat64
		for _, r := range successful {
			if r.RTTMs < minRTT {
				minRTT = r.RTTMs
			}
			if r.RTTMs > maxRTT {
				maxRTT = r.RTTMs
			}
			sumRTT += r.RTTMs
		}
		stats.Stats = &AggregateStats{
			MinRTT:  math.Round(minRTT*100) / 100,
			MaxRTT:  math.Round(maxRTT*100) / 100,
			AvgRTT:  math.Round(sumRTT/float64(len(successful))*100) / 100,
			Samples: len(successful),
		}
	}

	// Return last 10 results
	if len(successful) > 10 {
		stats.History = successful[len(successful)-10:]
	} else {
		stats.History = successful
	}

	return stats
}

// GetAllStats returns statistics for all peers.
func (lp *LatencyProbe) GetAllStats() *AllStats {
	lp.mu.RLock()
	asns := make([]uint32, 0, len(lp.peers))
	for asn := range lp.peers {
		asns = append(asns, asn)
	}
	running := lp.running
	paused := lp.paused
	lp.mu.RUnlock()

	peers := make(map[uint32]PeerStats, len(asns))
	for _, asn := range asns {
		if ps := lp.GetPeerStats(asn); ps != nil {
			peers[asn] = *ps
		}
	}

	return &AllStats{
		ProbeInterval: int(lp.probeInterval.Seconds()),
		PeerCount:     len(peers),
		Running:       running,
		Paused:        paused,
		Peers:         peers,
	}
}

// UpdatePeersFromSessions auto-populates the peer list from eBGP sessions.
// Called by SessionSync after each sync cycle.
func (lp *LatencyProbe) UpdatePeersFromSessions(sessions []SessionInfo) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	seen := make(map[uint32]bool, len(sessions))
	for _, s := range sessions {
		if s.Endpoint == "" {
			continue
		}
		seen[s.ASN] = true
		if _, exists := lp.peers[s.ASN]; !exists {
			lp.peers[s.ASN] = &PeerInfo{ASN: s.ASN, Endpoint: s.Endpoint}
			lp.history[s.ASN] = nil
			slog.Debug("auto-added peer to latency probe", "asn", s.ASN, "endpoint", s.Endpoint)
		} else {
			lp.peers[s.ASN].Endpoint = s.Endpoint
		}
	}

	// NOTE: We do NOT auto-remove peers that disappear from sessions.
	// Manual add/remove via API is respected. Stale peers will keep being probed
	// until explicitly removed.
}

// SessionInfo is a lightweight struct for session data needed by the probe.
type SessionInfo struct {
	ASN      uint32
	Endpoint string
}

// probeAll measures RTT to all registered peers concurrently.
func (lp *LatencyProbe) probeAll(ctx context.Context) {
	lp.mu.RLock()
	if len(lp.peers) == 0 {
		lp.mu.RUnlock()
		return
	}
	// Snapshot peers
	type peerSnapshot struct {
		asn      uint32
		endpoint string
	}
	peers := make([]peerSnapshot, 0, len(lp.peers))
	for _, p := range lp.peers {
		peers = append(peers, peerSnapshot{asn: p.ASN, endpoint: p.Endpoint})
	}
	lp.mu.RUnlock()

	slog.Debug("probing peers", "count", len(peers))

	var wg sync.WaitGroup
	results := make([]ProbeResult, len(peers))

	for i, p := range peers {
		wg.Add(1)
		go func(idx int, asn uint32, endpoint string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			rttMs, err := lp.ping(endpoint)
			now := time.Now().Unix()

			if err != nil {
				results[idx] = ProbeResult{
					Target:      endpoint,
					ASN:         asn,
					RTTMs:       0,
					LatencyTier: 8,
					Timestamp:   now,
					Success:     false,
					Error:       err.Error(),
				}
				return
			}

			tier := LatencyToTier(rttMs)
			results[idx] = ProbeResult{
				Target:      endpoint,
				ASN:         asn,
				RTTMs:       math.Round(rttMs*100) / 100,
				LatencyTier: tier,
				Timestamp:   now,
				Success:     true,
			}
		}(i, p.asn, p.endpoint)
	}
	wg.Wait()

	// Update state with results
	lp.mu.Lock()
	for _, r := range results {
		if r.ASN == 0 {
			continue // skipped due to ctx cancel
		}
		peer, exists := lp.peers[r.ASN]
		if !exists {
			continue
		}

		if r.Success {
			// Apply EWMA smoothing
			if peer.ProbeCount > 0 && peer.LastRTT > 0 {
				peer.LastRTT = lp.ewmaAlpha*r.RTTMs + (1-lp.ewmaAlpha)*peer.LastRTT
			} else {
				peer.LastRTT = r.RTTMs
			}
			peer.LastTier = LatencyToTier(peer.LastRTT)
			peer.LastProbe = r.Timestamp
			peer.ProbeCount++
		} else {
			peer.FailCount++
		}
		lp.appendHistory(r.ASN, r)
	}
	lp.mu.Unlock()

	slog.Info("latency probe cycle complete", "probed", len(peers))
}

// appendHistory adds a result to history, capping at maxHistory. Must hold lp.mu write lock.
func (lp *LatencyProbe) appendHistory(asn uint32, result ProbeResult) {
	lp.history[asn] = append(lp.history[asn], result)
	if len(lp.history[asn]) > lp.maxHistory {
		lp.history[asn] = lp.history[asn][len(lp.history[asn])-lp.maxHistory:]
	}
}

// ping executes ICMP ping to a target and returns average RTT in ms.
func (lp *LatencyProbe) ping(target string) (float64, error) {
	// target is a WireGuard endpoint, typically "host:port" (or "[v6]:port").
	// ping wants the bare host — strip the port so we don't hand ping a colon
	// that isn't an IPv6 separator (was producing "ping6 host:port" → exit 2).
	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}

	isIPv6 := strings.Contains(host, ":")
	pingCmd := "ping"
	if isIPv6 {
		pingCmd = "ping6"
	}

	timeoutSec := strconv.Itoa(int(lp.timeout.Seconds()))
	countStr := strconv.Itoa(lp.pingCount)

	ctx, cancel := context.WithTimeout(context.Background(),
		lp.timeout*time.Duration(lp.pingCount)+5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pingCmd, "-c", countStr, "-W", timeoutSec, host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ping failed: %w", err)
	}

	return parsePingRTT(string(output))
}

// rttPattern matches "rtt min/avg/max/mdev = X/Y/Z/W ms"
var rttPattern = regexp.MustCompile(`rtt min/avg/max/mdev = [\d.]+/([\d.]+)/[\d.]+/[\d.]+ ms`)

// rttAltPattern matches "min/avg/max = X/Y/Z ms"
var rttAltPattern = regexp.MustCompile(`min/avg/max = [\d.]+/([\d.]+)/[\d.]+ ms`)

// parsePingRTT extracts average RTT from ping output.
func parsePingRTT(output string) (float64, error) {
	match := rttPattern.FindStringSubmatch(output)
	if match != nil {
		return strconv.ParseFloat(match[1], 64)
	}
	match = rttAltPattern.FindStringSubmatch(output)
	if match != nil {
		return strconv.ParseFloat(match[1], 64)
	}
	return 0, fmt.Errorf("failed to parse RTT from ping output")
}

// filterSuccessful returns only successful probe results.
func filterSuccessful(results []ProbeResult) []ProbeResult {
	filtered := make([]ProbeResult, 0, len(results))
	for _, r := range results {
		if r.Success {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
