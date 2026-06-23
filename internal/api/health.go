package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/netutil"
	"github.com/moenet/moenet-agent/internal/wireguard"
)

// HealthHandler handles health check and repair endpoints.
type HealthHandler struct {
	birdPool   *bird.Pool
	wgExecutor *wireguard.Executor
	token      string
	nodeName   string
	dn42IPv6   string // loopback IPv6 for deriving link-local
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(birdPool *bird.Pool, wgExecutor *wireguard.Executor, token, nodeName, dn42IPv6 string) *HealthHandler {
	return &HealthHandler{
		birdPool:   birdPool,
		wgExecutor: wgExecutor,
		token:      token,
		nodeName:   nodeName,
		dn42IPv6:   dn42IPv6,
	}
}

// --- Response types ---

// HealthResponse is the top-level JSON response for GET /health/sessions.
type HealthResponse struct {
	Node      string           `json:"node"`
	Timestamp int64            `json:"timestamp"`
	Sessions  []SessionHealth  `json:"sessions"`
	Summary   HealthSummary    `json:"summary"`
}

// SessionHealth is the per-session health status.
type SessionHealth struct {
	ASN       int       `json:"asn"`
	Interface string    `json:"interface"`
	WG        WGHealth  `json:"wg"`
	BGP       BGPHealth `json:"bgp"`
}

// WGHealth is the WireGuard health status for a session.
type WGHealth struct {
	Exists        bool            `json:"exists"`
	Up            bool            `json:"up"`
	LastHandshake string          `json:"last_handshake"`
	HandshakeOK   bool            `json:"handshake_ok"`
	HasLinkLocal  bool            `json:"has_link_local"`
	Transfer      *TransferStats  `json:"transfer,omitempty"`
}

// BGPHealth is the BIRD BGP health status for a session.
type BGPHealth struct {
	State          string `json:"state"`
	Uptime         string `json:"uptime,omitempty"`
	RoutesImported int    `json:"routes_imported"`
	RoutesExported int    `json:"routes_exported"`
}

// HealthSummary is the aggregate health summary.
type HealthSummary struct {
	Total       int `json:"total"`
	Established int `json:"established"`
	BGPDown     int `json:"bgp_down"`
	WGDown      int `json:"wg_down"`
}

// FixResponse is the JSON response for POST /health/fix.
type FixResponse struct {
	Node          string       `json:"node"`
	AddressesFixed int         `json:"addresses_fixed"`
	BGPRestarted   int         `json:"bgp_restarted"`
	Details        []FixDetail `json:"details,omitempty"`
}

// FixDetail is the per-session fix result.
type FixDetail struct {
	ASN       int    `json:"asn"`
	Interface string `json:"interface"`
	Action    string `json:"action"`
	Result    string `json:"result"`
}

// --- Handlers ---

// HandleHealthSessions handles GET /health/sessions — returns real-time WG + BGP status.
func (h *HealthHandler) HandleHealthSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if h.token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.token {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Unauthorized"})
			return
		}
	}

	// Discover all dn42_ interfaces
	interfaces := listDN42Interfaces()

	// Derive expected link-local address
	linkLocal := netutil.DeriveLinkLocal(h.dn42IPv6)

	// Query each interface concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup
	sessions := make([]SessionHealth, 0, len(interfaces))

	for _, ifname := range interfaces {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			sh := h.querySessionHealth(name, linkLocal)

			mu.Lock()
			sessions = append(sessions, sh)
			mu.Unlock()
		}(ifname)
	}
	wg.Wait()

	// Build summary
	summary := HealthSummary{Total: len(sessions)}
	for _, s := range sessions {
		switch {
		case !s.WG.Exists || !s.WG.Up:
			summary.WGDown++
		case s.BGP.State == "Established":
			summary.Established++
		default:
			summary.BGPDown++
		}
	}

	resp := HealthResponse{
		Node:      h.nodeName,
		Timestamp: time.Now().Unix(),
		Sessions:  sessions,
		Summary:   summary,
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleHealthFix handles POST /health/fix — repairs missing link-local addresses and restarts BGP.
func (h *HealthHandler) HandleHealthFix(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	if h.token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.token {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Unauthorized"})
			return
		}
	}

	linkLocal := netutil.DeriveLinkLocal(h.dn42IPv6)
	if linkLocal == "" {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Cannot derive link-local address from config"})
		return
	}

	interfaces := listDN42Interfaces()
	resp := FixResponse{Node: h.nodeName}

	// Strip prefix for address comparison
	addrOnly := linkLocal
	if idx := strings.Index(addrOnly, "/"); idx > 0 {
		addrOnly = addrOnly[:idx]
	}

	for _, ifname := range interfaces {
		asn := extractASNFromInterface(ifname)

		// Check if link-local is missing
		out, err := exec.Command("ip", "-6", "addr", "show", "dev", ifname).Output()
		if err != nil {
			resp.Details = append(resp.Details, FixDetail{
				ASN: asn, Interface: ifname,
				Action: "check_address", Result: fmt.Sprintf("error: %v", err),
			})
			continue
		}

		if strings.Contains(string(out), addrOnly) {
			continue // Address already present
		}

		// Add missing address
		if err := h.wgExecutor.AddAddress(ifname, linkLocal); err != nil {
			slog.Warn("health fix: failed to add link-local",
				"interface", ifname, "error", err)
			resp.Details = append(resp.Details, FixDetail{
				ASN: asn, Interface: ifname,
				Action: "add_address", Result: fmt.Sprintf("error: %v", err),
			})
			continue
		}
		resp.AddressesFixed++

		// Restart corresponding BGP protocol
		protocolName := ifname // dn42_XXXXXXXXXX is also the BIRD protocol name
		if _, err := h.birdPool.Execute("restart \"" + protocolName + "\""); err != nil {
			slog.Warn("health fix: BGP restart failed",
				"protocol", protocolName, "error", err)
			resp.Details = append(resp.Details, FixDetail{
				ASN: asn, Interface: ifname,
				Action: "restart_bgp", Result: fmt.Sprintf("error: %v", err),
			})
		} else {
			resp.BGPRestarted++
			resp.Details = append(resp.Details, FixDetail{
				ASN: asn, Interface: ifname,
				Action: "fix_and_restart", Result: "ok",
			})
		}

		slog.Info("health fix: restored link-local and restarted BGP",
			"interface", ifname, "asn", asn)
	}

	json.NewEncoder(w).Encode(resp)
}

// --- Internal helpers ---

// querySessionHealth gathers WG + BGP status for a single dn42_ interface.
func (h *HealthHandler) querySessionHealth(ifname, linkLocal string) SessionHealth {
	asn := extractASNFromInterface(ifname)
	sh := SessionHealth{
		ASN:       asn,
		Interface: ifname,
	}

	// --- WireGuard status ---
	sh.WG.Exists = h.wgExecutor.InterfaceExists(ifname)
	if sh.WG.Exists {
		sh.WG.Up = h.wgExecutor.IsInterfaceUp(ifname)

		wgResult, err := h.wgExecutor.GetStatus(ifname)
		if err == nil {
			wgStatus, handshake, transfer := parseWireGuardStatus(wgResult)
			sh.WG.LastHandshake = handshake
			sh.WG.Transfer = transfer
			sh.WG.HandshakeOK = wgStatus == "up" && handshake != "never"
		}

		// Check link-local
		if linkLocal != "" {
			addrOnly := linkLocal
			if idx := strings.Index(addrOnly, "/"); idx > 0 {
				addrOnly = addrOnly[:idx]
			}
			out, err := exec.Command("ip", "-6", "addr", "show", "dev", ifname).Output()
			if err == nil {
				sh.WG.HasLinkLocal = strings.Contains(string(out), addrOnly)
			}
		}
	}

	// --- BGP status ---
	protocolName := ifname
	birdResult, err := h.birdPool.Execute("show protocols all \"" + protocolName + "\"")
	if err != nil {
		sh.BGP.State = "unknown"
	} else {
		bgpState, uptime, imported, exported, found := parseBIRDProtocol(birdResult)
		if !found {
			sh.BGP.State = "not_found"
		} else {
			sh.BGP.State = bgpState
			sh.BGP.Uptime = uptime
			sh.BGP.RoutesImported = imported
			sh.BGP.RoutesExported = exported
		}
	}

	return sh
}

// listDN42Interfaces discovers dn42_ prefixed interfaces via ip link show.
func listDN42Interfaces() []string {
	out, err := exec.Command("ip", "-o", "link", "show").Output()
	if err != nil {
		return nil
	}

	var interfaces []string
	re := regexp.MustCompile(`\d+:\s+(dn42_\d+):`)
	for _, match := range re.FindAllStringSubmatch(string(out), -1) {
		if len(match) > 1 {
			interfaces = append(interfaces, match[1])
		}
	}
	return interfaces
}

// extractASNFromInterface extracts the ASN number from interface name "dn42_XXXXXXXXXX".
func extractASNFromInterface(ifname string) int {
	parts := strings.SplitN(ifname, "_", 2)
	if len(parts) < 2 {
		return 0
	}
	asn, _ := strconv.Atoi(parts[1])
	return asn
}

