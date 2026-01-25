package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/config"
)

// Heartbeat handles node health reporting to Control Plane
type Heartbeat struct {
	config     *config.Config
	httpClient *http.Client

	// System info (cached at startup)
	kernel string
}

// NewHeartbeat creates a new heartbeat handler
func NewHeartbeat(cfg *config.Config) *Heartbeat {
	kernel := "unknown"
	if data, err := os.ReadFile("/proc/version"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) >= 3 {
			kernel = parts[2]
		}
	}

	return &Heartbeat{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		kernel: kernel,
	}
}

// Run starts the heartbeat task
func (h *Heartbeat) Run(ctx context.Context, wg *sync.WaitGroup, version string) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(h.config.ControlPlane.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	// Send initial heartbeat
	if err := h.sendHeartbeat(ctx, version); err != nil {
		log.Printf("[Heartbeat] Initial heartbeat failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[Heartbeat] Task stopped")
			return
		case <-ticker.C:
			if err := h.sendHeartbeat(ctx, version); err != nil {
				log.Printf("[Heartbeat] Failed to send heartbeat: %v", err)
			}
		}
	}
}

// sendHeartbeat sends health metrics to Control Plane
func (h *Heartbeat) sendHeartbeat(ctx context.Context, version string) error {
	payload := HeartbeatPayload{
		Version:       version,
		Kernel:        h.kernel,
		LoadAvg:       h.getLoadAvg(),
		Uptime:        h.getUptime(),
		Timestamp:     time.Now().Unix(),
		TxBytes:       h.getTxBytes(),
		RxBytes:       h.getRxBytes(),
		TCPConns:      h.getTCPConns(),
		UDPConns:      h.getUDPConns(),
		MeshPublicKey: h.getMeshPublicKey(),
	}

	body, err := json.Marshal(map[string]interface{}{
		"node_id":       h.config.Node.Name,
		"agent_version": version,
		"status":        payload,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v1/agent/heartbeat", h.config.ControlPlane.URL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+h.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Heartbeat] Sent successfully (load: %s)", payload.LoadAvg)
	return nil
}

// getLoadAvg returns system load average
func (h *Heartbeat) getLoadAvg() string {
	if runtime.GOOS != "linux" {
		return "0.00 0.00 0.00"
	}

	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0.00 0.00 0.00"
	}

	parts := strings.Fields(string(data))
	if len(parts) >= 3 {
		return fmt.Sprintf("%s %s %s", parts[0], parts[1], parts[2])
	}
	return "0.00 0.00 0.00"
}

// getUptime returns system uptime in seconds
func (h *Heartbeat) getUptime() int64 {
	if runtime.GOOS != "linux" {
		return 0
	}

	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}

	var uptime float64
	_, _ = fmt.Sscanf(string(data), "%f", &uptime)
	return int64(uptime)
}

// getTxBytes returns total transmitted bytes
func (h *Heartbeat) getTxBytes() uint64 {
	return h.getNetStat(9) // TX bytes is field 9
}

// getRxBytes returns total received bytes
func (h *Heartbeat) getRxBytes() uint64 {
	return h.getNetStat(1) // RX bytes is field 1
}

// getNetStat reads network statistics from /proc/net/dev
func (h *Heartbeat) getNetStat(fieldIdx int) uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}

	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0
	}

	var total uint64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[2:] { // Skip header lines
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		// Skip loopback
		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue
		}

		var val uint64
		_, _ = fmt.Sscanf(fields[fieldIdx], "%d", &val)
		total += val
	}
	return total
}

// getTCPConns returns number of established TCP connections
func (h *Heartbeat) getTCPConns() int {
	return h.countConnections("/proc/net/tcp") + h.countConnections("/proc/net/tcp6")
}

// getUDPConns returns number of UDP sockets
func (h *Heartbeat) getUDPConns() int {
	return h.countConnections("/proc/net/udp") + h.countConnections("/proc/net/udp6")
}

// countConnections counts lines in a /proc/net file
func (h *Heartbeat) countConnections(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	count := 0
	for _, line := range lines[1:] { // Skip header
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// getMeshPublicKey reads the WireGuard mesh public key
func (h *Heartbeat) getMeshPublicKey() string {
	// Try /etc/wireguard/public.key first
	data, err := os.ReadFile("/etc/wireguard/public.key")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
