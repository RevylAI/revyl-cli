package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func TestRemoteBuildNextStepKeepsExactArtifact(t *testing.T) {
	id := "33333333-3333-4333-8333-333333333333"
	command := remoteBuildStartDeviceCommand(remoteBuildPlatformConfig{Platform: "android", AppID: "app"}, &api.RemoteBuildStatusResponse{VersionId: &id})
	if !strings.Contains(command, "--build-version-id "+id) {
		t.Fatalf("next step re-resolves app default: %s", command)
	}
}

func TestUploadJSONIncludesCanonicalBranch(t *testing.T) {
	output := newBuildUploadJSONBuild("android", "android", "app", "app.apk", 0, &api.UploadBuildResponse{VersionID: "build", Version: "v1", BranchID: "branch", BranchName: "main"})
	if output.BranchID != "branch" || output.BranchName != "main" || output.BuildID != "build" {
		t.Fatalf("missing upload scope: %#v", output)
	}
}

const (
	buildListTestAppID    = "11111111-1111-4111-8111-111111111111"
	buildListTestBranchID = "22222222-2222-4222-8222-222222222222"
)

type buildListTestServer struct {
	revylBranches  map[string]bool
	builds         string
	branchLookups  []string
	listedBranchID []string
}

func newBuildListTestServer(t *testing.T, state *buildListTestServer) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + buildListTestAppID + "/branches":
			name := r.URL.Query().Get("name")
			state.branchLookups = append(state.branchLookups, name)
			if isDefault, ok := state.revylBranches[name]; ok {
				fmt.Fprintf(w, `{"branches":[{"id":%q,"app_id":%q,"name":%q,"is_default":%t}]}`, buildListTestBranchID, buildListTestAppID, name, isDefault)
				return
			}
			fmt.Fprint(w, `{"branches":[]}`)
		case "/api/v1/apps/" + buildListTestAppID + "/builds":
			if r.URL.Query().Has("branch_name") {
				t.Errorf("listed by branch name: %s", r.URL)
			}
			state.listedBranchID = append(state.listedBranchID, r.URL.Query().Get("branch_id"))
			fmt.Fprint(w, state.builds)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newBuildListTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := newLeafCommand("list", nil)
	cmd.Flags().String("branch", "", "")
	cmd.Flags().Bool("all-branches", false, "")
	cmd.Flags().Int("page", 1, "")
	cmd.Flags().Int("limit", 20, "")
	cmd.Flags().Int("page-size", 20, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestBuildListBranchScopeRequiresApp(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	previousApp := appIDFlag
	appIDFlag = ""
	t.Cleanup(func() { appIDFlag = previousApp })
	for _, args := range [][]string{{"--branch", "staging"}, {"--all-branches"}} {
		cmd := newBuildListTestCommand(t, args...)
		err := runBuildList(cmd, nil)
		if err == nil || !strings.Contains(err.Error(), args[0]+" requires --app") {
			t.Fatalf("build list %v error = %v, want app requirement", args, err)
		}
	}
}

const buildListGitBranchBuilds = `{"items":[` +
	`{"id":"feature-build","version":"feature-login-1","branch_name":"base","metadata":{"git":{"branch":"feature/login"}}},` +
	`{"id":"main-build","version":"main-1","branch_name":"base","metadata":{"git":{"branch":"main"}}}` +
	`],"total":2,"page":1,"page_size":20,"has_next":false}`

func TestBuildListDefaultsToAllBranchesWithoutInventingAFilter(t *testing.T) {
	repository := initDevRepository(t)
	setDevTestBranch(t, repository, "feature/login")
	t.Chdir(repository)
	for _, args := range [][]string{{"--json"}, {"--json", "--all-branches"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			state := &buildListTestServer{builds: `{"items":[` +
				`{"id":"base-build","version":"base-1","branch_id":"` + buildListTestBranchID + `","branch_name":"base"},` +
				`{"id":"feature-build","version":"feature-1","branch_id":"33333333-3333-4333-8333-333333333333","branch_name":"feature/login"}` +
				`],"total":5,"page":2,"page_size":2,"has_next":true}`}
			server := newBuildListTestServer(t, state)
			cmd := newBuildListTestCommand(t, append(args, "--page", "2", "--limit", "2")...)
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
					t.Fatal(err)
				}
			})
			result := parseJSON(t, stdout)
			if stderr != "" || len(state.branchLookups) != 0 || len(state.listedBranchID) != 1 || state.listedBranchID[0] != "" {
				t.Fatalf("unfiltered read narrowed or emitted diagnostics: branches=%v listed=%v stderr=%q", state.branchLookups, state.listedBranchID, stderr)
			}
			for _, key := range []string{"branch_filter", "branch_filter_kind"} {
				if _, found := result[key]; found {
					t.Fatalf("unfiltered list fabricated %s: %v", key, result)
				}
			}
			if result["count"] != float64(2) || result["scanned_count"] != float64(2) || result["total"] != float64(5) || result["page"] != float64(2) || result["page_size"] != float64(2) || result["has_next"] != true {
				t.Fatalf("pagination changed: %v", result)
			}
			versions := result["versions"].([]interface{})
			if versions[0].(map[string]interface{})["branch_name"] != "base" || versions[1].(map[string]interface{})["branch_name"] != "feature/login" {
				t.Fatalf("row provenance lost: %v", versions)
			}
		})
	}
}

func TestBuildListBranchNamesARevylBranchWhenOneExists(t *testing.T) {
	state := &buildListTestServer{revylBranches: map[string]bool{"staging": false}, builds: buildListGitBranchBuilds}
	server := newBuildListTestServer(t, state)
	cmd := newBuildListTestCommand(t, "--json", "--branch", "staging")
	output := captureStdout(t, func() {
		if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
			t.Fatal(err)
		}
	})
	result := parseJSON(t, output)
	if result["branch_filter"] != "staging" || result["branch_filter_kind"] != "revyl_branch" || result["count"] != float64(2) {
		t.Fatalf("Revyl branch listing = %v", result)
	}
	if len(state.listedBranchID) != 1 || state.listedBranchID[0] != buildListTestBranchID {
		t.Fatalf("listed branches %v", state.listedBranchID)
	}
}

func TestBuildListBranchFallsBackToTheRecordedGitBranch(t *testing.T) {
	for _, test := range []struct{ name, branchArgument string }{
		{"named Git branch", "feature/login"},
		{"HEAD", "HEAD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := initDevRepository(t)
			setDevTestBranch(t, repository, "feature/login")
			t.Chdir(repository)
			state := &buildListTestServer{revylBranches: map[string]bool{}, builds: buildListGitBranchBuilds}
			server := newBuildListTestServer(t, state)
			cmd := newBuildListTestCommand(t, "--json", "--branch", test.branchArgument)
			output := captureStdout(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
					t.Fatal(err)
				}
			})
			result := parseJSON(t, output)
			versions, _ := result["versions"].([]interface{})
			if result["branch_filter"] != "feature/login" || result["branch_filter_kind"] != "git_branch" || result["count"] != float64(1) || result["scanned_count"] != float64(2) || len(versions) != 1 {
				t.Fatalf("Git branch listing = %v", result)
			}
			if len(state.branchLookups) != 1 || state.branchLookups[0] != "feature/login" || len(state.listedBranchID) != 1 || state.listedBranchID[0] != "" {
				t.Fatalf("lookups %v listed %v", state.branchLookups, state.listedBranchID)
			}
		})
	}
}

func TestBuildListHumanOutputNamesTheGitBranchFilter(t *testing.T) {
	state := &buildListTestServer{revylBranches: map[string]bool{}, builds: buildListGitBranchBuilds}
	server := newBuildListTestServer(t, state)
	cmd := newBuildListTestCommand(t, "--branch", "feature/login")
	_, stderr := captureStdoutAndStderrSeparate(t, func() {
		if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{`No Revyl branch is named "feature/login"`, "GIT BRANCH", "REVYL BRANCH", "feature-login-1", "feature/login"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("human output missing %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{"main-1"} {
		if strings.Contains(stderr, unwanted) {
			t.Fatalf("human output has %q:\n%s", unwanted, stderr)
		}
	}
}

func TestBuildListTableNamesRevylBranchesOnlyAcrossBranches(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		headers []string
		absent  []string
	}{
		{"default", nil, []string{"VERSION", "BUILD ID", "UPLOADED", "GIT BRANCH", "COMMIT", "PACKAGE ID", "REVYL BRANCH"}, []string{"CURRENT"}},
		{"one branch", []string{"--branch", "base"}, []string{"VERSION", "BUILD ID", "UPLOADED", "GIT BRANCH", "COMMIT", "PACKAGE ID"}, []string{"REVYL BRANCH", "CURRENT"}},
		{"all branches", []string{"--all-branches"}, []string{"VERSION", "BUILD ID", "UPLOADED", "GIT BRANCH", "COMMIT", "PACKAGE ID", "REVYL BRANCH"}, []string{"CURRENT"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &buildListTestServer{revylBranches: map[string]bool{"base": true}, builds: buildListGitBranchBuilds}
			server := newBuildListTestServer(t, state)
			cmd := newBuildListTestCommand(t, test.args...)
			_, stderr := captureStdoutAndStderrSeparate(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
					t.Fatal(err)
				}
			})
			header := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.Contains(line, "VERSION") && strings.Contains(line, "BUILD ID") {
					header = line
					break
				}
			}
			if got := strings.Fields(strings.NewReplacer("BUILD ID", "BUILD_ID", "PACKAGE ID", "PACKAGE_ID", "GIT BRANCH", "GIT_BRANCH", "REVYL BRANCH", "REVYL_BRANCH").Replace(header)); len(got) != len(test.headers) {
				t.Fatalf("headers %q, want %q", got, test.headers)
			}
			for _, want := range append(append([]string{}, test.headers...), "feature/login", "main") {
				if !strings.Contains(stderr, want) {
					t.Fatalf("table missing %q:\n%s", want, stderr)
				}
			}
			for _, unwanted := range test.absent {
				if strings.Contains(header, unwanted) {
					t.Fatalf("header has %q: %s", unwanted, header)
				}
			}
		})
	}
}

func TestBuildListEmptyHintUploadsToTheListedBranch(t *testing.T) {
	for _, test := range []struct {
		name      string
		isDefault bool
		wantHint  string
	}{
		{"other branch", false, "--platform ios|android --branch staging"},
		{"default branch", true, "--platform ios|android\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &buildListTestServer{revylBranches: map[string]bool{"staging": test.isDefault}, builds: `{"items":[],"total":0,"page":1,"page_size":20,"has_next":false}`}
			server := newBuildListTestServer(t, state)
			cmd := newBuildListTestCommand(t, "--branch", "staging")
			_, stderr := captureStdoutAndStderrSeparate(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(stderr, test.wantHint) {
				t.Fatalf("hint missing %q:\n%s", test.wantHint, stderr)
			}
		})
	}
}

func TestAppBranchListMarksTheDefaultBranch(t *testing.T) {
	isDefault, notDefault := true, false
	for _, test := range []struct {
		branch api.BuildBranch
		want   string
	}{
		{api.BuildBranch{Name: "base", IsDefault: &isDefault}, "base (default)"},
		{api.BuildBranch{Name: "staging", IsDefault: &notDefault}, "staging"},
		{api.BuildBranch{Name: "release"}, "release"},
	} {
		if got := buildBranchListName(test.branch); got != test.want {
			t.Fatalf("buildBranchListName(%q) = %q, want %q", test.branch.Name, got, test.want)
		}
	}
}

func TestBuildListHEADNeedsACheckedOutGitBranch(t *testing.T) {
	const wantError = "--branch HEAD needs a checked-out Git branch; pass --branch <name>, or omit it to list every branch"
	detachedCheckout := func(t *testing.T) string {
		repository := initDevRepository(t)
		for _, args := range [][]string{
			{"-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-q", "-m", "init"},
			{"checkout", "-q", "--detach"},
		} {
			if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, output)
			}
		}
		return repository
	}
	for _, checkout := range []struct {
		name string
		dir  func(*testing.T) string
	}{
		{"outside Git", func(t *testing.T) string { return t.TempDir() }},
		{"detached HEAD", detachedCheckout},
	} {
		for _, args := range [][]string{{"--branch", "HEAD"}, {"--branch", "head", "--json"}} {
			t.Run(checkout.name+" "+strings.Join(args, " "), func(t *testing.T) {
				t.Chdir(checkout.dir(t))
				state := &buildListTestServer{revylBranches: map[string]bool{"base": true}, builds: buildListGitBranchBuilds}
				server := newBuildListTestServer(t, state)
				cmd := newBuildListTestCommand(t, args...)
				var err error
				stdout, _ := captureStdoutAndStderrSeparate(t, func() {
					err = listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID)
				})
				if err == nil || err.Error() != wantError {
					t.Fatalf("error = %v, want %q", err, wantError)
				}
				if stdout != "" || len(state.branchLookups) != 0 || len(state.listedBranchID) != 0 {
					t.Fatalf("listed builds anyway: stdout=%q lookups=%v listed=%v", stdout, state.branchLookups, state.listedBranchID)
				}
			})
		}
	}
}

func TestBuildListHeadingSaysWhichKindOfBranchMatched(t *testing.T) {
	for _, test := range []struct {
		name          string
		revylBranches map[string]bool
		want, absent  string
	}{
		{"Revyl branch", map[string]bool{"feature/login": false}, "Builds (Revyl branch: feature/login):", "Builds (Git branch:"},
		{"Git branch", map[string]bool{}, "Builds (Git branch: feature/login):", "Builds (Revyl branch:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &buildListTestServer{revylBranches: test.revylBranches, builds: buildListGitBranchBuilds}
			server := newBuildListTestServer(t, state)
			cmd := newBuildListTestCommand(t, "--branch", "feature/login")
			_, stderr := captureStdoutAndStderrSeparate(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("key", server.URL), buildListTestAppID); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(stderr, test.want) || strings.Contains(stderr, test.absent) || strings.Contains(stderr, "Builds (branch:") {
				t.Fatalf("heading should be %q:\n%s", test.want, stderr)
			}
		})
	}
}

func TestAppBranchListShowsEachBranchDefaultBuildAndTheBuildItRuns(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	const (
		pinnedID = "12121212-1212-4212-8212-121212121212"
		latestID = "20202020-2020-4020-8020-202020202020"
		goneID   = "02020202-0202-4202-8202-020202020202"
	)
	branches := `{"branches":[` +
		`{"id":"b1","name":"base","is_default":true,"pinned_build_id":"` + pinnedID + `","pinned_build_version":"v1.2","pinned_build_available":true,"latest_build":{"id":"13131313-1313-4313-8313-131313131313","version":"v1.3","uploaded_at":"2026-10-11T00:00:00Z"}},` +
		`{"id":"b2","name":"main","latest_build":{"id":"` + latestID + `","version":"v2.0","uploaded_at":"2026-10-11T00:00:00Z"}},` +
		`{"id":"b3","name":"release"},` +
		`{"id":"b4","name":"gone","pinned_build_id":"` + goneID + `","pinned_build_version":"v0.2","pinned_build_available":false}` +
		`],"total":4}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + buildListTestAppID:
			fmt.Fprintf(w, `{"id":%q,"name":"App","platform":"ios"}`, buildListTestAppID)
		case "/api/v1/apps/" + buildListTestAppID + "/builds":
			fmt.Fprint(w, `{"items":[],"total":0,"page":1,"page_size":1,"has_next":false}`)
		case "/api/v1/apps/" + buildListTestAppID + "/branches":
			fmt.Fprint(w, branches)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	registered, _, err := appCmd.Find([]string{"branch", "list"})
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonOutput := range []bool{false, true} {
		cmd := newLeafCommand("list", registered.RunE)
		cmd.Flags().String("app", buildListTestAppID, "")
		cmd.Flags().Bool("json", jsonOutput, "")
		cmd.Flags().Int("page", 1, "")
		cmd.Flags().Int("page-size", 50, "")
		stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
			if err := registered.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
		})
		if jsonOutput {
			var page map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &page); err != nil || stderr != "" {
				t.Fatalf("--json output=%q stderr=%q err=%v", stdout, stderr, err)
			}
			if listed := page["branches"].([]interface{}); len(listed) != 4 || strings.Contains(stdout, "BUILD ID") {
				t.Fatalf("--json changed shape: %s", stdout)
			}
			continue
		}
		lines := strings.Split(stderr, "\n")
		if header := lines[0]; !strings.Contains(header, "DEFAULT BUILD") || strings.Index(header, "DEFAULT BUILD") > strings.Index(header, "BUILD ID") {
			t.Fatalf("BUILD ID must follow DEFAULT BUILD:\n%s", stderr)
		}
		for _, want := range []struct{ branch, defaultBuild, buildID string }{
			{"base (default)", "v1.2", pinnedID},
			{"main", "Latest build (v2.0)", latestID},
			{"release", "Latest build (no builds)", "-"},
			{"gone", "v0.2 (unavailable)", goneID},
		} {
			row := ""
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), want.branch+" ") {
					row = line
				}
			}
			if !strings.Contains(row, want.defaultBuild) || !strings.HasSuffix(strings.TrimSpace(row), "  "+want.buildID) {
				t.Fatalf("%s row = %q, want DEFAULT BUILD %q then BUILD ID %q", want.branch, row, want.defaultBuild, want.buildID)
			}
		}
	}
}
