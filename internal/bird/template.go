package bird

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// PeerData contains data for rendering BIRD peer configuration
type PeerData struct {
	ASN                uint32
	Name               string
	Description        string
	NeighborAddr       string
	SourceAddr         string
	Multihop           int
	Password           string
	IPv4Enabled        bool
	IPv6Enabled        bool
	IPv6Only           bool
	LatencyTier        int
	BandwidthCommunity int
	GeneratedAt        string
}

// IBGPData contains data for rendering iBGP configuration
type IBGPData struct {
	LocalASN    uint32
	GeneratedAt string
	Peers       []IBGPPeerData
}

// IBGPPeerData represents a single iBGP peer
type IBGPPeerData struct {
	Name         string
	LoopbackAddr string
	IsRR         bool
}

// TemplateRenderer handles BIRD configuration generation
type TemplateRenderer struct {
	templateDir string
	peerConfDir string
	ibgpConfDir string

	peerTemplate *template.Template
	ibgpTemplate *template.Template
}

// NewTemplateRenderer creates a new template renderer
func NewTemplateRenderer(templateDir, peerConfDir, ibgpConfDir string) (*TemplateRenderer, error) {
	r := &TemplateRenderer{
		templateDir: templateDir,
		peerConfDir: peerConfDir,
		ibgpConfDir: ibgpConfDir,
	}

	// Load templates
	peerTmplPath := filepath.Join(templateDir, "peer.conf.tmpl")
	if data, err := os.ReadFile(peerTmplPath); err == nil {
		tmpl, err := template.New("peer").Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("failed to parse peer template: %w", err)
		}
		r.peerTemplate = tmpl
	}

	ibgpTmplPath := filepath.Join(templateDir, "ibgp.conf.tmpl")
	if data, err := os.ReadFile(ibgpTmplPath); err == nil {
		tmpl, err := template.New("ibgp").Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("failed to parse ibgp template: %w", err)
		}
		r.ibgpTemplate = tmpl
	}

	// Ensure output directories exist
	os.MkdirAll(peerConfDir, 0755)
	os.MkdirAll(ibgpConfDir, 0755)

	return r, nil
}

// RenderPeer generates and writes a peer configuration file
func (r *TemplateRenderer) RenderPeer(data PeerData) (string, error) {
	if r.peerTemplate == nil {
		return "", fmt.Errorf("peer template not loaded")
	}

	data.GeneratedAt = time.Now().Format(time.RFC3339)

	var buf bytes.Buffer
	if err := r.peerTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return buf.String(), nil
}

// WritePeer writes peer configuration to file
func (r *TemplateRenderer) WritePeer(asn uint32, config string) error {
	filename := fmt.Sprintf("dn42_%d.conf", asn)
	path := filepath.Join(r.peerConfDir, filename)

	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write peer config: %w", err)
	}

	return nil
}

// RemovePeer removes a peer configuration file
func (r *TemplateRenderer) RemovePeer(asn uint32) error {
	filename := fmt.Sprintf("dn42_%d.conf", asn)
	path := filepath.Join(r.peerConfDir, filename)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove peer config: %w", err)
	}

	return nil
}

// RenderIBGP generates iBGP configuration
func (r *TemplateRenderer) RenderIBGP(data IBGPData) (string, error) {
	if r.ibgpTemplate == nil {
		return "", fmt.Errorf("ibgp template not loaded")
	}

	data.GeneratedAt = time.Now().Format(time.RFC3339)

	var buf bytes.Buffer
	if err := r.ibgpTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render ibgp template: %w", err)
	}

	return buf.String(), nil
}

// WriteIBGP writes iBGP configuration to file
func (r *TemplateRenderer) WriteIBGP(config string) error {
	path := filepath.Join(r.ibgpConfDir, "ibgp_peers.conf")

	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write ibgp config: %w", err)
	}

	return nil
}
