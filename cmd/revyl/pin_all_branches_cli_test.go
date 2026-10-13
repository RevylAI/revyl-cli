package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/testutil"
)

// pinAllBranchesBackend records the request bodies the CLI sends for uploads
// and runs, and answers the reads an upload summary makes.
type pinAllBranchesBackend struct {
	mu            sync.Mutex
	uploadBodies  []map[string]any
	fromURLBodies []map[string]any
	runBodies     []map[string]any
	paths         []string
}

func (b *pinAllBranchesBackend) record(target *[]map[string]any, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	b.mu.Lock()
	defer b.mu.Unlock()
	*target = append(*target, body)
}

func (b *pinAllBranchesBackend) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.uploadBodies, b.fromURLBodies, b.runBodies, b.paths = nil, nil, nil, nil
}

func TestPinAndAllBranchesCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "revyl")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	const (
		appID      = "11111111-1111-4111-8111-111111111111"
		branchID   = "22222222-2222-4222-8222-222222222222"
		buildID    = "33333333-3333-4333-8333-333333333333"
		testID     = "44444444-4444-4444-8444-444444444444"
		workflowID = "55555555-5555-4555-8555-555555555555"
	)
	backend := &pinAllBranchesBackend{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend.mu.Lock()
		backend.paths = append(backend.paths, r.URL.Path)
		backend.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID + "/builds/upload-session":
			fmt.Fprintf(w, `{"upload_id":"upload-1","upload_url":%q,"content_type":"application/octet-stream","branch_id":%q,"branch_name":"staging"}`, server.URL+"/artifact", branchID)
		case "/artifact":
			_, _ = io.Copy(io.Discard, r.Body)
		case "/api/v1/apps/" + appID + "/builds":
			backend.record(&backend.uploadBodies, r)
			fmt.Fprintf(w, `{"id":%q,"version":"v2","branch_id":%q,"branch_name":"staging"}`, buildID, branchID)
		case "/api/v1/apps/" + appID + "/builds/from-url":
			backend.record(&backend.fromURLBodies, r)
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"v2","branch_id":%q,"branch_name":"staging"}`, buildID, appID, branchID)
		case "/api/v1/apps/resolve":
			fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"staging","resolution_status":"resolved"}`, buildID, branchID)
		case "/api/v1/apps/builds/" + buildID:
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"v2"}`, buildID, appID)
		case "/api/v1/apps/" + appID + "/branches/" + branchID:
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"staging","pinned_build_id":null}`, branchID, appID)
		case "/api/v1/tests/get_test_by_id/" + testID:
			fmt.Fprintf(w, `{"id":%q,"name":"Login"}`, testID)
		case "/api/v1/execution/api/execute_test_id_async":
			backend.record(&backend.runBodies, r)
			_, _ = w.Write([]byte(`{"task_id":"test-task"}`))
		case "/api/v1/workflows/get_workflow_info":
			fmt.Fprintf(w, `{"id":%q,"name":"Smoke"}`, workflowID)
		case "/api/v1/workflow-executions":
			backend.record(&backend.runBodies, r)
			_, _ = w.Write([]byte(`{"task_id":"workflow-task"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	testutil.SetHomeDir(t, t.TempDir())
	workDir := t.TempDir()
	artifact := filepath.Join(workDir, "app.apk")
	if err := os.WriteFile(artifact, []byte("fixture-apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_TELEMETRY_DISABLED", "1")
	t.Setenv("REVYL_NO_UPDATE_NOTIFIER", "1")
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE"} {
		t.Setenv(name, "")
	}
	run := func(t *testing.T, args ...string) (string, string, error) {
		t.Helper()
		backend.reset()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = workDir
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		return stdout.String(), stderr.String(), err
	}
	assertJSONStdout := func(t *testing.T, stdout string) {
		t.Helper()
		var decoded map[string]any
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			t.Fatalf("--json stdout is not one JSON object: %v: %q", err, stdout)
		}
	}
	const (
		setCurrentWarning   = "--set-current is deprecated and will be removed; use --pin to pin the branch to the new build"
		setCurrentOffWarn   = "--set-current=false is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
		noSetCurrentWarning = "--no-set-current is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
	)

	t.Run("upload pin flags", func(t *testing.T) {
		for _, scenario := range []struct {
			name    string
			args    []string
			wantPin bool
			warning string
		}{
			{name: "no flags leaves the pin alone"},
			{name: "pin", args: []string{"--pin"}, wantPin: true},
			{name: "deprecated set-current pins", args: []string{"--set-current"}, wantPin: true, warning: setCurrentWarning},
			{name: "deprecated set-current false", args: []string{"--set-current=false"}, warning: setCurrentOffWarn},
			{name: "deprecated no-set-current", args: []string{"--no-set-current"}, warning: noSetCurrentWarning},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				for _, source := range []string{"file", "url"} {
					args := []string{"build", "upload", "--app", appID, "--version", "v2", "--json"}
					if source == "file" {
						args = append(args, "--file", artifact)
					} else {
						args = append(args, "--url", "https://artifacts.example.com/app.apk")
					}
					stdout, stderr, err := run(t, append(args, scenario.args...)...)
					if err != nil {
						t.Fatalf("%s upload: %v\nstdout: %s\nstderr: %s", source, err, stdout, stderr)
					}
					assertJSONStdout(t, stdout)
					if strings.Contains(stdout, "deprecated") {
						t.Fatalf("warning reached stdout: %s", stdout)
					}
					if scenario.warning != "" && !strings.Contains(stderr, scenario.warning) {
						t.Fatalf("stderr missing %q:\n%s", scenario.warning, stderr)
					}
					if scenario.warning == "" && strings.Contains(stderr, "deprecated") {
						t.Fatalf("unexpected deprecation warning:\n%s", stderr)
					}
					bodies := backend.uploadBodies
					if source == "url" {
						bodies = backend.fromURLBodies
					}
					if len(bodies) != 1 {
						t.Fatalf("%s upload requests = %d, want 1", source, len(bodies))
					}
					if _, sent := bodies[0]["set_as_current"]; sent {
						t.Fatalf("%s upload sent set_as_current: %v", source, bodies[0])
					}
					pin, sent := bodies[0]["pin"]
					if scenario.wantPin != sent || (sent && pin != true) {
						t.Fatalf("%s upload pin = %v (sent %t), want pin only when asked (%t)", source, pin, sent, scenario.wantPin)
					}
				}
			})
		}
	})

	t.Run("upload contradictions fail before uploading", func(t *testing.T) {
		for _, args := range [][]string{{"--pin", "--no-set-current"}, {"--pin", "--set-current=false"}, {"--set-current", "--no-set-current"}} {
			stdout, stderr, err := run(t, append([]string{"build", "upload", "--file", artifact, "--app", appID, "--json"}, args...)...)
			if err == nil || !strings.Contains(stderr, "conflict; use only --pin to pin the branch to the new build") || stdout != "" {
				t.Fatalf("%v: err=%v stdout=%q stderr=%q", args, err, stdout, stderr)
			}
			if len(backend.paths) != 0 {
				t.Fatalf("%v made requests before refusing: %v", args, backend.paths)
			}
		}
	})

	t.Run("upload summary names what runs use", func(t *testing.T) {
		for _, scenario := range []struct {
			args []string
			want string
		}{
			{want: "Branch:          staging (Latest build: runs use this upload)"},
			{args: []string{"--pin"}, want: "Branch:          staging (pinned to this build)"},
		} {
			_, stderr, err := run(t, append([]string{"build", "upload", "--file", artifact, "--app", appID, "--version", "v2"}, scenario.args...)...)
			if err != nil || !strings.Contains(stderr, scenario.want) {
				t.Fatalf("%v: err=%v, stderr missing %q:\n%s", scenario.args, err, scenario.want, stderr)
			}
		}
	})

	t.Run("all branches runs", func(t *testing.T) {
		for _, scenario := range []struct {
			name string
			args []string
		}{
			{name: "test", args: []string{"test", "run", testID, "--all-branches", "--no-wait", "--json"}},
			{name: "test with latest", args: []string{"test", "run", testID, "--all-branches", "--build-selection", "latest", "--no-wait", "--json"}},
			{name: "workflow", args: []string{"workflow", "run", workflowID, "--all-branches", "--no-wait", "--json"}},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				stdout, stderr, err := run(t, scenario.args...)
				if err != nil {
					t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
				}
				assertJSONStdout(t, stdout)
				if len(backend.runBodies) != 1 {
					t.Fatalf("run requests = %d, want 1", len(backend.runBodies))
				}
				body := backend.runBodies[0]
				if body["build_selection"] != "latest_all_branches" {
					t.Fatalf("build_selection = %v, want latest_all_branches", body["build_selection"])
				}
				for _, field := range []string{"branch_name", "branch_id", "context_branch_name", "build_version_id", "pinned_build_id"} {
					if _, sent := body[field]; sent {
						t.Fatalf("all-branches run sent %s: %v", field, body)
					}
				}
			})
		}
	})

	t.Run("all branches conflicts", func(t *testing.T) {
		for _, scenario := range []struct {
			args []string
			want string
		}{
			{args: []string{"test", "run", testID, "--all-branches", "--branch", "main"}, want: "--all-branches cannot be combined with --branch"},
			{args: []string{"test", "run", testID, "--all-branches", "--build-id", buildID}, want: "--all-branches cannot be combined with --build-id"},
			{args: []string{"test", "run", testID, "--all-branches", "-b", buildID}, want: "--all-branches cannot be combined with --build-id"},
			{args: []string{"test", "run", testID, "--all-branches", "--build"}, want: "--all-branches cannot be combined with --build"},
			{args: []string{"test", "run", testID, "--all-branches", "--build-selection", "inherit"}, want: "--all-branches cannot be combined with --build-selection inherit"},
			{args: []string{"workflow", "run", workflowID, "--all-branches", "--branch", "main"}, want: "--all-branches cannot be combined with --branch"},
			{args: []string{"workflow", "run", workflowID, "--all-branches", "--ios-build-id", buildID}, want: "--all-branches cannot be combined with --ios-build-id"},
			{args: []string{"device", "start", "--app-id", appID, "--all-branches", "--branch", "main"}, want: "--all-branches cannot be combined with --branch"},
			{args: []string{"device", "start", "--app-id", appID, "--all-branches", "--build-selection", "pinned"}, want: "--all-branches cannot be combined with --build-selection pinned"},
			{args: []string{"explore", "run", appID, "--all-branches", "--build-id", buildID}, want: "--all-branches cannot be combined with --build-id"},
		} {
			stdout, stderr, err := run(t, append(scenario.args, "--json")...)
			if err == nil || !strings.Contains(stderr, scenario.want) || stdout != "" {
				t.Fatalf("%v: err=%v stdout=%q, stderr missing %q:\n%s", scenario.args, err, stdout, scenario.want, stderr)
			}
			if len(backend.runBodies) != 0 {
				t.Fatalf("%v started a run: %v", scenario.args, backend.runBodies)
			}
		}
	})
}
