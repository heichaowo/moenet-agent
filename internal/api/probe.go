package api

import (
	"encoding/json"
	"net/http"

	"github.com/moenet/moenet-agent/internal/probe"
)

// ProbeHandler handles latency and MTU probe API requests.
type ProbeHandler struct {
	latencyProbe *probe.LatencyProbe
	mtuProbe     *probe.MTUProbe
	token        string
}

// NewProbeHandler creates a new probe handler.
func NewProbeHandler(lp *probe.LatencyProbe, mp *probe.MTUProbe, token string) *ProbeHandler {
	return &ProbeHandler{
		latencyProbe: lp,
		mtuProbe:     mp,
		token:        token,
	}
}

// verifyAuth checks Bearer token. Returns true if authorized.
func (h *ProbeHandler) verifyAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+h.token {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Unauthorized"})
		return false
	}
	return true
}

// HandleGetStats handles GET /probe — returns all probe statistics.
func (h *ProbeHandler) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	stats := h.latencyProbe.GetAllStats()
	json.NewEncoder(w).Encode(stats)
}

// probeAddRequest is the body for POST /probe/add.
type probeAddRequest struct {
	ASN      uint32 `json:"asn"`
	Endpoint string `json:"endpoint"`
}

// HandleAddPeer handles POST /probe/add — add a peer to latency probing.
func (h *ProbeHandler) HandleAddPeer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req probeAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 || req.Endpoint == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing asn or endpoint"})
		return
	}

	h.latencyProbe.AddPeer(req.ASN, req.Endpoint)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":   "added",
		"asn":      req.ASN,
		"endpoint": req.Endpoint,
	})
}

// probeASNRequest is the body for endpoints that take only an ASN.
type probeASNRequest struct {
	ASN uint32 `json:"asn"`
}

// HandleRemovePeer handles POST /probe/remove — remove a peer from probing.
func (h *ProbeHandler) HandleRemovePeer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req probeASNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing asn"})
		return
	}

	h.latencyProbe.RemovePeer(req.ASN)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": "removed",
		"asn":    req.ASN,
	})
}

// HandleProbeNow handles POST /probe/now — immediately probe a specific peer.
func (h *ProbeHandler) HandleProbeNow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req probeASNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing asn"})
		return
	}

	result := h.latencyProbe.ProbeNow(req.ASN)
	if result == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Peer not found in probe list"})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// HandlePeerStats handles POST /probe/stats — get latency stats for a specific peer.
func (h *ProbeHandler) HandlePeerStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var req probeASNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing asn"})
		return
	}

	stats := h.latencyProbe.GetPeerStats(req.ASN)
	if stats == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Peer not found"})
		return
	}

	json.NewEncoder(w).Encode(stats)
}

// HandleStart handles POST /probe/start — resume the latency probe daemon.
func (h *ProbeHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	h.latencyProbe.Resume()
	json.NewEncoder(w).Encode(map[string]string{"result": "started"})
}

// HandleStop handles POST /probe/stop — pause the latency probe daemon.
func (h *ProbeHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	h.latencyProbe.Pause()
	json.NewEncoder(w).Encode(map[string]string{"result": "stopped"})
}

// mtuProbeRequest is the body for POST /probe/mtu.
type mtuProbeRequest struct {
	Target             string `json:"target"`
	IsIntercontinental bool   `json:"intercontinental"`
}

// HandleMTUProbe handles POST /probe/mtu — probe MTU for a target.
func (h *ProbeHandler) HandleMTUProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req mtuProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.Target == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing target"})
		return
	}

	result := h.mtuProbe.ProbeMTU(req.Target, req.IsIntercontinental)
	json.NewEncoder(w).Encode(result)
}
