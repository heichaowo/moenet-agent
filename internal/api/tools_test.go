// Package api – httptest-based handler tests for ToolsHandler.
//
// Tests focus on request-parsing, input validation, and auth; they do NOT
// require external binaries (ping, dig, bird) to succeed.  Where the handler
// shells out, we assert the HTTP status that results from the parsing path,
// not from the external command's exit code.
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestTools returns a ToolsHandler with a nil bird pool (ping/tcping/dig
// handlers do not access the pool) and the given auth token.
func newTestTools(token string) *ToolsHandler {
	return NewToolsHandler(nil, token)
}

// jsonBody encodes target into a ToolRequest JSON body.
func jsonBody(target string) *bytes.Reader {
	b, _ := json.Marshal(ToolRequest{Target: target})
	return bytes.NewReader(b)
}

// --- ping / tcping: host:port and hyphenated-name targets ---

// TestHandlePing_HostPortTarget guards against a regression where a colon in
// the target (e.g. "1.2.3.4:53") was incorrectly rejected by input validation.
func TestHandlePing_HostPortTarget(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("1.2.3.4:53"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("host:port target was incorrectly rejected: got 400, body=%s", w.Body.String())
	}
}

// TestHandlePing_HyphenatedHost verifies that a hyphenated hostname is not
// rejected by the input-validation filter.
func TestHandlePing_HyphenatedHost(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("my-node.dn42.example.com"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("hyphenated hostname incorrectly rejected: got 400, body=%s", w.Body.String())
	}
}

// TestHandleTcping_HostPort verifies that "host:port" input is parsed correctly
// by HandleTcping (net.SplitHostPort path) and the handler returns 200.
// TCP connections will fail in CI — the handler absorbs those errors and still
// returns 200 with a result string, so 200 is the right assertion here.
func TestHandleTcping_HostPort(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/tcping", jsonBody("1.2.3.4:53"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleTcping(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for host:port tcping, got %d: %s", w.Code, w.Body.String())
	}
	var resp ToolResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response body is not a valid ToolResponse: %v", err)
	}
}

// TestHandleTcping_HyphenatedHostPort verifies that a hyphenated hostname with
// port (e.g. "my-router.dn42:179") is not rejected with 400.
func TestHandleTcping_HyphenatedHostPort(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/tcping", jsonBody("my-router.dn42:179"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleTcping(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("hyphenated name:port incorrectly rejected: got 400, body=%s", w.Body.String())
	}
}

// --- dig: record-type whitelist ---

// TestHandleDig_ValidRecordType verifies that a whitelisted record type (AAAA)
// is accepted.  The handler may return 200 (or 500 if dig is absent in CI);
// a 400 would mean the whitelist is incorrectly blocking a valid type.
func TestHandleDig_ValidRecordType(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/dig", jsonBody("example.com AAAA"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleDig(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("valid record type AAAA incorrectly rejected: got 400, body=%s", w.Body.String())
	}
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("got 401 with no token configured")
	}
}

// TestHandleDig_DefaultRecordType verifies that a target with no type field
// defaults to A and is accepted (not rejected).
func TestHandleDig_DefaultRecordType(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/dig", jsonBody("example.com"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleDig(w, req)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("bare domain should use default A record and be accepted, got 400: %s", w.Body.String())
	}
}

// TestHandleDig_InvalidRecordType verifies that a non-whitelisted record type
// is rejected.  The whitelist guard returns an error from the fn callback,
// which the handler converts to 500.
func TestHandleDig_InvalidRecordType(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/dig", jsonBody("example.com NOTATYPE"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleDig(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("invalid record type NOTATYPE should be rejected, got 200")
	}
	// 500 is the expected status (whitelist error is surfaced as internal error)
	if w.Code != http.StatusInternalServerError {
		t.Logf("note: got %d (expected 500) for invalid type", w.Code)
	}
}

// TestHandleDig_InjectedShellCharsInTarget verifies that shell-special
// characters in the target field are blocked by ContainsAny before any
// command is executed — the handler must return 400.
func TestHandleDig_InjectedShellCharsInTarget(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"semicolon injection", "example.com; rm -rf /"},
		{"pipe injection", "example.com | cat /etc/passwd"},
		{"backtick injection", "example.com`id`"},
		{"dollar injection", "example.com $(id)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestTools("")
			req := httptest.NewRequest(http.MethodPost, "/dig", jsonBody(tc.target))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.HandleDig(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("injection target %q should get 400, got %d: %s",
					tc.target, w.Code, w.Body.String())
			}
		})
	}
}

// --- auth ---

// TestHandleTool_AuthRequired verifies that, when a token is configured, a
// request without any Authorization header gets 401.
func TestHandleTool_AuthRequired(t *testing.T) {
	h := newTestTools("supersecret")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("1.2.3.4"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no auth header, got %d", w.Code)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response body not valid ErrorResponse: %v", err)
	}
	if resp.Error != "Unauthorized" {
		t.Fatalf(`expected error "Unauthorized", got %q`, resp.Error)
	}
}

// TestHandleTool_WrongToken verifies that an incorrect Bearer token gets 401.
func TestHandleTool_WrongToken(t *testing.T) {
	h := newTestTools("supersecret")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("1.2.3.4"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}
}

// TestHandleTool_ValidToken verifies that the correct Bearer token is accepted
// (handler does not return 401).
func TestHandleTool_ValidToken(t *testing.T) {
	h := newTestTools("supersecret")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("1.2.3.4"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer supersecret")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("valid token should not return 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTool_NoTokenConfigured verifies that, when the token is empty,
// requests without auth headers are accepted (not 401) — auth is disabled.
func TestHandleTool_NoTokenConfigured(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/ping", jsonBody("1.2.3.4"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("empty-token handler should not require auth, got 401")
	}
}

// --- request validation ---

// TestHandleTool_MethodNotAllowed verifies that GET returns 405.
func TestHandleTool_MethodNotAllowed(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}
}

// TestHandleTool_MissingTarget verifies that an empty target returns 400.
func TestHandleTool_MissingTarget(t *testing.T) {
	h := newTestTools("")
	b, _ := json.Marshal(ToolRequest{Target: ""})
	req := httptest.NewRequest(http.MethodPost, "/ping", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty target, got %d", w.Code)
	}
}

// TestHandleTool_InvalidJSON verifies that malformed JSON body returns 400.
func TestHandleTool_InvalidJSON(t *testing.T) {
	h := newTestTools("")
	req := httptest.NewRequest(http.MethodPost, "/ping", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePing(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", w.Code)
	}
}
