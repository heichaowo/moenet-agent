package api

import (
	"encoding/json"
	"net/http"

	"github.com/moenet/moenet-agent/internal/blacklist"
)

// BlacklistHandler handles blacklist management API requests.
type BlacklistHandler struct {
	manager *blacklist.Manager
	token   string
}

// NewBlacklistHandler creates a new blacklist handler.
func NewBlacklistHandler(mgr *blacklist.Manager, token string) *BlacklistHandler {
	return &BlacklistHandler{
		manager: mgr,
		token:   token,
	}
}

// verifyAuth checks Bearer token. Returns true if authorized.
func (h *BlacklistHandler) verifyAuth(w http.ResponseWriter, r *http.Request) bool {
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

// blacklistResponse is the response for GET /blacklist.
type blacklistResponse struct {
	Blocked []int `json:"blocked"`
	Count   int   `json:"count"`
}

// blacklistModifyRequest is the body for POST /blacklist/add and /blacklist/remove.
type blacklistModifyRequest struct {
	ASN int `json:"asn"`
}

// blacklistModifyResponse is the response for add/remove operations.
type blacklistModifyResponse struct {
	Result string `json:"result"`
	ASN    int    `json:"asn"`
	Total  int    `json:"total"`
}

// HandleGetBlacklist handles GET /blacklist — return current blacklisted ASNs.
func (h *BlacklistHandler) HandleGetBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	blocked := h.manager.List()
	json.NewEncoder(w).Encode(blacklistResponse{
		Blocked: blocked,
		Count:   len(blocked),
	})
}

// HandleAddBlacklist handles POST /blacklist/add — add an ASN to the blacklist.
func (h *BlacklistHandler) HandleAddBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req blacklistModifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing ASN"})
		return
	}

	result, err := h.manager.Add(req.ASN)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to save blacklist"})
		return
	}

	json.NewEncoder(w).Encode(blacklistModifyResponse{
		Result: result,
		ASN:    req.ASN,
		Total:  h.manager.Count(),
	})
}

// HandleRemoveBlacklist handles POST /blacklist/remove — remove an ASN from the blacklist.
func (h *BlacklistHandler) HandleRemoveBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if !h.verifyAuth(w, r) {
		return
	}

	var req blacklistModifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.ASN == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing ASN"})
		return
	}

	result, err := h.manager.Remove(req.ASN)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Failed to save blacklist"})
		return
	}

	json.NewEncoder(w).Encode(blacklistModifyResponse{
		Result: result,
		ASN:    req.ASN,
		Total:  h.manager.Count(),
	})
}
