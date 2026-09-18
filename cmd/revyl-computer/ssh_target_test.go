package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/testutil"
)

func TestSSHAcceptsOptionalManagedInstanceID(t *testing.T) {
	for _, args := range [][]string{nil, {"mi-0123456789abcdef0"}} {
		if err := sshCmd.Args(sshCmd, args); err != nil {
			t.Fatalf("valid shell arguments %v rejected: %v", args, err)
		}
	}
}

func TestSSHRejectsInvalidTargetBeforeHTTPRequest(t *testing.T) {
	for _, args := range [][]string{
		{"some-machine"},
		{"i-0123456789abcdef0"},
		{"mi-0123456789ABCDEF0"},
		{"mi-0123456789abcdef"},
		{"mi-0123456789abcdef00"},
		{"mi-0123456789abcdefg"},
		{"mi-0123456789abcdef0/other"},
		{"mi-0123456789abcdef0?org=other"},
		{" mi-0123456789abcdef0"},
		{"mi-0123456789abcdef0\n"},
		{"mi-0123456789abcdef0", "mi-0123456789abcdef1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			stdout, _, err := executeComputerCommand(t, context.Background(), "ssh", args...)
			if err == nil || calls.Load() != 0 || stdout != "" {
				t.Fatalf("invalid target reached execution: err=%v, calls=%d, stdout=%q", err, calls.Load(), stdout)
			}
			if len(args) == 1 && !strings.Contains(err.Error(), "revyl-computer list") {
				t.Fatalf("invalid target error lacks a recovery command: %v", err)
			}
		})
	}
}

func TestSSHTargetRequiresAuthentication(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unauthenticated targeted shell reached backend")
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	stdout, stderr, err := executeComputerCommand(t, context.Background(), "ssh", "mi-0123456789abcdef0")
	if err == nil || err.Error() != "not authenticated" || stdout != "" || !strings.Contains(stderr, "revyl auth login") {
		t.Fatalf("unauthenticated target: stdout=%q, stderr=%q, err=%v", stdout, stderr, err)
	}
}

func TestSSHTargetUsesSharedCredentialsWithoutFallback(t *testing.T) {
	for _, source := range []string{"environment", "saved"} {
		t.Run(source, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			if source == "saved" {
				t.Setenv("REVYL_API_KEY", "")
				if err := auth.NewManager().SaveAPIKeyCredentials("test-api-key", "user@example.com", "test-org", "test-user"); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/computers/mi-0123456789abcdef0/shell-sessions" || r.URL.RawQuery != "" {
					t.Errorf("unexpected targeted shell request: %s %s", r.Method, r.URL.String())
				}
				if r.Header.Get("Authorization") != "Bearer test-api-key" {
					t.Error("targeted shell did not use shared credentials")
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"detail":"Computer unavailable"}`)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			stdout, _, err := executeComputerCommand(t, context.Background(), "ssh", "mi-0123456789abcdef0")
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound || calls.Load() != 1 || stdout != "" {
				t.Fatalf("unavailable target: err=%v, requests=%d, stdout=%q", err, calls.Load(), stdout)
			}
		})
	}
}

func TestTargetedShellPreservesSessionLifecycle(t *testing.T) {
	const instanceID = "mi-0123456789abcdef0"
	for _, cancelParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "canceled"}[cancelParent], func(t *testing.T) {
			var starts, terminations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "POST /api/v1/execution/computers/" + instanceID + "/shell-sessions":
					starts.Add(1)
					_ = json.NewEncoder(w).Encode(api.MacShellSession{SessionId: "test-session", InstanceId: instanceID})
				case "DELETE /api/v1/execution/mac/shell-sessions/test-session":
					terminations.Add(1)
					if r.Context().Err() != nil {
						t.Error("targeted shell cleanup inherited cancellation")
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected session request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test-key", server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := runBrokeredShellSessionWithOpener(ctx, client, func(startupCtx context.Context) (*api.MacShellSession, error) {
				return client.OpenComputerShellSession(startupCtx, instanceID)
			}, func(startupCtx context.Context, session *api.MacShellSession, onReady func()) error {
				deadline, ok := startupCtx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("targeted shell has no bounded startup deadline")
				}
				if session.InstanceId != instanceID {
					t.Fatal("targeted shell started on a different computer")
				}
				if cancelParent {
					cancel()
					return startupCtx.Err()
				}
				onReady()
				if startupCtx.Err() != context.Canceled {
					t.Fatal("ready targeted shell did not release startup context")
				}
				return nil
			})
			if starts.Load() != 1 || terminations.Load() != 1 {
				t.Fatalf("targeted session starts=%d, terminations=%d", starts.Load(), terminations.Load())
			}
			if cancelParent && !errors.Is(err, context.Canceled) || !cancelParent && err != nil {
				t.Fatalf("targeted session result = %v", err)
			}
		})
	}
}

func TestSSHTargetLifecycleDoesNotCaptureInstanceID(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	const instanceID = "mi-0123456789abcdef0"
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
		captured = payload
	})
	run := recorder.StartCommand(sshCmd, []string{instanceID})
	run.Complete(nil)
	run.Flush()
	if len(captured.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(captured.Events))
	}
	for _, event := range captured.Events {
		if event.Properties["command"] != "revyl-computer ssh" {
			t.Fatalf("targeted shell changed lifecycle identity: %#v", event.Properties)
		}
	}
	encoded, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), instanceID) {
		t.Fatal("targeted shell analytics captured an instance ID")
	}
}
