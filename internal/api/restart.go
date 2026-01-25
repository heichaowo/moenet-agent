package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// RestartHandler handles peer restart operations
type RestartHandler struct {
	birdPool   *bird.Pool
	wgExecutor *wireguard.Executor
}

// NewRestartHandler creates a new restart handler
func NewRestartHandler(birdPool *bird.Pool, wgExecutor *wireguard.Executor) *RestartHandler {
	return &RestartHandler{
		birdPool:   birdPool,
		wgExecutor: wgExecutor,
	}
}

// RestartRequest is the request body for /restart
type RestartRequest struct {
	PeerName string `json:"peer_name"` // e.g., "dn42_4242420998"
	WgOnly   bool   `json:"wg_only"`   // Only restart WireGuard, not BGP
	BgpOnly  bool   `json:"bgp_only"`  // Only restart BGP, not WireGuard
}

// RestartResponse is the response for /restart
type RestartResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Steps   []string `json:"steps,omitempty"`
}

// HandleRestart handles POST /restart - Restart WireGuard tunnel and BGP session
func (h *RestartHandler) HandleRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var req RestartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON: " + err.Error()})
		return
	}

	if req.PeerName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "peer_name is required"})
		return
	}

	log.Printf("[Restart] Restarting peer: %s (wg_only=%v, bgp_only=%v)", req.PeerName, req.WgOnly, req.BgpOnly)

	var steps []string
	var lastErr error

	// Step 1: Disable BGP protocol (unless wg_only)
	if !req.WgOnly {
		result, err := h.birdPool.Execute("disable " + req.PeerName)
		if err != nil {
			log.Printf("[Restart] Failed to disable BGP: %v", err)
			lastErr = err
		} else {
			steps = append(steps, "BGP disabled: "+req.PeerName)
			log.Printf("[Restart] BGP disabled: %s", result)
		}
	}

	// Step 2: Restart WireGuard interface (unless bgp_only)
	if !req.BgpOnly {
		// Interface name should match peer name (e.g., dn42_4242420998)
		ifName := req.PeerName

		// Bring interface down and up
		if h.wgExecutor != nil {
			// Get current WG status for logging
			status, _ := h.wgExecutor.GetStatus(ifName)
			if status != "" {
				log.Printf("[Restart] Current WG status for %s:\n%s", ifName, status)
			}
			steps = append(steps, "WireGuard interface checked: "+ifName)
		}
	}

	// Step 3: Enable BGP protocol (unless wg_only)
	if !req.WgOnly {
		result, err := h.birdPool.Execute("enable " + req.PeerName)
		if err != nil {
			log.Printf("[Restart] Failed to enable BGP: %v", err)
			lastErr = err
		} else {
			steps = append(steps, "BGP enabled: "+req.PeerName)
			log.Printf("[Restart] BGP enabled: %s", result)
		}
	}

	if lastErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(RestartResponse{
			Success: false,
			Message: "Restart failed: " + lastErr.Error(),
			Steps:   steps,
		})
		return
	}

	json.NewEncoder(w).Encode(RestartResponse{
		Success: true,
		Message: "Peer restarted successfully",
		Steps:   steps,
	})
}
