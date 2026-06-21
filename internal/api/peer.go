package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// PeerHandler handles per-peer live status queries.
type PeerHandler struct {
	birdPool   *bird.Pool
	wgExecutor *wireguard.Executor
	token      string
}

// NewPeerHandler creates a new peer status handler.
func NewPeerHandler(birdPool *bird.Pool, wgExecutor *wireguard.Executor, token string) *PeerHandler {
	return &PeerHandler{
		birdPool:   birdPool,
		wgExecutor: wgExecutor,
		token:      token,
	}
}

// PeerStatusResponse is the JSON response for GET /peer/{name}.
type PeerStatusResponse struct {
	PeerName       string          `json:"peer_name"`
	BGPStatus      string          `json:"bgp_status"`
	WGStatus       string          `json:"wg_status"`
	LastHandshake  string          `json:"last_handshake"`
	Transfer       *TransferStats  `json:"transfer,omitempty"`
	Uptime         string          `json:"uptime"`
	RoutesImported int             `json:"routes_imported"`
	RoutesExported int             `json:"routes_exported"`
}

// TransferStats holds WireGuard transfer counters.
type TransferStats struct {
	RX string `json:"rx"`
	TX string `json:"tx"`
}

// HandleGetPeerStatus handles GET /peer/{name} — returns live BIRD + WG status for a peer.
func (h *PeerHandler) HandleGetPeerStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	// Auth check
	if h.token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.token {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Unauthorized"})
			return
		}
	}

	// Extract peer name from path: /peer/dn42_4242420337 → dn42_4242420337
	name := strings.TrimPrefix(r.URL.Path, "/peer/")
	if name == "" || name == r.URL.Path {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing peer name"})
		return
	}

	// Basic input validation — prevent injection
	if strings.ContainsAny(name, ";& |`$(){}[]<>\\\"'") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid peer name"})
		return
	}

	resp := PeerStatusResponse{
		PeerName: name,
	}

	// Query BIRD protocol status
	bgpFound := false
	birdResult, err := h.birdPool.Execute("show protocols all \"" + name + "\"")
	if err != nil {
		slog.Warn("BIRD query failed for peer", "peer", name, "error", err)
		resp.BGPStatus = "unknown"
	} else {
		bgpState, uptime, imported, exported, found := parseBIRDProtocol(birdResult)
		if !found {
			// Protocol not found in BIRD — check if WG interface exists before returning 404
			_, wgErr := h.wgExecutor.GetStatus(name)
			if wgErr != nil {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(ErrorResponse{Error: "Peer not found"})
				return
			}
			resp.BGPStatus = "not_found"
		} else {
			bgpFound = true
			resp.BGPStatus = bgpState
			resp.Uptime = uptime
			resp.RoutesImported = imported
			resp.RoutesExported = exported
		}
	}

	// Query WireGuard interface status
	wgResult, err := h.wgExecutor.GetStatus(name)
	if err != nil {
		// WG query failed — might be interface doesn't exist or wg not available
		slog.Warn("WireGuard query failed for peer", "peer", name, "error", err)
		resp.WGStatus = "unknown"
		resp.LastHandshake = ""

		// If neither BIRD nor WG found this peer, 404
		if !bgpFound {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Peer not found"})
			return
		}
	} else {
		wgStatus, handshake, transfer := parseWireGuardStatus(wgResult)
		resp.WGStatus = wgStatus
		resp.LastHandshake = handshake
		resp.Transfer = transfer
	}

	json.NewEncoder(w).Encode(resp)
}

// parseBIRDProtocol extracts BGP state, uptime, and route counts from
// `show protocols all "<name>"` output.
//
// Example output:
//
//	dn42_4242420337  BGP      ---        up     2026-05-18 06:12:34  Established
//	  ...
//	  Routes:         842 imported, 15 exported, 842 preferred
func parseBIRDProtocol(output string) (state, uptime string, imported, exported int, found bool) {
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Match protocol summary line: name BGP --- state date State
		if strings.Contains(line, "BGP") && !strings.HasPrefix(trimmed, "Type:") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 6 {
				// State is the last field (e.g., "Established", "Connect", "Active", "Idle")
				state = parts[len(parts)-1]
				found = true

				// Try to extract uptime from date/time fields
				// Format: "up     2026-05-18 06:12:34" — fields[3] is "up", fields[4] is date
				for i, p := range parts {
					if p == "up" && i+1 < len(parts) {
						// Collect date+time as uptime reference
						uptime = strings.Join(parts[i+1:len(parts)-1], " ")
						break
					}
				}
			}
		}

		// Match routes line: "Routes:         842 imported, 15 exported, 842 preferred"
		if strings.Contains(trimmed, "Routes:") && strings.Contains(trimmed, "imported") {
			re := regexp.MustCompile(`(\d+)\s+imported.*?(\d+)\s+exported`)
			matches := re.FindStringSubmatch(trimmed)
			if len(matches) >= 3 {
				imported, _ = strconv.Atoi(matches[1])
				exported, _ = strconv.Atoi(matches[2])
			}
		}
	}

	return state, uptime, imported, exported, found
}

// parseWireGuardStatus extracts connection status, handshake time, and transfer
// stats from `wg show <name>` output.
//
// Example output:
//
//	interface: dn42_4242420337
//	  ...
//	peer: <pubkey>
//	  endpoint: 1.2.3.4:51820
//	  latest handshake: 1 minute, 30 seconds ago
//	  transfer: 1.23 GiB received, 456 MiB sent
func parseWireGuardStatus(output string) (status, handshake string, transfer *TransferStats) {
	if output == "" {
		return "down", "", nil
	}

	status = "up"
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "latest handshake:") {
			handshake = strings.TrimPrefix(trimmed, "latest handshake:")
			handshake = strings.TrimSpace(handshake)
		}

		if strings.HasPrefix(trimmed, "transfer:") {
			transferStr := strings.TrimPrefix(trimmed, "transfer:")
			transferStr = strings.TrimSpace(transferStr)
			transfer = parseTransferLine(transferStr)
		}
	}

	// If no handshake was seen, the tunnel may have no active peer
	if handshake == "" {
		handshake = "never"
	}

	return status, handshake, transfer
}

// parseTransferLine parses "1.23 GiB received, 456 MiB sent" into TransferStats.
func parseTransferLine(line string) *TransferStats {
	// Expected format: "X.XX UnitA received, Y.YY UnitB sent"
	parts := strings.Split(line, ",")
	if len(parts) < 2 {
		return &TransferStats{RX: line, TX: ""}
	}

	rx := strings.TrimSpace(parts[0])
	rx = strings.TrimSuffix(rx, " received")

	tx := strings.TrimSpace(parts[1])
	tx = strings.TrimSuffix(tx, " sent")

	return &TransferStats{RX: rx, TX: tx}
}

