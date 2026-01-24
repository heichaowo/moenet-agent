package bird

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewTemplateRenderer(t *testing.T) {
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	peerConfDir := filepath.Join(tmpDir, "peers")
	ibgpConfDir := filepath.Join(tmpDir, "ibgp")

	// Create template directory and files
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	ebgpTemplate := `# eBGP peer AS{{.ASN}}`
	if err := os.WriteFile(filepath.Join(templateDir, "ebgp.conf.tmpl"), []byte(ebgpTemplate), 0644); err != nil {
		t.Fatalf("Failed to write ebgp template: %v", err)
	}

	ibgpTemplate := `# iBGP peers`
	if err := os.WriteFile(filepath.Join(templateDir, "ibgp.conf.tmpl"), []byte(ibgpTemplate), 0644); err != nil {
		t.Fatalf("Failed to write ibgp template: %v", err)
	}

	// Rename the files to match what the renderer expects
	if err := os.Rename(filepath.Join(templateDir, "ebgp.conf.tmpl"), filepath.Join(templateDir, "peer.conf.tmpl")); err != nil {
		t.Fatalf("Failed to rename template: %v", err)
	}

	renderer, err := NewTemplateRenderer(templateDir, peerConfDir, ibgpConfDir)
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	if renderer == nil {
		t.Fatal("Renderer is nil")
	}

	// Verify directories were created
	if _, err := os.Stat(peerConfDir); os.IsNotExist(err) {
		t.Error("Peer conf dir was not created")
	}
	if _, err := os.Stat(ibgpConfDir); os.IsNotExist(err) {
		t.Error("IBGP conf dir was not created")
	}
}

func TestWritePeer(t *testing.T) {
	tmpDir := t.TempDir()
	peerConfDir := filepath.Join(tmpDir, "peers")
	if err := os.MkdirAll(peerConfDir, 0755); err != nil {
		t.Fatalf("Failed to create peer conf dir: %v", err)
	}

	renderer := &TemplateRenderer{
		peerConfDir: peerConfDir,
	}

	config := "# Test peer config for AS4242420919"
	err := renderer.WritePeer(4242420919, config)
	if err != nil {
		t.Fatalf("Failed to write peer: %v", err)
	}

	// Verify file exists
	expected := filepath.Join(peerConfDir, "dn42_4242420919.conf")
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("Failed to read peer file: %v", err)
	}

	if string(data) != config {
		t.Errorf("Config content mismatch")
	}
}

func TestRemovePeer(t *testing.T) {
	tmpDir := t.TempDir()
	peerConfDir := filepath.Join(tmpDir, "peers")
	if err := os.MkdirAll(peerConfDir, 0755); err != nil {
		t.Fatalf("Failed to create peer conf dir: %v", err)
	}

	// Create a peer file
	peerFile := filepath.Join(peerConfDir, "dn42_4242420919.conf")
	if err := os.WriteFile(peerFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create peer file: %v", err)
	}

	renderer := &TemplateRenderer{
		peerConfDir: peerConfDir,
	}

	err := renderer.RemovePeer(4242420919)
	if err != nil {
		t.Fatalf("Failed to remove peer: %v", err)
	}

	// Verify file was removed
	if _, err := os.Stat(peerFile); !os.IsNotExist(err) {
		t.Error("Peer file was not removed")
	}
}

func TestRemovePeerNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	renderer := &TemplateRenderer{
		peerConfDir: tmpDir,
	}

	// Should not error when removing nonexistent file
	err := renderer.RemovePeer(9999999)
	if err != nil {
		t.Errorf("Should not error on nonexistent file: %v", err)
	}
}

func TestRenderPeer(t *testing.T) {
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Create a simple template
	tmpl := `# Peer AS{{.ASN}} - {{.Name}}
neighbor {{.NeighborAddr}} as {{.ASN}};`
	if err := os.WriteFile(filepath.Join(templateDir, "peer.conf.tmpl"), []byte(tmpl), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	renderer, err := NewTemplateRenderer(templateDir, tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := PeerData{
		ASN:          4242420919,
		Name:         "Test Peer",
		NeighborAddr: "fe80::1",
	}

	result, err := renderer.RenderPeer(data)
	if err != nil {
		t.Fatalf("Failed to render peer: %v", err)
	}

	if !strings.Contains(result, "AS4242420919") {
		t.Error("Rendered output missing ASN")
	}
	if !strings.Contains(result, "Test Peer") {
		t.Error("Rendered output missing peer name")
	}
	if !strings.Contains(result, "fe80::1") {
		t.Error("Rendered output missing neighbor address")
	}
}
