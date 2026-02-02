package firewall

import (
	"log/slog"
	"os"
	"testing"
)

func TestNewExecutor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	e := NewExecutor(logger)

	if e.chain != "INPUT" {
		t.Errorf("Expected chain INPUT, got %s", e.chain)
	}
	if e.commentPrefix != "moenet-dn42" {
		t.Errorf("Expected commentPrefix moenet-dn42, got %s", e.commentPrefix)
	}
}

// Note: Full integration tests require root privileges and iptables.
// These tests verify the structure and basic logic only.
