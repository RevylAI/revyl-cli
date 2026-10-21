package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func TestBuildWhichCLI(t *testing.T) {
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
		appID               = "11111111-1111-4111-8111-111111111111"
		branchID            = "22222222-2222-4222-8222-222222222222"
		buildID             = "33333333-3333-4333-8333-333333333333"
		unavailablePinError = `Branch "release/customer-private" on "My App" is pinned to an unavailable build; pin another build or unpin the branch`
	)
	for _, scenario := range []struct {
		name, gitBranch, explicitBranch, resolvedBranch, source string
		status, serverError, wantError                          string
		fallback, pinned, pinChanged, appByName                 bool
		human, globalJSON, chdir, rejectBeforeAPI, telemetry    bool
		disabled                                                bool
	}{
		{name: "default latest", resolvedBranch: "base", source: "default_branch", telemetry: true},
		{name: "default pinned", resolvedBranch: "base", source: "default_branch", pinned: true},
		{name: "automatic disabled", gitBranch: "feature/login", resolvedBranch: "base", source: "default_branch", disabled: true},
		{name: "disabled explicit", gitBranch: "feature/login", explicitBranch: "release", resolvedBranch: "release", source: "explicit_branch", disabled: true},
		{name: "git latest", gitBranch: "feature/login", resolvedBranch: "feature/login", source: "git_branch"},
		{name: "git pinned", gitBranch: "feature/login", resolvedBranch: "feature/login", source: "git_branch", pinned: true},
		{name: "explicit overrides git", gitBranch: "feature/login", explicitBranch: "release", resolvedBranch: "release", source: "explicit_branch"},
		{name: "literal HEAD", gitBranch: "feature/login", explicitBranch: "HEAD", resolvedBranch: "HEAD", source: "explicit_branch"},
		{name: "missing git fallback", gitBranch: "missing", resolvedBranch: "base", source: "default_branch", fallback: true},
		{name: "empty git fallback to pinned", gitBranch: "empty", resolvedBranch: "base", source: "default_branch", fallback: true, pinned: true},
		{name: "unsupported git fallback", gitBranch: strings.Repeat("long/", 60) + "feature", resolvedBranch: "base", source: "default_branch", fallback: true},
		{name: "explicit empty branch", explicitBranch: "empty", status: "branch_has_no_builds", wantError: `branch "empty" on "My App" has no builds; upload a build to it first`, telemetry: true},
		{name: "no compatible default", status: "no_compatible_build", wantError: `branch "base" on "My App" has no usable build`},
		{name: "explicit no compatible build", explicitBranch: "preview", status: "no_compatible_build", wantError: `branch "preview" on "My App" has no usable build`},
		{name: "explicit missing branch", explicitBranch: "missing", serverError: "selected branch was not found", wantError: "selected branch was not found"},
		{name: "default unavailable pin", serverError: unavailablePinError, wantError: unavailablePinError, telemetry: true},
		{name: "git unavailable pin", gitBranch: "feature/login", serverError: unavailablePinError, wantError: unavailablePinError},
		{name: "explicit unsupported branch", explicitBranch: strings.Repeat("x", 256), wantError: "branch name must be", rejectBeforeAPI: true},
		{name: "blank explicit branch", explicitBranch: " ", wantError: "--branch requires a Revyl branch name", rejectBeforeAPI: true},
		{name: "current branch setting", resolvedBranch: "base", source: "default_branch", pinned: true, pinChanged: true},
		{name: "app name", resolvedBranch: "base", source: "default_branch", appByName: true},
		{name: "global json", resolvedBranch: "base", source: "default_branch", globalJSON: true},
		{name: "chdir git", gitBranch: "feature/login", resolvedBranch: "feature/login", source: "git_branch", chdir: true},
		{name: "human pinned fallback", gitBranch: "missing", resolvedBranch: "base", source: "default_branch", fallback: true, pinned: true, human: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			workDir := t.TempDir()
			if scenario.gitBranch != "" {
				gitInitBuildRepository(t, workDir)
				gitContext, cancelGit := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelGit()
				// The oversized Revyl branch name must still be a readable Git ref on Windows.
				if output, err := exec.CommandContext(gitContext, "git", "-C", workDir, "config", "--local", "core.longpaths", "true").CombinedOutput(); err != nil {
					t.Fatalf("enable long Git fixture paths: %v: %s", err, output)
				}
				branchRef := "refs/heads/" + scenario.gitBranch
				if output, err := exec.CommandContext(gitContext, "git", "-C", workDir, "symbolic-ref", "HEAD", branchRef).CombinedOutput(); err != nil {
					t.Fatalf("set Git branch: %v: %s", err, output)
				}
				output, err := exec.CommandContext(gitContext, "git", "-C", workDir, "symbolic-ref", "--quiet", "HEAD").CombinedOutput()
				if err != nil || strings.TrimRight(string(output), "\r\n") != branchRef {
					t.Fatalf("read Git fixture branch: %v: got %q, want %q", err, output, branchRef)
				}
			}
			var requests, resolutions atomic.Int32
			receivedEvents := make(chan analytics.TelemetryEvent, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/telemetry/cli-analytics" && scenario.telemetry {
					var payload analytics.TelemetryPayload
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					for _, event := range payload.Events {
						receivedEvents <- event
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet && !(r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/resolve") {
					t.Errorf("unexpected write: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected write", http.StatusBadRequest)
					return
				}
				switch r.URL.Path {
				case "/api/v1/apps/" + appID:
					_ = json.NewEncoder(w).Encode(api.App{ID: appID, Name: "My App", DefaultBranchID: branchID})
				case "/api/v1/apps/":
					if !scenario.appByName || r.URL.Query().Get("search") != "My App" {
						t.Errorf("unexpected app search: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []api.App{{ID: appID, Name: "My App", DefaultBranchID: branchID}}})
				case "/api/v1/apps/resolve":
					resolutions.Add(1)
					if r.URL.Query().Get("include_download_url") != "false" {
						t.Errorf("inspection requested a signed URL: %s", r.URL)
					}
					var request map[string]string
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					want := map[string]string{"build_var_id": appID}
					if scenario.explicitBranch != "" {
						want["branch_name"] = scenario.explicitBranch
					} else if scenario.gitBranch != "" {
						want["git_branch_name"] = scenario.gitBranch
					}
					if !reflect.DeepEqual(request, want) {
						t.Errorf("selection request = %#v, want %#v", request, want)
					}
					if scenario.serverError != "" {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"detail": scenario.serverError})
						return
					}
					if scenario.status != "" {
						_ = json.NewEncoder(w).Encode(map[string]string{"resolution_status": scenario.status, "branch_name": cmp.Or(scenario.explicitBranch, "base"), "app_name": "My App"})
						return
					}
					response := map[string]any{"version_id": buildID, "branch_id": branchID, "branch_name": scenario.resolvedBranch, "resolution_status": "resolved", "branch_source": scenario.source}
					if scenario.disabled {
						response["auto_git_branch_enabled"] = false
					} else if scenario.gitBranch != "" && scenario.explicitBranch == "" {
						response["git_branch_name"] = scenario.gitBranch
					}
					_ = json.NewEncoder(w).Encode(response)
				case "/api/v1/apps/builds/" + buildID:
					if r.URL.Query().Get("include_download_url") != "false" {
						t.Errorf("detail requested a signed URL: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(api.BuildVersionDetail{ID: buildID, AppID: appID, Version: "1.2.3", Metadata: map[string]interface{}{"must_not_print": "private metadata"}})
				case "/api/v1/apps/" + appID + "/branches/" + branchID:
					branch := api.BuildBranch{ID: branchID, AppID: appID, Name: scenario.resolvedBranch}
					if scenario.pinned {
						pin := buildID
						if scenario.pinChanged {
							pin = "44444444-4444-4444-8444-444444444444"
						}
						branch.PinnedBuildID = &pin
					}
					_ = json.NewEncoder(w).Encode(branch)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_TELEMETRY_DISABLED", "1")
			if scenario.telemetry {
				t.Setenv("REVYL_TELEMETRY_DISABLED", "0")
				t.Setenv("DO_NOT_TRACK", "0")
			}
			t.Setenv("REVYL_NO_UPDATE_NOTIFIER", "1")
			app := appID
			if scenario.appByName {
				app = "My App"
			}
			args := []string{"build", "which", "--app", app}
			if scenario.explicitBranch != "" {
				args = append(args, "--branch", scenario.explicitBranch)
			}
			if scenario.globalJSON {
				args = append([]string{"--json"}, args...)
			} else if !scenario.human {
				args = append(args, "--json")
			}
			commandDir := workDir
			if scenario.chdir {
				commandDir = t.TempDir()
				args = append([]string{"-C", workDir}, args...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = commandDir
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if scenario.telemetry {
				observed := map[string]int{}
				for range 2 {
					select {
					case event := <-receivedEvents:
						if event.Properties["command"] != "revyl build which" {
							t.Errorf("telemetry command = %v", event.Properties["command"])
						}
						encoded, encodeErr := json.Marshal(event)
						if encodeErr != nil {
							t.Fatal(encodeErr)
						}
						for _, privateValue := range []string{appID, buildID, "My App", "release/customer-private", "private metadata"} {
							if bytes.Contains(encoded, []byte(privateValue)) {
								t.Errorf("telemetry contains app/build data: %s", encoded)
							}
						}
						if event.Event == analytics.CliCommandFailedEvent {
							if _, exists := event.Properties["output_tail"]; exists {
								t.Errorf("branch failure retained output tail: %#v", event.Properties["output_tail"])
							}
							if scenario.serverError != "" {
								if got := event.Properties["error_message"]; got != "API request failed (HTTP 400)" {
									t.Errorf("error_message = %q, want bounded HTTP diagnostic", got)
								}
								if got := event.Properties["failure_class"]; got != "usage" {
									t.Errorf("failure_class = %q, want usage", got)
								}
							}
						}
						observed[event.Event]++
					case <-time.After(5 * time.Second):
						t.Fatal("missing centralized CLI lifecycle event")
					}
				}
				terminal := analytics.CliCommandCompletedEvent
				if scenario.wantError != "" {
					terminal = analytics.CliCommandFailedEvent
				}
				if observed[analytics.CliCommandStartedEvent] != 1 || observed[terminal] != 1 {
					t.Errorf("lifecycle events = %v", observed)
				}
			}
			if scenario.rejectBeforeAPI {
				if requests.Load() != 0 {
					t.Fatalf("invalid selection made %d API requests", requests.Load())
				}
			} else if resolutions.Load() != 1 {
				t.Errorf("resolution requests = %d, want exactly one", resolutions.Load())
			}
			if scenario.wantError != "" {
				if err == nil || !strings.Contains(stderr.String(), scenario.wantError) || stdout.Len() != 0 {
					t.Fatalf("err=%v stdout=%q stderr=%q, want error %q and empty stdout", err, stdout.String(), stderr.String(), scenario.wantError)
				}
				if strings.Contains(stderr.String(), "using the app's default branch") {
					t.Fatalf("failed selection reported fallback: %s", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("CLI: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if scenario.fallback {
				if !strings.Contains(stderr.String(), fmt.Sprintf("Git branch %q has no usable build on \"My App\"; using the app's default branch %q.", scenario.gitBranch, scenario.resolvedBranch)) {
					t.Fatalf("missing fallback notice: %q", stderr.String())
				}
			} else if scenario.disabled && scenario.explicitBranch == "" {
				if !strings.Contains(stderr.String(), "automatic Git branch detection is off") || strings.Contains(stderr.String(), "no usable build") {
					t.Fatalf("wrong disabled notice: %s", &stderr)
				}
			} else if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
			if scenario.human {
				for _, text := range []string{"Branch: base", "Reason: app default branch; Git branch has no usable build", "Build: 1.2.3 (" + buildID + ")", "Default build: Pinned build"} {
					if !strings.Contains(stdout.String(), text) {
						t.Errorf("human output missing %q: %s", text, &stdout)
					}
				}
				return
			}
			want := map[string]any{
				"app_id": appID, "app_name": "My App", "branch_id": branchID, "branch_name": scenario.resolvedBranch,
				"branch_source": scenario.source, "build_id": buildID, "build_version": "1.2.3", "branch_build_selection": "latest",
			}
			if scenario.gitBranch != "" && scenario.explicitBranch == "" && !scenario.disabled {
				want["git_branch_name"] = scenario.gitBranch
			}
			if scenario.disabled {
				want["auto_git_branch_enabled"] = false
			}
			if scenario.pinned {
				want["branch_build_selection"] = "pinned"
			}
			var result map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("stdout is not one JSON object: %v: %s", err, &stdout)
			}
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("result = %#v, want %#v", result, want)
			}
			if _, err := os.Stat(filepath.Join(workDir, ".revyl")); !os.IsNotExist(err) {
				t.Fatalf("inspection created project state: %v", err)
			}
		})
	}
}

func TestUploadedBuildBranchSummarySaysWhichBuildRuns(t *testing.T) {
	const (
		appID       = "11111111-1111-4111-8111-111111111111"
		branchID    = "22222222-2222-4222-8222-222222222222"
		uploadID    = "33333333-3333-4333-8333-333333333333"
		otherBuild  = "44444444-4444-4444-8444-444444444444"
		otherLabel  = "v1.2"
		uploadLabel = "v1.3"
	)
	for _, scenario := range []struct {
		name                string
		defaultBuildChanged bool
		runBuildID          string
		pinned              bool
		want                string
		wantLookups         bool
	}{
		{name: "--pin pinned the branch to this upload", defaultBuildChanged: true, want: "base (pinned to this build)"},
		{name: "latest build is this upload", runBuildID: uploadID, want: "base (Latest build: runs use this upload)", wantLookups: true},
		{name: "branch stays pinned to another build", runBuildID: otherBuild, pinned: true, want: "base (pinned to v1.2; runs that don't choose a build keep using v1.2)", wantLookups: true},
		{name: "latest build resolves elsewhere", runBuildID: otherBuild, want: "base (Latest build: runs use v1.2)", wantLookups: true},
		{name: "branch already pinned to this upload", runBuildID: uploadID, pinned: true, want: "base (pinned to this build)", wantLookups: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var lookups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lookups.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					var request map[string]string
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if want := map[string]string{"build_var_id": appID, "branch_id": branchID}; !reflect.DeepEqual(request, want) {
						t.Errorf("resolution request = %#v, want %#v", request, want)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"version_id": scenario.runBuildID, "branch_id": branchID, "branch_name": "base", "resolution_status": "resolved"})
				case "/api/v1/apps/builds/" + uploadID:
					_ = json.NewEncoder(w).Encode(api.BuildVersionDetail{ID: uploadID, AppID: appID, Version: uploadLabel})
				case "/api/v1/apps/builds/" + otherBuild:
					_ = json.NewEncoder(w).Encode(api.BuildVersionDetail{ID: otherBuild, AppID: appID, Version: otherLabel})
				case "/api/v1/apps/" + appID + "/branches/" + branchID:
					branch := api.BuildBranch{ID: branchID, AppID: appID, Name: "base"}
					if scenario.pinned {
						pin := scenario.runBuildID
						branch.PinnedBuildID = &pin
					}
					_ = json.NewEncoder(w).Encode(branch)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			summary, err := uploadedBuildBranchSummary(context.Background(), api.NewClientWithBaseURL("key", server.URL), uploadedBuild{
				AppID: appID, BranchID: branchID, BranchName: "base", BuildID: uploadID, BuildVersion: uploadLabel,
				DefaultBuildChanged: scenario.defaultBuildChanged,
			})
			if err != nil || summary != scenario.want {
				t.Fatalf("summary = %q, %v; want %q", summary, err, scenario.want)
			}
			if (lookups.Load() > 0) != scenario.wantLookups {
				t.Fatalf("lookups = %d, want lookups %t", lookups.Load(), scenario.wantLookups)
			}
		})
	}
}

func TestUploadedBuildBranchWarnsWithoutFailingWhenTheCheckFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"Branch resolution is unavailable"}`))
	}))
	defer server.Close()
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		printUploadedBuildBranch(context.Background(), api.NewClientWithBaseURL("key", server.URL), "Branch:          %s", uploadedBuild{
			AppID: "app", BranchName: "base", BuildID: "build", BuildVersion: "v1.3",
		})
	})
	if stdout != "" {
		t.Fatalf("branch line went to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "Branch:          base\n") {
		t.Fatalf("missing plain branch line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Couldn't check which build runs on base: could not select the app's build: Branch resolution is unavailable") {
		t.Fatalf("missing lookup warning:\n%s", stderr)
	}
}

func TestUploadedBuildBranchSkipsTheCheckWhenOutputIsQuiet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("quiet output looked up the branch: %s", r.URL)
		http.NotFound(w, r)
	}))
	defer server.Close()
	ui.SetQuietMode(true)
	t.Cleanup(func() { ui.SetQuietMode(false) })
	_, stderr := captureStdoutAndStderrSeparate(t, func() {
		printUploadedBuildBranch(context.Background(), api.NewClientWithBaseURL("key", server.URL), "Branch: %s", uploadedBuild{
			AppID: "app", BranchName: "base", BuildID: "build", BuildVersion: "v1.3",
		})
	})
	if stderr != "" {
		t.Fatalf("quiet output printed: %q", stderr)
	}
}
