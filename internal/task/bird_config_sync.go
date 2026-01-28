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

	mu             sync.RWMutex
	lastConfigHash string
	templates      map[string]*template.Template
}

// NewBirdConfigSync creates a new BIRD config sync handler
func NewBirdConfigSync(cfg *config.Config, birdPool *bird.Pool, httpClient *httpclient.Client) (*BirdConfigSync, error) {
	confDir := "/etc/bird"

	s := &BirdConfigSync{
		config:     cfg,
		birdPool:   birdPool,
		httpClient: httpClient,
		confDir:    confDir,
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
	url := fmt.Sprintf("%s/agent/%s/bird-config", s.config.ControlPlane.URL, s.config.Node.Name)

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

// filtersTemplate is the Go template for filters.conf (migrated from Jinja2)
const filtersTemplate = `# =============================================================================
# BIRD Filters for {{.Node.Name}} - Auto-generated by moenet-agent
# Generated at: {{now}}
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
# MoeNet Communities for {{.Node.Name}}
# Auto-generated by moenet-agent
# =============================================================================

# Node Info: {{.Node.Name}} (ID: {{.Node.ID}}, Region: {{.Node.RegionCode}})
# Bandwidth: {{.Node.Bandwidth}}
`
