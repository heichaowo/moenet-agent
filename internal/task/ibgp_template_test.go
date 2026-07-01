package task

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// TestIBGPTemplateRenders guards the iBGP config template — a parse/render error
// would break iBGP config generation on every node (a startup/reconcile failure).
func TestIBGPTemplateRenders(t *testing.T) {
	tmpl, err := template.New("ibgp").Parse(ibgpTemplate)
	if err != nil {
		t.Fatalf("parse ibgpTemplate: %v", err)
	}

	data := map[string]interface{}{
		"NodeID":         3,
		"NodeName":       "hk1",
		"LoopbackIPv6":   "fd00:4242:7777:101:3::1",
		"LoopbackIPv4":   "172.22.188.3",
		"IsRR":           true,
		"MarkAsRRClient": true,
		"LocalLoopback":  "fd00:4242:7777:101:1::1",
		"LocalASN":       uint32(4242420998),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute ibgpTemplate: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"local as 4242420998;",                   // L3: configurable ASN renders
		"neighbor fd00:4242:7777:101:3::1 as 4242420998;",
		"rr client;",                             // MarkAsRRClient path
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ibgp config missing %q\n---\n%s", want, out)
		}
	}
}
