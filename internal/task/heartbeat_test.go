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

	if payload.MeshPublicKey != "test-mesh-public-key-12345" {
		t.Errorf("Expected meshPublicKey test-mesh-public-key-12345, got %s", payload.MeshPublicKey)
	}
	if payload.Version != "v2.1.0" {
		t.Errorf("Expected version v2.1.0, got %s", payload.Version)
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
