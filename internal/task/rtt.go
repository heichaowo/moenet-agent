package task

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/config"
)

// RTTMeasurement handles latency measurements to other nodes
type RTTMeasurement struct {
	config  *config.Config
	results map[string]*RTTResult
	mu      sync.RWMutex
}

// RTTResult stores RTT measurement results
type RTTResult struct {
	Target    string    `json:"target"`
	RTTMs     float64   `json:"rtt_ms"`
	Loss      float64   `json:"loss_percent"`
	Timestamp time.Time `json:"timestamp"`
}

// NewRTTMeasurement creates a new RTT measurement handler
func NewRTTMeasurement(cfg *config.Config) *RTTMeasurement {
	return &RTTMeasurement{
		config:  cfg,
		results: make(map[string]*RTTResult),
	}
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
	// For now, we measure RTT to common endpoints
	// In production, this would use mesh peer IPs
	targets := []string{
		"8.8.8.8",         // Google DNS
		"1.1.1.1",         // Cloudflare DNS
		"172.20.0.53",     // DN42 anycast DNS
		"fd42:d42:d42::1", // DN42 anycast DNS v6
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

	log.Printf("[RTT] Measured %d targets", len(r.results))
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
