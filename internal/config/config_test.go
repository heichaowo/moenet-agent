package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configContent := `{
		"server": {
			"listen": ":9090",
			"readTimeout": 15,
			"writeTimeout": 15,
			"idleTimeout": 60
		},
		"node": {
			"name": "test-node",
			"id": 1,
			"region": "test"
		},
		"controlPlane": {
			"url": "https://test.example.com",
			"token": "test-token",
			"requestTimeout": 10,
			"heartbeatInterval": 30,
			"syncInterval": 60,
			"metricInterval": 60
		},
		"bird": {
			"controlSocket": "/tmp/bird.ctl",
			"poolSize": 3,
			"poolSizeMax": 10,
			"peerConfDir": "/tmp/peers",
			"ebgpConfTemplateFile": "/tmp/ebgp.tmpl",
			"ibgpConfDir": "/tmp/ibgp"
		},
		"wireguard": {
			"privateKeyPath": "/tmp/privatekey",
			"configDir": "/tmp/wg"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Test loading
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify values
	if cfg.Server.Listen != ":9090" {
		t.Errorf("Expected listen :9090, got %s", cfg.Server.Listen)
	}
	if cfg.Node.Name != "test-node" {
		t.Errorf("Expected node name test-node, got %s", cfg.Node.Name)
	}
	if cfg.Node.ID != 1 {
		t.Errorf("Expected node ID 1, got %d", cfg.Node.ID)
	}
	if cfg.ControlPlane.URL != "https://test.example.com" {
		t.Errorf("Expected CP URL https://test.example.com, got %s", cfg.ControlPlane.URL)
	}
	if cfg.Bird.PoolSize != 3 {
		t.Errorf("Expected pool size 3, got %d", cfg.Bird.PoolSize)
	}
}

func TestLoadDefaults(t *testing.T) {
	// Create minimal config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configContent := `{
		"node": {"name": "minimal"},
		"controlPlane": {"url": "https://cp.test", "token": "tok"}
	}`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Check defaults are applied
	if cfg.Server.Listen != ":8080" {
		t.Errorf("Expected default listen :8080, got %s", cfg.Server.Listen)
	}
	if cfg.ControlPlane.HeartbeatInterval != 30 {
		t.Errorf("Expected default heartbeat 30, got %d", cfg.ControlPlane.HeartbeatInterval)
	}
	if cfg.Bird.PoolSize != 5 {
		t.Errorf("Expected default pool size 5, got %d", cfg.Bird.PoolSize)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.json")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")

	if err := os.WriteFile(configPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}
