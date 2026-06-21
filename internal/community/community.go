// Package community manages BGP community parsing, modification, and filtering rules.
package community

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/moenet/moenet-agent/internal/bird"
)

// DN42 Community ASN prefix.
const DN42CommunityASN = 64511

// Community represents a standard BGP community (ASN, Value).
type Community struct {
	ASN   int `json:"asn"`
	Value int `json:"value"`
}

// LargeCommunity represents a BGP large community (ASN, Type, Value).
type LargeCommunity struct {
	ASN   int `json:"asn"`
	Type  int `json:"type"`
	Value int `json:"value"`
}

// DN42 Latency Communities (64511, 1-9) — Round Trip Time tiers.
var DN42Latency = map[int]Community{
	0: {DN42CommunityASN, 1}, // RTT < 2.7ms
	1: {DN42CommunityASN, 2}, // RTT < 7.3ms
	2: {DN42CommunityASN, 3}, // RTT < 20ms
	3: {DN42CommunityASN, 4}, // RTT < 55ms
	4: {DN42CommunityASN, 5}, // RTT < 148ms
	5: {DN42CommunityASN, 6}, // RTT < 403ms
	6: {DN42CommunityASN, 7}, // RTT < 1097ms
	7: {DN42CommunityASN, 8}, // RTT < 2981ms
	8: {DN42CommunityASN, 9}, // RTT >= 2981ms
}

// LatencyThresholds defines RTT upper bounds (ms) for each latency tier.
var LatencyThresholds = []float64{2.7, 7.3, 20, 55, 148, 403, 1097, 2981}

// DN42 Bandwidth Communities (64511, 21-26).
// Formula: bw >= 10^(x-2) mbit (ascending order per DN42 standard).
var DN42Bandwidth = map[string]Community{
	"100k": {DN42CommunityASN, 21}, // >= 0.1 Mbit (100 Kbps)
	"1m":   {DN42CommunityASN, 22}, // >= 1 Mbit
	"10m":  {DN42CommunityASN, 23}, // >= 10 Mbit
	"100m": {DN42CommunityASN, 24}, // >= 100 Mbit
	"1g":   {DN42CommunityASN, 25}, // >= 1000 Mbit (1 Gbps)
	"10g":  {DN42CommunityASN, 26}, // >= 10000 Mbit (10 Gbps, MoeNet extension)
}

// DN42 Crypto Communities (64511, 31-34).
var DN42Crypto = map[string]Community{
	"none":      {DN42CommunityASN, 31}, // No encryption
	"unsafe":    {DN42CommunityASN, 32}, // Encrypted but insecure
	"encrypted": {DN42CommunityASN, 33}, // Encrypted (WireGuard, OpenVPN)
	"latency":   {DN42CommunityASN, 34}, // Encrypted with latency-critical
}

// DN42 Region Communities (64511, 41-57) — May 2022 revision.
var DN42Region = map[string]Community{
	"eu":    {DN42CommunityASN, 41}, // Europe
	"na-e":  {DN42CommunityASN, 42}, // North America - East
	"na-c":  {DN42CommunityASN, 43}, // North America - Central
	"na-w":  {DN42CommunityASN, 44}, // North America - West
	"ca":    {DN42CommunityASN, 45}, // Central America
	"sa-e":  {DN42CommunityASN, 46}, // South America - East
	"sa-w":  {DN42CommunityASN, 47}, // South America - West
	"af-n":  {DN42CommunityASN, 48}, // Africa - North (above Sahara)
	"af-s":  {DN42CommunityASN, 49}, // Africa - South (below Sahara)
	"as-s":  {DN42CommunityASN, 50}, // Asia - South (IN, PK, BD)
	"as-se": {DN42CommunityASN, 51}, // Asia - Southeast (TH, SG, PH, ID, MY)
	"as-e":  {DN42CommunityASN, 52}, // Asia - East (JP, CN, KR, TW, HK)
	"oc":    {DN42CommunityASN, 53}, // Pacific & Oceania (AU, NZ, FJ)
	"ant":   {DN42CommunityASN, 54}, // Antarctica
	"as-n":  {DN42CommunityASN, 55}, // Asia - North (RU)
	"as-w":  {DN42CommunityASN, 56}, // Asia - West (IR, TR, UAE)
	"as-ca": {DN42CommunityASN, 57}, // Central Asia (AF, UZ, KZ)
}

// DN42 Action Communities.
var DN42Actions = map[string]Community{
	"no_export":   {DN42CommunityASN, 65281},
	"no_announce": {DN42CommunityASN, 65282},
}

// LatencyToTier converts RTT in milliseconds to a latency tier (0-8).
func LatencyToTier(rttMs float64) int {
	for tier, threshold := range LatencyThresholds {
		if rttMs < threshold {
			return tier
		}
	}
	return 8
}

// RouteCommunities holds parsed communities for a route.
type RouteCommunities struct {
	Prefix           string           `json:"prefix"`
	ASPath           []uint32         `json:"as_path"`
	Communities      []Community      `json:"communities"`
	LargeCommunities []LargeCommunity `json:"large_communities"`

	// Classified community values
	LatencyTier *int   `json:"latency_tier"`
	Bandwidth   string `json:"bandwidth,omitempty"`
	Crypto      string `json:"crypto,omitempty"`
	Region      string `json:"region,omitempty"`
	Actions     []string `json:"actions,omitempty"`

	// Human-readable descriptions
	Descriptions []string `json:"descriptions,omitempty"`
}

// PeerSettings holds community settings for a specific peer.
type PeerSettings struct {
	LatencyTier *int    `json:"latency_tier,omitempty"`
	Bandwidth   string  `json:"bandwidth,omitempty"`
	Crypto      string  `json:"crypto,omitempty"`
	Region      string  `json:"region,omitempty"`
	LastRTT     float64 `json:"last_rtt,omitempty"`
}

// FilterRule represents a community-based filter rule.
type FilterRule struct {
	Name           string   `json:"name"`
	MatchType      string   `json:"match_type"`      // "community", "large_community", "as_path"
	MatchValue     string   `json:"match_value"`      // e.g., "(64511, 1..9)" or "4242420000..4242429999"
	Action         string   `json:"action"`           // "accept", "reject", "modify"
	ModifyCommands []string `json:"modify_commands,omitempty"` // For action=modify
}

// CommunityStats holds aggregate community usage statistics.
type CommunityStats struct {
	TotalRoutes           int            `json:"total_routes"`
	LatencyDistribution   map[int]int    `json:"latency_distribution"`
	BandwidthDistribution map[string]int `json:"bandwidth_distribution"`
	CryptoDistribution    map[string]int `json:"crypto_distribution"`
	RegionDistribution    map[string]int `json:"region_distribution"`
}

// Manager manages BGP communities for routes and peers.
type Manager struct {
	birdPool  *bird.Pool
	filterDir string

	mu              sync.RWMutex
	peerCommunities map[uint32]PeerSettings
	filterRules     []FilterRule
}

// Regex patterns for parsing BIRD output.
var (
	stdCommunityRe   = regexp.MustCompile(`\((\d+),\s*(\d+)\)`)
	largeCommunityRe = regexp.MustCompile(`\((\d+),\s*(\d+),\s*(\d+)\)`)
)

// NewManager creates a new community manager.
func NewManager(birdPool *bird.Pool, filterDir string) *Manager {
	// Ensure filter directory exists
	if err := os.MkdirAll(filterDir, 0755); err != nil {
		slog.Warn("failed to create filter directory", "dir", filterDir, "error", err)
	}

	return &Manager{
		birdPool:        birdPool,
		filterDir:       filterDir,
		peerCommunities: make(map[uint32]PeerSettings),
	}
}

// GetRouteCommunities queries communities for a specific route/prefix.
func (m *Manager) GetRouteCommunities(prefix string) (*RouteCommunities, error) {
	result, err := m.birdPool.Execute(fmt.Sprintf("show route for %s all", prefix))
	if err != nil {
		return nil, fmt.Errorf("BIRD query failed: %w", err)
	}

	route := m.parseRouteOutput(result, prefix)
	if route == nil {
		return nil, nil
	}
	return route, nil
}

// GetPeerRoutesCommunities queries communities for routes from a specific peer.
func (m *Manager) GetPeerRoutesCommunities(asn uint32, limit int) ([]RouteCommunities, error) {
	protocolName := fmt.Sprintf("dn42_%d", asn)
	result, err := m.birdPool.Execute(fmt.Sprintf("show route protocol %s all", protocolName))
	if err != nil {
		return nil, fmt.Errorf("BIRD query failed: %w", err)
	}

	var routes []RouteCommunities
	var currentPrefix string
	var currentLines []string

	for _, line := range strings.Split(result, "\n") {
		// New route starts with a non-whitespace character
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "BIRD") {
			if currentPrefix != "" && len(currentLines) > 0 {
				route := m.parseRouteOutput(strings.Join(currentLines, "\n"), currentPrefix)
				if route != nil {
					routes = append(routes, *route)
					if len(routes) >= limit {
						return routes, nil
					}
				}
			}
			// Extract prefix from line (first word)
			parts := strings.Fields(line)
			if len(parts) > 0 {
				currentPrefix = parts[0]
				currentLines = []string{line}
			}
		} else {
			currentLines = append(currentLines, line)
		}
	}

	// Process last route
	if currentPrefix != "" && len(currentLines) > 0 && len(routes) < limit {
		route := m.parseRouteOutput(strings.Join(currentLines, "\n"), currentPrefix)
		if route != nil {
			routes = append(routes, *route)
		}
	}

	return routes, nil
}

// parseRouteOutput parses BIRD route output to extract communities.
func (m *Manager) parseRouteOutput(output, prefix string) *RouteCommunities {
	route := &RouteCommunities{
		Prefix:  prefix,
		Actions: []string{},
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		// Parse AS path
		if strings.Contains(line, "BGP.as_path:") || strings.Contains(line, "bgp_path:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				for _, asStr := range strings.Fields(parts[1]) {
					if asn, err := strconv.ParseUint(asStr, 10, 32); err == nil {
						route.ASPath = append(route.ASPath, uint32(asn))
					}
				}
			}
		}

		// Parse standard communities
		if strings.Contains(line, "BGP.community:") || strings.Contains(line, "bgp_community:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				for _, match := range stdCommunityRe.FindAllStringSubmatch(parts[1], -1) {
					if len(match) == 3 {
						asn, _ := strconv.Atoi(match[1])
						val, _ := strconv.Atoi(match[2])
						c := Community{ASN: asn, Value: val}
						route.Communities = append(route.Communities, c)
						m.classifyCommunity(route, c)
					}
				}
			}
		}

		// Parse large communities
		if strings.Contains(line, "BGP.large_community:") || strings.Contains(line, "bgp_large_community:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				for _, match := range largeCommunityRe.FindAllStringSubmatch(parts[1], -1) {
					if len(match) == 4 {
						a, _ := strconv.Atoi(match[1])
						t, _ := strconv.Atoi(match[2])
						v, _ := strconv.Atoi(match[3])
						route.LargeCommunities = append(route.LargeCommunities, LargeCommunity{ASN: a, Type: t, Value: v})
					}
				}
			}
		}
	}

	// Add human-readable descriptions
	for _, c := range route.Communities {
		route.Descriptions = append(route.Descriptions, DescribeCommunity(c))
	}

	return route
}

// classifyCommunity classifies a standard community and updates route attributes.
func (m *Manager) classifyCommunity(route *RouteCommunities, c Community) {
	if c.ASN != DN42CommunityASN {
		return
	}

	// Check latency
	for tier, expected := range DN42Latency {
		if c.Value == expected.Value {
			route.LatencyTier = &tier
			return
		}
	}

	// Check bandwidth
	for name, expected := range DN42Bandwidth {
		if c.Value == expected.Value {
			route.Bandwidth = name
			return
		}
	}

	// Check crypto
	for name, expected := range DN42Crypto {
		if c.Value == expected.Value {
			route.Crypto = name
			return
		}
	}

	// Check region
	for name, expected := range DN42Region {
		if c.Value == expected.Value {
			route.Region = name
			return
		}
	}

	// Check actions
	for name, expected := range DN42Actions {
		if c.Value == expected.Value {
			route.Actions = append(route.Actions, name)
			return
		}
	}
}

// GetPeerCommunities retrieves cached community settings for a peer.
func (m *Manager) GetPeerCommunities(asn uint32) PeerSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.peerCommunities[asn]
}

// SetPeerCommunities stores community settings for a peer.
func (m *Manager) SetPeerCommunities(asn uint32, settings PeerSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peerCommunities[asn] = settings
	slog.Info("set communities for peer", "asn", asn, "settings", settings)
}

// AddFilterRule adds a community filter rule and regenerates config.
func (m *Manager) AddFilterRule(rule FilterRule) error {
	m.mu.Lock()
	m.filterRules = append(m.filterRules, rule)
	m.mu.Unlock()

	if err := m.regenerateFilterConfig(); err != nil {
		return fmt.Errorf("failed to regenerate filter config: %w", err)
	}
	return nil
}

// RemoveFilterRule removes a filter rule by name and regenerates config.
func (m *Manager) RemoveFilterRule(name string) bool {
	m.mu.Lock()
	originalLen := len(m.filterRules)
	filtered := make([]FilterRule, 0, originalLen)
	for _, r := range m.filterRules {
		if r.Name != name {
			filtered = append(filtered, r)
		}
	}
	m.filterRules = filtered
	removed := len(m.filterRules) < originalLen
	m.mu.Unlock()

	if removed {
		if err := m.regenerateFilterConfig(); err != nil {
			slog.Error("failed to regenerate filter config after removal", "error", err)
		}
	}
	return removed
}

// ListFilterRules returns all filter rules.
func (m *Manager) ListFilterRules() []FilterRule {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]FilterRule, len(m.filterRules))
	copy(result, m.filterRules)
	return result
}

// regenerateFilterConfig writes BIRD filter configuration from rules.
func (m *Manager) regenerateFilterConfig() error {
	m.mu.RLock()
	rules := make([]FilterRule, len(m.filterRules))
	copy(rules, m.filterRules)
	m.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("# =============================================================================\n")
	sb.WriteString("# Auto-generated community filter rules\n")
	sb.WriteString("# DO NOT EDIT - Managed by MoeNet Agent\n")
	sb.WriteString("# =============================================================================\n\n")

	for i, rule := range rules {
		funcName := fmt.Sprintf("community_rule_%d", i)
		sb.WriteString(fmt.Sprintf("# Rule: %s\n", rule.Name))
		sb.WriteString(fmt.Sprintf("function %s() {\n", funcName))

		switch rule.MatchType {
		case "community":
			sb.WriteString(fmt.Sprintf("    if (%s ~ bgp_community) then {\n", rule.MatchValue))
		case "large_community":
			sb.WriteString(fmt.Sprintf("    if (%s ~ bgp_large_community) then {\n", rule.MatchValue))
		case "as_path":
			sb.WriteString(fmt.Sprintf("    if (bgp_path ~ [%s]) then {\n", rule.MatchValue))
		}

		switch rule.Action {
		case "reject":
			sb.WriteString("        return false;\n")
		case "accept":
			sb.WriteString("        return true;\n")
		case "modify":
			for _, cmd := range rule.ModifyCommands {
				sb.WriteString(fmt.Sprintf("        %s;\n", cmd))
			}
			sb.WriteString("        return true;\n")
		}

		sb.WriteString("    }\n")
		sb.WriteString("    return true;\n")
		sb.WriteString("}\n\n")
	}

	filterPath := filepath.Join(m.filterDir, "community_rules.conf")
	if err := os.WriteFile(filterPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write filter config: %w", err)
	}

	slog.Info("regenerated community filters", "path", filterPath, "rules", len(rules))
	return nil
}

// GeneratePeerFilter generates BIRD filter snippet for a peer based on community settings.
func (m *Manager) GeneratePeerFilter(asn uint32) string {
	m.mu.RLock()
	settings, ok := m.peerCommunities[asn]
	m.mu.RUnlock()

	if !ok {
		return ""
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("# Community settings for AS%d", asn))

	if settings.LatencyTier != nil {
		tier := *settings.LatencyTier
		if tier >= 0 && tier <= 8 {
			c := DN42Latency[tier]
			lines = append(lines, fmt.Sprintf("define PEER_%d_LATENCY = (%d, %d);", asn, c.ASN, c.Value))
		}
	}

	if settings.Bandwidth != "" {
		if c, ok := DN42Bandwidth[settings.Bandwidth]; ok {
			lines = append(lines, fmt.Sprintf("define PEER_%d_BANDWIDTH = (%d, %d);", asn, c.ASN, c.Value))
		}
	}

	if settings.Region != "" {
		if c, ok := DN42Region[settings.Region]; ok {
			lines = append(lines, fmt.Sprintf("define PEER_%d_REGION = (%d, %d);", asn, c.ASN, c.Value))
		}
	}

	return strings.Join(lines, "\n")
}

// GetCommunityStats returns aggregate community usage across all routes.
func (m *Manager) GetCommunityStats() (*CommunityStats, error) {
	result, err := m.birdPool.Execute("show route all")
	if err != nil {
		return nil, fmt.Errorf("BIRD query failed: %w", err)
	}

	stats := &CommunityStats{
		LatencyDistribution:   make(map[int]int),
		BandwidthDistribution: make(map[string]int),
		CryptoDistribution:    make(map[string]int),
		RegionDistribution:    make(map[string]int),
	}

	// Initialize all keys
	for i := 0; i <= 8; i++ {
		stats.LatencyDistribution[i] = 0
	}
	for k := range DN42Bandwidth {
		stats.BandwidthDistribution[k] = 0
	}
	for k := range DN42Crypto {
		stats.CryptoDistribution[k] = 0
	}
	for k := range DN42Region {
		stats.RegionDistribution[k] = 0
	}

	var currentLines []string
	for _, line := range strings.Split(result, "\n") {
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "BIRD") {
			if len(currentLines) > 0 {
				route := m.parseRouteOutput(strings.Join(currentLines, "\n"), "")
				if route != nil {
					stats.TotalRoutes++
					if route.LatencyTier != nil {
						stats.LatencyDistribution[*route.LatencyTier]++
					}
					if route.Bandwidth != "" {
						stats.BandwidthDistribution[route.Bandwidth]++
					}
					if route.Crypto != "" {
						stats.CryptoDistribution[route.Crypto]++
					}
					if route.Region != "" {
						stats.RegionDistribution[route.Region]++
					}
				}
			}
			currentLines = []string{line}
		} else {
			currentLines = append(currentLines, line)
		}
	}

	// Process last route
	if len(currentLines) > 0 {
		route := m.parseRouteOutput(strings.Join(currentLines, "\n"), "")
		if route != nil {
			stats.TotalRoutes++
			if route.LatencyTier != nil {
				stats.LatencyDistribution[*route.LatencyTier]++
			}
			if route.Bandwidth != "" {
				stats.BandwidthDistribution[route.Bandwidth]++
			}
			if route.Crypto != "" {
				stats.CryptoDistribution[route.Crypto]++
			}
			if route.Region != "" {
				stats.RegionDistribution[route.Region]++
			}
		}
	}

	return stats, nil
}
