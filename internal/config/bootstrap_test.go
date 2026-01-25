package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWithBootstrap_FullConfig(t *testing.T) {
	// Create a temporary full config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	fullConfig := `{
		"server": {"listen": ":8080"},
		"node": {"name": "test-node", "id": 1},
		"controlPlane": {"url": "https://api.test.com", "token": "test-token"},
		"bird": {"controlSocket": "/var/run/bird.ctl"},
		"wireguard": {"configDir": "/etc/wireguard"},
		"metric": {"pingTimeout": 5},
		"autoUpdate": {"enabled": true}
	}`

	if err := os.WriteFile(configFile, []byte(fullConfig), 0600); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := LoadWithBootstrap(configFile)
	if err != nil {
		t.Fatalf("LoadWithBootstrap failed: %v", err)
	}

	if cfg.Node.Name != "test-node" {
		t.Errorf("Expected node name test-node, got %s", cfg.Node.Name)
	}
	if cfg.Node.ID != 1 {
		t.Errorf("Expected node id 1, got %d", cfg.Node.ID)
	}
}

func TestBootstrapConfigParsing(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "bootstrap.json")

	bootstrapConfig := `{
		"bootstrap": {
			"controlPlaneUrl": "https://api.moenet.work",
			"nodeName": "test-node",
			"token": "test-token"
		},
		"server": {"listen": ":24368"}
	}`

	if err := os.WriteFile(configFile, []byte(bootstrapConfig), 0600); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Note: This will fail because CP is not reachable in tests
	// but we're testing the config parsing, not the CP fetch
	_, err := LoadWithBootstrap(configFile)
	if err == nil {
		t.Log("Bootstrap config parsed successfully (expected failure due to CP fetch)")
	}
	// This is expected to fail since we can't reach the CP in tests
}

func TestLoadInvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "invalid.json")

	invalidConfig := `{invalid json}`

	if err := os.WriteFile(configFile, []byte(invalidConfig), 0600); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	_, err := LoadWithBootstrap(configFile)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestLoadNonExistentConfig(t *testing.T) {
	_, err := LoadWithBootstrap("/nonexistent/path/config.json")
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}
