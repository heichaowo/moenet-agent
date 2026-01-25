// Package circuitbreaker implements the circuit breaker pattern for resilient service communication.
//
// The circuit breaker has three states:
//   - Closed: Normal operation, requests pass through
//   - Open: Circuit tripped, requests fail immediately
//   - HalfOpen: Testing if service recovered
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int

const (
	// StateClosed allows requests through, counting failures
	StateClosed State = iota
	// StateOpen rejects requests immediately
	StateOpen
	// StateHalfOpen allows limited requests to test recovery
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config configures the circuit breaker behavior
type Config struct {
	// FailureThreshold is the number of failures before opening the circuit
	FailureThreshold int
	// SuccessThreshold is the number of successes in half-open to close the circuit
	SuccessThreshold int
	// OpenDuration is how long to wait before transitioning to half-open
	OpenDuration time.Duration
	// HalfOpenMaxRequests is the max concurrent requests allowed in half-open state
	HalfOpenMaxRequests int
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		FailureThreshold:    5,
		SuccessThreshold:    3,
		OpenDuration:        30 * time.Second,
		HalfOpenMaxRequests: 1,
	}
}

// Errors
var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config Config

	mu            sync.RWMutex
	state         State
	failureCount  int
	successCount  int
	lastFailure   time.Time
	halfOpenCount int
}

// New creates a new circuit breaker with the given configuration
func New(config Config) *CircuitBreaker {
	// Apply defaults
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold == 0 {
		config.SuccessThreshold = 3
	}
	if config.OpenDuration == 0 {
		config.OpenDuration = 30 * time.Second
	}
	if config.HalfOpenMaxRequests == 0 {
		config.HalfOpenMaxRequests = 1
	}

	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Allow checks if a request should be allowed through
// Returns nil if allowed, ErrCircuitOpen if circuit is open
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if we should transition to half-open
		if now.After(cb.lastFailure.Add(cb.config.OpenDuration)) {
			cb.state = StateHalfOpen
			cb.halfOpenCount = 0
			cb.successCount = 0
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Limit concurrent requests in half-open state
		if cb.halfOpenCount >= cb.config.HalfOpenMaxRequests {
			return ErrTooManyRequests
		}
		cb.halfOpenCount++
		return nil
	}

	return nil
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		// Reset failure count on success
		cb.failureCount = 0

	case StateHalfOpen:
		cb.successCount++
		cb.halfOpenCount--
		// Check if we should close the circuit
		if cb.successCount >= cb.config.SuccessThreshold {
			cb.state = StateClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.lastFailure = now

	switch cb.state {
	case StateClosed:
		cb.failureCount++
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.state = StateOpen
		}

	case StateHalfOpen:
		// Any failure in half-open reopens the circuit
		cb.state = StateOpen
		cb.halfOpenCount = 0
	}
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCount = 0
}

// Execute runs the given function with circuit breaker protection
// Returns the result of fn or ErrCircuitOpen if the circuit is open
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.Allow(); err != nil {
		return err
	}

	err := fn()
	if err != nil {
		cb.RecordFailure()
		return err
	}

	cb.RecordSuccess()
	return nil
}
