package task

import (
	"testing"
)

func TestHeartbeatPayloadWithMeshPublicKey(t *testing.T) {
	payload := HeartbeatPayload{
		Version:       "v2.1.0",
		Kernel:        "5.15.0",
		LoadAvg:       "0.50 0.40 0.30",
		Uptime:        86400,
		Timestamp:     1700000000,
		TxBytes:       1000000,
		RxBytes:       2000000,
		TCPConns:      50,
		UDPConns:      10,
		MeshPublicKey: "test-mesh-public-key-12345",
	}

	// Verify all fields are set correctly
	if payload.MeshPublicKey != "test-mesh-public-key-12345" {
		t.Errorf("Expected meshPublicKey test-mesh-public-key-12345, got %s", payload.MeshPublicKey)
	}
	if payload.Version != "v2.1.0" {
		t.Errorf("Expected version v2.1.0, got %s", payload.Version)
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

func TestHeartbeatPayloadEmpty(t *testing.T) {
	payload := HeartbeatPayload{}

	if payload.MeshPublicKey != "" {
		t.Errorf("Expected empty meshPublicKey, got %s", payload.MeshPublicKey)
	}
	if payload.Uptime != 0 {
		t.Errorf("Expected zero uptime, got %d", payload.Uptime)
	}
}
