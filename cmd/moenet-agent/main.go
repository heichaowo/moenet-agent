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

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
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

	// Log startup
	log.Printf("%s %s starting...\n", serverSignature, Version)
	log.Printf("  Node: %s\n", cfg.Node.Name)
	log.Printf("  Control Plane: %s\n", cfg.ControlPlane.URL)
	log.Printf("  Listen: %s\n", cfg.Server.Listen)

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/status", handleStatus)
	mux.HandleFunc("/sync", handleSync)

	server := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	// Create WaitGroup for background tasks
	var wg sync.WaitGroup
	taskCount := 6

	wg.Add(taskCount)
	go heartbeatTask(ctx, &wg)
	go sessionSyncTask(ctx, &wg)
	go metricTask(ctx, &wg)
	go rttTask(ctx, &wg)
	go meshSyncTask(ctx, &wg)
	go ibgpSyncTask(ctx, &wg)

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

// Placeholder handlers
func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, Version)
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"sync_triggered"}`)
}

// Placeholder background tasks
func heartbeatTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(cfg.ControlPlane.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Heartbeat] Task stopped")
			return
		case <-ticker.C:
			log.Println("[Heartbeat] Sending heartbeat...")
			// TODO: Implement heartbeat
		}
	}
}

func sessionSyncTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(cfg.ControlPlane.SyncInterval) * time.Second)
	defer ticker.Stop()

	// Initial sync
	log.Println("[SessionSync] Initial sync...")

	for {
		select {
		case <-ctx.Done():
			log.Println("[SessionSync] Task stopped")
			return
		case <-ticker.C:
			log.Println("[SessionSync] Syncing sessions...")
			// TODO: Implement session sync
		}
	}
}

func metricTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(cfg.ControlPlane.MetricInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Metric] Task stopped")
			return
		case <-ticker.C:
			log.Println("[Metric] Collecting metrics...")
			// TODO: Implement metric collection
		}
	}
}

func rttTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(300 * time.Second) // 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[RTT] Task stopped")
			return
		case <-ticker.C:
			log.Println("[RTT] Measuring RTT...")
			// TODO: Implement RTT measurement
		}
	}
}

func meshSyncTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(120 * time.Second) // 2 minutes
	defer ticker.Stop()

	// Initial sync
	log.Println("[MeshSync] Initial sync...")

	for {
		select {
		case <-ctx.Done():
			log.Println("[MeshSync] Task stopped")
			return
		case <-ticker.C:
			log.Println("[MeshSync] Syncing mesh...")
			// TODO: Implement mesh sync
		}
	}
}

func ibgpSyncTask(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(120 * time.Second) // 2 minutes
	defer ticker.Stop()

	// Initial sync
	log.Println("[iBGP] Initial sync...")

	for {
		select {
		case <-ctx.Done():
			log.Println("[iBGP] Task stopped")
			return
		case <-ticker.C:
			log.Println("[iBGP] Syncing iBGP peers...")
			// TODO: Implement iBGP sync
		}
	}
}
