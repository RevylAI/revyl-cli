package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devloop"
)

func TestDevLeavesANewInferredBranchToItsFirstUploadThenFreezesIt(t *testing.T) {
	resetDevSelectionFlags(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	root := initDevRepository(t)
	setDevTestBranch(t, root, "feature/fresh")
	lookups := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/apps/"+appID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"auto_git_branch_enabled":true}`, appID)
		case r.URL.Path == "/api/v1/apps/"+appID+"/branches" && r.Method == http.MethodGet:
			lookups++
			if r.URL.Query().Get("name") != "feature/fresh" {
				t.Errorf("branch query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"branches":[],"total":0}`))
		case r.URL.Path == "/api/v1/apps/"+appID+"/branches/"+branchID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"feature/fresh"}`, branchID, appID)
		default:
			t.Errorf("unexpected request, such as creating the branch before its upload: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("test", server.URL)
	initial, err := resolveDevDestination(context.Background(), client, projectDevInvocation{AppID: appID, ProjectRoot: root}, "", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if initial.BranchID != "" || initial.GitBranchName != "feature/fresh" || !initial.branchAwaitsFirstUpload() {
		t.Fatalf("a missing Git branch must wait for its first upload: %+v", initial)
	}
	setDevTestBranch(t, root, "feature/changed")
	if selected, _, _, err := selectDevStartBuild(context.Background(), client, appID, initial); err != nil || selected != nil {
		t.Fatalf("new branch must bootstrap a build: selected=%+v err=%v", selected, err)
	}
	firstUpload := projectBuildInvocationFromDev(initial)
	if firstUpload.BranchID != "" || firstUpload.BranchName != "" || firstUpload.GitBranchName != "feature/fresh" || !firstUpload.GitBranchResolved {
		t.Fatalf("first upload must name the detected Git branch: %+v", firstUpload)
	}
	uploaded := initial.withResolvedBranch(branchID, "feature/fresh")
	continued, err := resolveDevDestination(context.Background(), client, uploaded, uploaded.BranchID, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if continued.BranchID != branchID || continued.GitBranchName != "feature/fresh" || continued.branchAwaitsFirstUpload() || lookups != 1 {
		t.Fatalf("destination changed: %+v lookups=%d", continued, lookups)
	}
	rebuild := projectBuildInvocationFromDev(continued)
	if rebuild.BranchID != branchID || rebuild.BranchName != "" || rebuild.GitBranchName != "" {
		t.Fatalf("rebuild upload lost its frozen destination: %+v", rebuild)
	}
}

func TestDevKeepsAMissingExplicitBranchForItsFirstUpload(t *testing.T) {
	resetDevSelectionFlags(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/apps/"+appID+"/branches" || r.Method != http.MethodGet || r.URL.Query().Get("name") != "release" {
			t.Errorf("unexpected request, such as creating the branch before its upload: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"branches":[],"total":0}`))
	}))
	defer server.Close()
	invocation, err := resolveDevDestination(context.Background(), api.NewClientWithBaseURL("test", server.URL), projectDevInvocation{AppID: appID, ProjectRoot: t.TempDir(), Profile: "dev", Platform: "android"}, "", "release", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.BranchID != "" || invocation.BranchName != "release" || !invocation.branchAwaitsFirstUpload() {
		t.Fatalf("a missing explicit branch must wait for its first upload: %+v", invocation)
	}
	if branchID, branchName, gitBranchName := invocation.uploadDestination(); branchID != "" || branchName != "release" || gitBranchName != "" {
		t.Fatalf("first upload destination = %q, %q, %q; want the branch name", branchID, branchName, gitBranchName)
	}
	args := projectDetachArgs([]string{"dev", "--branch", "release", "--detach"}, invocation)
	if strings.Join(args, " ") != "dev --branch release --profile dev --platform android" {
		t.Fatalf("detached child lost the new branch: %v", args)
	}
}

func TestDevDisabledAutomaticSelectionRemainsServerOwnedUntilBuildResolved(t *testing.T) {
	resetDevSelectionFlags(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	t.Setenv(devloop.GitBranchEnvironment, "feature/mobile")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"auto_git_branch_enabled":false,"default_branch_id":%q}`, appID, branchID)
		case "/api/v1/apps/" + appID + "/branches/" + branchID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"name":"base"}`, branchID)
		default:
			t.Errorf("disabled automatic selection looked up Git branch: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	invocation, err := resolveDevDestination(context.Background(), api.NewClientWithBaseURL("test", server.URL), projectDevInvocation{AppID: appID, Profile: "dev", Platform: "android"}, "", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.BranchID != branchID || invocation.BranchName != "base" || !invocation.AutomaticBranchSelection {
		t.Fatalf("wrong preview destination: %+v", invocation)
	}
	selection := devBuildSelection(invocation)
	if selection.BranchID != branchID || selection.GitBranchName != "" || selection.Mode != "" {
		t.Fatalf("seed request lost the app-policy destination or bypassed its pin: %+v", selection)
	}
	build := projectBuildInvocationFromDev(invocation)
	if build.BranchID != "" || build.BranchName != "" || build.GitBranchName != "feature/mobile" || !build.GitBranchResolved {
		t.Fatalf("automatic upload request became explicit: %+v", build)
	}
	args := projectDetachArgs([]string{"dev", "--detach"}, invocation)
	if strings.Contains(strings.Join(args, " "), "--branch") {
		t.Fatalf("detached child promoted automatic destination: %v", args)
	}
	if !strings.Contains(strings.Join(devDetachEnvironment(invocation), "\n"), devloop.GitBranchEnvironment+"=feature/mobile") {
		t.Fatal("detached child lost the captured Git context")
	}
	frozen := invocation.withResolvedBranch(branchID, "base")
	if frozen.AutomaticBranchSelection || devBuildSelection(frozen).BranchID != branchID {
		t.Fatalf("resolved build did not freeze session destination: %+v", frozen)
	}
}

func TestDevUploadSeedPreservesAppPolicyAndBootstrapsEmptyGitBranch(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const defaultID = "22222222-2222-4222-8222-222222222222"
	const gitID = "33333333-3333-4333-8333-333333333333"
	const buildID = "44444444-4444-4444-8444-444444444444"
	for _, test := range []struct {
		name                       string
		enabled, missing, explicit bool
	}{
		{name: "enabled empty Git branch", enabled: true},
		{name: "enabled missing Git branch", enabled: true, missing: true},
		{name: "disabled uses default build"},
		{name: "disabled explicit empty branch", explicit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			t.Setenv(devloop.GitBranchEnvironment, "feature")
			seedRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + appID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q,"auto_git_branch_enabled":%t}`, appID, defaultID, test.enabled)
				case "/api/v1/apps/" + appID + "/branches":
					if test.missing {
						_, _ = w.Write([]byte(`{"branches":[]}`))
					} else {
						_, _ = fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"feature"}]}`, gitID)
					}
				case "/api/v1/apps/" + appID + "/branches/" + defaultID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"name":"base"}`, defaultID)
				case "/api/v1/apps/resolve":
					seedRequests++
					var selection map[string]string
					_ = json.NewDecoder(r.Body).Decode(&selection)
					wantBranch := defaultID
					if test.enabled || test.explicit {
						wantBranch = gitID
					}
					if selection["branch_id"] != wantBranch || selection["git_branch_name"] != "" {
						t.Errorf("upload seed must check the policy-selected branch without falling back: %#v", selection)
					}
					if wantBranch == gitID {
						_, _ = fmt.Fprintf(w, `{"branch_id":%q,"resolution_status":"branch_has_no_builds"}`, gitID)
					} else {
						_, _ = fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"base","download_url":"https://example.test/app.zip","resolution_status":"resolved"}`, buildID, defaultID)
					}
				case "/api/v1/apps/builds/" + buildID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q}`, buildID, appID)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test", server.URL)
			explicitName := ""
			if test.explicit {
				explicitName = "feature"
			}
			invocation, err := resolveDevDestination(context.Background(), client, projectDevInvocation{AppID: appID}, "", explicitName, "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			selected, _, _, err := selectDevStartBuild(context.Background(), client, appID, invocation)
			if err != nil {
				t.Fatal(err)
			}
			if test.enabled || test.explicit {
				if selected != nil || (test.missing && seedRequests != 0) {
					t.Fatalf("empty/missing Git branch should bootstrap there: %+v requests=%d", selected, seedRequests)
				}
			} else if selected == nil || selected.ID != buildID {
				t.Fatalf("disabled detection did not use default seed: %+v", selected)
			}
		})
	}
}

func TestSelectDevStartBuildHonorsCapturedBranchDefault(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const pinnedID = "33333333-3333-4333-8333-333333333333"
	const latestID = "44444444-4444-4444-8444-444444444444"
	for _, test := range []struct {
		name, status, wantBuild string
		seedLatest, missingPin  bool
		wantErr                 error
	}{
		{name: "inferred branch uses its pin", status: "resolved", wantBuild: pinnedID},
		{name: "seed latest bypasses unavailable pin", status: "resolved", seedLatest: true, missingPin: true, wantBuild: latestID},
		{name: "unavailable pin aborts", missingPin: true},
		{name: "inferred branch without compatible build aborts", status: "no_compatible_build", wantErr: api.ErrBranchHasNoCompatibleBuild},
		{name: "empty inferred branch builds", status: "branch_has_no_builds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			devStartSeedLatest = test.seedLatest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					var request map[string]string
					_ = json.NewDecoder(r.Body).Decode(&request)
					mode := ""
					if test.seedLatest {
						mode = "latest"
					}
					if request["branch_id"] != branchID || request["git_branch_name"] != "" || request["build_selection"] != mode {
						t.Errorf("selection did not preserve frozen branch default: %#v", request)
					}
					if test.missingPin && request["build_selection"] != "latest" {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"detail":"pinned build is unavailable"}`))
						return
					}
					_, _ = fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"feature/login","download_url":"https://example.test/app.zip","resolution_status":%q}`, test.wantBuild, branchID, test.status)
				case "/api/v1/apps/builds/" + test.wantBuild:
					_, _ = fmt.Fprintf(w, `{"id":%q,"version":"1.0.0","app_id":%q}`, test.wantBuild, appID)
				default:
					t.Errorf("unexpected branch scan or fallback: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			invocation := projectDevInvocation{AppID: appID, BranchID: branchID, GitBranchName: "feature/login"}
			selected, _, _, err := selectDevStartBuild(context.Background(), api.NewClientWithBaseURL("test", server.URL), appID, invocation)
			if test.missingPin && !test.seedLatest {
				if err == nil || !strings.Contains(err.Error(), "pinned build is unavailable") {
					t.Fatalf("missing pin error = %v", err)
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			gotBuild := ""
			if selected != nil {
				gotBuild = selected.ID
			}
			if gotBuild != test.wantBuild {
				t.Fatalf("selected build = %q, want %q", gotBuild, test.wantBuild)
			}
		})
	}
}

func TestDevNoBuildFallbackRetainsDefaultBranchPolicy(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, seedLatest := range []bool{false, true} {
		t.Run(fmt.Sprintf("seed_latest_%t", seedLatest), func(t *testing.T) {
			resetDevSelectionFlags(t)
			devStartSeedLatest = seedLatest
			t.Setenv(devloop.GitBranchEnvironment, "feature/empty")
			mode := ""
			if seedLatest {
				mode = "latest"
			}
			resolveCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					resolveCalls++
					var request map[string]string
					_ = json.NewDecoder(r.Body).Decode(&request)
					if request["build_selection"] != mode {
						t.Errorf("mode changed across fallback: %#v", request)
					}
					if resolveCalls == 1 {
						if request["git_branch_name"] != "feature/empty" || request["branch_id"] != "" {
							t.Errorf("missing inference: %#v", request)
						}
					} else if request["git_branch_name"] != "feature/empty" || request["branch_id"] != "" {
						t.Errorf("automatic destination became explicit before seed selection: %#v", request)
					}
					source := "explicit_branch"
					if resolveCalls == 1 {
						source = "default_branch"
					}
					_, _ = fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"base","branch_source":%q,"download_url":"https://example.test/app.zip","resolution_status":"resolved"}`, buildID, branchID, source)
				case "/api/v1/apps/" + appID + "/branches/" + branchID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"base"}`, branchID, appID)
				case "/api/v1/apps/builds/" + buildID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"version":"1.0.0","app_id":%q}`, buildID, appID)
				default:
					t.Errorf("unexpected upload or branch creation: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test", server.URL)
			invocation, err := resolveDevDestination(context.Background(), client, projectDevInvocation{AppID: appID}, "", "", "", mode, false)
			if err != nil || invocation.GitBranchName != "feature/empty" || invocation.BranchID != branchID {
				t.Fatalf("fallback invocation=%+v err=%v", invocation, err)
			}
			selected, _, _, err := selectDevStartBuild(context.Background(), client, appID, invocation)
			if err != nil || selected == nil || selected.ID != buildID || resolveCalls != 2 {
				t.Fatalf("fallback selected=%+v err=%v calls=%d", selected, err, resolveCalls)
			}
		})
	}
}

func TestDevRebuildOnlyNoBuildStopsBeforeUploadOrDeviceStart(t *testing.T) {
	resetDevSelectionFlags(t)
	previousNoBuild, previousBuild := devStartNoBuild, devStartBuild
	t.Cleanup(func() { devStartNoBuild, devStartBuild = previousNoBuild, previousBuild })
	devStartNoBuild, devStartBuild = true, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/apps/resolve" {
			t.Errorf("unexpected upload or device request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"resolution_status":"branch_has_no_builds"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	invocation := projectDevInvocation{ProjectRoot: t.TempDir(), AppID: "11111111-1111-4111-8111-111111111111", BranchID: "22222222-2222-4222-8222-222222222222", GitBranchName: "feature/empty", Platform: "ios", Profile: "development"}
	cmd := &cobra.Command{Use: "dev"}
	cmd.SetContext(context.Background())
	if err := runDevRebuildOnly(cmd, invocation, "test", "default"); err == nil || !strings.Contains(err.Error(), "no existing build found and --no-build is active") {
		t.Fatalf("no-build error = %v", err)
	}
}

func TestDevMCPAbsentHintDoesNotDetectFallbackCheckout(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	root := initDevRepository(t)
	setDevTestBranch(t, root, "server/unrelated")
	t.Setenv(devloop.GitBranchEnvironment, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, appID, branchID)
		case "/api/v1/apps/" + appID + "/branches/" + branchID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"default"}`, branchID, appID)
		default:
			t.Errorf("unexpected inferred branch request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	invocation, err := resolveDevDestination(context.Background(), api.NewClientWithBaseURL("test", server.URL), projectDevInvocation{AppID: appID, ProjectRoot: root}, "", "", "", "", true)
	if err != nil || invocation.BranchID != branchID {
		t.Fatalf("invocation=%+v err=%v", invocation, err)
	}
}

func setDevTestBranch(t *testing.T, root, name string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/"+name).CombinedOutput(); err != nil {
		t.Fatalf("set test branch: %v: %s", err, output)
	}
}

func resetDevSelectionFlags(t *testing.T) {
	t.Helper()
	branch, branchID, selection, seedLatest, buildID := devStartBranch, devStartBranchID, devStartBuildSelection, devStartSeedLatest, devStartBuildVerID
	t.Cleanup(func() {
		devStartBranch, devStartBranchID, devStartBuildSelection, devStartSeedLatest, devStartBuildVerID = branch, branchID, selection, seedLatest, buildID
	})
	devStartBranch, devStartBranchID, devStartBuildSelection, devStartSeedLatest, devStartBuildVerID = "", "", "", false, ""
}

func TestDevSelectionIsAppDefaultOnlyWithoutExplicitChoices(t *testing.T) {
	resetDevSelectionFlags(t)
	if !devSelectionIsAppDefault(projectDevInvocation{}) {
		t.Fatal("no explicit choice should use the app default selection")
	}
	for name, choose := range map[string]func(){
		"branch":          func() { devStartBranch = "staging" },
		"branch-id":       func() { devStartBranchID = "11111111-1111-4111-8111-111111111111" },
		"build-selection": func() { devStartBuildSelection = "inherit" },
		"seed-latest":     func() { devStartSeedLatest = true },
		"build-version":   func() { devStartBuildVerID = "22222222-2222-4222-8222-222222222222" },
	} {
		t.Run(name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			choose()
			if devSelectionIsAppDefault(projectDevInvocation{}) {
				t.Fatalf("--%s should keep its explicit selection", name)
			}
		})
	}
}

func TestDevBuildSelectionUsesLatestOnlyWithoutExplicitChoices(t *testing.T) {
	const buildID = "22222222-2222-4222-8222-222222222222"
	invocation := projectDevInvocation{BranchID: "branch-id"}
	resetDevSelectionFlags(t)
	if got, want := devBuildSelection(invocation), (api.BuildSelection{BranchID: "branch-id", Mode: api.BuildSelectionModeLatest}); got != want {
		t.Fatalf("app default selection = %+v, want %+v", got, want)
	}
	for name, test := range map[string]struct {
		choose func()
		want   api.BuildSelection
	}{
		"branch":          {func() { devStartBranch = "staging" }, api.BuildSelection{BranchID: "branch-id"}},
		"build-selection": {func() { devStartBuildSelection = "inherit" }, api.BuildSelection{BranchID: "branch-id", Mode: api.BuildSelectionModeInherit}},
		"seed-latest":     {func() { devStartSeedLatest = true }, api.BuildSelection{BranchID: "branch-id", Mode: api.BuildSelectionModeLatest}},
		"build-version":   {func() { devStartBuildVerID = buildID }, api.BuildSelection{BranchID: "branch-id", BuildID: buildID}},
	} {
		t.Run(name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			test.choose()
			if got := devBuildSelection(invocation); got != test.want {
				t.Fatalf("--%s selection = %+v, want %+v", name, got, test.want)
			}
		})
	}
}

func TestDevBuildsInsteadOfAbortingOnlyWhenNothingUsableIsSelected(t *testing.T) {
	resetDevSelectionFlags(t)
	for _, err := range []error{api.ErrBranchHasNoBuilds, api.ErrBranchHasNoCompatibleBuild, fmt.Errorf("resolve: %w", api.ErrBranchHasNoCompatibleBuild)} {
		if !devResolveErrorNeedsBuild(projectDevInvocation{}, err) {
			t.Fatalf("app default selection should build after %v", err)
		}
	}
	if devResolveErrorNeedsBuild(projectDevInvocation{}, errors.New("selected build unavailable")) {
		t.Fatal("other resolve failures must abort")
	}
	devStartBranch = "staging"
	if !devResolveErrorNeedsBuild(projectDevInvocation{}, api.ErrBranchHasNoBuilds) {
		t.Fatal("an empty named branch should build")
	}
	if devResolveErrorNeedsBuild(projectDevInvocation{}, api.ErrBranchHasNoCompatibleBuild) {
		t.Fatal("a named branch without a compatible build must keep aborting")
	}
}

func TestSelectDevStartBuildUsesLatestByDefaultAndKeepsNamedBranchPolicy(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	resolved := `{"version_id":"` + buildID + `","branch_id":"` + branchID + `","branch_name":"base","download_url":"https://example.test/app.zip","resolution_status":"resolved"}`
	noCompatible := `{"branch_id":"` + branchID + `","branch_name":"base","resolution_status":"no_compatible_build"}`
	root := initDevRepository(t)
	setDevTestBranch(t, root, "feature/login")
	for _, test := range []struct {
		name, namedBranch, resolution, wantMode, wantBuild string
		wantWarnings                                       int
		wantErr                                            error
	}{
		{"default branch latest without captured Git branch", "", resolved, "latest", buildID, 0, nil},
		{"default branch without a compatible build builds one", "", noCompatible, "latest", "", 0, nil},
		{"named branch keeps its policy", "staging", resolved, "", buildID, 0, nil},
		{"named branch without a compatible build aborts", "staging", noCompatible, "", "", 0, api.ErrBranchHasNoCompatibleBuild},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDevSelectionFlags(t)
			devStartBranch = test.namedBranch
			var resolveBody map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					_ = json.NewDecoder(r.Body).Decode(&resolveBody)
					_, _ = w.Write([]byte(test.resolution))
				case "/api/v1/apps/builds/" + buildID:
					_, _ = fmt.Fprintf(w, `{"id":%q,"version":"1.0.0","app_id":%q}`, buildID, appID)
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			invocation := projectDevInvocation{AppID: appID, ProjectRoot: root, BranchID: branchID}
			selected, _, warnings, err := selectDevStartBuild(context.Background(), api.NewClientWithBaseURL("test", server.URL), appID, invocation)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err = %v, want %v", err, test.wantErr)
			}
			if mode, _ := resolveBody["build_selection"].(string); mode != test.wantMode || resolveBody["branch_id"] != branchID {
				t.Fatalf("resolve request = %#v, want build_selection %q on the frozen branch", resolveBody, test.wantMode)
			}
			gotBuild := ""
			if selected != nil {
				gotBuild = selected.ID
			}
			if gotBuild != test.wantBuild || len(warnings) != test.wantWarnings {
				t.Fatalf("selected %q with warnings %q, want %q with %d warnings", gotBuild, warnings, test.wantBuild, test.wantWarnings)
			}
		})
	}
}

func TestDevUploadsCreateANewBranchThenRebuildsUploadToIt(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	var sessions []api.BuildUploadSessionRequest
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID + "/builds/upload-session":
			var session api.BuildUploadSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&session); err != nil {
				t.Error(err)
			}
			sessions = append(sessions, session)
			_, _ = fmt.Fprintf(w, `{"upload_id":%q,"upload_url":%q,"content_type":"application/octet-stream","branch_id":%q}`, buildID, server.URL+"/artifact", branchID)
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + appID + "/builds":
			_, _ = fmt.Fprintf(w, `{"id":%q,"version":"v1","branch_id":%q,"branch_name":"main"}`, buildID, branchID)
		case "/api/v1/apps/builds/" + buildID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"v1","download_url":"https://example.com/app.apk"}`, buildID, appID)
		default:
			t.Errorf("unexpected request, such as creating the branch before its upload: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("test", server.URL)
	artifact := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(artifact, []byte("fixture-apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name       string
		invocation projectDevInvocation
		want       api.BuildUploadSessionRequest
	}{
		{name: "detected Git branch", invocation: projectDevInvocation{AppID: appID, GitBranchName: "main"}, want: api.BuildUploadSessionRequest{GitBranchName: stringPtrOrNil("main")}},
		{name: "explicit branch", invocation: projectDevInvocation{AppID: appID, BranchName: "main"}, want: api.BuildUploadSessionRequest{BranchName: stringPtrOrNil("main")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			sessions = nil
			invocation := scenario.invocation
			for range 2 {
				_, _, uploaded, err := uploadExistingArtifact(context.Background(), client, artifact, appID, t.TempDir(), invocation)
				if err != nil {
					t.Fatal(err)
				}
				invocation = invocation.withResolvedBranch(uploaded.BranchID, uploaded.BranchName)
			}
			if invocation.BranchID != branchID || invocation.BranchName != "main" {
				t.Fatalf("the loop did not freeze the branch its first upload created: %+v", invocation)
			}
			if len(sessions) != 2 {
				t.Fatalf("upload sessions = %d, want 2", len(sessions))
			}
			first, second := sessions[0], sessions[1]
			if first.BranchId != nil || stringValue(first.BranchName) != stringValue(scenario.want.BranchName) || stringValue(first.GitBranchName) != stringValue(scenario.want.GitBranchName) {
				t.Fatalf("first upload destination = %+v, want %+v", first, scenario.want)
			}
			if second.BranchId == nil || second.BranchId.String() != branchID || second.BranchName != nil || second.GitBranchName != nil {
				t.Fatalf("rebuild upload destination = %+v, want the created branch by ID", second)
			}
		})
	}
}
