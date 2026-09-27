package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

const busyNoticeForList = "Revyl is busy listing computers, retrying"

type fakeBusyClock struct {
	now   time.Time
	slept []time.Duration
}

func useFakeBusyRetry(t *testing.T) *fakeBusyClock {
	t.Helper()
	clock := &fakeBusyClock{now: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}
	original := busyRetry
	busyRetry = busyRetryPolicy{
		budget:   60 * time.Second,
		minDelay: time.Second,
		jitter:   func() time.Duration { return 250 * time.Millisecond },
		now:      func() time.Time { return clock.now },
		sleep: func(ctx context.Context, delay time.Duration) error {
			clock.slept = append(clock.slept, delay)
			clock.now = clock.now.Add(delay)
			return ctx.Err()
		},
	}
	t.Cleanup(func() { busyRetry = original })
	return clock
}

type scriptedResponse struct {
	status     int
	retryAfter string
	body       string
}

type scriptedServer struct {
	mu        sync.Mutex
	requests  []string
	responses map[string][]scriptedResponse
}

func newScriptedServer(t *testing.T, responses map[string][]scriptedResponse) (*httptest.Server, *scriptedServer) {
	t.Helper()
	script := &scriptedServer{responses: responses}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		script.mu.Lock()
		script.requests = append(script.requests, key)
		queue := script.responses[key]
		if len(queue) == 0 {
			script.mu.Unlock()
			t.Errorf("unexpected request: %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		response := queue[0]
		if len(queue) > 1 {
			script.responses[key] = queue[1:]
		}
		script.mu.Unlock()
		if response.retryAfter != "" {
			w.Header().Set("Retry-After", response.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, response.body)
	}))
	t.Cleanup(server.Close)
	return server, script
}

func (s *scriptedServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func busy(retryAfter string) scriptedResponse {
	return scriptedResponse{
		status:     http.StatusTooManyRequests,
		retryAfter: retryAfter,
		body:       `{"detail":"Revyl is busy right now; retry shortly"}`,
	}
}

const listPath = "GET /api/v1/execution/computers"
const computerListBody = `{"computers":[{"instance_id":"mi-0123456789abcdef0","last_seen_at":null,"name":"Revyl-Mac-Studio-T65T47F91M","status":"online"}]}`

func TestListRetriesThrottlingUsingRetryAfterUntilSuccess(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "notice", true: "quiet"}[quiet], func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			clock := useFakeBusyRetry(t)
			server, script := newScriptedServer(t, map[string][]scriptedResponse{
				listPath: {busy("3"), busy("3"), {status: http.StatusOK, body: computerListBody}},
			})
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			args := []string{"--json"}
			if quiet {
				args = append(args, "--quiet")
			}

			stdout, stderr, err := executeComputerList(t, context.Background(), args...)

			if err != nil {
				t.Fatal(err)
			}
			var listed api.CustomerComputerList
			if decodeErr := json.Unmarshal([]byte(stdout), &listed); decodeErr != nil || len(listed.Computers) != 1 {
				t.Fatalf("stdout is not the clean JSON inventory: %q (%v)", stdout, decodeErr)
			}
			if got := script.recorded(); len(got) != 3 {
				t.Fatalf("requests = %v, want three list attempts", got)
			}
			want := []time.Duration{3250 * time.Millisecond, 3250 * time.Millisecond}
			if !reflect.DeepEqual(clock.slept, want) {
				t.Fatalf("waits = %v, want Retry-After plus jitter %v", clock.slept, want)
			}
			wantNotices := 1
			if quiet {
				wantNotices = 0
			}
			if strings.Count(stderr, busyNoticeForList) != wantNotices {
				t.Fatalf("stderr = %q, want %d busy notice(s)", stderr, wantNotices)
			}
		})
	}
}

func TestBusyRetryUsesMinimumDelayWithoutRetryAfter(t *testing.T) {
	clock := useFakeBusyRetry(t)
	server, _ := newScriptedServer(t, map[string][]scriptedResponse{
		listPath: {busy(""), {status: http.StatusOK, body: computerListBody}},
	})

	_, err := retryWhileBusy(context.Background(), "listing computers", api.NewClientWithBaseURL("test-key", server.URL).ListComputers)

	if err != nil || !reflect.DeepEqual(clock.slept, []time.Duration{1250 * time.Millisecond}) {
		t.Fatalf("err = %v, waits = %v", err, clock.slept)
	}
}

func TestBusyRetryGivesUpWithinBudget(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	clock := useFakeBusyRetry(t)
	server, script := newScriptedServer(t, map[string][]scriptedResponse{
		listPath: {busy("5")},
	})
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	stdout, _, err := executeComputerList(t, context.Background(), "--json")

	var apiErr *api.APIError
	if stdout != "" || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("exhausted list: stdout=%q, err=%v", stdout, err)
	}
	if !strings.Contains(err.Error(), "gave up after retrying for 60s") {
		t.Fatalf("exhaustion is not distinguishable: %v", err)
	}
	var waited time.Duration
	for _, delay := range clock.slept {
		waited += delay
	}
	if waited > busyRetry.budget || len(clock.slept) != 11 || len(script.recorded()) != 12 {
		t.Fatalf("waited %s over %d waits and %d requests; want at most %s", waited, len(clock.slept), len(script.recorded()), busyRetry.budget)
	}
}

func TestBusyRetryDoesNotRetryOtherFailures(t *testing.T) {
	const instanceID = "mi-0123456789abcdef0"
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusNotFound, http.StatusForbidden, http.StatusUnauthorized, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			clock := useFakeBusyRetry(t)
			server, script := newScriptedServer(t, map[string][]scriptedResponse{
				"POST /api/v1/execution/computers/" + instanceID + "/shell-sessions": {
					{status: status, retryAfter: "1", body: `{"detail":"Computer unavailable"}`},
				},
			})
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			_, _, err := executeComputerCommand(t, context.Background(), "ssh", instanceID)

			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("open error = %v, want HTTP %d", err, status)
			}
			if len(script.recorded()) != 1 || len(clock.slept) != 0 {
				t.Fatalf("requests = %v, waits = %v; want one attempt", script.recorded(), clock.slept)
			}
		})
	}
}

func TestSSHNameResolutionRetriesThrottledInventory(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	clock := useFakeBusyRetry(t)
	openPath := "POST /api/v1/execution/computers/mi-0123456789abcdef0/shell-sessions"
	server, script := newScriptedServer(t, map[string][]scriptedResponse{
		listPath: {busy("2"), {status: http.StatusOK, body: computerListBody}},
		openPath: {{status: http.StatusNotFound, body: `{"detail":"Computer unavailable"}`}},
	})
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	_, stderr, err := executeComputerCommand(t, context.Background(), "ssh", "T65T47F91M")

	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("resolved shell error = %v", err)
	}
	if want := []string{listPath, listPath, openPath}; !reflect.DeepEqual(script.recorded(), want) {
		t.Fatalf("requests = %v, want %v", script.recorded(), want)
	}
	if !reflect.DeepEqual(clock.slept, []time.Duration{2250 * time.Millisecond}) || !strings.Contains(stderr, busyNoticeForList) {
		t.Fatalf("waits = %v, stderr = %q", clock.slept, stderr)
	}
}

func TestShellOpenRetriesThrottlingThenKeepsStartupDeadline(t *testing.T) {
	for _, targeted := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "targeted"}[targeted], func(t *testing.T) {
			const instanceID = "mi-0123456789abcdef0"
			clock := useFakeBusyRetry(t)
			openPath := "POST /api/v1/execution/mac/shell-sessions"
			if targeted {
				openPath = "POST /api/v1/execution/computers/" + instanceID + "/shell-sessions"
			}
			deletePath := "DELETE /api/v1/execution/mac/shell-sessions/test-session"
			session, _ := json.Marshal(api.MacShellSession{SessionId: "test-session", InstanceId: instanceID})
			server, script := newScriptedServer(t, map[string][]scriptedResponse{
				openPath:   {busy("2"), busy("4"), {status: http.StatusOK, body: string(session)}},
				deletePath: {{status: http.StatusNoContent}},
			})
			client := api.NewClientWithBaseURL("test-key", server.URL)
			open := client.OpenMacShellSession
			if targeted {
				open = func(ctx context.Context) (*api.MacShellSession, error) {
					return client.OpenComputerShellSession(ctx, instanceID)
				}
			}

			err := runBrokeredShellSessionWithOpener(context.Background(), client, open, "", func(startupCtx context.Context, opened *api.MacShellSession, _ string, onReady func()) error {
				deadline, ok := startupCtx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
					t.Fatal("connection startup deadline did not start after the session opened")
				}
				if opened.SessionId != "test-session" {
					t.Fatalf("session = %#v", opened)
				}
				onReady()
				return nil
			})

			if err != nil {
				t.Fatal(err)
			}
			if want := []string{openPath, openPath, openPath, deletePath}; !reflect.DeepEqual(script.recorded(), want) {
				t.Fatalf("requests = %v, want %v", script.recorded(), want)
			}
			if want := []time.Duration{2250 * time.Millisecond, 4250 * time.Millisecond}; !reflect.DeepEqual(clock.slept, want) {
				t.Fatalf("waits = %v, want %v", clock.slept, want)
			}
		})
	}
}

func TestBusyRetryStopsWhenCanceledDuringWait(t *testing.T) {
	useFakeBusyRetry(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	_, err := retryWhileBusy(ctx, "opening shells", func(context.Context) (*api.MacShellSession, error) {
		calls++
		cancel()
		return nil, &api.APIError{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Second}
	})

	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestSleepContextReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := sleepContext(ctx, time.Minute); !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("sleepContext = %v after %s", err, time.Since(started))
	}
}
