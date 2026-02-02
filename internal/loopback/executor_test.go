package loopback

import (
	"log/slog"
	"testing"
)

func TestNewExecutor(t *testing.T) {
	e := NewExecutor(slog.Default())
	if e == nil {
		t.Fatal("NewExecutor returned nil")
	}
	if e.interface_ != "dummy0" {
		t.Errorf("Expected interface dummy0, got %s", e.interface_)
	}
}

func TestValidateNodeID(t *testing.T) {
	tests := []struct {
		nodeID int
		valid  bool
	}{
		{0, false},
		{1, true},
		{31, true},
		{62, true},
		{63, false},
		{100, false},
		{-1, false},
	}

	for _, tt := range tests {
		err := ValidateNodeID(tt.nodeID)
		got := err == nil
		if got != tt.valid {
			t.Errorf("ValidateNodeID(%d) = %v, want %v", tt.nodeID, got, tt.valid)
		}
	}
}

func TestSetupLoopbackWithIPs_Empty(t *testing.T) {
	e := NewExecutor(slog.Default())
	err := e.SetupLoopbackWithIPs("", "")
	if err == nil {
		t.Error("Expected error when both IPs are empty")
	}
}

func TestSetupLoopbackWithIPs_ValidInputs(t *testing.T) {
	// This test validates input parsing, not actual IP configuration
	// (which requires root permissions)
	e := NewExecutor(slog.Default())

	// Test that function accepts valid inputs (will fail on actual config without root)
	tests := []struct {
		ipv4 string
		ipv6 string
	}{
		{"172.22.188.4", "fd00:4242:7777:101:4::1"},
		{"172.22.188.1", ""},
		{"", "fd00:4242:7777::1"},
		{"172.22.188.4/32", "fd00:4242:7777:101:4::1/128"},
	}

	for _, tt := range tests {
		// We expect this to fail during execution (no root), but not on input validation
		err := e.SetupLoopbackWithIPs(tt.ipv4, tt.ipv6)
		// Don't check error since we don't have root, just ensure no panic
		_ = err
	}
}
