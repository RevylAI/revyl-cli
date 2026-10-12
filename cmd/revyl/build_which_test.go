package main

import (
	"bytes"
	"context"
	"encoding/json"
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
		appID    = "11111111-1111-4111-8111-111111111111"
		branchID = "22222222-2222-4222-8222-222222222222"
		buildID  = "33333333-3333-4333-8333-333333333333"
	)
	for _, scenario := range []struct {
		name, gitBranch, explicitBranch, resolvedBranch, source string
		status, serverError, wantError                          string
		pinned, pinChanged, appByName                           bool
		human, globalJSON, chdir, rejectBeforeAPI, telemetry    bool
	}{
		{name: "default latest", resolvedBranch: "base", source: "default_branch", telemetry: true},
		{name: "default pinned", resolvedBranch: "base", source: "default_branch", pinned: true},
		{name: "feature checkout uses default latest", gitBranch: "feature/login", resolvedBranch: "base", source: "default_branch"},
		{name: "feature checkout uses default pin", gitBranch: "feature/login", resolvedBranch: "base", source: "default_branch", pinned: true},
		{name: "explicit overrides git", gitBranch: "feature/login", explicitBranch: "release", resolvedBranch: "release", source: "explicit_branch"},
		{name: "literal HEAD", gitBranch: "feature/login", explicitBranch: "HEAD", resolvedBranch: "HEAD", source: "explicit_branch"},
		{name: "unrelated checkout uses default", gitBranch: "missing", resolvedBranch: "base", source: "default_branch"},
		{name: "unrelated checkout uses default pin", gitBranch: "empty", resolvedBranch: "base", source: "default_branch", pinned: true},
		{name: "unsupported Git name does not affect selection", gitBranch: strings.Repeat("long/", 60) + "feature", resolvedBranch: "base", source: "default_branch"},
		{name: "explicit empty branch", explicitBranch: "empty", status: "branch_has_no_builds", wantError: "selected branch has no builds"},
		{name: "no compatible default", status: "no_compatible_build", wantError: "selected branch has no compatible build"},
		{name: "explicit no compatible build", explicitBranch: "preview", status: "no_compatible_build", wantError: "selected branch has no compatible build"},
		{name: "explicit missing branch", explicitBranch: "missing", serverError: "selected branch was not found", wantError: "selected branch was not found"},
		{name: "default unavailable pin", serverError: "pinned build is unavailable", wantError: "pinned build is unavailable", telemetry: true},
		{name: "feature checkout default pin unavailable", gitBranch: "feature/login", serverError: "pinned build is unavailable", wantError: "pinned build is unavailable"},
		{name: "explicit unsupported branch", explicitBranch: strings.Repeat("x", 256), wantError: "branch name must be", rejectBeforeAPI: true},
		{name: "blank explicit branch", explicitBranch: " ", wantError: "--branch requires a Revyl branch name", rejectBeforeAPI: true},
		{name: "current branch setting", resolvedBranch: "base", source: "default_branch", pinned: true, pinChanged: true},
		{name: "app name", resolvedBranch: "base", source: "default_branch", appByName: true},
		{name: "global json", resolvedBranch: "base", source: "default_branch", globalJSON: true},
		{name: "chdir feature checkout uses default", gitBranch: "feature/login", resolvedBranch: "base", source: "default_branch", chdir: true},
		{name: "human default pin", gitBranch: "missing", resolvedBranch: "base", source: "default_branch", pinned: true, human: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			workDir := t.TempDir()
			if scenario.gitBranch != "" {
				gitInitBuildRepository(t, workDir)
				if output, err := exec.Command("git", "-C", workDir, "symbolic-ref", "HEAD", "refs/heads/"+scenario.gitBranch).CombinedOutput(); err != nil {
					t.Fatalf("set Git branch: %v: %s", err, output)
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
						_ = json.NewEncoder(w).Encode(map[string]string{"resolution_status": scenario.status})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"version_id": buildID, "branch_id": branchID, "branch_name": scenario.resolvedBranch, "resolution_status": "resolved"})
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
						for _, privateValue := range []string{appID, buildID, "My App", "private metadata"} {
							if bytes.Contains(encoded, []byte(privateValue)) {
								t.Errorf("telemetry contains app/build data: %s", encoded)
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
				if strings.Contains(stderr.String(), "using app default") {
					t.Fatalf("failed selection reported fallback: %s", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("CLI: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
			if scenario.human {
				for _, text := range []string{"Branch: base", "Reason: app default branch", "Build: 1.2.3 (" + buildID + ")", "Default build: Exact build"} {
					if !strings.Contains(stdout.String(), text) {
						t.Errorf("human output missing %q: %s", text, &stdout)
					}
				}
				return
			}
			want := map[string]any{
				"app_id": appID, "app_name": "My App", "branch_id": branchID, "branch_name": scenario.resolvedBranch,
				"branch_source": scenario.source,
				"build_id":      buildID, "build_version": "1.2.3", "branch_build_selection": "latest",
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
		{name: "upload changed the default build", defaultBuildChanged: true, want: "base (default build is now v1.3)"},
		{name: "latest build is this upload", runBuildID: uploadID, want: "base (Latest build: runs use this upload)", wantLookups: true},
		{name: "exact default build stays", runBuildID: otherBuild, pinned: true, want: "base (default build stays v1.2; runs won't use this upload)", wantLookups: true},
		{name: "latest build resolves elsewhere", runBuildID: otherBuild, want: "base (Latest build: runs use v1.2)", wantLookups: true},
		{name: "reused upload is already the default build", runBuildID: uploadID, pinned: true, want: "base (default build is now v1.3)", wantLookups: true},
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
