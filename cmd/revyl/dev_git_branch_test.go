package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devloop"
)

func TestDevCreatesInferredBranchThenFreezesDestination(t *testing.T) {
	resetDevSelectionFlags(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	root := initDevRepository(t)
	setDevTestBranch(t, root, "feature/fresh")
	lookups, creates := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/apps/"+appID+"/branches" && r.Method == http.MethodGet:
			lookups++
			if r.URL.Query().Get("name") != "feature/fresh" {
				t.Errorf("branch query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"branches":[],"total":0}`))
		case r.URL.Path == "/api/v1/apps/"+appID+"/branches" && r.Method == http.MethodPost:
			creates++
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["name"] != "feature/fresh" {
				t.Errorf("created branch = %#v", request)
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"feature/fresh"}`, branchID, appID)
		case r.URL.Path == "/api/v1/apps/"+appID+"/branches/"+branchID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"renamed"}`, branchID, appID)
		case r.URL.Path == "/api/v1/apps/resolve":
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["branch_id"] != branchID || request["git_branch_name"] != "" || request["build_selection"] != "" {
				t.Errorf("seed scope was not frozen: %#v", request)
			}
			_, _ = w.Write([]byte(`{"resolution_status":"branch_has_no_builds"}`))
		default:
			t.Errorf("unexpected default/fallback request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("test", server.URL)
	initial, err := resolveDevDestination(context.Background(), client, projectDevInvocation{AppID: appID, ProjectRoot: root}, "", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	setDevTestBranch(t, root, "feature/changed")
	if selected, _, _, err := selectDevStartBuild(context.Background(), client, appID, initial); err != nil || selected != nil {
		t.Fatalf("new branch must bootstrap a build: selected=%+v err=%v", selected, err)
	}
	continued, err := resolveDevDestination(context.Background(), client, initial, initial.BranchID, "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if continued.BranchID != branchID || continued.GitBranchName != "feature/fresh" || lookups != 1 || creates != 1 {
		t.Fatalf("destination changed: %+v lookups=%d creates=%d", continued, lookups, creates)
	}
	if projectBuildInvocationFromDev(continued).BranchID != branchID {
		t.Fatal("rebuild upload lost its frozen destination")
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
					} else if request["git_branch_name"] != "" || request["branch_id"] != branchID {
						t.Errorf("fallback destination not frozen: %#v", request)
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
