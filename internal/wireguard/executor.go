package wireguard

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Executor manages WireGuard interfaces
type Executor struct {
	configDir  string
	privateKey string
	publicKey  string
}

// NewExecutor creates a new WireGuard executor
func NewExecutor(configDir, privateKeyPath string) (*Executor, error) {
	e := &Executor{
		configDir: configDir,
	}

	// Load or create keys
	if err := e.loadOrCreateKeys(privateKeyPath); err != nil {
		return nil, err
	}

	return e, nil
}

// loadOrCreateKeys loads existing keys or generates new ones
func (e *Executor) loadOrCreateKeys(privateKeyPath string) error {
	// Try to load existing private key
	if data, err := os.ReadFile(privateKeyPath); err == nil {
		e.privateKey = strings.TrimSpace(string(data))
	} else {
		// Generate new key pair
		out, err := exec.Command("wg", "genkey").Output()
		if err != nil {
			return fmt.Errorf("failed to generate private key: %w", err)
		}
		e.privateKey = strings.TrimSpace(string(out))

		// Save private key
		if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0700); err != nil {
			return fmt.Errorf("failed to create key directory: %w", err)
		}
		if err := os.WriteFile(privateKeyPath, []byte(e.privateKey), 0600); err != nil {
			return fmt.Errorf("failed to save private key: %w", err)
		}
	}

	// Derive public key
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(e.privateKey)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to derive public key: %w", err)
	}
	e.publicKey = strings.TrimSpace(string(out))

	return nil
}

// PublicKey returns the WireGuard public key
func (e *Executor) PublicKey() string {
	return e.publicKey
}

// CreateInterface creates a WireGuard interface
func (e *Executor) CreateInterface(name string, listenPort int, peerKey, presharedKey, endpoint string, allowedIPs []string, keepalive int) error {
	// Create interface if it doesn't exist
	if !e.InterfaceExists(name) {
		if err := exec.Command("ip", "link", "add", "dev", name, "type", "wireguard").Run(); err != nil {
			return fmt.Errorf("failed to create interface: %w", err)
		}
	}

	// Set private key
	cmd := exec.Command("wg", "set", name, "private-key", "/dev/stdin")
	cmd.Stdin = strings.NewReader(e.privateKey)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set private key: %w", err)
	}

	// Set listen port if specified
	if listenPort > 0 {
		if err := exec.Command("wg", "set", name, "listen-port", fmt.Sprintf("%d", listenPort)).Run(); err != nil {
			return fmt.Errorf("failed to set listen port: %w", err)
		}
	}

	// Configure peer
	args := []string{"set", name, "peer", peerKey, "allowed-ips", strings.Join(allowedIPs, ",")}
	if endpoint != "" {
		args = append(args, "endpoint", endpoint)
	}
	if keepalive > 0 {
		args = append(args, "persistent-keepalive", fmt.Sprintf("%d", keepalive))
	}
	if presharedKey != "" {
		// Write PSK to temp file (wg requires file path for preshared-key)
		pskFile, err := os.CreateTemp("", "wg-psk-*")
		if err != nil {
			return fmt.Errorf("failed to create PSK temp file: %w", err)
		}
		defer os.Remove(pskFile.Name())
		if _, err := pskFile.WriteString(presharedKey); err != nil {
			pskFile.Close()
			return fmt.Errorf("failed to write PSK: %w", err)
		}
		pskFile.Close()
		args = append(args, "preshared-key", pskFile.Name())
	}

	cmd = exec.Command("wg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure peer: %w (stderr: %s)", err, stderr.String())
	}

	// Bring interface up
	if err := exec.Command("ip", "link", "set", name, "up").Run(); err != nil {
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	log.Printf("[WireGuard] Interface %s configured", name)
	return nil
}

// AddAddress adds an IP address to an interface
func (e *Executor) AddAddress(ifname, addr string) error {
	// Check if address already exists
	out, _ := exec.Command("ip", "addr", "show", ifname).Output()
	if strings.Contains(string(out), addr) {
		return nil // Already exists
	}

	if err := exec.Command("ip", "addr", "add", addr, "dev", ifname).Run(); err != nil {
		return fmt.Errorf("failed to add address %s: %w", addr, err)
	}
	return nil
}

// SetMTU sets the MTU for an interface
func (e *Executor) SetMTU(ifname string, mtu int) error {
	return exec.Command("ip", "link", "set", ifname, "mtu", fmt.Sprintf("%d", mtu)).Run()
}

// DeleteInterface removes a WireGuard interface
func (e *Executor) DeleteInterface(name string) error {
	if !e.InterfaceExists(name) {
		return nil
	}

	if err := exec.Command("ip", "link", "set", name, "down").Run(); err != nil {
		log.Printf("[WireGuard] Warning: failed to bring down %s: %v", name, err)
	}

	if err := exec.Command("ip", "link", "del", name).Run(); err != nil {
		return fmt.Errorf("failed to delete interface: %w", err)
	}

	log.Printf("[WireGuard] Interface %s deleted", name)
	return nil
}

// InterfaceExists checks if a network interface exists.
func (e *Executor) InterfaceExists(name string) bool {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), name+":") {
			return true
		}
	}
	return false
}

// GetStatus returns the status of a WireGuard interface
func (e *Executor) GetStatus(name string) (string, error) {
	out, err := exec.Command("wg", "show", name).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetPeerEndpoint returns the peer's currently-learned source IP (no port) for
// a single-peer interface. For NAT peers configured without an endpoint, the
// kernel populates this from the most recent handshake — it's the only place we
// can observe where the peer actually connects from. Returns "" if the peer has
// no endpoint yet (never handshaked). The port is stripped; only the host IP is
// returned so the CP can geolocate it.
func (e *Executor) GetPeerEndpoint(name string) (string, error) {
	// `wg show <iface> endpoints` prints one line per peer: "<pubkey>\t<ip:port>".
	// "(none)" appears when no endpoint has been learned yet.
	out, err := exec.Command("wg", "show", name, "endpoints").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ep := fields[len(fields)-1]
		if ep == "(none)" || ep == "" {
			continue
		}
		return stripPort(ep), nil
	}
	return "", nil
}

// stripPort removes the trailing :port from a WireGuard endpoint, handling both
// "1.2.3.4:51820" and "[2001:db8::1]:51820" forms, and returns the bare host IP.
func stripPort(ep string) string {
	if strings.HasPrefix(ep, "[") {
		// [IPv6]:port
		if end := strings.LastIndex(ep, "]"); end > 0 {
			return ep[1:end]
		}
	}
	// IPv4:port — a bare IPv6 has multiple colons and no brackets, so only
	// strip when there's exactly one colon.
	if strings.Count(ep, ":") == 1 {
		return ep[:strings.LastIndex(ep, ":")]
	}
	return ep
}

// IsInterfaceUp checks if a network interface is operationally UP.
// Returns false if the interface doesn't exist or is DOWN/UNKNOWN.
func (e *Executor) IsInterfaceUp(name string) bool {
	data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/operstate", name))
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(data))
	// WireGuard interfaces report "unknown" when UP (no carrier concept)
	return state == "up" || state == "unknown"
}
