package api

import (
	"encoding/json"
	"net/http"

	"github.com/moenet/moenet-agent/internal/community"
)

// CommunityHandler handles community management API requests.
type CommunityHandler struct {
	manager *community.Manager
	token   string
}

// NewCommunityHandler creates a new community handler.
func NewCommunityHandler(mgr *community.Manager, token string) *CommunityHandler {
	return &CommunityHandler{
		manager: mgr,
		token:   token,
	}
}

// verifyAuth checks Bearer token. Returns true if authorized.
func (h *CommunityHandler) verifyAuth(w http.ResponseWriter, r *http.Request) bool {
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

// HandleGetStats handles GET /community — community usage statistics across all routes.
func (h *CommunityHandler) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	stats, err := h.manager.GetCommunityStats()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to get community stats"})
		return
	}

	json.NewEncoder(w).Encode(stats)
}

// routeQueryRequest is the body for POST /community/route.
type routeQueryRequest struct {
	Prefix string `json:"prefix"`
}

// HandleRouteQuery handles POST /community/route — query communities for a specific prefix.
func (h *CommunityHandler) HandleRouteQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var req routeQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.Prefix == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing prefix"})
		return
	}

	route, err := h.manager.GetRouteCommunities(req.Prefix)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Route lookup failed"})
		return
	}

	if route == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Route not found"})
		return
	}

	json.NewEncoder(w).Encode(route)
}

// peerCommunityRequest is the body for POST /community/peer and /community/peer/set.
type peerCommunityRequest struct {
	ASN       uint32  `json:"asn"`
	LatencyTier *int    `json:"latency_tier,omitempty"`
	Bandwidth   string  `json:"bandwidth,omitempty"`
	Crypto      string  `json:"crypto,omitempty"`
	Region      string  `json:"region,omitempty"`
	LastRTT     float64 `json:"last_rtt,omitempty"`
}

// peerCommunityResponse is the response for GET peer communities.
type peerCommunityResponse struct {
	ASN          uint32                      `json:"asn"`
	Settings     community.PeerSettings      `json:"settings"`
	SampleRoutes []community.RouteCommunities `json:"sample_routes"`
}

// HandleGetPeerCommunities handles POST /community/peer — get peer settings + sample routes.
func (h *CommunityHandler) HandleGetPeerCommunities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	var req peerCommunityRequest
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

	settings := h.manager.GetPeerCommunities(req.ASN)

	// Also get sample routes from this peer
	routes, err := h.manager.GetPeerRoutesCommunities(req.ASN, 5)
	if err != nil {
		// Non-fatal: return settings without sample routes
		routes = nil
	}

	json.NewEncoder(w).Encode(peerCommunityResponse{
		ASN:          req.ASN,
		Settings:     settings,
		SampleRoutes: routes,
	})
}

// HandleSetPeerCommunities handles POST /community/peer/set — set peer community settings.
func (h *CommunityHandler) HandleSetPeerCommunities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req peerCommunityRequest
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

	settings := community.PeerSettings{
		LatencyTier: req.LatencyTier,
		Bandwidth:   req.Bandwidth,
		Crypto:      req.Crypto,
		Region:      req.Region,
		LastRTT:     req.LastRTT,
	}

	h.manager.SetPeerCommunities(req.ASN, settings)

	// Generate filter snippet for reference
	filterSnippet := h.manager.GeneratePeerFilter(req.ASN)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":         "ok",
		"asn":            req.ASN,
		"settings":       settings,
		"filter_snippet": filterSnippet,
	})
}

// HandleListFilters handles GET /community/filters — list all filter rules.
func (h *CommunityHandler) HandleListFilters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	rules := h.manager.ListFilterRules()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"rules": rules,
	})
}

// addFilterRequest is the body for POST /community/filters.
type addFilterRequest struct {
	Name           string   `json:"name"`
	MatchType      string   `json:"match_type"`
	MatchValue     string   `json:"match_value"`
	Action         string   `json:"action"`
	ModifyCommands []string `json:"modify_commands,omitempty"`
}

// HandleAddFilter handles POST /community/filters — add a community filter rule.
func (h *CommunityHandler) HandleAddFilter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req addFilterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.Name == "" || req.MatchValue == "" || req.Action == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing required fields: name, match_value, action"})
		return
	}

	rule := community.FilterRule{
		Name:           req.Name,
		MatchType:      req.MatchType,
		MatchValue:     req.MatchValue,
		Action:         req.Action,
		ModifyCommands: req.ModifyCommands,
	}

	if err := h.manager.AddFilterRule(rule); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to add filter rule"})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": "added",
		"rule":   rule,
	})
}

// removeFilterRequest is the body for POST /community/filters/remove.
type removeFilterRequest struct {
	Name string `json:"name"`
}

// HandleRemoveFilter handles POST /community/filters/remove — remove a filter rule by name.
func (h *CommunityHandler) HandleRemoveFilter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req removeFilterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing name"})
		return
	}

	if h.manager.RemoveFilterRule(req.Name) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": "removed",
			"name":   req.Name,
		})
	} else {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Rule not found"})
	}
}
