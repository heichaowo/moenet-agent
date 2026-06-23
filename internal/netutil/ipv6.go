package netutil

import (
	"fmt"
	"strings"
)

// DeriveLinkLocal derives a link-local address from a DN42 loopback IPv6 address.
// Loopback format: fd00:4242:7777:{region}:{local_index}::1/128
// LLA format:      fe80::998:{region}:{local_index}:1/64  (998 = MoeNet identifier)
//
// Returns empty string if the loopback address cannot be parsed.
func DeriveLinkLocal(loopback string) string {
	if loopback == "" {
		return ""
	}
	parts := SplitIPv6(loopback)
	if len(parts) < 5 {
		return ""
	}
	// parts[0:3] = "fd00", "4242", "7777"
	// parts[3] = region (e.g., "302")
	// parts[4] = local_index (e.g., "1")
	return fmt.Sprintf("fe80::998:%s:%s:1/64", parts[3], parts[4])
}

// SplitIPv6 splits an IPv6 address by colon, stripping any CIDR suffix.
// It does NOT expand "::" — it simply collects non-empty groups before
// the first "::" occurrence, which is sufficient for extracting the first
// 5 groups from addresses like fd00:4242:7777:101:2::1/128.
func SplitIPv6(addr string) []string {
	// Remove CIDR suffix
	if idx := strings.Index(addr, "/"); idx > 0 {
		addr = addr[:idx]
	}

	parts := []string{}
	current := ""
	for _, c := range addr {
		if c == ':' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
