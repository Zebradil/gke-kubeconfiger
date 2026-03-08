package cmd

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
)

// withRetry executes fn and retries on transient API errors (429, 5xx)
// with exponential backoff and jitter.
func withRetry[T any](maxRetries int, operation string, fn func() (T, error), sleepFn func(time.Duration)) (T, error) {
	var lastErr error
	for attempt := range maxRetries + 1 {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err

		statusCode, retryable := isRetryableError(err)
		if !retryable {
			var zero T
			return zero, err
		}

		if attempt == maxRetries {
			break
		}

		delay := computeBackoff(attempt, statusCode, err)
		log.WithFields(log.Fields{
			"operation":  operation,
			"attempt":    attempt + 1,
			"maxRetries": maxRetries,
			"statusCode": statusCode,
			"delay":      delay,
			"error":      err,
		}).Warn("Retrying after transient API error")

		sleepFn(delay)
	}

	var zero T
	return zero, lastErr
}

// isRetryableError checks if the error is a transient Google API error
// that should be retried.
func isRetryableError(err error) (int, bool) {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return 0, false
	}
	switch apiErr.Code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return apiErr.Code, true
	default:
		return apiErr.Code, false
	}
}

// getRetryAfter extracts the Retry-After header value from a Google API error.
// Returns 0 if the header is absent or cannot be parsed.
func getRetryAfter(err error) time.Duration {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return 0
	}
	if apiErr.Header == nil {
		return 0
	}
	ra := apiErr.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	seconds, parseErr := strconv.Atoi(ra)
	if parseErr != nil {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// computeBackoff calculates the delay before the next retry attempt.
// For 429 errors, it respects the Retry-After header if present.
// Otherwise, it uses exponential backoff with jitter.
func computeBackoff(attempt, statusCode int, err error) time.Duration {
	const (
		baseDelay = 1 * time.Second
		maxDelay  = 60 * time.Second
	)

	var delay time.Duration
	if statusCode == http.StatusTooManyRequests {
		if ra := getRetryAfter(err); ra > 0 {
			// Retry-After present: use server value with upward-only jitter [1.0, 1.5)
			jitter := 1.0 + rand.Float64()*0.5
			delay = time.Duration(float64(ra) * jitter)
			if delay > maxDelay {
				delay = maxDelay
			}
			return delay
		}
	}

	// Exponential backoff using bit shift to avoid float overflow.
	// Guard against overflow: for attempt >= 62, shift would overflow int64.
	if attempt >= 62 {
		delay = maxDelay
	} else {
		delay = baseDelay << attempt
		if delay <= 0 || delay > maxDelay {
			delay = maxDelay
		}
	}

	// Add jitter: multiply by random factor in [0.5, 1.5)
	jitter := 0.5 + rand.Float64()
	delay = time.Duration(float64(delay) * jitter)

	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}
