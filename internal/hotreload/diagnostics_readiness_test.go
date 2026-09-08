package hotreload

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForExpoMetroTransport_DoesNotProbeRelayWhileLocalMetroIsUnavailable(t *testing.T) {
	for _, connectionRefused := range []bool{false, true} {
		name := "unhealthy"
		if connectionRefused {
			name = "connection_refused"
		}
		t.Run(name, func(t *testing.T) {
			localMetro := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer localMetro.Close()
			localPort := serverPort(t, localMetro)
			if connectionRefused {
				localMetro.Close()
			}

			var relayRequests atomic.Int32
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				relayRequests.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer relay.Close()

			result, err := WaitForExpoMetroTransport(context.Background(), localPort, relay.URL, 80*time.Millisecond, 10*time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), "Metro health") {
				t.Fatalf("expected local readiness timeout, got %v", err)
			}
			if result == nil || result.AllPassed || len(result.Checks) != 1 || result.Checks[0].Passed {
				t.Fatalf("expected only a failed local readiness check, got %+v", result)
			}
			if got := relayRequests.Load(); got != 0 {
				t.Fatalf("relay received %d requests while local Metro was unavailable", got)
			}
		})
	}
}

func TestWaitForExpoMetroTransport_RechecksLocalReadinessBeforeEachRelayAttempt(t *testing.T) {
	var localRequests atomic.Int32
	localMetro := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := localRequests.Add(1)
		if attempt < 3 || attempt == 4 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer localMetro.Close()

	var relayRequests atomic.Int32
	var prematureRelayRequests atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := relayRequests.Add(1)
		if localAttempt := localRequests.Load(); localAttempt != 3 && localAttempt != 5 {
			prematureRelayRequests.Add(1)
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer relay.Close()

	result, err := WaitForExpoMetroTransport(context.Background(), serverPort(t, localMetro), relay.URL, time.Second, 10*time.Millisecond)
	if err != nil || result == nil || !result.AllPassed || len(result.Checks) != 2 {
		t.Fatalf("expected local and relay readiness after retry, got result=%+v error=%v", result, err)
	}
	if got := localRequests.Load(); got != 5 {
		t.Fatalf("local readiness requests = %d, want 5", got)
	}
	if got := relayRequests.Load(); got != 2 {
		t.Fatalf("relay readiness requests = %d, want 2", got)
	}
	if got := prematureRelayRequests.Load(); got != 0 {
		t.Fatalf("relay received %d requests without a successful local readiness check", got)
	}
}

func TestWaitForExpoMetroTransport_ReportsRelayFailureAfterLocalMetroIsReady(t *testing.T) {
	localMetro := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer localMetro.Close()

	var relayRequests atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayRequests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer relay.Close()

	result, err := WaitForExpoMetroTransport(context.Background(), serverPort(t, localMetro), relay.URL, 80*time.Millisecond, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "Tunnel HTTP") || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected relay failure in readiness timeout, got %v", err)
	}
	if result == nil || result.AllPassed || len(result.Checks) != 2 || !result.Checks[0].Passed || result.Checks[1].Passed {
		t.Fatalf("expected healthy local Metro and failed relay check, got %+v", result)
	}
	if got := relayRequests.Load(); got < 2 {
		t.Fatalf("relay requests = %d, want retries after local readiness", got)
	}
}

func TestWaitForExpoMetroTransport_CancelsWhileLocalMetroIsWarming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	localMetro := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer localMetro.Close()

	var relayRequests atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayRequests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer relay.Close()

	result, err := WaitForExpoMetroTransport(ctx, serverPort(t, localMetro), relay.URL, time.Second, time.Second)
	if !errors.Is(err, context.Canceled) || result == nil || result.AllPassed {
		t.Fatalf("expected cancellation with failed local readiness, got result=%+v error=%v", result, err)
	}
	if got := relayRequests.Load(); got != 0 {
		t.Fatalf("relay received %d requests during canceled local warm-up", got)
	}
}
