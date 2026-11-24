package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type proxyRoundTripFunc func(*http.Request) (*http.Response, error)

func (f proxyRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func proxyResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestProxyLaunchUsesBoundedLaunchTimeoutInsteadOfDefault(t *testing.T) {
	var deadlineRemaining atomic.Int64
	client := NewClientWithBaseURL("test-key", "https://api.example.test")
	client.httpClient = &http.Client{
		Timeout: DefaultTimeout,
		Transport: proxyRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("launch request has no deadline")
			}
			deadlineRemaining.Store(int64(time.Until(deadline)))
			return proxyResponse(r, http.StatusOK, `{"success":true}`), nil
		}),
	}

	_, status, err := client.ProxyWorkerRequest(context.Background(), "wf-1", "launch", map[string]string{"bundle_id": "com.example.app"})

	if err != nil || status != http.StatusOK {
		t.Fatalf("ProxyWorkerRequest() = %d, %v; want 200, nil", status, err)
	}
	remaining := time.Duration(deadlineRemaining.Load())
	if remaining <= DefaultTimeout || remaining > LaunchTimeout {
		t.Fatalf("launch deadline remaining = %s, want within (%s, %s]", remaining, DefaultTimeout, LaunchTimeout)
	}
	if client.httpClient.Timeout != DefaultTimeout {
		t.Fatalf("shared client timeout = %s, want %s unchanged", client.httpClient.Timeout, DefaultTimeout)
	}
}

func TestProxyLaunchIsSentOnceOnServerError(t *testing.T) {
	var attempts atomic.Int32
	client := NewClientWithBaseURL("test-key", "https://api.example.test")
	client.retryBaseDelay = time.Millisecond
	client.httpClient = &http.Client{
		Transport: proxyRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts.Add(1)
			return proxyResponse(r, http.StatusServiceUnavailable, `{"detail":"device not ready"}`), nil
		}),
	}

	_, status, err := client.ProxyWorkerRequest(context.Background(), "wf-1", "launch", map[string]string{"bundle_id": "com.example.app"})

	if err != nil || status != http.StatusServiceUnavailable {
		t.Fatalf("ProxyWorkerRequest() = %d, %v; want 503, nil", status, err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 for non-idempotent launch", got)
	}
}
