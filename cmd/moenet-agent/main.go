package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/moenet/moenet-agent/internal/api"
	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
	"github.com/moenet/moenet-agent/internal/maintenance"
	"github.com/moenet/moenet-agent/internal/task"
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

	// Load configuration
	var err error
	cfg, err = config.Load(*configFile)
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

	// Log startup
	log.Printf("%s %s starting...\n", serverSignature, Version)
	log.Printf("  Node: %s\n", cfg.Node.Name)
	log.Printf("  Control Plane: %s\n", cfg.ControlPlane.URL)
	log.Printf("  Listen: %s\n", cfg.Server.Listen)

	// Initialize maintenance state
	maintenanceState := maintenance.NewState(birdPool)

	// Create API handler
	apiHandler := api.NewHandler(Version, maintenanceState)

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/status", apiHandler.HandleStatus)
	mux.HandleFunc("/sync", handleSync)
	mux.HandleFunc("/maintenance", apiHandler.HandleMaintenance)
	mux.HandleFunc("/maintenance/start", apiHandler.HandleMaintenanceStart)
	mux.HandleFunc("/maintenance/stop", apiHandler.HandleMaintenanceStop)

	server := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	// Create background tasks
	heartbeat := task.NewHeartbeat(cfg)
	sessionSync := task.NewSessionSync(cfg, birdPool, birdConfig, wgExecutor)
	metricCollector := task.NewMetricCollector(cfg, birdPool)
	meshSync := task.NewMeshSync(cfg, wgExecutor)
	ibgpSync, err := task.NewIBGPSync(cfg, birdPool)
	if err != nil {
		log.Fatalf("Failed to initialize iBGP sync: %v", err)
	}
	rttMeasurement := task.NewRTTMeasurement(cfg)

	// Create WaitGroup for background tasks
	var wg sync.WaitGroup
	taskCount := 6

	wg.Add(taskCount)
	go heartbeat.Run(ctx, &wg, Version)
	go sessionSync.Run(ctx, &wg)
	go metricCollector.Run(ctx, &wg)
	go rttMeasurement.Run(ctx, &wg)
	go meshSync.Run(ctx, &wg)
	go ibgpSync.Run(ctx, &wg)

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
