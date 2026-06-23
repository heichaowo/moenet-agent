package task

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseBIRDProtocolState extracts the BGP state from `show protocols all "<name>"` output.
// Returns the state string (e.g., "Established", "Active", "Connect") and whether a BGP
// protocol line was found.
func parseBIRDProtocolState(output string) (state string, found bool) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		// Match protocol summary line: name BGP --- state date State
		// Skip header lines like "Type: BGP"
		if strings.Contains(line, "BGP") && !strings.HasPrefix(trimmed, "Type:") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 6 {
				// State is the last field (e.g., "Established", "Connect", "Active", "Idle")
				return parts[len(parts)-1], true
			}
		}
	}
	return "", false
}

// parseHandshakeAge extracts the latest handshake age from `wg show <name>` output
// and converts it to a time.Duration.
//
// Example input lines:
//
//	latest handshake: 1 minute, 30 seconds ago
//	latest handshake: 3 hours, 42 minutes, 10 seconds ago
//
// Returns the parsed duration and whether a handshake line was found.
// If handshake is "never" or missing, found is false.
func parseHandshakeAge(output string) (age time.Duration, found bool) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "latest handshake:") {
			continue
		}

		value := strings.TrimPrefix(trimmed, "latest handshake:")
		value = strings.TrimSpace(value)
		value = strings.TrimSuffix(value, " ago")

		if value == "" || value == "never" {
			return 0, false
		}

		// Parse compound durations: "1 hour, 30 minutes, 10 seconds"
		re := regexp.MustCompile(`(\d+)\s+(second|minute|hour|day)s?`)
		matches := re.FindAllStringSubmatch(value, -1)
		if len(matches) == 0 {
			return 0, false
		}

		var total time.Duration
		for _, m := range matches {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			switch m[2] {
			case "second":
				total += time.Duration(n) * time.Second
			case "minute":
				total += time.Duration(n) * time.Minute
			case "hour":
				total += time.Duration(n) * time.Hour
			case "day":
				total += time.Duration(n) * 24 * time.Hour
			}
		}
		return total, true
	}
	return 0, false
}

// listDN42Interfaces scans /proc/net/dev and returns all interface names
// that start with "dn42_". Returns an empty slice (not error) if /proc/net/dev
// is unreadable — this is a best-effort scan.
func listDN42Interfaces() []string {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		slog.Warn("cannot open /proc/net/dev for orphan scan", "error", err)
		return nil
	}
	defer file.Close()

	var interfaces []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Each line: "iface_name: <stats...>"
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colonIdx])
		if strings.HasPrefix(name, "dn42_") {
			interfaces = append(interfaces, name)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("error scanning /proc/net/dev", "error", err)
	}
	return interfaces
}

// bounceInterface brings a network interface down and back up to
// reset the WireGuard tunnel state.
func bounceInterface(name string) error {
	if err := exec.Command("ip", "link", "set", name, "down").Run(); err != nil {
		return fmt.Errorf("failed to bring down %s: %w", name, err)
	}
	if err := exec.Command("ip", "link", "set", name, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring up %s: %w", name, err)
	}
	return nil
}

// hasIPv6Address checks if a network interface has a specific IPv6 address assigned.
// addrWithPrefix should be in format "fe80::998:101:2:1/64".
// Returns false if the address is missing or the interface doesn't exist.
func hasIPv6Address(ifname, addrWithPrefix string) bool {
	addr := addrWithPrefix
	if idx := strings.Index(addr, "/"); idx > 0 {
		addr = addr[:idx]
	}

	out, err := exec.Command("ip", "-6", "addr", "show", "dev", ifname).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), addr)
}
