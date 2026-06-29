package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchExpectedSHA256 guards the SHA256SUMS parsing — a bug here would
// reject every future fleet update (fail-closed), so it must be correct.
func TestFetchExpectedSHA256(t *testing.T) {
	body := "abc123def456  moenet-agent-linux-amd64\n" +
		"999fffeee  moenet-agent-linux-arm64\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	u := New("v3.4.3", "/tmp/agent", Config{}, "heichaowo/moenet-agent")

	got, err := u.fetchExpectedSHA256(context.Background(), srv.URL, "moenet-agent-linux-amd64")
	if err != nil {
		t.Fatalf("amd64: %v", err)
	}
	if got != "abc123def456" {
		t.Fatalf("amd64: got %q, want abc123def456", got)
	}

	got, err = u.fetchExpectedSHA256(context.Background(), srv.URL, "moenet-agent-linux-arm64")
	if err != nil || got != "999fffeee" {
		t.Fatalf("arm64: got %q err %v", got, err)
	}

	if _, err := u.fetchExpectedSHA256(context.Background(), srv.URL, "moenet-agent-windows-amd64"); err == nil {
		t.Fatal("expected error for missing asset")
	}
}
