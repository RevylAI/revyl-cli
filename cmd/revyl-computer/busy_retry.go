package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// busyRetryPolicy bounds automatic retries of HTTP 429 responses, which the
// broker returns only when a request produced nothing and is safe to repeat.
type busyRetryPolicy struct {
	budget   time.Duration
	minDelay time.Duration
	jitter   func() time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

var busyRetry = busyRetryPolicy{
	budget:   60 * time.Second,
	minDelay: time.Second,
	jitter:   func() time.Duration { return rand.N(time.Second) }, // #nosec G404 -- jitter only spreads retry timing; it is not a security value
	now:      time.Now,
	sleep:    sleepContext,
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryWhileBusy[T any](ctx context.Context, activity string, call func(context.Context) (T, error)) (T, error) {
	deadline := busyRetry.now().Add(busyRetry.budget)
	announced := false
	for {
		result, err := call(ctx)
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
			return result, err
		}
		delay := max(apiErr.RetryAfter, busyRetry.minDelay) + busyRetry.jitter()
		if busyRetry.now().Add(delay).After(deadline) {
			var none T
			return none, fmt.Errorf(
				"gave up after retrying for %ds because Revyl is still busy %s; try again shortly: %w",
				int(busyRetry.budget.Seconds()), activity, err,
			)
		}
		if !announced {
			ui.PrintInfo("Revyl is busy %s, retrying", activity)
			announced = true
		}
		if err := busyRetry.sleep(ctx, delay); err != nil {
			var none T
			return none, err
		}
	}
}
