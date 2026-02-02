// Package loopback provides management of loopback addresses on dummy0.
//
// This is used for DN42 BGP peering to have a stable source IP.
package loopback

import (
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
)

// Executor manages loopback addresses on dummy0.
type Executor struct {
	interface_ string
	logger     *slog.Logger
}

// NewExecutor creates a new loopback executor.
func NewExecutor(logger *slog.Logger) *Executor {
	return &Executor{
		interface_: "dummy0",
		logger:     logger,
	}
}

// EnsureInterfaceUp ensures dummy0 interface exists and is up.
func (e *Executor) EnsureInterfaceUp() error {
	// Check if interface exists
	cmd := exec.Command("ip", "link", "show", e.interface_)
	if err := cmd.Run(); err != nil {
		// Create interface
		if err := exec.Command("ip", "link", "add", e.interface_, "type", "dummy").Run(); err != nil {
			return fmt.Errorf("failed to create %s: %w", e.interface_, err)
		}
		e.logger.Info("created loopback interface", "interface", e.interface_)
	}

	// Bring interface up
	if err := exec.Command("ip", "link", "set", e.interface_, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring up %s: %w", e.interface_, err)
	}

	return nil
}

// SetupLoopbackWithIPs configures dummy0 with specific IPv4 and IPv6 addresses.
// This uses the exact IPs from the control plane config instead of calculating them.
//
// Parameters:
//   - ipv4: IPv4 address without CIDR (e.g., "172.22.188.4")
//   - ipv6: IPv6 address without CIDR (e.g., "fd00:4242:7777:101:4::1")
func (e *Executor) SetupLoopbackWithIPs(ipv4, ipv6 string) error {
	if ipv4 == "" && ipv6 == "" {
		return fmt.Errorf("at least one of ipv4 or ipv6 must be provided")
	}

	// Ensure interface exists
	if err := e.EnsureInterfaceUp(); err != nil {
		return err
	}

	// Add addresses
	var added []string

	if ipv4 != "" {
		addr := ipv4
		if !strings.Contains(addr, "/") {
			addr = addr + "/32"
		}
		if err := e.addAddress(addr, "IPv4 loopback"); err != nil {
			e.logger.Warn("failed to add IPv4 address", "addr", addr, "error", err)
		} else {
			added = append(added, ipv4)
		}
	}

	if ipv6 != "" {
		addr := ipv6
		if !strings.Contains(addr, "/") {
			addr = addr + "/128"
		}
		if err := e.addAddress(addr, "IPv6 loopback"); err != nil {
			e.logger.Warn("failed to add IPv6 address", "addr", addr, "error", err)
		} else {
			added = append(added, ipv6)
		}
	}

	if len(added) > 0 {
		e.logger.Info("loopback configured", "addresses", added)
	}

	return nil
}

// SetupLoopback configures dummy0 with DN42 IPs based on node_id.
// DEPRECATED: Use SetupLoopbackWithIPs with config values instead.
//
// Configures:
//   - IPv4: 172.22.188.{node_id}/32 for krt_prefsrc
//   - IPv6: fd00:4242:7777::{node_id}/128 for krt_prefsrc6
func (e *Executor) SetupLoopback(nodeID int) error {
	// Validate node_id is within /26 range (1-62)
	if nodeID < 1 || nodeID > 62 {
		return fmt.Errorf("invalid nodeID=%d: must be 1-62 for /26 subnet", nodeID)
	}

	// Calculate legacy addresses (for backwards compat)
	ipv4 := fmt.Sprintf("172.22.188.%d", nodeID)
	ipv6 := fmt.Sprintf("fd00:4242:7777::%d", nodeID)

	return e.SetupLoopbackWithIPs(ipv4, ipv6)
}

// ValidateNodeID checks if a nodeID is valid for loopback config.
func ValidateNodeID(nodeID int) error {
	if nodeID < 1 || nodeID > 62 {
		return fmt.Errorf("nodeID must be 1-62, got %d", nodeID)
	}
	return nil
}

// addAddress adds an IP address to the interface if not already present.
func (e *Executor) addAddress(addr, desc string) error {
	// Check if already configured
	output, err := exec.Command("ip", "addr", "show", e.interface_).Output()
	if err == nil && strings.Contains(string(output), strings.Split(addr, "/")[0]) {
		e.logger.Debug("address already configured", "addr", addr)
		return nil
	}

	cmd := exec.Command("ip", "addr", "add", addr, "dev", e.interface_)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add %s (%s): %w", addr, desc, err)
	}

	e.logger.Info("added address", "addr", addr, "desc", desc)
	return nil
}

// GetConfiguredAddresses returns all IP addresses on dummy0.
func (e *Executor) GetConfiguredAddresses() ([]string, error) {
	output, err := exec.Command("ip", "addr", "show", e.interface_).Output()
	if err != nil {
		return nil, err
	}

	var addresses []string
	// Parse inet and inet6 addresses
	re := regexp.MustCompile(`inet6?\s+([^\s]+)`)
	matches := re.FindAllStringSubmatch(string(output), -1)
	for _, m := range matches {
		if len(m) > 1 {
			addresses = append(addresses, m[1])
		}
	}

	return addresses, nil
}

// RemoveAddress removes an IP address from dummy0.
func (e *Executor) RemoveAddress(addr string) error {
	cmd := exec.Command("ip", "addr", "del", addr, "dev", e.interface_)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove %s: %w", addr, err)
	}
	e.logger.Info("removed address", "addr", addr)
	return nil
}
