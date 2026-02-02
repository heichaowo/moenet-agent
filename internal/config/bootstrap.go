// Package config provides agent configuration loading with CP bootstrap support
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// BootstrapConfig is the minimal local configuration for bootstrap mode
type BootstrapConfig struct {
	Bootstrap struct {
		APIURL   string `json:"apiUrl"`
		NodeName string `json:"nodeName"`
		Token    string `json:"token"`
	} `json:"bootstrap"`
	Server ServerConfig `json:"server"`
}

// LoadWithBootstrap loads minimal local config and fetches full config from CP
func LoadWithBootstrap(path string) (*Config, error) {
	// First try to load as full config
	fullCfg, err := Load(path)
	if err == nil && fullCfg.Node.Name != "" && fullCfg.WireGuard.DN42IPv4 != "" {
		// Full config loaded successfully with complete WireGuard config, use it
		log.Printf("[Config] Loaded full config for node %s", fullCfg.Node.Name)
		return fullCfg, nil
	}

	// Try bootstrap mode
	log.Printf("[Config] Attempting bootstrap mode...")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var bootstrap BootstrapConfig
	if err := json.Unmarshal(data, &bootstrap); err != nil {
		return nil, fmt.Errorf("failed to parse bootstrap config: %w", err)
	}

	if bootstrap.Bootstrap.APIURL == "" || bootstrap.Bootstrap.NodeName == "" {
		return nil, fmt.Errorf("bootstrap config missing required fields (apiUrl, nodeName)")
	}

	log.Printf("[Config] Bootstrap: fetching config from %s for node %s",
		bootstrap.Bootstrap.APIURL, bootstrap.Bootstrap.NodeName)

	// Fetch config from control plane
	remoteCfg, err := fetchConfigFromCP(bootstrap)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config from control plane: %w", err)
	}

	log.Printf("[Config] Bootstrap: received config with node.id=%d, dn42IPv4=%s",
		remoteCfg.Node.ID, remoteCfg.WireGuard.DN42IPv4)

	// Merge local and remote config
	cfg := mergeConfig(bootstrap, remoteCfg)

	return cfg, nil
}

// fetchConfigFromCP fetches agent configuration from control plane
func fetchConfigFromCP(bootstrap BootstrapConfig) (*RemoteConfig, error) {
	url := fmt.Sprintf("%s/api/v1/agent/%s/config",
		bootstrap.Bootstrap.APIURL,
		bootstrap.Bootstrap.NodeName,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+bootstrap.Bootstrap.Token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Code int          `json:"code"`
		Data RemoteConfig `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// RemoteConfig represents the config returned by CP
type RemoteConfig struct {
	Node       NodeConfig       `json:"node"`
	Bird       BirdConfig       `json:"bird"`
	WireGuard  WireGuardConfig  `json:"wireguard"`
	Metric     MetricConfig     `json:"metric"`
	AutoUpdate AutoUpdateConfig `json:"autoUpdate"`
}

// mergeConfig merges bootstrap and remote config into final config
func mergeConfig(bootstrap BootstrapConfig, remote *RemoteConfig) *Config {
	cfg := &Config{
		Server:     bootstrap.Server,
		Node:       remote.Node,
		Bird:       remote.Bird,
		WireGuard:  remote.WireGuard,
		Metric:     remote.Metric,
		AutoUpdate: remote.AutoUpdate,
		ControlPlane: ControlPlaneConfig{
			URL:   bootstrap.Bootstrap.APIURL,
			Token: bootstrap.Bootstrap.Token,
		},
	}

	// Set defaults
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = ":8080"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 120
	}
	if cfg.ControlPlane.RequestTimeout == 0 {
		cfg.ControlPlane.RequestTimeout = 15
	}
	if cfg.ControlPlane.HeartbeatInterval == 0 {
		cfg.ControlPlane.HeartbeatInterval = 30
	}
	if cfg.ControlPlane.SyncInterval == 0 {
		cfg.ControlPlane.SyncInterval = 60
	}
	if cfg.ControlPlane.MetricInterval == 0 {
		cfg.ControlPlane.MetricInterval = 60
	}
	if cfg.ControlPlane.MaxRetries == 0 {
		cfg.ControlPlane.MaxRetries = 3
	}
	if cfg.ControlPlane.RetryInitialDelay == 0 {
		cfg.ControlPlane.RetryInitialDelay = 1000
	}

	return cfg
}
