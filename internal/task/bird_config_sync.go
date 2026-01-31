package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
	"github.com/moenet/moenet-agent/internal/config"
	"github.com/moenet/moenet-agent/internal/httpclient"
)

// BirdConfigSync handles BIRD policy configuration synchronization from Control Plane
type BirdConfigSync struct {
	config     *config.Config
	birdPool   *bird.Pool
	httpClient *httpclient.Client
	confDir    string
	ibgpSync   *IBGPSync // Reference to iBGP sync for peer updates

	mu             sync.RWMutex
	lastConfigHash string
	templates      map[string]*template.Template
}

// NewBirdConfigSync creates a new BIRD config sync handler
func NewBirdConfigSync(cfg *config.Config, birdPool *bird.Pool, httpClient *httpclient.Client, ibgpSync *IBGPSync) (*BirdConfigSync, error) {
	confDir := "/etc/bird"

	s := &BirdConfigSync{
		config:     cfg,
		birdPool:   birdPool,
		httpClient: httpClient,
		confDir:    confDir,
		ibgpSync:   ibgpSync,
		templates:  make(map[string]*template.Template),
	}

	// Load embedded templates
	if err := s.loadTemplates(); err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	return s, nil
}

// Run starts the BIRD config sync task
func (s *BirdConfigSync) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Initial sync after 30 seconds (give time for other services to start)
	time.Sleep(30 * time.Second)
	log.Println("[BirdConfig] Performing initial sync...")
	if err := s.Sync(ctx); err != nil {
		log.Printf("[BirdConfig] Initial sync failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[BirdConfig] Task stopped")
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				log.Printf("[BirdConfig] Sync failed: %v", err)
			}
		}
	}
}

// Sync fetches configuration from Control Plane and renders templates if changed
func (s *BirdConfigSync) Sync(ctx context.Context) error {
	// Fetch configuration from Control Plane
	birdConfig, err := s.fetchBirdConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch bird config: %w", err)
	}

	// Always update iBGP peers (regardless of config hash)
	if s.ibgpSync != nil && len(birdConfig.IBGPPeers) > 0 {
		s.ibgpSync.UpdatePeersFromAPI(birdConfig.IBGPPeers)
		log.Printf("[BirdConfig] Updated iBGP peers: %d peers", len(birdConfig.IBGPPeers))
	}

	// Check if config has changed
	s.mu.RLock()
	lastHash := s.lastConfigHash
	s.mu.RUnlock()

	if birdConfig.ConfigHash == lastHash {
		log.Println("[BirdConfig] Config unchanged, skipping render")
		return nil
	}

	log.Printf("[BirdConfig] Config changed (hash: %s -> %s), rendering templates...",
		lastHash, birdConfig.ConfigHash)

	// Render templates
	if err := s.renderFilters(birdConfig); err != nil {
		return fmt.Errorf("failed to render filters.conf: %w", err)
	}

	if err := s.renderCommunities(birdConfig); err != nil {
		return fmt.Errorf("failed to render moenet_communities.conf: %w", err)
	}

	if err := s.renderBabel(birdConfig); err != nil {
		return fmt.Errorf("failed to render babel.conf: %w", err)
	}

	// Update last config hash
	s.mu.Lock()
	s.lastConfigHash = birdConfig.ConfigHash
	s.mu.Unlock()

	// Reload BIRD
	if err := s.birdPool.Configure(); err != nil {
		log.Printf("[BirdConfig] Warning: BIRD reconfigure failed: %v", err)
	} else {
		log.Println("[BirdConfig] BIRD configuration reloaded successfully")
	}

	return nil
}

// fetchBirdConfig retrieves BIRD configuration from Control Plane
func (s *BirdConfigSync) fetchBirdConfig(ctx context.Context) (*BirdConfigResponse, error) {
	url := fmt.Sprintf("%s/api/v1/agent/%s/bird-config", s.config.ControlPlane.URL, s.config.Node.Name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.config.ControlPlane.Token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Code int                `json:"code"`
		Data BirdConfigResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &apiResp.Data, nil
}

// loadTemplates loads the embedded Go templates
func (s *BirdConfigSync) loadTemplates() error {
	// filters.conf template
	filtersTmpl, err := template.New("filters").Parse(filtersTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse filters template: %w", err)
	}
	s.templates["filters"] = filtersTmpl

	// moenet_communities.conf template
	commTmpl, err := template.New("communities").Parse(communitiesTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse communities template: %w", err)
	}
	s.templates["communities"] = commTmpl

	// babel.conf template
	babelTmpl, err := template.New("babel").Parse(babelTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse babel template: %w", err)
	}
	s.templates["babel"] = babelTmpl

	return nil
}

// renderFilters renders the filters.conf template
func (s *BirdConfigSync) renderFilters(cfg *BirdConfigResponse) error {
	tmpl := s.templates["filters"]
	if tmpl == nil {
		return fmt.Errorf("filters template not loaded")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("template execution failed: %w", err)
	}

	path := filepath.Join(s.confDir, "filters.conf")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write filters.conf: %w", err)
	}

	log.Printf("[BirdConfig] Rendered filters.conf (%d bytes)", buf.Len())
	return nil
}

// renderCommunities renders the moenet_communities.conf template
func (s *BirdConfigSync) renderCommunities(cfg *BirdConfigResponse) error {
	tmpl := s.templates["communities"]
	if tmpl == nil {
		return fmt.Errorf("communities template not loaded")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("template execution failed: %w", err)
	}

	path := filepath.Join(s.confDir, "moenet_communities.conf")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write moenet_communities.conf: %w", err)
	}

	log.Printf("[BirdConfig] Rendered moenet_communities.conf (%d bytes)", buf.Len())
	return nil
}

// renderBabel renders the babel.conf template for Babel IGP
func (s *BirdConfigSync) renderBabel(cfg *BirdConfigResponse) error {
	tmpl := s.templates["babel"]
	if tmpl == nil {
		return fmt.Errorf("babel template not loaded")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return fmt.Errorf("template execution failed: %w", err)
	}

	path := filepath.Join(s.confDir, "babel.conf")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write babel.conf: %w", err)
	}

	log.Printf("[BirdConfig] Rendered babel.conf (%d bytes)", buf.Len())
	return nil
}

// filtersTemplate is the Go template for filters.conf (migrated from Jinja2)
const filtersTemplate = `# =============================================================================
# BIRD Filters for {{.Node.Name}} - Auto-generated by moenet-agent
# Generated by moenet-agent
# Config Hash: {{.ConfigHash}}
# =============================================================================

# -----------------------------------------------------------------------------
# DN42 BGP Community Definitions
# -----------------------------------------------------------------------------

# Latency Communities (64511, 1-9)
define DN42_LATENCY_0    = (64511, 1);  # RTT < 2.7ms
define DN42_LATENCY_1    = (64511, 2);  # RTT < 7.3ms
define DN42_LATENCY_2    = (64511, 3);  # RTT < 20ms
define DN42_LATENCY_3    = (64511, 4);  # RTT < 55ms
define DN42_LATENCY_4    = (64511, 5);  # RTT < 148ms
define DN42_LATENCY_5    = (64511, 6);  # RTT < 403ms
define DN42_LATENCY_6    = (64511, 7);  # RTT < 1097ms
define DN42_LATENCY_7    = (64511, 8);  # RTT < 2981ms
define DN42_LATENCY_8    = (64511, 9);  # RTT >= 2981ms

# Bandwidth Communities (64511, 21-25)
define DN42_BW_100M_PLUS = (64511, 21);
define DN42_BW_10G_PLUS  = (64511, 22);
define DN42_BW_1G_PLUS   = (64511, 23);
define DN42_BW_100K_PLUS = (64511, 24);
define DN42_BW_10M_PLUS  = (64511, 25);

# Crypto Communities (64511, 31-34)
define DN42_CRYPTO_NONE      = (64511, 31);
define DN42_CRYPTO_UNSAFE    = (64511, 32);
define DN42_CRYPTO_ENCRYPTED = (64511, 33);
define DN42_CRYPTO_LATENCY   = (64511, 34);

# Region Communities (64511, 41-53)
define DN42_REGION_EU       = (64511, 41);
define DN42_REGION_NA_E     = (64511, 42);
define DN42_REGION_NA_C     = (64511, 43);
define DN42_REGION_NA_W     = (64511, 44);
define DN42_REGION_CA       = (64511, 45);
define DN42_REGION_SA       = (64511, 46);
define DN42_REGION_AF       = (64511, 47);
define DN42_REGION_AS_S     = (64511, 48);
define DN42_REGION_AS_SE    = (64511, 49);
define DN42_REGION_AS_E     = (64511, 50);
define DN42_REGION_OC       = (64511, 51);
define DN42_REGION_ME       = (64511, 52);
define DN42_REGION_AS_N     = (64511, 53);

# Action Communities
define DN42_NO_EXPORT   = (64511, 65281);
define DN42_NO_ANNOUNCE = (64511, 65282);

# RFC 8326 Graceful Shutdown
define GRACEFUL_SHUTDOWN = (65535, 0);

# -----------------------------------------------------------------------------
# MoeNet Large Communities
# -----------------------------------------------------------------------------

define LC_ACCEPTED_HERE = ({{.Policy.DN42As}}, 100, {{.Node.ID}});
define LC_REJECT_SELF      = ({{.Policy.DN42As}}, 150, 1);
define LC_REJECT_PREFIX    = ({{.Policy.DN42As}}, 150, 2);
define LC_REJECT_ROA       = ({{.Policy.DN42As}}, 150, 3);
define LC_REJECT_PATH_LEN  = ({{.Policy.DN42As}}, 150, 4);
define LC_REJECT_BLACKLIST = ({{.Policy.DN42As}}, 150, 5);

# -----------------------------------------------------------------------------
# Prefix Validation
# -----------------------------------------------------------------------------

function is_valid_dn42_prefix() -> bool {
    return net ~ [
        172.20.0.0/14{21,29},
        172.20.0.0/24{28,32},
        172.21.0.0/24{28,32},
        172.22.0.0/24{28,32},
        172.23.0.0/24{28,32},
        172.31.0.0/16+,
        10.0.0.0/8{15,24}
    ];
}

function is_valid_dn42_prefix6() -> bool {
    return net ~ [
        fd00::/8{44,64}
    ];
}

# -----------------------------------------------------------------------------
# ROA Validation
# -----------------------------------------------------------------------------

function roa_check() -> bool {
    if (roa_check(dn42_roa, net, bgp_path.last) = ROA_VALID) then return true;
    if (roa_check(dn42_roa, net, bgp_path.last) = ROA_UNKNOWN) then return true;
    return false;
}

# -----------------------------------------------------------------------------
# Import/Export Filters
# -----------------------------------------------------------------------------

function update_local_pref_from_latency() {
    bgp_local_pref = 100;
    if (DN42_LATENCY_0 ~ bgp_community) then bgp_local_pref = 260;
    if (DN42_LATENCY_1 ~ bgp_community) then bgp_local_pref = 250;
    if (DN42_LATENCY_2 ~ bgp_community) then bgp_local_pref = 240;
    if (DN42_LATENCY_3 ~ bgp_community) then bgp_local_pref = 230;
    if (DN42_LATENCY_4 ~ bgp_community) then bgp_local_pref = 220;
    if (DN42_LATENCY_5 ~ bgp_community) then bgp_local_pref = 210;
    if (DN42_LATENCY_6 ~ bgp_community) then bgp_local_pref = 200;
    if (DN42_LATENCY_7 ~ bgp_community) then bgp_local_pref = 150;
    if (DN42_LATENCY_8 ~ bgp_community) then bgp_local_pref = 100;
    if (GRACEFUL_SHUTDOWN ~ bgp_community) then bgp_local_pref = 0;
}

filter dn42_import_filter {
    if (bgp_path.len > {{.Policy.ASPathMaxLen}}) then {
        bgp_large_community.add(LC_REJECT_PATH_LEN);
        reject "AS path too long";
    }
    if (!is_valid_dn42_prefix()) then {
        bgp_large_community.add(LC_REJECT_PREFIX);
        reject "Invalid DN42 prefix";
    }
    if (!roa_check()) then {
        bgp_large_community.add(LC_REJECT_ROA);
        reject "ROA check failed";
    }
    update_local_pref_from_latency();
    bgp_large_community.add(LC_ACCEPTED_HERE);
    accept;
}

filter dn42_export_filter {
    if (!is_valid_dn42_prefix()) then reject;
    if (source !~ [RTS_BGP, RTS_STATIC]) then reject;
    accept;
}
`

// communitiesTemplate is the Go template for moenet_communities.conf
const communitiesTemplate = `# =============================================================================
# MoeNet Large Community Definitions
# For internal cold potato routing within MoeNet backbone
# Auto-generated by moenet-agent
# =============================================================================

# Node Info: {{.Node.Name}} (ID: {{.Node.ID}}, Region: {{.Node.RegionCode}})
# Bandwidth: {{.Node.Bandwidth}}

# Our ASN
define MOENET_ASN = {{.Policy.DN42As}};

# -----------------------------------------------------------------------------
# Type 1: Continent Origin (for cold potato routing)
# Format: (4242420998, 1, <continent_code>)
# -----------------------------------------------------------------------------
define LC_ORIGIN_AS = (MOENET_ASN, 1, 100);  # Asia
define LC_ORIGIN_NA = (MOENET_ASN, 1, 200);  # North America
define LC_ORIGIN_EU = (MOENET_ASN, 1, 300);  # Europe
define LC_ORIGIN_OC = (MOENET_ASN, 1, 400);  # Oceania
define LC_ORIGIN_OTHER = (MOENET_ASN, 1, 500);  # Other (AF, ME, SA, CA)

# -----------------------------------------------------------------------------
# Type 2: Sub-region (more granular routing)
# Format: (4242420998, 2, <subregion_code>)
# Codes: 1xx=Asia, 2xx=NA, 3xx=EU, 4xx=OC, 5xx=Other
# -----------------------------------------------------------------------------

# Asia (matching DN42 standard)
define LC_REGION_AS_E  = (MOENET_ASN, 2, 101);  # East Asia: HK, JP, KR, TW
define LC_REGION_AS_SE = (MOENET_ASN, 2, 102);  # Southeast: SG, MY
define LC_REGION_AS_S  = (MOENET_ASN, 2, 103);  # South: IN
define LC_REGION_AS_N  = (MOENET_ASN, 2, 104);  # North: RU/Siberia

# North America (matching DN42 standard)
define LC_REGION_NA_E = (MOENET_ASN, 2, 201);  # East coast
define LC_REGION_NA_C = (MOENET_ASN, 2, 202);  # Central
define LC_REGION_NA_W = (MOENET_ASN, 2, 203);  # West coast
define LC_REGION_CA   = (MOENET_ASN, 2, 204);  # Central America
define LC_REGION_SA   = (MOENET_ASN, 2, 205);  # South America

# Europe (MoeNet extension, DN42 only has eu)
define LC_REGION_EU_W = (MOENET_ASN, 2, 301);  # Western: GB, FR
define LC_REGION_EU_C = (MOENET_ASN, 2, 302);  # Central: DE, CH, NL
define LC_REGION_EU_E = (MOENET_ASN, 2, 303);  # Eastern: PL, RU-West

# Oceania
define LC_REGION_OC = (MOENET_ASN, 2, 401);    # AU, NZ

# Other regions
define LC_REGION_AF = (MOENET_ASN, 2, 501);    # Africa
define LC_REGION_ME = (MOENET_ASN, 2, 502);    # Middle East

# -----------------------------------------------------------------------------
# Type 4: Link Characteristics
# Format: (4242420998, 4, <characteristic>)
# -----------------------------------------------------------------------------
define LC_LINK_INTERCONT = (MOENET_ASN, 4, 1);   # Intercontinental link
define LC_LINK_HIGH_LAT  = (MOENET_ASN, 4, 2);   # High latency (>200ms)
define LC_LINK_LOW_MTU   = (MOENET_ASN, 4, 3);   # Low MTU (<1400)

# -----------------------------------------------------------------------------
# Type 5: Granular Bandwidth (MoeNet internal only)
# Format: (4242420998, 5, <bandwidth_mbps>)
# Used for iBGP path selection within MoeNet backbone
# -----------------------------------------------------------------------------
define LC_BW_10G   = (MOENET_ASN, 5, 10000);  # 10 Gbps
define LC_BW_5G    = (MOENET_ASN, 5, 5000);   # 5 Gbps
define LC_BW_2G    = (MOENET_ASN, 5, 2000);   # 2 Gbps
define LC_BW_1G    = (MOENET_ASN, 5, 1000);   # 1 Gbps
define LC_BW_500M  = (MOENET_ASN, 5, 500);    # 500 Mbps
define LC_BW_200M  = (MOENET_ASN, 5, 200);    # 200 Mbps
define LC_BW_100M  = (MOENET_ASN, 5, 100);    # 100 Mbps
define LC_BW_50M   = (MOENET_ASN, 5, 50);     # 50 Mbps
define LC_BW_10M   = (MOENET_ASN, 5, 10);     # 10 Mbps

# Our node's bandwidth
define OUR_LC_BANDWIDTH = LC_BW_{{.Node.Bandwidth}};

# -----------------------------------------------------------------------------
# Helper: Map sub-region to continent
# -----------------------------------------------------------------------------
function get_continent_from_region(pair region) -> lc {
    if region = LC_REGION_AS_E  then return LC_ORIGIN_AS;
    if region = LC_REGION_AS_SE then return LC_ORIGIN_AS;
    if region = LC_REGION_AS_S  then return LC_ORIGIN_AS;
    if region = LC_REGION_AS_N  then return LC_ORIGIN_AS;
    if region = LC_REGION_NA_E  then return LC_ORIGIN_NA;
    if region = LC_REGION_NA_C  then return LC_ORIGIN_NA;
    if region = LC_REGION_NA_W  then return LC_ORIGIN_NA;
    if region = LC_REGION_CA    then return LC_ORIGIN_NA;
    if region = LC_REGION_SA    then return LC_ORIGIN_OTHER;
    if region = LC_REGION_EU_W  then return LC_ORIGIN_EU;
    if region = LC_REGION_EU_C  then return LC_ORIGIN_EU;
    if region = LC_REGION_EU_E  then return LC_ORIGIN_EU;
    if region = LC_REGION_OC    then return LC_ORIGIN_OC;
    if region = LC_REGION_AF    then return LC_ORIGIN_OTHER;
    if region = LC_REGION_ME    then return LC_ORIGIN_OTHER;
    return (0, 0, 0);
}

# -----------------------------------------------------------------------------
# Helper: Add MoeNet bandwidth to iBGP routes
# Call this in iBGP export filter
# -----------------------------------------------------------------------------
function add_moenet_bandwidth() {
    bgp_large_community.delete([(MOENET_ASN, 5, *)]);
    bgp_large_community.add(OUR_LC_BANDWIDTH);
}
`

// babelTemplate is the Go template for babel.conf (Babel IGP for mesh)
const babelTemplate = `# Babel IGP Configuration - Auto-generated by MoeNet Agent
# Purpose: Exchange loopback addresses for iBGP next-hop reachability
# Mode: P2P (one interface per peer)
# DO NOT EDIT MANUALLY

protocol babel babel_igp {
    # P2P mode: each peer has its own interface (dn42-wg-igp-{node_id})
    # Using wildcard to match all mesh interfaces

    interface "dn42-wg-igp-*" {
        type tunnel;            # WireGuard is a tunnel interface
        rxcost 64;              # Higher base cost, reduces RTT impact ratio

        # Hybrid approach: RTT for path selection, not for reachability
        # Low RTT weight + extreme threshold = smart routing without 65535
        rtt cost 32;            # Reduced weight (was 96)
        rtt min 200 ms;         # Start adding cost above 200ms
        rtt max 10000 ms;       # Extreme ceiling - RTT up to 10s still just "slow" not "dead"

        # Long intervals for stability on unreliable links
        hello interval 10 s;    # ~30s before neighbor timeout
        update interval 40 s;
    };

    # Dummy0 interface for loopback address announcement
    interface "dummy0" {
        type wired;
        rxcost 1;               # Very low cost for local interface
        hello interval 10 s;    # Match tunnel interfaces
        update interval 40 s;
    };

    ipv4 {
        # Exchange IPv4 loopback addresses for iBGP next-hop reachability
        import filter {
            # Only accept /32 loopback addresses within our loopback range
            if net.len = 32 && net ~ 172.22.188.0/26 then accept;
            reject;
        };
        export filter {
            # Only export our own loopback address
            if net.len = 32 && net ~ 172.22.188.0/26 then accept;
            reject;
        };
    };

    ipv6 {
        # CRITICAL: Only exchange loopback addresses!
        # Never propagate DN42 full table via Babel - use iBGP for that
        import filter {
            # Only accept /128 loopback addresses within our loopback range
            if net.len = 128 && net ~ fd00:4242:7777::/48 then accept;
            reject;
        };
        export filter {
            # Only export our own loopback address
            if net.len = 128 && net ~ fd00:4242:7777::/48 then accept;
            reject;
        };
    };
}
`
