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
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/testutil"
)

func TestSSHAcceptsOptionalComputerTarget(t *testing.T) {
	for _, args := range [][]string{nil, {"mi-0123456789abcdef0"}, {"Revyl-Mac-Studio-T65T47F91M"}, {"T65T47F91M"}, {"some-machine"}, {"i-0123456789abcdef0"}, {"mi-build-host"}, {"mi-0123456789ABCDEF0"}, {"name:mi-0123456789abcdef0"}} {
		if err := sshCmd.Args(sshCmd, args); err != nil {
			t.Fatalf("valid shell arguments %v rejected: %v", args, err)
		}
	}
}

func TestSSHRejectsInvalidTargetBeforeHTTPRequest(t *testing.T) {
	for _, args := range [][]string{
		{"mi-0123456789abcdef0/other"},
		{"mi-0123456789abcdef0?org=other"},
		{" mi-0123456789abcdef0"},
		{"mi-0123456789abcdef0\n"},
		{"name:"},
		{"name:mi-build-host/other"},
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

func TestSSHNameOrSerialResolvesOnceThenOpensByInstanceID(t *testing.T) {
	for _, target := range []string{"Revyl-Mac-Studio-T65T47F91M", "T65T47F91M", "mi-build-host", "name:mi-0123456789abcdef1"} {
		t.Run(target, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/execution/computers":
					if r.URL.RawQuery != "" {
						t.Errorf("resolution query = %q", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, `{"computers":[{"instance_id":"mi-0123456789abcdef0","last_seen_at":null,"name":"Revyl-Mac-Studio-T65T47F91M","status":"online"},{"instance_id":"mi-0123456789abcdef1","last_seen_at":null,"name":"mi-build-host","status":"online"},{"instance_id":"mi-0123456789abcdef2","last_seen_at":null,"name":"mi-0123456789abcdef1","status":"online"}]}`)
				case "POST /api/v1/execution/computers/mi-0123456789abcdef0/shell-sessions":
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"detail":"Computer unavailable"}`)
				case "POST /api/v1/execution/computers/mi-0123456789abcdef1/shell-sessions", "POST /api/v1/execution/computers/mi-0123456789abcdef2/shell-sessions":
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"detail":"Computer unavailable"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			_, _, err := executeComputerCommand(t, context.Background(), "ssh", target)
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				t.Fatalf("resolved shell error = %v", err)
			}
			instanceID := "mi-0123456789abcdef0"
			if target == "mi-build-host" {
				instanceID = "mi-0123456789abcdef1"
			} else if target == "name:mi-0123456789abcdef1" {
				instanceID = "mi-0123456789abcdef2"
			}
			want := []string{
				"GET /api/v1/execution/computers",
				"POST /api/v1/execution/computers/" + instanceID + "/shell-sessions",
			}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestSSHInstanceIDSkipsInventory(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"Computer unavailable"}`)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	_, _, err := executeComputerCommand(t, context.Background(), "ssh", "mi-0123456789abcdef1")
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("direct instance ID error = %v", err)
	}
	want := []string{"POST /api/v1/execution/computers/mi-0123456789abcdef1/shell-sessions"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestResolveComputerTargetFetchesFullInventoryOnce(t *testing.T) {
	const target = "T65T47F91M"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/computers" || r.URL.RawQuery != "" {
			t.Errorf("unexpected resolution request: %s %s", r.Method, r.URL.String())
		}
		_, _ = io.WriteString(w, `{"computers":[{"instance_id":"mi-0123456789abcdef0","last_seen_at":null,"name":"Revyl-Mac-Studio-T65T47F91M","status":"online"}]}`)
	}))
	defer server.Close()

	computer, err := resolveComputerTarget(
		context.Background(), api.NewClientWithBaseURL("test-key", server.URL), target,
	)
	if err != nil || computer == nil || computer.InstanceId != "mi-0123456789abcdef0" || calls.Load() != 1 {
		t.Fatalf("resolved computer = %#v, err=%v, calls=%d", computer, err, calls.Load())
	}
}

func TestResolveMatchingComputerRejectsAmbiguousNameOrSerial(t *testing.T) {
	for _, target := range []string{"Shared-Mac", "T65T47F91M"} {
		computers := []api.CustomerComputer{
			{InstanceId: "mi-0123456789abcdef0", Name: "Shared-Mac-T65T47F91M"},
			{InstanceId: "mi-0123456789abcdef1", Name: "shared-mac-T65T47F91M"},
		}
		if target == "Shared-Mac" {
			computers[0].Name = "Shared-Mac"
			computers[1].Name = "shared-mac"
		}

		matched, err := resolveMatchingComputer(computers, target)
		if matched != nil || err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("ambiguous target %q = %#v, %v", target, matched, err)
		}
	}
}

func TestComputerSerialFromNameUsesOnlyValidFinalSegment(t *testing.T) {
	for _, testCase := range []struct {
		name string
		want string
	}{
		{"Revyl-Mac-Studio-T65T47F91M", "T65T47F91M"},
		{"T65T47F91M", "T65T47F91M"},
		{"Revyl-T65T47F91M-extra", ""},
		{"Revyl-Mac-t65t47f91m", ""},
		{"Revyl-Mac-TOO-SHORT", ""},
	} {
		if got := computerSerialFromName(testCase.name); got != testCase.want {
			t.Fatalf("computerSerialFromName(%q) = %q, want %q", testCase.name, got, testCase.want)
		}
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
			}, "test-computer", func(startupCtx context.Context, session *api.MacShellSession, displayName string, onReady func()) error {
				deadline, ok := startupCtx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("targeted shell has no bounded startup deadline")
				}
				if session.InstanceId != instanceID {
					t.Fatal("targeted shell started on a different computer")
				}
				if displayName != "test-computer" {
					t.Fatalf("display name = %q", displayName)
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
