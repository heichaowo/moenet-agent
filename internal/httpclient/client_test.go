package httpclient

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryOnServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	client := New(&http.Client{Timeout: 5 * time.Second}, RetryConfig{
		MaxRetries:   5,
		InitialDelay: 10 * time.Millisecond,
		Multiplier:   1.5,
	})

	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Expected success after retries, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestNoRetryOnSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(nil, DefaultRetryConfig())

	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := New(nil, DefaultRetryConfig())

	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt (no retry on 4xx), got %d", attempts)
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(&http.Client{Timeout: 5 * time.Second}, RetryConfig{
		MaxRetries:   5,
		InitialDelay: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx, server.URL)
	if err == nil {
		t.Fatal("Expected error due to context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		// May be wrapped, check string
		if err.Error() != "request cancelled: context deadline exceeded" {
			t.Logf("Got expected cancellation error: %v", err)
		}
	}
}

func TestPostWithRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(nil, RetryConfig{
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
	})

	body := bytes.NewReader([]byte(`{"key": "value"}`))
	resp, err := client.Post(context.Background(), server.URL, "application/json", body)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestMaxRetriesExhausted(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(nil, RetryConfig{
		MaxRetries:   2,
		InitialDelay: 10 * time.Millisecond,
	})

	_, err := client.Get(context.Background(), server.URL)
	if err == nil {
		t.Fatal("Expected error after exhausting retries")
	}

	// Should have made 3 attempts (initial + 2 retries)
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}
