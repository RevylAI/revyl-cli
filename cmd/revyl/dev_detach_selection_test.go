package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devloop"
)

type detachedDevSelectionResult struct {
	BranchID            string
	UploadBranchID      string
	AppDefault          bool
	NativeNeedsBuild    bool
	HotReloadNeedsBuild bool
	Error               string
	NoBuildError        string
}

func TestDetachedDevSelectionChild(t *testing.T) {
	resultPath := os.Getenv("REVYL_TEST_DEV_DETACH_RESULT")
	if resultPath == "" {
		return
	}
	result := detachedDevSelectionResult{}
	defer func() {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resultPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}()
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		t.Fatal("missing child arguments")
	}
	cmd := newLeafCommand("dev", runDevStart)
	registerDevStartFlags(cmd)
	if err := cmd.ParseFlags(os.Args[separator+1:]); err != nil {
		t.Fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := resolveDevInvocation(root, "", devStartProfile, devStartPlatform, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	mode := devStartBuildSelection
	if devStartSeedLatest {
		mode = string(api.BuildSelectionModeLatest)
	}
	client := api.NewClientWithBaseURL("test", os.Getenv("REVYL_BACKEND_URL"))
	invocation, err = resolveDevDestination(cmd.Context(), client, invocation, devStartBranchID, devStartBranch, devStartBuildVerID, mode, !devStartNoBuild)
	if err != nil {
		result.Error = err.Error()
		return
	}
	result.BranchID = invocation.BranchID
	result.UploadBranchID = projectBuildInvocationFromDev(invocation).BranchID
	if devStartNoBuild {
		if err := runDevStart(cmd, nil); err != nil {
			result.NoBuildError = err.Error()
		}
	}
	// Selection provenance must outlive the parent's readiness handshake.
	if err := os.Unsetenv(devDetachedAppDefaultEnv); err != nil {
		t.Fatal(err)
	}
	result.AppDefault = devSelectionIsAppDefault(invocation)
	_, resolveErr := client.ResolveBuildSelection(cmd.Context(), invocation.AppID, devBuildSelection(invocation))
	result.NativeNeedsBuild = devResolveErrorNeedsBuild(invocation, resolveErr)
	selected, _, _, selectErr := selectDevStartBuild(cmd.Context(), client, invocation.AppID, invocation)
	result.HotReloadNeedsBuild = selectErr == nil && selected == nil
	if selectErr != nil {
		result.Error = selectErr.Error()
	}
}

func TestProjectDetachPreservesBuildSelectionPolicy(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, test := range []struct {
		name, framework, gitBranch, status, wantMode     string
		flags                                            []string
		detachedHEAD, appDefault, needsBuild, exactBuild bool
	}{
		{name: "detached HEAD automatic", detachedHEAD: true, appDefault: true, needsBuild: true, wantMode: "latest"},
		{name: "empty inherited context automatic", appDefault: true, needsBuild: true, wantMode: "latest"},
		{name: "automatic empty branch", appDefault: true, needsBuild: true, wantMode: "latest", status: "branch_has_no_builds"},
		{name: "automatic native no build", flags: []string{"--no-build"}, appDefault: true, needsBuild: true, wantMode: "latest"},
		{name: "automatic hot reload no build", framework: "expo", flags: []string{"--no-build", "--tunnel", "https://example.test"}, appDefault: true, needsBuild: true, wantMode: "latest"},
		{name: "explicit default branch name", flags: []string{"--branch", "base"}},
		{name: "explicit default branch ID", flags: []string{"--branch-id", branchID}},
		{name: "explicit latest on default branch", flags: []string{"--branch-id", branchID, "--build-selection", "latest"}, wantMode: "latest"},
		{name: "explicit inherit", flags: []string{"--build-selection=inherit"}, wantMode: "inherit"},
		{name: "explicit latest", flags: []string{"--build-selection=latest"}, wantMode: "latest"},
		{name: "explicit seed latest", flags: []string{"--seed-latest"}, wantMode: "latest"},
		{name: "false seed stays automatic", flags: []string{"--seed-latest=false"}, appDefault: true, needsBuild: true, wantMode: "latest"},
		{name: "exact build", flags: []string{"--build-version-id", buildID}, exactBuild: true, wantMode: "pinned"},
		{name: "explicit pin", flags: []string{"--build-selection=pinned", "--build-version-id", buildID}, exactBuild: true, wantMode: "pinned"},
		{name: "inferred branch", gitBranch: "feature/original"},
		{name: "inferred read fallback", gitBranch: "feature/missing", flags: []string{"--no-build"}},
		{name: "explicit empty branch still builds", flags: []string{"--branch", "base"}, needsBuild: true, status: "branch_has_no_builds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			previousProfile, previousPlatform, previousNoBuild := devStartProfile, devStartPlatform, devStartNoBuild
			t.Cleanup(func() {
				devStartProfile, devStartPlatform, devStartNoBuild = previousProfile, previousPlatform, previousNoBuild
			})
			t.Setenv("CI", "")
			t.Setenv("GITHUB_ACTIONS", "")
			t.Setenv("BUILDKITE", "")
			t.Setenv("REVYL_API_KEY", "test")
			t.Setenv(devDetachedEnv, "")
			t.Setenv(devDetachedAppDefaultEnv, fmt.Sprintf("%d:%s", os.Getppid(), branchID))
			t.Setenv(devloop.GitBranchEnvironment, test.gitBranch)
			root := initDevRepository(t)
			setDevTestBranch(t, root, "feature/local")
			if test.detachedHEAD {
				for _, args := range [][]string{
					{"-c", "user.name=Test", "-c", "user.email=test@example.test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture"},
					{"checkout", "--detach"},
				} {
					command := exec.Command("git", args...)
					command.Dir = root
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("prepare detached HEAD: %v: %s", err, output)
					}
				}
				if err := os.Unsetenv(devloop.GitBranchEnvironment); err != nil {
					t.Fatal(err)
				}
			}
			framework := test.framework
			if framework == "" {
				framework = "ios"
			}
			writeProjectDevConfig(t, root, fmt.Sprintf("project:\n  id: %s\nbuild:\n  framework: %s\n  profiles:\n    development:\n      ios:\n        app_id: %s\n        build_commands: [must-not-run]\n", devProjectID, framework, appID))
			var childStarted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + appID:
					if childStarted.Load() {
						t.Error("child reselected the changed app default")
						_, _ = fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, appID, buildID)
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, appID, branchID)
				case "/api/v1/apps/" + appID + "/branches":
					if childStarted.Load() {
						t.Error("child reselected a branch by name")
					}
					_, _ = fmt.Fprintf(w, `{"branches":[{"id":%q,"app_id":%q,"name":%q}],"total":1}`, branchID, appID, r.URL.Query().Get("name"))
				case "/api/v1/apps/" + appID + "/branches/" + branchID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"renamed"}`, branchID, appID)
				case "/api/v1/apps/resolve":
					var request map[string]string
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request["git_branch_name"] != "" {
						if childStarted.Load() || request["git_branch_name"] != test.gitBranch {
							t.Errorf("unexpected inferred context: %#v", request)
						}
						_, _ = fmt.Fprintf(w, `{"branch_id":%q,"branch_name":"base","branch_source":"default_branch"}`, branchID)
						return
					}
					if childStarted.Load() || !test.exactBuild {
						if request["branch_id"] != branchID || request["build_selection"] != test.wantMode {
							t.Errorf("selection lost frozen scope or mode: %#v", request)
						}
					}
					if test.exactBuild {
						if childStarted.Load() {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"detail":"pinned build is unavailable"}`))
						} else {
							_, _ = fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"base","download_url":"https://example.test/app.zip"}`, buildID, branchID)
						}
						return
					}
					status := test.status
					if status == "" {
						status = "no_compatible_build"
					}
					_, _ = fmt.Fprintf(w, `{"branch_id":%q,"resolution_status":%q}`, branchID, status)
				case "/api/v1/apps/builds/" + buildID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"1"}`, buildID, appID)
				default:
					t.Errorf("unexpected upload, device, or fallback request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			parentCommand := newLeafCommand("dev", runDevStart)
			parentCommand.Flags().StringVar(&devStartBranch, "branch", "", "")
			parentCommand.Flags().StringVar(&devStartBranchID, "branch-id", "", "")
			parentCommand.Flags().StringVar(&devStartBuildSelection, "build-selection", "", "")
			parentCommand.Flags().StringVar(&devStartBuildVerID, "build-version-id", "", "")
			parentCommand.Flags().BoolVar(&devStartSeedLatest, "seed-latest", false, "")
			parentCommand.Flags().BoolVar(&devStartNoBuild, "no-build", false, "")
			parentCommand.Flags().String("tunnel", "", "")
			if err := parentCommand.ParseFlags(test.flags); err != nil {
				t.Fatal(err)
			}
			invocation, err := resolveDevInvocation(root, "", "", "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			mode := devStartBuildSelection
			if devStartSeedLatest {
				mode = "latest"
			}
			client := api.NewClientWithBaseURL("test", server.URL)
			invocation, err = resolveDevDestination(context.Background(), client, invocation, devStartBranchID, devStartBranch, devStartBuildVerID, mode, !devStartNoBuild)
			if err != nil {
				t.Fatal(err)
			}
			if devSelectionIsAppDefault(invocation) != test.appDefault {
				t.Fatalf("unexpected parent provenance: %+v", invocation)
			}
			if !test.exactBuild {
				selected, _, _, err := selectDevStartBuild(context.Background(), client, appID, invocation)
				if (selected == nil && err == nil) != test.needsBuild {
					t.Fatalf("unexpected foreground policy: selected=%+v err=%v", selected, err)
				}
			}
			setDevTestBranch(t, root, "feature/changed")
			t.Setenv(devloop.GitBranchEnvironment, "feature/changed")
			resultPath := filepath.Join(t.TempDir(), "result.json")
			t.Setenv("REVYL_TEST_DEV_DETACH_RESULT", resultPath)
			childArgs := projectDetachArgs(append([]string{"dev", "--detach"}, test.flags...), invocation)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestDetachedDevSelectionChild$", "--"}, childArgs...)...)
			child.Dir = root
			child.Env = devDetachEnvironment(invocation)
			markerCount := 0
			for _, entry := range child.Env {
				if value, ok := strings.CutPrefix(entry, devDetachedAppDefaultEnv+"="); ok {
					markerCount++
					if (value != "") != test.appDefault {
						t.Fatalf("stale provenance survived explicit selection: %q", value)
					}
				}
			}
			if markerCount != 1 {
				t.Fatalf("provenance entries = %d, want one", markerCount)
			}
			childStarted.Store(true)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("child failed: %v\n%s", err, output)
			}
			data, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatal(err)
			}
			var result detachedDevSelectionResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if test.exactBuild {
				if !strings.Contains(result.Error, "pinned build is unavailable") {
					t.Fatalf("exact selection must fail: %+v", result)
				}
				return
			}
			if result.BranchID != branchID || result.UploadBranchID != branchID || result.AppDefault != test.appDefault ||
				result.NativeNeedsBuild != test.needsBuild || result.HotReloadNeedsBuild != test.needsBuild {
				t.Fatalf("child changed the foreground policy or destination: %+v", result)
			}
			if !test.needsBuild && result.Error == "" {
				t.Fatalf("explicit or inferred incompatible selection must fail: %+v", result)
			}
			if devStartNoBuild && test.appDefault && !strings.Contains(result.NoBuildError, "no existing build found and --no-build is active") {
				t.Fatalf("no-build must stop before building or device startup: %+v", result)
			}
		})
	}
}

func TestDetachedDevAppDefaultRejectsUnrelatedHandoffs(t *testing.T) {
	resetDevSelectionFlags(t)
	const branchID = "22222222-2222-4222-8222-222222222222"
	t.Setenv(devDetachedEnv, "1")
	t.Setenv(devDetachedAppDefaultEnv, fmt.Sprintf("%d:%s", os.Getppid(), branchID))
	if !isDetachedDevAppDefault(branchID, "", "", "latest") {
		t.Fatal("matching private handoff was rejected")
	}
	for _, test := range []struct{ branchID, branchName, buildID, mode string }{
		{"different-branch", "", "", "latest"},
		{branchID, "base", "", "latest"},
		{branchID, "", "build-id", "latest"},
		{branchID, "", "", "inherit"},
	} {
		if isDetachedDevAppDefault(test.branchID, test.branchName, test.buildID, test.mode) {
			t.Fatalf("accepted unrelated selection: %+v", test)
		}
	}
	devStartSeedLatest = true
	if isDetachedDevAppDefault(branchID, "", "", "latest") {
		t.Fatal("explicit seed must not use automatic provenance")
	}
	devStartSeedLatest = false
	t.Setenv(devDetachedEnv, "")
	if isDetachedDevAppDefault(branchID, "", "", "latest") {
		t.Fatal("foreground invocation must not use detached provenance")
	}
	t.Setenv(devDetachedEnv, "1")
	t.Setenv(devDetachedAppDefaultEnv, fmt.Sprintf("%d:%s", os.Getpid(), branchID))
	if isDetachedDevAppDefault(branchID, "", "", "latest") {
		t.Fatal("handoff from another generation must not relax explicit selection")
	}
}
