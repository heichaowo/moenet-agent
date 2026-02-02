// Package api provides HTTP handlers for the agent API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/moenet/moenet-agent/internal/bird"
)

// ToolsHandler handles network diagnostic tool requests.
type ToolsHandler struct {
	birdPool *bird.Pool
	token    string // Authentication token
}

// NewToolsHandler creates a new tools handler.
func NewToolsHandler(birdPool *bird.Pool, token string) *ToolsHandler {
	return &ToolsHandler{
		birdPool: birdPool,
		token:    token,
	}
}

// ToolRequest is the request body for tool endpoints.
type ToolRequest struct {
	Target string `json:"target"`
}

// ToolResponse is the response for tool endpoints.
type ToolResponse struct {
	Result string `json:"result"`
}

// HandlePing handles POST /ping - ICMP ping
func (h *ToolsHandler) HandlePing(w http.ResponseWriter, r *http.Request) {
	h.handleTool(w, r, func(target string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "ping", "-c", "4", "-W", "2", target)
		output, err := cmd.CombinedOutput()
		if err != nil {
			// ping returns non-zero on packet loss, include output anyway
			return string(output), nil
		}
		return string(output), nil
	})
}

// HandleTcping handles POST /tcping - TCP connectivity test
func (h *ToolsHandler) HandleTcping(w http.ResponseWriter, r *http.Request) {
	h.handleTool(w, r, func(target string) (string, error) {
		// Parse host:port
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			// No port specified, default to 80
			host = target
			port = "80"
		}

		var results []string
		for i := 0; i < 4; i++ {
			start := time.Now()
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
			elapsed := time.Since(start)

			if err != nil {
				results = append(results, fmt.Sprintf("Connection %d: failed - %v", i+1, err))
			} else {
				conn.Close()
				results = append(results, fmt.Sprintf("Connection %d: connected in %v", i+1, elapsed.Round(time.Millisecond)))
			}
			time.Sleep(250 * time.Millisecond)
		}
		return strings.Join(results, "\n"), nil
	})
}

// HandleTrace handles POST /trace - Traceroute
func (h *ToolsHandler) HandleTrace(w http.ResponseWriter, r *http.Request) {
	h.handleTool(w, r, func(target string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "traceroute", "-m", "20", "-w", "2", target)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return string(output), nil
		}
		return string(output), nil
	})
}

// HandleRoute handles POST /route - BIRD route lookup
func (h *ToolsHandler) HandleRoute(w http.ResponseWriter, r *http.Request) {
	h.handleTool(w, r, func(target string) (string, error) {
		// Use Pool.Execute which handles connection pool internally
		result, err := h.birdPool.Execute(fmt.Sprintf("show route for %s all", target))
		if err != nil {
			return "", fmt.Errorf("BIRD query failed: %w", err)
		}
		return result, nil
	})
}

// HandlePath handles POST /path - AS path lookup
func (h *ToolsHandler) HandlePath(w http.ResponseWriter, r *http.Request) {
	h.handleTool(w, r, func(target string) (string, error) {
		// Use Pool.Execute which handles connection pool internally
		result, err := h.birdPool.Execute(fmt.Sprintf("show route for %s all", target))
		if err != nil {
			return "", fmt.Errorf("BIRD query failed: %w", err)
		}

		// Filter for AS path info
		lines := strings.Split(result, "\n")
		var filtered []string
		for _, line := range lines {
			if strings.Contains(line, "BGP.as_path") || strings.Contains(line, "via") || strings.Contains(line, "unicast") {
				filtered = append(filtered, line)
			}
		}
		if len(filtered) == 0 {
			return result, nil
		}
		return strings.Join(filtered, "\n"), nil
	})
}

// handleTool is a helper that handles common tool request/response logic.
func (h *ToolsHandler) handleTool(w http.ResponseWriter, r *http.Request, fn func(target string) (string, error)) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Method not allowed"})
		return
	}

	// Verify Bearer token
	if h.token != "" {
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + h.token
		if auth != expected {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "Unauthorized"})
			return
		}
	}

	var req ToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.Target == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Missing target"})
		return
	}

	// Basic input validation - prevent command injection
	if strings.ContainsAny(req.Target, ";&|`$(){}[]<>\\\"'") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "Invalid target"})
		return
	}

	result, err := fn(req.Target)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		// Sanitize error message to avoid leaking internal details
		errMsg := "Command execution failed"
		if strings.Contains(err.Error(), "timeout") {
			errMsg = "Command timed out"
		} else if strings.Contains(err.Error(), "BIRD") {
			errMsg = "Route lookup failed"
		}
		json.NewEncoder(w).Encode(ErrorResponse{Error: errMsg})
		return
	}

	json.NewEncoder(w).Encode(ToolResponse{Result: result})
}
