package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config represents the agent configuration
type Config struct {
	Server       ServerConfig       `json:"server"`
	Node         NodeConfig         `json:"node"`
	ControlPlane ControlPlaneConfig `json:"controlPlane"`
	Bird         BirdConfig         `json:"bird"`
	WireGuard    WireGuardConfig    `json:"wireguard"`
	Metric       MetricConfig       `json:"metric"`
	AutoUpdate   AutoUpdateConfig   `json:"autoUpdate"`
}

// ServerConfig contains HTTP server settings
type ServerConfig struct {
	Listen       string `json:"listen"`
	ReadTimeout  int    `json:"readTimeout"`
	WriteTimeout int    `json:"writeTimeout"`
	IdleTimeout  int    `json:"idleTimeout"`
}

// NodeConfig contains node identity settings
type NodeConfig struct {
	Name     string `json:"name"`
	ID       int    `json:"id"`
	Region   string `json:"region"`
	Location string `json:"location"`
	Provider string `json:"provider"`
}

// ControlPlaneConfig contains CP communication settings
type ControlPlaneConfig struct {
	URL               string `json:"url"`
	Token             string `json:"token"`
	RequestTimeout    int    `json:"requestTimeout"`
	HeartbeatInterval int    `json:"heartbeatInterval"`
	SyncInterval      int    `json:"syncInterval"`
	MetricInterval    int    `json:"metricInterval"`
	// Retry settings
	MaxRetries        int `json:"maxRetries"`
	RetryInitialDelay int `json:"retryInitialDelay"` // milliseconds
}

// BirdConfig contains BIRD integration settings
type BirdConfig struct {
	ControlSocket        string `json:"controlSocket"`
	PoolSize             int    `json:"poolSize"`
	PoolSizeMax          int    `json:"poolSizeMax"`
	PeerConfDir          string `json:"peerConfDir"`
	EbgpConfTemplateFile string `json:"ebgpConfTemplateFile"`
	IBGPConfDir          string `json:"ibgpConfDir"`
}

// WireGuardConfig contains WireGuard settings
type WireGuardConfig struct {
	PrivateKeyPath              string `json:"privateKeyPath"`
	PublicKeyPath               string `json:"publicKeyPath"`
	ConfigDir                   string `json:"configDir"`
	PersistentKeepaliveInterval int    `json:"persistentKeepaliveInterval"`
	DN42IPv4                    string `json:"dn42Ipv4"`
	DN42IPv6                    string `json:"dn42Ipv6"`
	DN42IPv6LinkLocal           string `json:"dn42Ipv6LinkLocal"`
}

// MetricConfig contains metric collection settings
type MetricConfig struct {
	PingTimeout int `json:"pingTimeout"`
	PingCount   int `json:"pingCount"`
	PingWorkers int `json:"pingWorkers"`
}

// AutoUpdateConfig contains self-update settings
type AutoUpdateConfig struct {
	Enabled       bool   `json:"enabled"`
	CheckInterval int    `json:"checkInterval"` // minutes
	Channel       string `json:"channel"`       // stable / beta
	GitHubRepo    string `json:"githubRepo"`
}

// Load loads configuration from a JSON file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
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
		cfg.ControlPlane.RetryInitialDelay = 1000 // 1 second
	}
	if cfg.Bird.ControlSocket == "" {
		cfg.Bird.ControlSocket = "/var/run/bird/bird.ctl"
	}
	if cfg.Bird.PoolSize == 0 {
		cfg.Bird.PoolSize = 5
	}
	if cfg.Bird.PoolSizeMax == 0 {
		cfg.Bird.PoolSizeMax = 64
	}
	if cfg.Bird.PeerConfDir == "" {
		cfg.Bird.PeerConfDir = "/etc/bird/peers"
	}
	if cfg.Metric.PingTimeout == 0 {
		cfg.Metric.PingTimeout = 5
	}
	if cfg.Metric.PingCount == 0 {
		cfg.Metric.PingCount = 4
	}
	if cfg.Metric.PingWorkers == 0 {
		cfg.Metric.PingWorkers = 32
	}

	// AutoUpdate defaults
	if cfg.AutoUpdate.CheckInterval == 0 {
		cfg.AutoUpdate.CheckInterval = 60 // 1 hour
	}
	if cfg.AutoUpdate.Channel == "" {
		cfg.AutoUpdate.Channel = "stable"
	}
	if cfg.AutoUpdate.GitHubRepo == "" {
		cfg.AutoUpdate.GitHubRepo = "heichaowo/moenet-agent"
	}

	return &cfg, nil
}
