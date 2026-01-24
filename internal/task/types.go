package task

// BgpSession represents a BGP peering session from Control Plane
type BgpSession struct {
	UUID          string   `json:"uuid"`
	ASN           uint32   `json:"asn"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Status        int      `json:"status"`
	Type          string   `json:"type"` // wireguard, gre, ip6gre
	Interface     string   `json:"interface"`
	Endpoint      string   `json:"endpoint"`
	Credential    string   `json:"credential"` // WireGuard public key
	IPv4          string   `json:"ipv4"`
	IPv6          string   `json:"ipv6"`
	IPv6LinkLocal string   `json:"ipv6LinkLocal"`
	MTU           int      `json:"mtu"`
	Extensions    []string `json:"extensions"` // mp-bgp, extended-nexthop
	Policy        string   `json:"policy"`
	LastError     string   `json:"lastError"`
	Data          any      `json:"data"` // Additional data
}

// Session status constants (matching iedon's implementation)
const (
	StatusDeleted = iota
	StatusDisabled
	StatusEnabled
	StatusPendingApproval
	StatusQueuedForSetup
	StatusQueuedForDelete
	StatusProblem
	StatusTeardown
)

// SessionMetric represents collected metrics for a session
type SessionMetric struct {
	UUID      string         `json:"uuid"`
	ASN       uint32         `json:"asn"`
	Timestamp int64          `json:"timestamp"`
	Router    string         `json:"router"`
	BGP       []BGPStats     `json:"bgp"`
	Interface InterfaceStats `json:"interface"`
	RTT       RTTStats       `json:"rtt"`
}

// BGPStats represents BGP protocol statistics
type BGPStats struct {
	Name   string     `json:"name"`
	State  string     `json:"state"`
	Info   string     `json:"info"`
	Type   string     `json:"type"`
	Since  string     `json:"since"`
	Routes RouteStats `json:"routes"`
}

// RouteStats represents route import/export counts
type RouteStats struct {
	IPv4 RouteCount `json:"ipv4"`
	IPv6 RouteCount `json:"ipv6"`
}

// RouteCount represents current route count
type RouteCount struct {
	Imported int `json:"imported"`
	Exported int `json:"exported"`
}

// InterfaceStats represents network interface statistics
type InterfaceStats struct {
	IPv4          string `json:"ipv4"`
	IPv6          string `json:"ipv6"`
	IPv6LinkLocal string `json:"ipv6LinkLocal"`
	MAC           string `json:"mac"`
	MTU           int    `json:"mtu"`
	Status        string `json:"status"`
	TxBytes       uint64 `json:"txBytes"`
	RxBytes       uint64 `json:"rxBytes"`
	TxRate        uint64 `json:"txRate"` // bytes/sec
	RxRate        uint64 `json:"rxRate"` // bytes/sec
}

// RTTStats represents RTT measurement results
type RTTStats struct {
	Current float64 `json:"current"` // Current RTT in ms
	Loss    float64 `json:"loss"`    // Packet loss percentage
	Tier    int     `json:"tier"`    // Latency tier (0-8)
}

// ControlPlaneResponse represents a generic API response
type ControlPlaneResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// SessionsResponse wraps the sessions list from CP
type SessionsResponse struct {
	BgpSessions []BgpSession `json:"bgpSessions"`
}

// MeshPeer represents a peer in the IGP mesh
type MeshPeer struct {
	NodeID       int    `json:"nodeId"`
	NodeName     string `json:"nodeName"`
	LoopbackIPv4 string `json:"loopbackIpv4"`
	LoopbackIPv6 string `json:"loopbackIpv6"`
	PublicKey    string `json:"publicKey"`
	Endpoint     string `json:"endpoint"`
	MTU          int    `json:"mtu"`
	IsRR         bool   `json:"isRr"`
}

// MeshConfig represents the mesh network configuration
type MeshConfig struct {
	LocalNodeID    int        `json:"localNodeId"`
	LocalLoopback4 string     `json:"localLoopback4"`
	LocalLoopback6 string     `json:"localLoopback6"`
	Peers          []MeshPeer `json:"peers"`
}

// HeartbeatPayload represents the heartbeat data sent to CP
type HeartbeatPayload struct {
	Version   string `json:"version"`
	Kernel    string `json:"kernel"`
	LoadAvg   string `json:"loadAvg"`
	Uptime    int64  `json:"uptime"`
	Timestamp int64  `json:"timestamp"`
	TxBytes   uint64 `json:"tx"`
	RxBytes   uint64 `json:"rx"`
	TCPConns  int    `json:"tcp"`
	UDPConns  int    `json:"udp"`
}
