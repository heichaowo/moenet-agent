package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/moenet/moenet-agent/internal/api"
	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
	"github.com/moenet/moenet-agent/internal/firewall"
	"github.com/moenet/moenet-agent/internal/httpclient"
	"github.com/moenet/moenet-agent/internal/loopback"
	"github.com/moenet/moenet-agent/internal/maintenance"
	"github.com/moenet/moenet-agent/internal/task"
	"github.com/moenet/moenet-agent/internal/updater"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// Build-time variables (set by -ldflags)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

const (
	serverSignature = "MoeNet Agent"
	shutdownTimeout = 30 * time.Second
)

// Global state
var (
	cfg      *config.Config
	birdPool *bird.Pool
)

func main() {
	configFile := flag.String("c", "config.json", "Path to configuration file")
	showVersion := flag.Bool("v", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s (commit: %s, built: %s)\n", serverSignature, Version, Commit, BuildTime)
		os.Exit(0)
	}

	// Create root context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration (supports bootstrap mode)
	var err error
	cfg, err = config.LoadWithBootstrap(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize BIRD connection pool
	birdPool, err = bird.NewPool(cfg.Bird.ControlSocket, cfg.Bird.PoolSize, cfg.Bird.PoolSizeMax)
	if err != nil {
		log.Fatalf("Failed to initialize BIRD pool: %v", err)
	}
	defer birdPool.Close()

	// Initialize BIRD config generator
	birdConfig, err := bird.NewConfigGenerator(cfg.Bird.PeerConfDir)
	if err != nil {
		log.Fatalf("Failed to initialize BIRD config generator: %v", err)
	}

	// Initialize WireGuard executor
	wgExecutor, err := wireguard.NewExecutor(cfg.WireGuard.ConfigDir, cfg.WireGuard.PrivateKeyPath)
	if err != nil {
		log.Fatalf("Failed to initialize WireGuard executor: %v", err)
	}

	// Initialize loopback executor and setup dummy0 interface
	lbExecutor := loopback.NewExecutor(slog.Default())
	if cfg.WireGuard.DN42IPv4 != "" || cfg.WireGuard.DN42IPv6 != "" {
		if err := lbExecutor.SetupLoopbackWithIPs(cfg.WireGuard.DN42IPv4, cfg.WireGuard.DN42IPv6); err != nil {
			log.Printf("Warning: failed to setup loopback: %v", err)
		} else {
			log.Printf("Loopback configured: %s, %s", cfg.WireGuard.DN42IPv4, cfg.WireGuard.DN42IPv6)
		}
	}

	// Log startup
	log.Printf("%s %s starting...\n", serverSignature, Version)
	log.Printf("  Node: %s\n", cfg.Node.Name)
	log.Printf("  Control Plane: %s\n", cfg.ControlPlane.URL)
	log.Printf("  Listen: %s\n", cfg.Server.Listen)

	// Initialize maintenance state
	maintenanceState := maintenance.NewState(birdPool)

	// Create API handler
	apiHandler := api.NewHandler(Version, maintenanceState)

	// Create restart handler
	restartHandler := api.NewRestartHandler(birdPool, wgExecutor)

	// Create tools handler for network diagnostics
	toolsHandler := api.NewToolsHandler(birdPool, cfg.ControlPlane.Token)

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/status", apiHandler.HandleStatus)
	mux.HandleFunc("/sync", handleSync)
	mux.HandleFunc("/metrics", apiHandler.HandleMetrics)
	mux.HandleFunc("/maintenance", apiHandler.HandleMaintenance)
	mux.HandleFunc("/maintenance/start", apiHandler.HandleMaintenanceStart)
	mux.HandleFunc("/maintenance/stop", apiHandler.HandleMaintenanceStop)
	mux.HandleFunc("/restart", restartHandler.HandleRestart)

	// Network diagnostic tools
	mux.HandleFunc("/ping", toolsHandler.HandlePing)
	mux.HandleFunc("/tcping", toolsHandler.HandleTcping)
	mux.HandleFunc("/trace", toolsHandler.HandleTrace)
	mux.HandleFunc("/route", toolsHandler.HandleRoute)
	mux.HandleFunc("/path", toolsHandler.HandlePath)

	server := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	// Create background tasks
	heartbeat := task.NewHeartbeat(cfg)

	// Initialize firewall executor for port management
	fwExecutor := firewall.NewExecutor(slog.Default())
	log.Println("Firewall executor initialized")

	sessionSync := task.NewSessionSync(cfg, birdPool, birdConfig, wgExecutor, fwExecutor)
	metricCollector := task.NewMetricCollector(cfg, birdPool)
	meshSync := task.NewMeshSync(cfg, wgExecutor)
	ibgpSync, err := task.NewIBGPSync(cfg, birdPool)
	if err != nil {
		log.Fatalf("Failed to initialize iBGP sync: %v", err)
	}
	rttMeasurement := task.NewRTTMeasurement(cfg)

	// Initialize HTTP client for BirdConfigSync
	httpClient := httpclient.New(nil, httpclient.DefaultRetryConfig())

	// Initialize BIRD config sync (connects to iBGP sync)
	birdConfigSync, err := task.NewBirdConfigSync(cfg, birdPool, httpClient, ibgpSync)
	if err != nil {
		log.Fatalf("Failed to initialize BIRD config sync: %v", err)
	}

	// Connect MeshSync to RTT so RTT can use mesh peer loopback IPs
	meshSync.SetOnPeersUpdated(rttMeasurement.UpdateMeshPeers)

	// Create WaitGroup for background tasks
	var wg sync.WaitGroup
	taskCount := 7 // heartbeat, sessionSync, metricCollector, rttMeasurement, meshSync, ibgpSync, birdConfigSync

	// Initialize auto-updater if enabled
	var agentUpdater *updater.Updater
	if cfg.AutoUpdate.Enabled {
		taskCount++
		agentUpdater = updater.New(
			Version,
			os.Args[0],
			updater.Config{
				Enabled:       cfg.AutoUpdate.Enabled,
				CheckInterval: cfg.AutoUpdate.CheckInterval,
				Channel:       cfg.AutoUpdate.Channel,
			},
			cfg.AutoUpdate.GitHubRepo,
		)
		log.Printf("[Updater] Auto-update enabled, checking every %d minutes", cfg.AutoUpdate.CheckInterval)
	}

	wg.Add(taskCount)
	go heartbeat.Run(ctx, &wg, Version)
	go sessionSync.Run(ctx, &wg)
	go metricCollector.Run(ctx, &wg)
	go rttMeasurement.Run(ctx, &wg)
	go meshSync.Run(ctx, &wg)
	go ibgpSync.Run(ctx, &wg)
	go birdConfigSync.Run(ctx, &wg)
	if agentUpdater != nil {
		go agentUpdater.Run(ctx, &wg)
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start HTTP server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("HTTP server starting on %s", cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case sig := <-sigChan:
		log.Printf("Shutdown signal received: %v", sig)
	case err := <-serverErr:
		log.Printf("HTTP server error: %v", err)
	}

	// Graceful shutdown
	log.Println("Initiating graceful shutdown...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Wait for background tasks
	waitChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		log.Println("All background tasks completed")
	case <-shutdownCtx.Done():
		log.Println("Shutdown timeout reached")
	}

	log.Printf("%s stopped\n", serverSignature)
}

// handleSync handles sync requests (placeholder)
func handleSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"sync_triggered"}`)
}
