package bird

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Pool manages a pool of BIRD control socket connections
type Pool struct {
	socket      string
	poolSize    int
	maxSize     int
	connections chan *Conn
	mu          sync.Mutex
	closed      bool
}

// Conn represents a single BIRD control socket connection
type Conn struct {
	conn   net.Conn
	reader *bufio.Reader
}

// NewPool creates a new BIRD connection pool
func NewPool(socket string, poolSize, maxSize int) (*Pool, error) {
	p := &Pool{
		socket:      socket,
		poolSize:    poolSize,
		maxSize:     maxSize,
		connections: make(chan *Conn, maxSize),
	}

	// Pre-populate pool with initial connections
	for i := 0; i < poolSize; i++ {
		conn, err := p.newConn()
		if err != nil {
			// Close any already-created connections
			p.Close()
			return nil, fmt.Errorf("failed to create initial connection: %w", err)
		}
		p.connections <- conn
	}

	return p, nil
}

// newConn creates a new connection to the BIRD socket
func (p *Pool) newConn() (*Conn, error) {
	conn, err := net.DialTimeout("unix", p.socket, 5*time.Second)
	if err != nil {
		return nil, err
	}

	c := &Conn{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}

	// Read the welcome message
	if _, err := c.readResponse(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read welcome: %w", err)
	}

	return c, nil
}

// acquire gets a connection from the pool
func (p *Pool) acquire() (*Conn, error) {
	select {
	case conn := <-p.connections:
		return conn, nil
	default:
		// Pool empty, try to create new connection
		p.mu.Lock()
		if len(p.connections) < p.maxSize {
			p.mu.Unlock()
			return p.newConn()
		}
		p.mu.Unlock()
		// Wait for a connection
		return <-p.connections, nil
	}
}

// release returns a connection to the pool
func (p *Pool) release(conn *Conn) {
	if conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.conn.Close()
		return
	}
	select {
	case p.connections <- conn:
	default:
		// Pool full, close excess connection
		conn.conn.Close()
	}
}

// Close closes all connections in the pool
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	close(p.connections)
	for conn := range p.connections {
		conn.conn.Close()
	}
}

// Execute runs a BIRD command and returns the response
// It will retry once with a new connection if the first attempt fails (e.g., broken pipe)
func (p *Pool) Execute(cmd string) (string, error) {
	return p.executeWithRetry(cmd, 1)
}

// executeWithRetry attempts to execute a command with retry support
func (p *Pool) executeWithRetry(cmd string, retries int) (string, error) {
	conn, err := p.acquire()
	if err != nil {
		return "", fmt.Errorf("failed to acquire connection: %w", err)
	}

	// Send command
	if _, err := fmt.Fprintf(conn.conn, "%s\n", cmd); err != nil {
		// Connection broken, discard and retry
		p.discard(conn)
		if retries > 0 {
			log.Printf("[BIRD] Connection error, retrying: %v", err)
			return p.executeWithRetry(cmd, retries-1)
		}
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	// Read response
	result, err := conn.readResponse()
	if err != nil {
		// Connection broken, discard and retry
		p.discard(conn)
		if retries > 0 {
			log.Printf("[BIRD] Read error, retrying: %v", err)
			return p.executeWithRetry(cmd, retries-1)
		}
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Success - return connection to pool
	p.release(conn)
	return result, nil
}

// discard closes a broken connection without returning it to the pool
func (p *Pool) discard(conn *Conn) {
	if conn != nil && conn.conn != nil {
		conn.conn.Close()
	}
}

// Configure triggers BIRD to reload configuration
func (p *Pool) Configure() error {
	result, err := p.Execute("configure")
	if err != nil {
		return err
	}
	// BIRD 3.x responses may include multiple lines from connection pool reuse.
	// Check for explicit error codes (8xxx = runtime error, 9xxx = parse error)
	// Success codes: 0002 (info), 0003 (success), 0018 (restart)
	if strings.Contains(result, "Reconfigured") ||
		strings.Contains(result, "Reconfiguration in progress") ||
		strings.Contains(result, "0003 ") ||
		strings.Contains(result, "0018 ") ||
		strings.Contains(result, "0002-") {
		return nil
	}
	// Only fail on explicit error codes
	if strings.Contains(result, "8") || strings.Contains(result, "9") {
		for _, line := range strings.Split(result, "\n") {
			if len(line) >= 4 && (line[0] == '8' || line[0] == '9') {
				return fmt.Errorf("configure failed: %s", line)
			}
		}
	}
	// Log and accept if we don't see explicit errors
	log.Printf("[BIRD] Configure response (assumed success): %s", strings.TrimSpace(result))
	return nil
}

// ShowProtocols returns the output of 'show protocols'
func (p *Pool) ShowProtocols() (string, error) {
	return p.Execute("show protocols")
}

// readResponse reads a complete BIRD response
func (c *Conn) readResponse() (string, error) {
	var result strings.Builder
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		result.WriteString(line)

		// BIRD responses end with a line starting with 4 digits and a space
		// (e.g., "0001 BIRD 3.0.0 ready.\n")
		if len(line) >= 5 && line[4] == ' ' {
			break
		}
	}
	return result.String(), nil
}
