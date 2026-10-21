package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"
)

var ErrComputerOutcomeUnconfirmed = errors.New("computer request outcome is unconfirmed; inspect the original run or operation before submitting again")

func safeComputerTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrComputerOutcomeUnconfirmed, context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrComputerOutcomeUnconfirmed, context.Canceled)
	}
	return ErrComputerOutcomeUnconfirmed
}

func (c *Client) doComputerMutationOnce(ctx context.Context, path string, body any, requestID string, timeout time.Duration, extraHeaders ...map[string]string) (*http.Response, error) {
	if timeout <= 0 {
		return nil, errors.New("computer request requires a positive timeout")
	}
	client := *c.httpClient
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	headers := map[string]string{traceRequestIDHeader: requestID}
	for _, extra := range extraHeaders {
		maps.Copy(headers, extra)
	}
	response, err := c.doRequestOnceWithClient(ctx, http.MethodPost, path, body, &client, headers)
	if err != nil {
		return nil, safeComputerTransportError(err)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		response.Body.Close()
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Code:       "computer_redirect_refused",
			Message:    "computer request redirect refused; inspect the original run or operation before submitting again",
		}
	}
	return response, nil
}
