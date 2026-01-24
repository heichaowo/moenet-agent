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
	if payload.Uptime != 86400 {
		t.Errorf("Expected uptime 86400, got %d", payload.Uptime)
	}
}
