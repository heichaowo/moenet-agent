package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/config"
)

// RTTMeasurement handles latency measurements to other nodes
type RTTMeasurement struct {
	config      *config.Config
	httpClient  *http.Client
	results     map[string]*RTTResult
	meshTargets []string // loopback IPs from mesh peers
	mu          sync.RWMutex
}

// RTTResult stores RTT measurement results
type RTTResult struct {
	Target    string    `json:"target"`
	RTTMs     float64   `json:"rtt_ms"`
	Loss      float64   `json:"loss"`
	Timestamp time.Time `json:"timestamp"`
}

// NewRTTMeasurement creates a new RTT measurement handler
func NewRTTMeasurement(cfg *config.Config) *RTTMeasurement {
	return &RTTMeasurement{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		results:     make(map[string]*RTTResult),
		meshTargets: []string{},
	}
}

// UpdateMeshPeers updates the list of mesh peer targets for RTT measurement
func (r *RTTMeasurement) UpdateMeshPeers(peers map[int]*MeshPeer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.meshTargets = make([]string, 0, len(peers))
	for _, peer := range peers {
		// Prefer IPv6 loopback, fall back to IPv4
		if peer.LoopbackIPv6 != "" {
			r.meshTargets = append(r.meshTargets, peer.LoopbackIPv6)
		} else if peer.LoopbackIPv4 != "" {
			r.meshTargets = append(r.meshTargets, peer.LoopbackIPv4)
		}
	}
	log.Printf("[RTT] Updated %d mesh peer targets", len(r.meshTargets))
}

// Run starts the RTT measurement task
func (r *RTTMeasurement) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(300 * time.Second) // 5 minutes
	defer ticker.Stop()

	// Initial measurement
	log.Println("[RTT] Performing initial measurement...")
	r.measureAll(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[RTT] Task stopped")
			return
		case <-ticker.C:
			r.measureAll(ctx)
		}
	}
}

// measureAll measures RTT to all known targets
func (r *RTTMeasurement) measureAll(ctx context.Context) {
	r.mu.RLock()
	meshCount := len(r.meshTargets)
	r.mu.RUnlock()

	var targets []string

	if meshCount > 0 {
		// Use mesh peer loopback IPs
		r.mu.RLock()
		targets = make([]string, len(r.meshTargets))
		copy(targets, r.meshTargets)
		r.mu.RUnlock()
	} else {
		// Fallback to default targets when no mesh peers available
		targets = []string{
			"172.20.0.53",     // DN42 anycast DNS
			"fd42:d42:d42::1", // DN42 anycast DNS v6
		}
	}

	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			result := r.measure(ctx, t)
			if result != nil {
				r.mu.Lock()
				r.results[t] = result
				r.mu.Unlock()
			}
		}(target)
	}
	wg.Wait()

	log.Printf("[RTT] Measured %d targets (mesh=%d)", len(r.results), meshCount)

	// Report results to Control Plane
	if err := r.reportResults(ctx); err != nil {
		log.Printf("[RTT] Failed to report results: %v", err)
	}
}

// measure performs RTT measurement to a single target
func (r *RTTMeasurement) measure(ctx context.Context, target string) *RTTResult {
	pingCount := r.config.Metric.PingCount
	if pingCount == 0 {
		pingCount = 4
	}
	timeout := time.Duration(r.config.Metric.PingTimeout) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	var successCount int
	var totalRTT time.Duration

	for i := 0; i < pingCount; i++ {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		rtt, err := r.tcpPing(target, timeout)
		if err == nil {
			successCount++
			totalRTT += rtt
		}
		time.Sleep(100 * time.Millisecond)
	}

	if successCount == 0 {
		return &RTTResult{
			Target:    target,
			RTTMs:     -1,
			Loss:      100.0,
			Timestamp: time.Now(),
		}
	}

	avgRTT := float64(totalRTT.Microseconds()/int64(successCount)) / 1000.0
	loss := float64(pingCount-successCount) / float64(pingCount) * 100.0

	return &RTTResult{
		Target:    target,
		RTTMs:     avgRTT,
		Loss:      loss,
		Timestamp: time.Now(),
	}
}

// tcpPing performs a TCP connect to measure RTT
func (r *RTTMeasurement) tcpPing(target string, timeout time.Duration) (time.Duration, error) {
	// Use port 53 (DNS) for TCP ping
	addr := target
	if net.ParseIP(target) != nil {
		addr = fmt.Sprintf("%s:53", target)
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	conn.Close()
	return rtt, nil
}

// GetResults returns current RTT results
func (r *RTTMeasurement) GetResults() map[string]*RTTResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make(map[string]*RTTResult)
	for k, v := range r.results {
		results[k] = v
	}
	return results
}

// reportResults sends RTT measurements to Control Plane
func (r *RTTMeasurement) reportResults(ctx context.Context) error {
	r.mu.RLock()
	measurements := make([]map[string]interface{}, 0, len(r.results))
	for _, result := range r.results {
		measurements = append(measurements, map[string]interface{}{
			"target": result.Target,
			"rtt_ms": result.RTTMs,
			"loss":   result.Loss,
		})
	}
	r.mu.RUnlock()

	if len(measurements) == 0 {
		return nil
	}

	url := fmt.Sprintf("%s/api/v1/agent/%s/rtt", r.config.ControlPlane.URL, r.config.Node.Name)

	body, err := json.Marshal(map[string]interface{}{
		"measurements": measurements,
		"timestamp":    time.Now().Unix(),
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+r.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CP returned status %d", resp.StatusCode)
	}

	log.Printf("[RTT] Reported %d measurements to CP", len(measurements))
	return nil
}
