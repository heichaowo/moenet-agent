package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestClosedState(t *testing.T) {
	cb := New(Config{FailureThreshold: 3})

	// Should allow requests in closed state
	for i := 0; i < 10; i++ {
		if err := cb.Allow(); err != nil {
			t.Errorf("Request %d should be allowed in closed state", i)
		}
	}

	if cb.State() != StateClosed {
		t.Errorf("Expected closed state, got %s", cb.State())
	}
}

func TestTransitionToOpen(t *testing.T) {
	cb := New(Config{FailureThreshold: 3})

	// Record failures until threshold
	for i := 0; i < 3; i++ {
		cb.Allow()
		cb.RecordFailure()
	}

	if cb.State() != StateOpen {
		t.Errorf("Expected open state after 3 failures, got %s", cb.State())
	}

	// Should reject requests in open state
	err := cb.Allow()
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestTransitionToHalfOpen(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenDuration:     50 * time.Millisecond,
	})

	// Trip the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("Expected open state, got %s", cb.State())
	}

	// Wait for open duration
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open on next request
	if err := cb.Allow(); err != nil {
		t.Errorf("Expected request to be allowed (half-open), got %v", err)
	}

	if cb.State() != StateHalfOpen {
		t.Errorf("Expected half-open state, got %s", cb.State())
	}
}

func TestHalfOpenToClosedOnSuccess(t *testing.T) {
	cb := New(Config{
		FailureThreshold:    2,
		SuccessThreshold:    2,
		OpenDuration:        10 * time.Millisecond,
		HalfOpenMaxRequests: 5,
	})

	// Trip the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	time.Sleep(15 * time.Millisecond)
	cb.Allow() // Transition to half-open

	// Record successes
	cb.RecordSuccess()
	if cb.State() != StateHalfOpen {
		t.Errorf("Should still be half-open after 1 success, got %s", cb.State())
	}

	cb.Allow()
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Errorf("Should be closed after 2 successes, got %s", cb.State())
	}
}

func TestHalfOpenToOpenOnFailure(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenDuration:     10 * time.Millisecond,
	})

	// Trip the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	time.Sleep(15 * time.Millisecond)
	cb.Allow() // Transition to half-open

	// Any failure in half-open reopens circuit
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Errorf("Expected open state after failure in half-open, got %s", cb.State())
	}
}

func TestExecute(t *testing.T) {
	cb := New(Config{FailureThreshold: 2})

	// Successful execution
	err := cb.Execute(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Failed executions
	testErr := errors.New("test error")
	for i := 0; i < 2; i++ {
		err := cb.Execute(func() error {
			return testErr
		})
		if !errors.Is(err, testErr) {
			t.Errorf("Expected test error, got %v", err)
		}
	}

	// Circuit should be open now
	err = cb.Execute(func() error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestReset(t *testing.T) {
	cb := New(Config{FailureThreshold: 2})

	// Trip the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("Expected open state, got %s", cb.State())
	}

	// Reset
	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("Expected closed state after reset, got %s", cb.State())
	}

	// Should allow requests again
	if err := cb.Allow(); err != nil {
		t.Errorf("Expected request to be allowed after reset, got %v", err)
	}
}
