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

// IP refresh interval - check IP every hour
const ipRefreshInterval = time.Hour

// Heartbeat handles node health reporting to Control Plane
type Heartbeat struct {
	config     *config.Config
	httpClient *http.Client

	// System info (cached at startup)
	kernel string

	// Cached public IPs (refreshed every ipRefreshInterval)
	cachedIPv4   string
	cachedIPv6   string
	lastIPCheck  time.Time
	ipMutex      sync.RWMutex
	reportedIPv4 string // Last IP reported to API
	reportedIPv6 string // Last IP reported to API
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

	h := &Heartbeat{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		kernel: kernel,
	}

	// Detect IPs at startup
	h.refreshPublicIPs()

	return h
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
	// Get IPs to report (only if changed since last report)
	ipv4, ipv6 := h.getIPsForHeartbeat()

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
		PublicIPv4:    ipv4, // Only set if changed
		PublicIPv6:    ipv6, // Only set if changed
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

// getPublicIP detects the public IP address (IPv4 or IPv6)
func (h *Heartbeat) getPublicIP(version string) string {
	var url string
	if version == "4" {
		url = "https://api4.ipify.org"
	} else {
		url = "https://api6.ipify.org"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("[Heartbeat] Failed to create IP detection request: %v", err)
		return ""
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// Only log IPv4 failures (IPv6 is expected to fail on many nodes)
		if version == "4" {
			log.Printf("[Heartbeat] Failed to detect public IPv%s: %v", version, err)
		}
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Heartbeat] IP detection returned status %d", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Heartbeat] Failed to read IP response: %v", err)
		return ""
	}

	ip := strings.TrimSpace(string(body))
	return ip
}

// refreshPublicIPs fetches public IPs from external service and caches them
func (h *Heartbeat) refreshPublicIPs() {
	h.ipMutex.Lock()
	defer h.ipMutex.Unlock()

	// Fetch IPv4
	ipv4 := h.getPublicIP("4")
	if ipv4 != "" {
		if h.cachedIPv4 != ipv4 {
			log.Printf("[Heartbeat] Detected public IPv4: %s", ipv4)
		}
		h.cachedIPv4 = ipv4
	}

	// Fetch IPv6
	ipv6 := h.getPublicIP("6")
	if ipv6 != "" {
		if h.cachedIPv6 != ipv6 {
			log.Printf("[Heartbeat] Detected public IPv6: %s", ipv6)
		}
		h.cachedIPv6 = ipv6
	}

	h.lastIPCheck = time.Now()
}

// getIPsForHeartbeat returns IPs to report (only if changed since last report)
// Also refreshes cache if interval has passed
func (h *Heartbeat) getIPsForHeartbeat() (ipv4, ipv6 string) {
	h.ipMutex.Lock()
	defer h.ipMutex.Unlock()

	// Refresh IPs if interval has passed
	if time.Since(h.lastIPCheck) >= ipRefreshInterval {
		h.ipMutex.Unlock() // Unlock before blocking I/O
		h.refreshPublicIPs()
		h.ipMutex.Lock()
	}

	// Only return IP if it changed since last report
	if h.cachedIPv4 != h.reportedIPv4 {
		ipv4 = h.cachedIPv4
		h.reportedIPv4 = h.cachedIPv4
	}
	if h.cachedIPv6 != h.reportedIPv6 {
		ipv6 = h.cachedIPv6
		h.reportedIPv6 = h.cachedIPv6
	}

	return ipv4, ipv6
}
