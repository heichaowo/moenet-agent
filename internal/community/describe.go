package community

import "fmt"

// DescribeCommunity returns a human-readable description for a standard DN42 community.
func DescribeCommunity(c Community) string {
	if c.ASN != DN42CommunityASN {
		return fmt.Sprintf("Unknown (%d, %d)", c.ASN, c.Value)
	}

	// Check latency
	for tier, expected := range DN42Latency {
		if c.Value == expected.Value {
			if tier == 8 {
				return fmt.Sprintf("Latency ≥%.0fms", LatencyThresholds[7])
			}
			return fmt.Sprintf("Latency <%.1fms", LatencyThresholds[tier])
		}
	}

	// Check bandwidth
	for name, expected := range DN42Bandwidth {
		if c.Value == expected.Value {
			return fmt.Sprintf("Bandwidth ≥%s", name)
		}
	}

	// Check crypto
	for name, expected := range DN42Crypto {
		if c.Value == expected.Value {
			return fmt.Sprintf("Crypto: %s", name)
		}
	}

	// Check region
	for name, expected := range DN42Region {
		if c.Value == expected.Value {
			return fmt.Sprintf("Region: %s", name)
		}
	}

	// Check actions
	for name, expected := range DN42Actions {
		if c.Value == expected.Value {
			return fmt.Sprintf("Action: %s", name)
		}
	}

	return fmt.Sprintf("Unknown (%d, %d)", c.ASN, c.Value)
}
