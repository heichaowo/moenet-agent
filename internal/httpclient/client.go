// Package httpclient provides an HTTP client with exponential backoff retry.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// RetryConfig configures the retry behavior
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default: 3)
	MaxRetries int
	// InitialDelay is the initial delay before the first retry (default: 1s)
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries (default: 30s)
	MaxDelay time.Duration
	// Multiplier is the factor by which delay increases (default: 2.0)
	Multiplier float64
	// Jitter adds randomness to delays to prevent thundering herd (default: 0.1)
	Jitter float64
}

// DefaultRetryConfig returns sensible defaults for retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
	}
}

// Client wraps http.Client with retry capability
type Client struct {
	httpClient *http.Client
	config     RetryConfig
}

// New creates a new retry-capable HTTP client
func New(httpClient *http.Client, config RetryConfig) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	// Apply defaults for zero values
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.InitialDelay == 0 {
		config.InitialDelay = time.Second
	}
	if config.MaxDelay == 0 {
		config.MaxDelay = 30 * time.Second
	}
	if config.Multiplier == 0 {
		config.Multiplier = 2.0
	}

	return &Client{
		httpClient: httpClient,
		config:     config,
	}
}

// isRetryable determines if a request should be retried based on the response
func isRetryable(resp *http.Response, err error) bool {
	// Always retry network errors
	if err != nil {
		return true
	}

	// Retry on server errors (5xx)
	if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		return true
	}

	// Retry on 429 Too Many Requests
	if resp.StatusCode == 429 {
		return true
	}

	return false
}

// calculateDelay computes the delay for a given attempt with jitter
func (c *Client) calculateDelay(attempt int) time.Duration {
	// Exponential backoff: initialDelay * (multiplier ^ attempt)
	delay := float64(c.config.InitialDelay) * math.Pow(c.config.Multiplier, float64(attempt))

	// Add jitter
	if c.config.Jitter > 0 {
		jitterRange := delay * c.config.Jitter
		delay += (rand.Float64()*2 - 1) * jitterRange
	}

	// Cap at max delay
	if delay > float64(c.config.MaxDelay) {
		delay = float64(c.config.MaxDelay)
	}

	return time.Duration(delay)
}

// Do executes an HTTP request with retries
// The request body must be replayable (use GetBody or a bytes.Reader)
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		// Check context cancellation
		if err := req.Context().Err(); err != nil {
			return nil, fmt.Errorf("request cancelled: %w", err)
		}

		// Clone request for retry (need fresh body reader)
		var reqCopy *http.Request
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("failed to get request body for retry: %w", err)
			}
			reqCopy = req.Clone(req.Context())
			reqCopy.Body = body
		} else {
			reqCopy = req
		}

		// Execute request
		resp, err := c.httpClient.Do(reqCopy)

		// Check if we should retry
		if !isRetryable(resp, err) {
			// Success or non-retryable error
			return resp, err
		}

		// Store for potential return
		lastErr = err
		if resp != nil {
			// Drain and close body to allow connection reuse
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastResp = resp
		}

		// Don't sleep after the last attempt
		if attempt < c.config.MaxRetries {
			delay := c.calculateDelay(attempt)
			log.Printf("[HTTPClient] Request failed, retrying in %v (attempt %d/%d): %v",
				delay, attempt+1, c.config.MaxRetries, err)

			// Wait with context awareness
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// All retries exhausted
	if lastErr != nil {
		return nil, fmt.Errorf("all %d retries failed: %w", c.config.MaxRetries, lastErr)
	}

	// Return the last response if no error but unsuccessful status
	return lastResp, errors.New("all retries failed with server errors")
}

// Get performs a GET request with retries
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post performs a POST request with retries
// The body should be a replayable reader (bytes.Reader, strings.Reader)
func (c *Client) Post(ctx context.Context, url, contentType string, body io.ReadSeeker) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	// Enable body replay for retries
	req.GetBody = func() (io.ReadCloser, error) {
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return io.NopCloser(body), nil
	}

	return c.Do(req)
}
