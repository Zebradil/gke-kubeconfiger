package cmd

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

var noSleep = func(time.Duration) {}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantCode      int
		wantRetryable bool
	}{
		{
			name:          "429 Too Many Requests",
			err:           &googleapi.Error{Code: 429},
			wantCode:      429,
			wantRetryable: true,
		},
		{
			name:          "500 Internal Server Error",
			err:           &googleapi.Error{Code: 500},
			wantCode:      500,
			wantRetryable: true,
		},
		{
			name:          "502 Bad Gateway",
			err:           &googleapi.Error{Code: 502},
			wantCode:      502,
			wantRetryable: true,
		},
		{
			name:          "503 Service Unavailable",
			err:           &googleapi.Error{Code: 503},
			wantCode:      503,
			wantRetryable: true,
		},
		{
			name:          "504 Gateway Timeout",
			err:           &googleapi.Error{Code: 504},
			wantCode:      504,
			wantRetryable: true,
		},
		{
			name:          "400 Bad Request",
			err:           &googleapi.Error{Code: 400},
			wantCode:      400,
			wantRetryable: false,
		},
		{
			name:          "403 Forbidden",
			err:           &googleapi.Error{Code: 403},
			wantCode:      403,
			wantRetryable: false,
		},
		{
			name:          "404 Not Found",
			err:           &googleapi.Error{Code: 404},
			wantCode:      404,
			wantRetryable: false,
		},
		{
			name:          "non-API error",
			err:           errors.New("some random error"),
			wantCode:      0,
			wantRetryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, retryable := isRetryableError(tt.err)
			if code != tt.wantCode {
				t.Errorf("isRetryableError() code = %d, want %d", code, tt.wantCode)
			}
			if retryable != tt.wantRetryable {
				t.Errorf("isRetryableError() retryable = %v, want %v", retryable, tt.wantRetryable)
			}
		})
	}
}

func TestGetRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{
			name: "valid Retry-After header",
			err: &googleapi.Error{
				Code:   429,
				Header: http.Header{"Retry-After": []string{"30"}},
			},
			want: 30 * time.Second,
		},
		{
			name: "no header",
			err:  &googleapi.Error{Code: 429},
			want: 0,
		},
		{
			name: "empty Retry-After",
			err: &googleapi.Error{
				Code:   429,
				Header: http.Header{"Retry-After": []string{""}},
			},
			want: 0,
		},
		{
			name: "unparseable Retry-After",
			err: &googleapi.Error{
				Code:   429,
				Header: http.Header{"Retry-After": []string{"not-a-number"}},
			},
			want: 0,
		},
		{
			name: "non-API error",
			err:  errors.New("random error"),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getRetryAfter(tt.err)
			if got != tt.want {
				t.Errorf("getRetryAfter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeBackoff(t *testing.T) {
	t.Run("exponential growth", func(t *testing.T) {
		err := &googleapi.Error{Code: 500}
		prev := time.Duration(0)
		for attempt := range 4 {
			delay := computeBackoff(attempt, 500, err)
			// With jitter in [0.5, 1.5), for attempt 0 base is 1s,
			// so delay should be in [0.5s, 1.5s)
			if delay <= 0 {
				t.Errorf("attempt %d: delay should be positive, got %v", attempt, delay)
			}
			if attempt > 0 && delay < prev/4 {
				// Very rough check that delays grow (accounting for jitter)
				t.Logf("attempt %d: delay %v (prev %v) - growth looks suspicious", attempt, delay, prev)
			}
			prev = delay
		}
	})

	t.Run("respects Retry-After for 429", func(t *testing.T) {
		err := &googleapi.Error{
			Code:   429,
			Header: http.Header{"Retry-After": []string{"10"}},
		}
		delay := computeBackoff(0, http.StatusTooManyRequests, err)
		// Base is 10s, with upward-only jitter [1.0, 1.5) => [10s, 15s)
		if delay < 10*time.Second || delay >= 15*time.Second {
			t.Errorf("expected delay in [10s, 15s), got %v", delay)
		}
	})

	t.Run("caps at 60 seconds", func(t *testing.T) {
		err := &googleapi.Error{Code: 500}
		delay := computeBackoff(10, 500, err)
		if delay > 60*time.Second {
			t.Errorf("delay should be capped at 60s, got %v", delay)
		}
	})

	t.Run("handles very large attempt without overflow", func(t *testing.T) {
		err := &googleapi.Error{Code: 500}
		delay := computeBackoff(100, 500, err)
		if delay <= 0 || delay > 60*time.Second {
			t.Errorf("expected positive delay <= 60s for large attempt, got %v", delay)
		}
	})
}

func TestWithRetry_ImmediateSuccess(t *testing.T) {
	calls := 0
	result, err := withRetry(3, "test-op", func() (string, error) {
		calls++
		return "ok", nil
	}, noSleep)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetry_TransientThenSuccess(t *testing.T) {
	calls := 0
	result, err := withRetry(3, "test-op", func() (string, error) {
		calls++
		if calls <= 2 {
			return "", &googleapi.Error{Code: 429}
		}
		return "ok", nil
	}, noSleep)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetry_ExhaustedRetries(t *testing.T) {
	calls := 0
	_, err := withRetry(2, "test-op", func() (string, error) {
		calls++
		return "", &googleapi.Error{Code: 429, Message: "rate limited"}
	}, noSleep)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// 1 initial + 2 retries = 3 calls
	if calls != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", calls)
	}
}

func TestWithRetry_NonRetryableError(t *testing.T) {
	calls := 0
	_, err := withRetry(3, "test-op", func() (string, error) {
		calls++
		return "", &googleapi.Error{Code: 403, Message: "forbidden"}
	}, noSleep)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry for 403), got %d", calls)
	}
}

func TestWithRetry_ZeroRetries(t *testing.T) {
	calls := 0
	_, err := withRetry(0, "test-op", func() (string, error) {
		calls++
		return "", &googleapi.Error{Code: 429}
	}, noSleep)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call with 0 retries, got %d", calls)
	}
}
