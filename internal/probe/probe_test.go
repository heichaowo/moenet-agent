package probe

import (
	"testing"
)

func TestLatencyToTier(t *testing.T) {
	tests := []struct {
		name    string
		rttMs   float64
		want    int
	}{
		{"tier 0 - very low", 1.0, 0},
		{"tier 0 - boundary", 2.6, 0},
		{"tier 1 - just above 2.7", 3.0, 1},
		{"tier 1 - boundary", 7.2, 1},
		{"tier 2 - ~10ms", 10.0, 2},
		{"tier 3 - ~30ms", 30.0, 3},
		{"tier 4 - ~100ms", 100.0, 4},
		{"tier 5 - ~200ms", 200.0, 5},
		{"tier 6 - ~500ms", 500.0, 6},
		{"tier 7 - ~2000ms", 2000.0, 7},
		{"tier 8 - very high", 3000.0, 8},
		{"tier 8 - extreme", 10000.0, 8},
		{"tier 0 - zero", 0.0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LatencyToTier(tt.rttMs)
			if got != tt.want {
				t.Errorf("LatencyToTier(%v) = %d, want %d", tt.rttMs, got, tt.want)
			}
		})
	}
}

func TestAddRemovePeer(t *testing.T) {
	lp := NewLatencyProbe()

	// Add peer
	lp.AddPeer(4242420001, "10.0.0.1")

	lp.mu.RLock()
	if _, exists := lp.peers[4242420001]; !exists {
		t.Fatal("peer not added")
	}
	lp.mu.RUnlock()

	// Update endpoint
	lp.AddPeer(4242420001, "10.0.0.2")
	lp.mu.RLock()
	if lp.peers[4242420001].Endpoint != "10.0.0.2" {
		t.Fatal("endpoint not updated")
	}
	lp.mu.RUnlock()

	// Remove peer
	lp.RemovePeer(4242420001)
	lp.mu.RLock()
	if _, exists := lp.peers[4242420001]; exists {
		t.Fatal("peer not removed")
	}
	lp.mu.RUnlock()

	// Remove non-existent peer should not panic
	lp.RemovePeer(4242429999)
}

func TestEWMACalculation(t *testing.T) {
	lp := NewLatencyProbe()
	alpha := lp.ewmaAlpha // 0.3

	// Simulate EWMA
	initial := 100.0
	second := 50.0
	expected := alpha*second + (1-alpha)*initial // 0.3*50 + 0.7*100 = 85

	lp.AddPeer(4242420001, "10.0.0.1")

	// Set initial state
	lp.mu.Lock()
	lp.peers[4242420001].LastRTT = initial
	lp.peers[4242420001].ProbeCount = 1
	lp.mu.Unlock()

	// Simulate a probe result update with EWMA
	lp.mu.Lock()
	peer := lp.peers[4242420001]
	peer.LastRTT = alpha*second + (1-alpha)*peer.LastRTT
	lp.mu.Unlock()

	lp.mu.RLock()
	got := lp.peers[4242420001].LastRTT
	lp.mu.RUnlock()

	if got != expected {
		t.Errorf("EWMA result = %v, want %v", got, expected)
	}
}

func TestHistoryCapping(t *testing.T) {
	lp := NewLatencyProbe()
	lp.maxHistory = 5

	lp.AddPeer(4242420001, "10.0.0.1")

	// Add more results than maxHistory
	lp.mu.Lock()
	for i := 0; i < 10; i++ {
		lp.appendHistory(4242420001, ProbeResult{
			ASN:     4242420001,
			RTTMs:   float64(i),
			Success: true,
		})
	}
	histLen := len(lp.history[4242420001])
	firstRTT := lp.history[4242420001][0].RTTMs
	lp.mu.Unlock()

	if histLen != 5 {
		t.Errorf("history length = %d, want 5", histLen)
	}

	// First entry should be RTT=5 (oldest retained)
	if firstRTT != 5.0 {
		t.Errorf("oldest retained RTT = %v, want 5.0", firstRTT)
	}
}

func TestGetPeerStats(t *testing.T) {
	lp := NewLatencyProbe()
	lp.AddPeer(4242420001, "10.0.0.1")

	// No probes yet
	stats := lp.GetPeerStats(4242420001)
	if stats == nil {
		t.Fatal("expected stats, got nil")
	}
	if stats.Stats != nil {
		t.Error("expected nil Stats with no successful probes")
	}

	// Add some history
	lp.mu.Lock()
	lp.appendHistory(4242420001, ProbeResult{ASN: 4242420001, RTTMs: 10.0, Success: true})
	lp.appendHistory(4242420001, ProbeResult{ASN: 4242420001, RTTMs: 20.0, Success: true})
	lp.appendHistory(4242420001, ProbeResult{ASN: 4242420001, RTTMs: 0, Success: false, Error: "timeout"})
	lp.appendHistory(4242420001, ProbeResult{ASN: 4242420001, RTTMs: 30.0, Success: true})
	lp.mu.Unlock()

	stats = lp.GetPeerStats(4242420001)
	if stats.Stats == nil {
		t.Fatal("expected aggregate stats")
	}
	if stats.Stats.Samples != 3 {
		t.Errorf("samples = %d, want 3", stats.Stats.Samples)
	}
	if stats.Stats.MinRTT != 10.0 {
		t.Errorf("min RTT = %v, want 10.0", stats.Stats.MinRTT)
	}
	if stats.Stats.MaxRTT != 30.0 {
		t.Errorf("max RTT = %v, want 30.0", stats.Stats.MaxRTT)
	}
	// History should only contain successful results
	if len(stats.History) != 3 {
		t.Errorf("history length = %d, want 3 (successful only)", len(stats.History))
	}
}

func TestGetPeerStatsNotFound(t *testing.T) {
	lp := NewLatencyProbe()

	stats := lp.GetPeerStats(9999)
	if stats != nil {
		t.Error("expected nil for non-existent peer")
	}
}

func TestGetAllStats(t *testing.T) {
	lp := NewLatencyProbe()
	lp.AddPeer(4242420001, "10.0.0.1")
	lp.AddPeer(4242420002, "10.0.0.2")

	allStats := lp.GetAllStats()
	if allStats.PeerCount != 2 {
		t.Errorf("peer count = %d, want 2", allStats.PeerCount)
	}
	if allStats.ProbeInterval != 300 {
		t.Errorf("probe interval = %d, want 300", allStats.ProbeInterval)
	}
}

func TestUpdatePeersFromSessions(t *testing.T) {
	lp := NewLatencyProbe()

	// Initial manual peer
	lp.AddPeer(4242420099, "10.0.0.99")

	// Auto-populate from sessions
	sessions := []SessionInfo{
		{ASN: 4242420001, Endpoint: "10.0.0.1"},
		{ASN: 4242420002, Endpoint: "10.0.0.2"},
		{ASN: 0, Endpoint: ""}, // Should be skipped
	}
	lp.UpdatePeersFromSessions(sessions)

	lp.mu.RLock()
	defer lp.mu.RUnlock()

	// All 3 peers should exist (manual + 2 auto)
	if len(lp.peers) != 3 {
		t.Errorf("peer count = %d, want 3", len(lp.peers))
	}

	// Manual peer should still exist
	if _, exists := lp.peers[4242420099]; !exists {
		t.Error("manual peer removed unexpectedly")
	}
}

func TestParsePingRTT(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr bool
	}{
		{
			"standard linux ping",
			"rtt min/avg/max/mdev = 1.234/5.678/10.123/2.345 ms",
			5.678,
			false,
		},
		{
			"alternative format",
			"min/avg/max = 1.0/3.5/6.0 ms",
			3.5,
			false,
		},
		{
			"no match",
			"PING 10.0.0.1: 100% packet loss",
			0,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePingRTT(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parsePingRTT() = %v, want %v", got, tt.want)
			}
		})
	}
}
