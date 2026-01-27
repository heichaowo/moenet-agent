package task

import (
	"testing"
)

func TestSessionStatusConstants(t *testing.T) {
	tests := []struct {
		status   int
		expected int
	}{
		{StatusDeleted, 0},
		{StatusDisabled, 1},
		{StatusEnabled, 2},
		{StatusPendingApproval, 3},
		{StatusQueuedForSetup, 4},
		{StatusQueuedForDelete, 5},
		{StatusProblem, 6},
		{StatusTeardown, 7},
	}

	for _, tt := range tests {
		if tt.status != tt.expected {
			t.Errorf("Status constant mismatch: got %d, expected %d", tt.status, tt.expected)
		}
	}
}

func TestBgpSessionStruct(t *testing.T) {
	session := BgpSession{
		UUID:   "test-uuid",
		ASN:    4242420919,
		Name:   "Test Peer",
		Status: StatusEnabled,
		Type:   "wireguard",
	}

	if session.UUID != "test-uuid" {
		t.Errorf("Expected UUID test-uuid, got %s", session.UUID)
	}
	if session.ASN != 4242420919 {
		t.Errorf("Expected ASN 4242420919, got %d", session.ASN)
	}
	if session.Status != StatusEnabled {
		t.Errorf("Expected status %d, got %d", StatusEnabled, session.Status)
	}
	if session.Name != "Test Peer" {
		t.Errorf("Expected Name 'Test Peer', got %s", session.Name)
	}
	if session.Type != "wireguard" {
		t.Errorf("Expected Type wireguard, got %s", session.Type)
	}
}

func TestMeshPeerStruct(t *testing.T) {
	peer := MeshPeer{
		NodeID:       1,
		NodeName:     "jp-edge",
		LoopbackIPv4: "172.22.188.1",
		LoopbackIPv6: "fd00:4242:7777::1",
		PublicKey:    "test-pubkey",
		IsRR:         false,
	}

	if peer.NodeID != 1 {
		t.Errorf("Expected NodeID 1, got %d", peer.NodeID)
	}
	if peer.NodeName != "jp-edge" {
		t.Errorf("Expected NodeName jp-edge, got %s", peer.NodeName)
	}
	if peer.LoopbackIPv4 != "172.22.188.1" {
		t.Errorf("Expected LoopbackIPv4 172.22.188.1, got %s", peer.LoopbackIPv4)
	}
	if peer.LoopbackIPv6 != "fd00:4242:7777::1" {
		t.Errorf("Expected LoopbackIPv6 fd00:4242:7777::1, got %s", peer.LoopbackIPv6)
	}
	if peer.PublicKey != "test-pubkey" {
		t.Errorf("Expected PublicKey test-pubkey, got %s", peer.PublicKey)
	}
	if peer.IsRR {
		t.Error("Expected IsRR to be false")
	}
}

func TestHeartbeatPayload(t *testing.T) {
	payload := HeartbeatPayload{
		Version:   "v2.0.0",
		Kernel:    "5.15.0",
		LoadAvg:   "0.50 0.40 0.30",
		Uptime:    86400,
		Timestamp: 1700000000,
		TxBytes:   1000000,
		RxBytes:   2000000,
		TCPConns:  50,
		UDPConns:  10,
	}

	if payload.Version != "v2.0.0" {
		t.Errorf("Expected version v2.0.0, got %s", payload.Version)
	}
	if payload.Kernel != "5.15.0" {
		t.Errorf("Expected kernel 5.15.0, got %s", payload.Kernel)
	}
	if payload.LoadAvg != "0.50 0.40 0.30" {
		t.Errorf("Expected loadAvg 0.50 0.40 0.30, got %s", payload.LoadAvg)
	}
	if payload.Uptime != 86400 {
		t.Errorf("Expected uptime 86400, got %d", payload.Uptime)
	}
	if payload.Timestamp != 1700000000 {
		t.Errorf("Expected timestamp 1700000000, got %d", payload.Timestamp)
	}
	if payload.TxBytes != 1000000 {
		t.Errorf("Expected txBytes 1000000, got %d", payload.TxBytes)
	}
	if payload.RxBytes != 2000000 {
		t.Errorf("Expected rxBytes 2000000, got %d", payload.RxBytes)
	}
	if payload.TCPConns != 50 {
		t.Errorf("Expected tcpConns 50, got %d", payload.TCPConns)
	}
	if payload.UDPConns != 10 {
		t.Errorf("Expected udpConns 10, got %d", payload.UDPConns)
	}
}
