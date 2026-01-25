package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
)

// MetricCollector handles BGP statistics and metric reporting
type MetricCollector struct {
	config     *config.Config
	httpClient *http.Client
	birdPool   *bird.Pool

	mu      sync.RWMutex
	metrics map[string]*SessionMetric // key: peer UUID
}

// NewMetricCollector creates a new metric collector
func NewMetricCollector(cfg *config.Config, birdPool *bird.Pool) *MetricCollector {
	return &MetricCollector{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		birdPool: birdPool,
		metrics:  make(map[string]*SessionMetric),
	}
}

// Run starts the metric collection task
func (m *MetricCollector) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(time.Duration(m.config.ControlPlane.MetricInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Metric] Task stopped")
			return
		case <-ticker.C:
			if err := m.collectAndReport(ctx); err != nil {
				log.Printf("[Metric] Collection failed: %v", err)
			}
		}
	}
}

// collectAndReport collects metrics and sends to CP
func (m *MetricCollector) collectAndReport(ctx context.Context) error {
	// Collect BGP statistics
	sessions := m.collectBGPStats()

	if len(sessions) == 0 {
		log.Println("[Metric] No sessions to report")
		return nil
	}

	// Send to Control Plane
	return m.reportMetrics(ctx, sessions)
}

// collectBGPStats collects BGP protocol statistics from BIRD
func (m *MetricCollector) collectBGPStats() []map[string]interface{} {
	output, err := m.birdPool.ShowProtocols()
	if err != nil {
		log.Printf("[Metric] Failed to get BIRD protocols: %v", err)
		return nil
	}

	var sessions []map[string]interface{}
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		// Skip header and empty lines
		if len(line) == 0 || strings.HasPrefix(line, "Name") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		name := fields[0]
		proto := fields[1]
		state := fields[3]
		info := ""
		if len(fields) > 5 {
			info = strings.Join(fields[5:], " ")
		}

		// Only report DN42 eBGP sessions
		if proto == "BGP" && strings.HasPrefix(name, "dn42_") {
			sessions = append(sessions, map[string]interface{}{
				"name":  name,
				"type":  "bgp",
				"state": state,
				"info":  info,
			})
		}
	}

	return sessions
}

// reportMetrics sends metrics to Control Plane
func (m *MetricCollector) reportMetrics(ctx context.Context, sessions []map[string]interface{}) error {
	url := fmt.Sprintf("%s/api/v1/agent/%s/report", m.config.ControlPlane.URL, m.config.Node.Name)

	payload := map[string]interface{}{
		"node_id":   m.config.Node.Name,
		"timestamp": time.Now().Unix(),
		"sessions":  sessions,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.config.ControlPlane.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("CP returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Metric] Reported %d sessions", len(sessions))
	return nil
}

// UpdateMetric updates metrics for a session
func (m *MetricCollector) UpdateMetric(uuid string, metric *SessionMetric) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[uuid] = metric
}

// GetMetric returns metrics for a session
func (m *MetricCollector) GetMetric(uuid string) *SessionMetric {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metrics[uuid]
}
