package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/commandanalytics"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func executeComputerList(t *testing.T, ctx context.Context, args ...string) (string, string, error) {
	t.Helper()
	return executeComputerCommand(t, ctx, "list", args...)
}

func executeComputerCommand(t *testing.T, ctx context.Context, command string, args ...string) (string, string, error) {
	t.Helper()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = stderr
	var stdout strings.Builder
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(append([]string{command}, args...))
	rootCmd.SetContext(ctx)
	listCmd.SetContext(ctx)
	sshCmd.SetContext(ctx)
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = stderr.Close()
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		rootCmd.SetContext(context.Background())
		listCmd.SetContext(context.Background())
		sshCmd.SetContext(context.Background())
		_ = listCmd.Flags().Set("json", "false")
		_ = rootCmd.PersistentFlags().Set("dev", "false")
		_ = rootCmd.PersistentFlags().Set("quiet", "false")
		ui.SetQuietMode(false)
	})
	err = rootCmd.Execute()
	output, readErr := os.ReadFile(stderr.Name())
	if readErr != nil {
		t.Fatal(readErr)
	}
	return stdout.String(), string(output), err
}

func TestListCommandIdentity(t *testing.T) {
	cmd, remaining, err := rootCmd.Find([]string{"list"})
	if err != nil || cmd != listCmd || len(remaining) != 0 || cmd.CommandPath() != "revyl-computer list" {
		t.Fatalf("find list = %v, %v, %v", cmd, remaining, err)
	}
	if cmd.Flags().Lookup("json") == nil {
		t.Fatal("list has no JSON flag")
	}
}

func TestListRequiresAuthenticationAndRejectsArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--json"}, {"computer-a"}, {"--json", "computer-a"}, {"--org", "other-org"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			stdout, stderr, err := executeComputerList(t, context.Background(), args...)
			if err == nil || stdout != "" || calls.Load() != 0 {
				t.Fatalf("invalid invocation: stdout=%q, err=%v, requests=%d", stdout, err, calls.Load())
			}
			expectAuthentication := len(args) == 0 || len(args) == 1 && args[0] == "--json"
			if expectAuthentication && (err.Error() != "not authenticated" || !strings.Contains(stderr, "revyl auth login")) {
				t.Fatalf("missing actionable authentication failure: %v, %q", err, stderr)
			}
			if !expectAuthentication && err.Error() == "not authenticated" {
				t.Fatal("arguments were not rejected before authentication")
			}
		})
	}
}

func TestListOutputAndSharedCredentials(t *testing.T) {
	const assigned = `{"computers":[{"instance_id":"computer-a","status":"online"},{"instance_id":"computer-b","status":"offline"}]}`
	const empty = `{"computers":[]}`
	for _, testCase := range []struct {
		name  string
		body  string
		args  []string
		saved bool
	}{
		{name: "human environment", body: assigned},
		{name: "human saved", body: assigned, saved: true},
		{name: "human empty", body: empty},
		{name: "json", body: assigned, args: []string{"--json"}},
		{name: "json saved", body: assigned, args: []string{"--json"}, saved: true},
		{name: "json empty", body: empty, args: []string{"--json"}},
		{name: "quiet", body: assigned, args: []string{"--quiet"}},
		{name: "quiet empty", body: empty, args: []string{"--quiet"}},
		{name: "quiet json", body: assigned, args: []string{"--quiet", "--json"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			if testCase.saved {
				t.Setenv("REVYL_API_KEY", "")
				if err := auth.NewManager().SaveAPIKeyCredentials("test-api-key", "user@example.com", "test-org", "test-user"); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/computers" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				if r.Header.Get("Authorization") != "Bearer test-api-key" {
					t.Error("list did not use shared Revyl credentials")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("list must not send caller-supplied targeting")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			stdout, stderr, err := executeComputerList(t, context.Background(), testCase.args...)
			if err != nil || calls.Load() != 1 {
				t.Fatalf("list error = %v, requests = %d", err, calls.Load())
			}
			if strings.Contains(testCase.name, "json") {
				if stdout != testCase.body+"\n" || stderr != "" {
					t.Fatalf("JSON output = %q, stderr = %q; want exact response and no banners", stdout, stderr)
				}
				var got, want api.CustomerComputerList
				if err := json.Unmarshal([]byte(stdout), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(testCase.body), &want); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("typed JSON response = %#v, want %#v", got, want)
				}
				return
			}
			if stdout != "" {
				t.Fatalf("human output leaked to stdout: %q", stdout)
			}
			if strings.Contains(testCase.name, "quiet") {
				if stderr != "" {
					t.Fatalf("quiet output = %q", stderr)
				}
				return
			}
			if testCase.body == empty {
				if !strings.Contains(stderr, "No computers are assigned to your organization.") {
					t.Fatalf("empty output = %q", stderr)
				}
				return
			}
			for _, text := range []string{"INSTANCE ID", "STATUS", "computer-a", "online", "computer-b", "offline"} {
				if !strings.Contains(stderr, text) {
					t.Errorf("human output missing %q: %q", text, stderr)
				}
			}
			if strings.Index(stderr, "computer-a") > strings.Index(stderr, "computer-b") {
				t.Fatalf("backend ordering changed: %q", stderr)
			}
		})
	}
}

func TestListDevMode(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv("REVYL_BACKEND_URL", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"computers":[]}`)
	}))
	defer server.Close()
	backendURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(backendURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVYL_BACKEND_PORT", port)
	stdout, stderr, err := executeComputerList(t, context.Background(), "--dev", "--json")
	if err != nil || stdout != "{\"computers\":[]}\n" || stderr != "" {
		t.Fatalf("development list: stdout=%q, stderr=%q, error=%v", stdout, stderr, err)
	}
}

func TestListPropagatesCancellationAndAPIErrors(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "forbidden", true: "canceled"}[canceled], func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv("REVYL_TELEMETRY_DISABLED", "true")
			originalRun := listCmd.RunE
			commandanalytics.Install(listCmd, analytics.Config{})
			t.Cleanup(func() { listCmd.RunE = originalRun })
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/computers" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"detail":"Access denied"}`)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			stdout, _, err := executeComputerList(t, ctx, "--json")
			if stdout != "" || err == nil || !strings.Contains(err.Error(), "failed to list computers") {
				t.Fatalf("list failure: stdout=%q, error=%v", stdout, err)
			}
			if canceled {
				if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
					t.Fatalf("canceled list: err=%v, requests=%d", err, calls.Load())
				}
			} else {
				var apiErr *api.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden || calls.Load() != 1 {
					t.Fatalf("forbidden list: err=%v, requests=%d", err, calls.Load())
				}
			}
		})
	}
}

func TestListCommandLifecycleIdentity(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	for _, failed := range []bool{false, true} {
		var captured analytics.TelemetryPayload
		recorder := analytics.NewWithFlusher(analytics.Config{Version: "test-version"}, func(payload analytics.TelemetryPayload) {
			captured = payload
		})
		run := recorder.StartCommand(listCmd, nil)
		var err error
		terminalEvent := analytics.CliCommandCompletedEvent
		if failed {
			err = errors.New("failed to list computers")
			terminalEvent = analytics.CliCommandFailedEvent
		}
		run.Complete(err)
		run.Flush()
		if len(captured.Events) != 2 || captured.Events[0].Event != analytics.CliCommandStartedEvent || captured.Events[1].Event != terminalEvent {
			t.Fatalf("unexpected command lifecycle: %#v", captured.Events)
		}
		for _, event := range captured.Events {
			if event.Properties["command"] != "revyl-computer list" {
				t.Fatalf("wrong command identity: %#v", event.Properties)
			}
		}
	}
}
