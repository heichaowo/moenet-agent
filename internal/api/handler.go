// Package api provides HTTP handlers for the agent API.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/moenet/moenet-agent/internal/maintenance"
	"github.com/moenet/moenet-agent/internal/metrics"
)

// Handler holds the dependencies for API handlers.
type Handler struct {
	Version          string
	MaintenanceState *maintenance.State
}

// NewHandler creates a new API handler.
func NewHandler(version string, maintenanceState *maintenance.State) *Handler {
	return &Handler{
		Version:          version,
		MaintenanceState: maintenanceState,
	}
}

// StatusResponse is the response for the /status endpoint.
type StatusResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	MaintenanceMode bool   `json:"maintenance_mode"`
	Uptime          int64  `json:"uptime,omitempty"`
}

// MaintenanceResponse is the response for maintenance endpoints.
type MaintenanceResponse struct {
	MaintenanceMode bool      `json:"maintenance_mode"`
	EnteredAt       time.Time `json:"entered_at,omitempty"`
	Message         string    `json:"message,omitempty"`
}

// ErrorResponse is the response for errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

var startTime = time.Now()

// HandleStatus handles GET /status
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := StatusResponse{
		Status:          "ok",
		Version:         h.Version,
		MaintenanceMode: h.MaintenanceState.IsEnabled(),
		Uptime:          int64(time.Since(startTime).Seconds()),
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleMaintenance handles GET /maintenance
func (h *Handler) HandleMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := MaintenanceResponse{
		MaintenanceMode: h.MaintenanceState.IsEnabled(),
		EnteredAt:       h.MaintenanceState.EnteredAt(),
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleMaintenanceStart handles POST /maintenance/start
func (h *Handler) HandleMaintenanceStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if err := h.MaintenanceState.Enter(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	resp := MaintenanceResponse{
		MaintenanceMode: true,
		EnteredAt:       h.MaintenanceState.EnteredAt(),
		Message:         "Maintenance mode enabled, eBGP sessions gracefully shutdown",
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleMaintenanceStop handles POST /maintenance/stop
func (h *Handler) HandleMaintenanceStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if err := h.MaintenanceState.Exit(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: err.Error()})
		return
	}

	resp := MaintenanceResponse{
		MaintenanceMode: false,
		Message:         "Maintenance mode disabled, eBGP sessions restored",
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleMetrics handles GET /metrics (Prometheus format)
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	m := metrics.Get()
	m.SetVersion(h.Version)
	m.Handler()(w, r)
}
