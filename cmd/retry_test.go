package cmd

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestDoWith429RetryImmediateSuccess(t *testing.T) {
	restoreWaitForRetry(t, nil)

	attempts := 0
	result, err := doWith429Retry(context.Background(), "test request", func() (string, error) {
		attempts++
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("doWith429Retry returned error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("doWith429Retry returned %q, want %q", result, "ok")
	}
	if attempts != 1 {
		t.Fatalf("doWith429Retry made %d attempts, want 1", attempts)
	}
}

func TestDoWith429RetryRetriesThenSucceeds(t *testing.T) {
	delays := restoreWaitForRetry(t, nil)

	attempts := 0
	result, err := doWith429Retry(context.Background(), "test request", func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", rateLimitError("")
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("doWith429Retry returned error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("doWith429Retry returned %q, want %q", result, "ok")
	}
	if attempts != 3 {
		t.Fatalf("doWith429Retry made %d attempts, want 3", attempts)
	}
	if !slices.Equal(*delays, []time.Duration{retry429BaseDelay, 2 * retry429BaseDelay}) {
		t.Fatalf("doWith429Retry waited %v, want %v", *delays, []time.Duration{retry429BaseDelay, 2 * retry429BaseDelay})
	}
}

func TestDoWith429RetryStopsAfterMaxAttempts(t *testing.T) {
	delays := restoreWaitForRetry(t, nil)

	attempts := 0
	_, err := doWith429Retry(context.Background(), "test request", func() (string, error) {
		attempts++
		return "", rateLimitError("")
	})

	if err == nil {
		t.Fatal("doWith429Retry returned nil error, want rate-limit error")
	}
	if attempts != retry429MaxAttempts {
		t.Fatalf("doWith429Retry made %d attempts, want %d", attempts, retry429MaxAttempts)
	}
	if len(*delays) != retry429MaxAttempts-1 {
		t.Fatalf("doWith429Retry waited %d times, want %d", len(*delays), retry429MaxAttempts-1)
	}
}

func TestDoWith429RetryDoesNotRetryOtherErrors(t *testing.T) {
	delays := restoreWaitForRetry(t, nil)

	attempts := 0
	expectedErr := &googleapi.Error{Code: http.StatusInternalServerError}
	_, err := doWith429Retry(context.Background(), "test request", func() (string, error) {
		attempts++
		return "", expectedErr
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("doWith429Retry returned %v, want %v", err, expectedErr)
	}
	if attempts != 1 {
		t.Fatalf("doWith429Retry made %d attempts, want 1", attempts)
	}
	if len(*delays) != 0 {
		t.Fatalf("doWith429Retry waited %d times, want 0", len(*delays))
	}
}

func TestDoWith429RetryHonorsRetryAfterSeconds(t *testing.T) {
	delays := restoreWaitForRetry(t, nil)

	attempts := 0
	result, err := doWith429Retry(context.Background(), "test request", func() (string, error) {
		attempts++
		if attempts == 1 {
			return "", rateLimitError("3")
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("doWith429Retry returned error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("doWith429Retry returned %q, want %q", result, "ok")
	}
	if !slices.Equal(*delays, []time.Duration{3 * time.Second}) {
		t.Fatalf("doWith429Retry waited %v, want %v", *delays, []time.Duration{3 * time.Second})
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.March, 8, 10, 0, 0, 0, time.UTC)

	delay, ok := parseRetryAfter("Sun, 08 Mar 2026 10:00:05 GMT", now)
	if !ok {
		t.Fatal("parseRetryAfter returned ok=false, want true")
	}
	if delay != 5*time.Second {
		t.Fatalf("parseRetryAfter returned %v, want %v", delay, 5*time.Second)
	}
}

func restoreWaitForRetry(t *testing.T, err error) *[]time.Duration {
	t.Helper()

	original := waitForRetry
	delays := []time.Duration{}
	waitForRetry = func(ctx context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return err
	}
	t.Cleanup(func() {
		waitForRetry = original
	})

	return &delays
}

func rateLimitError(retryAfter string) error {
	header := http.Header{}
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}

	return &googleapi.Error{
		Code:   http.StatusTooManyRequests,
		Header: header,
	}
}
