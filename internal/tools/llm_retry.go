package tools

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

// LLMRetryConfig configures retry behavior for LLM calls
type LLMRetryConfig struct {
	MaxRetries      int           `json:"max_retries"`       // Maximum number of retry attempts
	InitialDelay    time.Duration `json:"initial_delay"`     // Initial delay between retries
	MaxDelay        time.Duration `json:"max_delay"`         // Maximum delay between retries
	BackoffFactor   float64       `json:"backoff_factor"`    // Exponential backoff multiplier
	TimeoutPerRetry time.Duration `json:"timeout_per_retry"` // Timeout for each individual retry
}

// DefaultLLMRetryConfig returns a sensible default configuration
func DefaultLLMRetryConfig() LLMRetryConfig {
	return LLMRetryConfig{
		MaxRetries:      3,                // Try up to 3 times
		InitialDelay:    1 * time.Second,  // Start with 1 second delay
		MaxDelay:        30 * time.Second, // Cap at 30 seconds
		BackoffFactor:   2.0,              // Double delay each retry
		TimeoutPerRetry: 60 * time.Second, // 60 second timeout per attempt
	}
}

// LLMRetryWrapper wraps an LLM with retry logic
type LLMRetryWrapper struct {
	llm     llms.Model
	config  LLMRetryConfig
	verbose bool
}

// NewLLMRetryWrapper creates a new retry wrapper for an LLM
func NewLLMRetryWrapper(llm llms.Model, config LLMRetryConfig, verbose bool) *LLMRetryWrapper {
	return &LLMRetryWrapper{
		llm:     llm,
		config:  config,
		verbose: verbose,
	}
}

// GenerateContent calls the LLM with retry logic for transient failures
func (w *LLMRetryWrapper) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	var lastErr error
	delay := w.config.InitialDelay

	for attempt := 0; attempt <= w.config.MaxRetries; attempt++ {
		if w.verbose && attempt > 0 {
			fmt.Printf("🔄 LLM call attempt %d/%d (delay: %v)\n", attempt+1, w.config.MaxRetries+1, delay)
		}

		// Create timeout context for this specific retry
		retryCtx, cancel := context.WithTimeout(ctx, w.config.TimeoutPerRetry)

		// Make the LLM call
		response, err := w.llm.GenerateContent(retryCtx, messages, options...)
		cancel() // Always cancel the timeout context

		if err == nil {
			// Success!
			if w.verbose && attempt > 0 {
				fmt.Printf("✅ LLM call succeeded on attempt %d\n", attempt+1)
			}
			return response, nil
		}

		lastErr = err

		// Check if this is the last attempt
		if attempt >= w.config.MaxRetries {
			break
		}

		// Check if the error is retryable
		if !w.isRetryableError(err) {
			if w.verbose {
				fmt.Printf("❌ LLM error is not retryable: %v\n", err)
			}
			break
		}

		if w.verbose {
			fmt.Printf("⚠️ LLM call failed (attempt %d/%d): %v, retrying in %v\n",
				attempt+1, w.config.MaxRetries+1, err, delay)
		}

		// Wait before next retry (unless context is cancelled)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during retry delay: %w", ctx.Err())
		case <-time.After(delay):
			// Continue to next retry
		}

		// Calculate next delay with exponential backoff
		delay = time.Duration(float64(delay) * w.config.BackoffFactor)
		if delay > w.config.MaxDelay {
			delay = w.config.MaxDelay
		}
	}

	// All retries exhausted
	if w.verbose {
		fmt.Printf("❌ LLM call failed after %d attempts: %v\n", w.config.MaxRetries+1, lastErr)
	}

	return nil, fmt.Errorf("LLM call failed after %d attempts: %w", w.config.MaxRetries+1, lastErr)
}

// isRetryableError determines if an error is worth retrying
func (w *LLMRetryWrapper) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Context cancellation errors (most common transient error)
	if strings.Contains(errStr, "context canceled") ||
		strings.Contains(errStr, "context cancelled") ||
		strings.Contains(errStr, "context deadline exceeded") {
		return true
	}

	// Network-related errors
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection timeout") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "temporary failure") {
		return true
	}

	// HTTP status code errors that are retryable
	if strings.Contains(errStr, "500") || // Internal Server Error
		strings.Contains(errStr, "502") || // Bad Gateway
		strings.Contains(errStr, "503") || // Service Unavailable
		strings.Contains(errStr, "504") || // Gateway Timeout
		strings.Contains(errStr, "429") { // Too Many Requests
		return true
	}

	// OpenAI-specific retryable errors
	if strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "server error") ||
		strings.Contains(errStr, "service unavailable") {
		return true
	}

	// DNS errors
	if strings.Contains(errStr, "dns") {
		return true
	}

	// Check for specific error types
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || netErr.Temporary()
	}

	if urlErr, ok := err.(*url.Error); ok {
		return w.isRetryableError(urlErr.Err)
	}

	// Default to not retryable for unknown errors to avoid infinite loops
	return false
}

// CallLLMWithRetry is a convenience function to call an LLM with default retry configuration
func CallLLMWithRetry(ctx context.Context, llm llms.Model, messages []llms.MessageContent, verbose bool, options ...llms.CallOption) (*llms.ContentResponse, error) {
	wrapper := NewLLMRetryWrapper(llm, DefaultLLMRetryConfig(), verbose)
	return wrapper.GenerateContent(ctx, messages, options...)
}

// CallLLMWithCustomRetry is a convenience function with custom retry configuration
func CallLLMWithCustomRetry(ctx context.Context, llm llms.Model, messages []llms.MessageContent, config LLMRetryConfig, verbose bool, options ...llms.CallOption) (*llms.ContentResponse, error) {
	wrapper := NewLLMRetryWrapper(llm, config, verbose)
	return wrapper.GenerateContent(ctx, messages, options...)
}
