// Package firewall manages iptables rules for WireGuard peer ports.
package firewall

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Executor manages iptables rules for DN42 WireGuard ports.
type Executor struct {
	chain         string
	commentPrefix string
	logger        *slog.Logger
}

// NewExecutor creates a new firewall executor.
func NewExecutor(logger *slog.Logger) *Executor {
	return &Executor{
		chain:         "INPUT",
		commentPrefix: "moenet-dn42",
		logger:        logger,
	}
}

// AllowPort opens a UDP port in iptables for WireGuard traffic.
func (e *Executor) AllowPort(port int) error {
	if e.portExists(port) {
		e.logger.Debug("port already open", "port", port)
		return nil
	}

	comment := fmt.Sprintf("%s-%d", e.commentPrefix, port)

	// IPv4
	if err := e.runIPTables("iptables", "-A", e.chain, "-p", "udp", "--dport", strconv.Itoa(port),
		"-m", "comment", "--comment", comment, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("iptables v4 failed: %w", err)
	}

	// IPv6
	if err := e.runIPTables("ip6tables", "-A", e.chain, "-p", "udp", "--dport", strconv.Itoa(port),
		"-m", "comment", "--comment", comment, "-j", "ACCEPT"); err != nil {
		// Try to rollback IPv4
		_ = e.runIPTables("iptables", "-D", e.chain, "-p", "udp", "--dport", strconv.Itoa(port),
			"-m", "comment", "--comment", comment, "-j", "ACCEPT")
		return fmt.Errorf("ip6tables failed: %w", err)
	}

	e.logger.Info("opened port", "port", port)
	e.saveRules()
	return nil
}

// RemovePort removes a UDP port rule from iptables.
func (e *Executor) RemovePort(port int) error {
	comment := fmt.Sprintf("%s-%d", e.commentPrefix, port)

	// Remove IPv4 rule (ignore errors if not exists)
	_ = e.runIPTables("iptables", "-D", e.chain, "-p", "udp", "--dport", strconv.Itoa(port),
		"-m", "comment", "--comment", comment, "-j", "ACCEPT")

	// Remove IPv6 rule
	_ = e.runIPTables("ip6tables", "-D", e.chain, "-p", "udp", "--dport", strconv.Itoa(port),
		"-m", "comment", "--comment", comment, "-j", "ACCEPT")

	e.logger.Info("removed port", "port", port)
	e.saveRules()
	return nil
}

// GetOpenPorts returns list of ports opened by this agent.
func (e *Executor) GetOpenPorts() ([]int, error) {
	cmd := exec.Command("iptables", "-L", e.chain, "-n", "--line-numbers")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("iptables list failed: %w", err)
	}

	ports := make(map[int]struct{})
	dptRegex := regexp.MustCompile(`dpt:(\d+)`)

	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, e.commentPrefix) {
			continue
		}
		matches := dptRegex.FindStringSubmatch(line)
		if len(matches) >= 2 {
			if port, err := strconv.Atoi(matches[1]); err == nil {
				ports[port] = struct{}{}
			}
		}
	}

	result := make([]int, 0, len(ports))
	for port := range ports {
		result = append(result, port)
	}
	return result, nil
}

// SyncPorts ensures only expected ports are open.
// Returns the number of ports added and removed.
func (e *Executor) SyncPorts(expectedPorts []int) (added, removed int, err error) {
	current, err := e.GetOpenPorts()
	if err != nil {
		return 0, 0, err
	}

	currentSet := make(map[int]struct{})
	for _, p := range current {
		currentSet[p] = struct{}{}
	}

	expectedSet := make(map[int]struct{})
	for _, p := range expectedPorts {
		expectedSet[p] = struct{}{}
	}

	// Add missing ports
	for port := range expectedSet {
		if _, exists := currentSet[port]; !exists {
			if err := e.AllowPort(port); err != nil {
				e.logger.Error("failed to add port", "port", port, "error", err)
			} else {
				added++
			}
		}
	}

	// Remove extra ports
	for port := range currentSet {
		if _, exists := expectedSet[port]; !exists {
			if err := e.RemovePort(port); err != nil {
				e.logger.Error("failed to remove port", "port", port, "error", err)
			} else {
				removed++
			}
		}
	}

	if added > 0 || removed > 0 {
		e.logger.Info("synced ports", "added", added, "removed", removed)
	}
	return added, removed, nil
}

// portExists checks if a port rule already exists.
func (e *Executor) portExists(port int) bool {
	cmd := exec.Command("iptables", "-C", e.chain, "-p", "udp", "--dport", strconv.Itoa(port), "-j", "ACCEPT")
	return cmd.Run() == nil
}

// runIPTables executes an iptables command.
func (e *Executor) runIPTables(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %v: %s", cmd, args, stderr.String())
	}
	return nil
}

// saveRules persists iptables rules to disk.
func (e *Executor) saveRules() {
	// Try common save locations
	_ = exec.Command("sh", "-c", "iptables-save > /etc/iptables/rules.v4 2>/dev/null || true").Run()
	_ = exec.Command("sh", "-c", "ip6tables-save > /etc/iptables/rules.v6 2>/dev/null || true").Run()
}
