package cmd

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
)

const (
	retry429MaxAttempts = 5
	retry429BaseDelay   = time.Second
	retry429MaxDelay    = 8 * time.Second
)

var waitForRetry = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func doWith429Retry[T any](ctx context.Context, requestName string, do func() (T, error)) (T, error) {
	var zero T
	delay := retry429BaseDelay

	for attempt := 1; attempt <= retry429MaxAttempts; attempt++ {
		result, err := do()
		if err == nil {
			return result, nil
		}

		retryDelay, shouldRetry := getRetryDelay(err, delay, time.Now())
		if !shouldRetry || attempt == retry429MaxAttempts {
			return zero, err
		}

		log.WithFields(log.Fields{
			"attempt":      attempt,
			"maxAttempts":  retry429MaxAttempts,
			"nextDelay":    retryDelay,
			"requestName":  requestName,
			"statusCode":   http.StatusTooManyRequests,
			"backoffDelay": delay,
		}).Warn("Request was rate limited, retrying")

		if err := waitForRetry(ctx, retryDelay); err != nil {
			return zero, err
		}

		if retryDelay > delay {
			delay = retryDelay
		}
		delay *= 2
		if delay > retry429MaxDelay {
			delay = retry429MaxDelay
		}
	}

	panic("doWith429Retry: reached unreachable code; check retry429MaxAttempts")
}

func getRetryDelay(err error, fallback time.Duration, now time.Time) (time.Duration, bool) {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusTooManyRequests {
		return 0, false
	}

	if apiErr.Header != nil {
		if retryAfter, ok := parseRetryAfter(apiErr.Header.Get("Retry-After"), now); ok {
			if retryAfter > retry429MaxDelay {
				retryAfter = retry429MaxDelay
			}
			return retryAfter, true
		}
	}

	return fallback, true
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		maxSeconds := int(retry429MaxDelay.Seconds())
		if maxSeconds < 1 {
			maxSeconds = 1
		}
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		return time.Duration(seconds) * time.Second, true
	}

	retryTime, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}

	if retryTime.Before(now) {
		return 0, true
	}

	d := retryTime.Sub(now)
	if d > retry429MaxDelay {
		d = retry429MaxDelay
	}
	return d, true
}
