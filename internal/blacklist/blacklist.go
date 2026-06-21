// Package blacklist manages the ASN blacklist for BIRD route filtering.
//
// It generates and maintains /etc/bird/blacklist.conf, which defines an
// is_blacklisted() function used by BIRD import filters to reject routes
// passing through blacklisted ASNs.
package blacklist

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/moenet/moenet-agent/internal/bird"
)

// Manager handles ASN blacklist file management and BIRD reload.
type Manager struct {
	confPath string
	birdPool *bird.Pool
	mu       sync.RWMutex
	asns     map[int]bool
}

// NewManager creates a new blacklist manager and loads existing blacklist from disk.
func NewManager(confPath string, pool *bird.Pool) *Manager {
	m := &Manager{
		confPath: confPath,
		birdPool: pool,
		asns:     make(map[int]bool),
	}

	if err := m.Load(); err != nil {
		slog.Warn("failed to load existing blacklist, starting empty",
			"path", confPath, "error", err)
	} else {
		slog.Info("blacklist loaded", "path", confPath, "count", len(m.asns))
	}

	// Ensure the file exists (create empty if missing) so BIRD include doesn't fail
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		if saveErr := m.save(); saveErr != nil {
			slog.Warn("failed to create initial blacklist.conf", "error", saveErr)
		}
	}

	return m
}

// asnPattern matches the ASN list inside the BIRD function syntax:
//
//	bgp_path ~ [= * [ASN1, ASN2, ...] * =]
var asnPattern = regexp.MustCompile(`\[(\d+(?:,\s*\d+)*)\]`)

// Load reads and parses the blacklist file from disk.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.confPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.asns = make(map[int]bool)
			return nil
		}
		return fmt.Errorf("read blacklist file: %w", err)
	}

	content := string(data)
	m.asns = make(map[int]bool)

	match := asnPattern.FindStringSubmatch(content)
	if match == nil || len(match) < 2 {
		// No ASN list found — file might be empty or have "return false"
		return nil
	}

	for _, part := range strings.Split(match[1], ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		asn, err := strconv.Atoi(part)
		if err != nil {
			slog.Warn("skipping invalid ASN in blacklist", "value", part)
			continue
		}
		m.asns[asn] = true
	}

	return nil
}

// save writes the blacklist to disk and reloads BIRD configuration.
// Caller must hold m.mu write lock.
func (m *Manager) save() error {
	// Ensure directory exists
	dir := filepath.Dir(m.confPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	var b strings.Builder
	b.WriteString("# Blacklist - Managed by moenet-agent\n")
	b.WriteString("# DO NOT EDIT MANUALLY - changes will be overwritten\n")
	b.WriteString("#\n")
	b.WriteString("# This file is included by bird.conf and provides is_blacklisted()\n")
	b.WriteString("# to check if a route passes through any blacklisted ASN.\n\n")

	b.WriteString("function is_blacklisted() -> bool {\n")
	if len(m.asns) > 0 {
		sorted := m.sortedASNs()
		parts := make([]string, len(sorted))
		for i, asn := range sorted {
			parts[i] = strconv.Itoa(asn)
		}
		asnList := strings.Join(parts, ", ")
		b.WriteString(fmt.Sprintf("    return bgp_path ~ [= * [%s] * =];\n", asnList))
	} else {
		b.WriteString("    return false;  # No ASNs in blacklist\n")
	}
	b.WriteString("}\n")

	if err := os.WriteFile(m.confPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write blacklist file: %w", err)
	}

	// Reload BIRD configuration
	if err := m.birdPool.Configure(); err != nil {
		slog.Warn("blacklist saved but BIRD reconfigure may have failed", "error", err)
		// File was saved successfully, don't return error
	} else {
		slog.Info("blacklist saved and BIRD reconfigured", "count", len(m.asns))
	}

	return nil
}

// sortedASNs returns the blacklisted ASNs in sorted order.
// Caller must hold at least m.mu read lock.
func (m *Manager) sortedASNs() []int {
	result := make([]int, 0, len(m.asns))
	for asn := range m.asns {
		result = append(result, asn)
	}
	sort.Ints(result)
	return result
}

// List returns all blacklisted ASNs in sorted order.
func (m *Manager) List() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sortedASNs()
}

// Add adds an ASN to the blacklist. Returns the operation result:
// "added" if newly added, "already_blocked" if already present.
func (m *Manager) Add(asn int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.asns[asn] {
		return "already_blocked", nil
	}

	m.asns[asn] = true
	if err := m.save(); err != nil {
		// Rollback in-memory state
		delete(m.asns, asn)
		return "", fmt.Errorf("save blacklist after add: %w", err)
	}

	slog.Info("added ASN to blacklist", "asn", asn)
	return "added", nil
}

// Remove removes an ASN from the blacklist. Returns the operation result:
// "removed" if found and removed, "not_found" if not present.
func (m *Manager) Remove(asn int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.asns[asn] {
		return "not_found", nil
	}

	delete(m.asns, asn)
	if err := m.save(); err != nil {
		// Rollback in-memory state
		m.asns[asn] = true
		return "", fmt.Errorf("save blacklist after remove: %w", err)
	}

	slog.Info("removed ASN from blacklist", "asn", asn)
	return "removed", nil
}

// Count returns the number of blacklisted ASNs.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.asns)
}
