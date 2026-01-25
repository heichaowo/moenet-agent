// Package metrics provides Prometheus metrics for the moenet-agent.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// Metrics holds all agent metrics
type Metrics struct {
	mu sync.RWMutex

	// Agent info
	startTime time.Time
	version   string

	// Control Plane communication
	cpRequestsTotal       int64
	cpRequestsSuccess     int64
	cpRequestsFailed      int64
	cpLastHeartbeat       time.Time
	cpCircuitBreakerState string

	// BGP sessions
	sessionsTotal  int
	sessionsActive int
	sessionsError  int
	sessionsSynced int64

	// HTTP client
	httpRetryTotal   int64
	httpRetrySuccess int64
}

var (
	instance *Metrics
	once     sync.Once
)

// Get returns the global metrics instance
func Get() *Metrics {
	once.Do(func() {
		instance = &Metrics{
			startTime:             time.Now(),
			cpCircuitBreakerState: "closed",
		}
	})
	return instance
}

// SetVersion sets the agent version
func (m *Metrics) SetVersion(v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.version = v
}

// RecordCPRequest records a control plane request
func (m *Metrics) RecordCPRequest(success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cpRequestsTotal++
	if success {
		m.cpRequestsSuccess++
	} else {
		m.cpRequestsFailed++
	}
}

// RecordHeartbeat records a successful heartbeat
func (m *Metrics) RecordHeartbeat() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cpLastHeartbeat = time.Now()
}

// SetCircuitBreakerState sets the current circuit breaker state
func (m *Metrics) SetCircuitBreakerState(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cpCircuitBreakerState = state
}

// UpdateSessionCounts updates BGP session counts
func (m *Metrics) UpdateSessionCounts(total, active, errored int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsTotal = total
	m.sessionsActive = active
	m.sessionsError = errored
}

// RecordSessionSync records a session sync
func (m *Metrics) RecordSessionSync() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsSynced++
}

// RecordHTTPRetry records an HTTP retry attempt
func (m *Metrics) RecordHTTPRetry(success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpRetryTotal++
	if success {
		m.httpRetrySuccess++
	}
}

// Handler returns an HTTP handler for Prometheus metrics
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		defer m.mu.RUnlock()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Agent info
		fmt.Fprintf(w, "# HELP moenet_agent_info Agent information\n")
		fmt.Fprintf(w, "# TYPE moenet_agent_info gauge\n")
		fmt.Fprintf(w, "moenet_agent_info{version=%q,go_version=%q} 1\n", m.version, runtime.Version())

		// Uptime
		fmt.Fprintf(w, "# HELP moenet_agent_uptime_seconds Agent uptime in seconds\n")
		fmt.Fprintf(w, "# TYPE moenet_agent_uptime_seconds counter\n")
		fmt.Fprintf(w, "moenet_agent_uptime_seconds %.0f\n", time.Since(m.startTime).Seconds())

		// Control Plane requests
		fmt.Fprintf(w, "# HELP moenet_cp_requests_total Total Control Plane requests\n")
		fmt.Fprintf(w, "# TYPE moenet_cp_requests_total counter\n")
		fmt.Fprintf(w, "moenet_cp_requests_total{result=\"success\"} %d\n", m.cpRequestsSuccess)
		fmt.Fprintf(w, "moenet_cp_requests_total{result=\"failed\"} %d\n", m.cpRequestsFailed)

		// Last heartbeat
		if !m.cpLastHeartbeat.IsZero() {
			fmt.Fprintf(w, "# HELP moenet_cp_last_heartbeat_timestamp Last successful heartbeat timestamp\n")
			fmt.Fprintf(w, "# TYPE moenet_cp_last_heartbeat_timestamp gauge\n")
			fmt.Fprintf(w, "moenet_cp_last_heartbeat_timestamp %d\n", m.cpLastHeartbeat.Unix())
		}

		// Circuit breaker state (0=closed, 1=open, 2=half-open)
		cbState := 0
		switch m.cpCircuitBreakerState {
		case "open":
			cbState = 1
		case "half-open":
			cbState = 2
		}
		fmt.Fprintf(w, "# HELP moenet_circuit_breaker_state Circuit breaker state (0=closed, 1=open, 2=half-open)\n")
		fmt.Fprintf(w, "# TYPE moenet_circuit_breaker_state gauge\n")
		fmt.Fprintf(w, "moenet_circuit_breaker_state %d\n", cbState)

		// BGP sessions
		fmt.Fprintf(w, "# HELP moenet_bgp_sessions BGP session counts\n")
		fmt.Fprintf(w, "# TYPE moenet_bgp_sessions gauge\n")
		fmt.Fprintf(w, "moenet_bgp_sessions{status=\"total\"} %d\n", m.sessionsTotal)
		fmt.Fprintf(w, "moenet_bgp_sessions{status=\"active\"} %d\n", m.sessionsActive)
		fmt.Fprintf(w, "moenet_bgp_sessions{status=\"error\"} %d\n", m.sessionsError)

		// Session syncs
		fmt.Fprintf(w, "# HELP moenet_session_syncs_total Total session sync operations\n")
		fmt.Fprintf(w, "# TYPE moenet_session_syncs_total counter\n")
		fmt.Fprintf(w, "moenet_session_syncs_total %d\n", m.sessionsSynced)

		// HTTP retries
		fmt.Fprintf(w, "# HELP moenet_http_retries_total HTTP retry attempts\n")
		fmt.Fprintf(w, "# TYPE moenet_http_retries_total counter\n")
		fmt.Fprintf(w, "moenet_http_retries_total{result=\"success\"} %d\n", m.httpRetrySuccess)
		fmt.Fprintf(w, "moenet_http_retries_total{result=\"exhausted\"} %d\n", m.httpRetryTotal-m.httpRetrySuccess)

		// Go runtime stats
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		fmt.Fprintf(w, "# HELP go_memstats_alloc_bytes Current memory allocation\n")
		fmt.Fprintf(w, "# TYPE go_memstats_alloc_bytes gauge\n")
		fmt.Fprintf(w, "go_memstats_alloc_bytes %d\n", memStats.Alloc)

		fmt.Fprintf(w, "# HELP go_goroutines Number of goroutines\n")
		fmt.Fprintf(w, "# TYPE go_goroutines gauge\n")
		fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())
	}
}
