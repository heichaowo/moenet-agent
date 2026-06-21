package probe

import (
	"context"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MTU test values in descending order.
var mtuTestValues = []int{1420, 1400, 1380, 1320, 1280}

// MinMTU is the minimum safe MTU (IPv6 minimum).
const MinMTU = 1280

// icmpOverhead is the ICMP/IP header overhead.
const icmpOverhead = 28

// MTUResult stores the result of an MTU probe.
type MTUResult struct {
	Target             string `json:"target"`
	OptimalMTU         int    `json:"optimal_mtu"`
	TestedAt           int64  `json:"tested_at"` // Unix seconds
	IsIntercontinental bool   `json:"is_intercontinental"`
	IsLowMTU           bool   `json:"is_low_mtu"` // MTU < 1400
}

// MTUProbe detects optimal path MTU to peers using ICMP with DF flag.
type MTUProbe struct {
	cache   map[string]*MTUResult
	timeout time.Duration
	mu      sync.RWMutex
}

// NewMTUProbe creates a new MTU probe.
func NewMTUProbe() *MTUProbe {
	return &MTUProbe{
		cache:   make(map[string]*MTUResult),
		timeout: 5 * time.Second,
	}
}

// ProbeMTU probes the optimal MTU to a target by testing descending MTU values.
func (mp *MTUProbe) ProbeMTU(target string, isIntercontinental bool) *MTUResult {
	optimalMTU := MinMTU

	for _, mtu := range mtuTestValues {
		packetSize := mtu - icmpOverhead
		if mp.pingWithSize(target, packetSize) {
			optimalMTU = mtu
			break // Found highest working MTU
		}
	}

	result := &MTUResult{
		Target:             target,
		OptimalMTU:         optimalMTU,
		TestedAt:           time.Now().Unix(),
		IsIntercontinental: isIntercontinental,
		IsLowMTU:           optimalMTU < 1400,
	}

	mp.mu.Lock()
	mp.cache[target] = result
	mp.mu.Unlock()

	slog.Info("MTU probe complete",
		"target", target,
		"optimal_mtu", optimalMTU,
		"intercontinental", isIntercontinental,
		"low_mtu", result.IsLowMTU,
	)

	return result
}

// GetCachedMTU returns the cached MTU for a target, if available.
func (mp *MTUProbe) GetCachedMTU(target string) (int, bool) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result, exists := mp.cache[target]
	if !exists {
		return 0, false
	}
	return result.OptimalMTU, true
}

// GetAllCached returns all cached MTU results.
func (mp *MTUProbe) GetAllCached() map[string]*MTUResult {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	results := make(map[string]*MTUResult, len(mp.cache))
	for k, v := range mp.cache {
		results[k] = v
	}
	return results
}

// pingWithSize pings a target with a specific packet size and DF flag.
func (mp *MTUProbe) pingWithSize(target string, size int) bool {
	isIPv6 := strings.Contains(target, ":")
	timeoutSec := strconv.Itoa(int(math.Ceil(mp.timeout.Seconds())))
	sizeStr := strconv.Itoa(size)

	ctx, cancel := context.WithTimeout(context.Background(), mp.timeout+time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if isIPv6 {
		// IPv6: fragmentation is handled by endpoints, DF is implicit
		cmd = exec.CommandContext(ctx, "ping6", "-c", "1", "-W", timeoutSec, "-s", sizeStr, target)
	} else {
		// IPv4: -M do sets DF flag (don't fragment)
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", timeoutSec, "-M", "do", "-s", sizeStr, target)
	}

	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
