package task

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// TestBirdConfTemplateRenders guards the bird.conf template: a syntax or
// field-reference error here would crash every agent on startup (the template
// is parsed in NewBirdConfigSync), so this must pass before any release.
func TestBirdConfTemplateRenders(t *testing.T) {
	tmpl, err := template.New("bird_conf").Parse(birdConfTemplate)
	if err != nil {
		t.Fatalf("parse birdConfTemplate: %v", err)
	}

	cfg := &BirdConfigResponse{
		Policy: BirdPolicy{
			DN42As: "4242420998",
			RPKIServers: []RPKIServer{
				{Name: "akae", Host: "rpki.akae.re", Port: 8082},
				{Name: "launchpadx", Host: "rpki.dn42.launchpadx.top", Port: 8082},
			},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		t.Fatalf("execute (populated): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"local as 4242420998;",          // #5: DN42As-driven local AS
		"protocol rpki rpki_akae",        // #6: RPKIServers range
		"protocol rpki rpki_launchpadx",
		`remote "rpki.dn42.launchpadx.top" port 8082`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered config missing %q", want)
		}
	}

	// Fallback path: empty RPKIServers must still produce a working RPKI block.
	cfg.Policy.RPKIServers = nil
	buf.Reset()
	if err := tmpl.Execute(&buf, cfg); err != nil {
		t.Fatalf("execute (empty RPKIServers): %v", err)
	}
	if !strings.Contains(buf.String(), "protocol rpki rpki_akae") {
		t.Errorf("fallback config missing rpki_akae")
	}
}
