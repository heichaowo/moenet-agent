package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/config"
)

// MetricCollector handles BGP statistics and metric reporting
type MetricCollector struct {
	config     *config.Config
	httpClient *http.Client

	mu      sync.RWMutex
	metrics map[string]*SessionMetric // key: peer UUID
}

// NewMetricCollector creates a new metric collector
func NewMetricCollector(cfg *config.Config) *MetricCollector {
	return &MetricCollector{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.ControlPlane.RequestTimeout) * time.Second,
		},
		metrics: make(map[string]*SessionMetric),
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
	// TODO: Query BIRD for protocol statistics
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
	// TODO: Implement actual BIRD query
	// For now, return empty slice
	return []map[string]interface{}{}
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
