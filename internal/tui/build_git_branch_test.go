package tui

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
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

func selectGitBranchWorktree(t *testing.T, branch string) string {
	t.Helper()
	root := initializeSettingsGitWorktree(t)
	if output, err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/"+branch).CombinedOutput(); err != nil {
		t.Fatalf("select fixture branch: %v: %s", err, output)
	}
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "")
	t.Chdir(root)
	return root
}

func TestRuntimeGitBranchUsesEffectiveDirectoryWithoutConfig(t *testing.T) {
	selectGitBranchWorktree(t, "Feature/Customer")
	selection, err := withRuntimeGitBranch(context.Background(), api.BuildSelection{Mode: api.BuildSelectionModeLatest})
	if err != nil || selection.GitBranchName != "Feature/Customer" || selection.BranchID != "" || selection.BranchName != "" || selection.Mode != api.BuildSelectionModeLatest {
		t.Fatalf("Automatic+Latest lost customer context: %#v, %v", selection, err)
	}
	t.Chdir(t.TempDir())
	selection, err = withRuntimeGitBranch(context.Background(), api.BuildSelection{})
	if err != nil || selection.GitBranchName != "" {
		t.Fatalf("non-Git directory gained context: %#v, %v", selection, err)
	}
}

func TestRuntimeGitBranchPreservesExplicitAndFrozenChoices(t *testing.T) {
	selectGitBranchWorktree(t, "feature/new")
	for _, selection := range []api.BuildSelection{
		{BranchID: pickerBranchID},
		{BranchName: "main"},
		{BuildID: pickerBuildID, Mode: api.BuildSelectionModePinned},
		{GitBranchName: "feature/frozen", Mode: api.BuildSelectionModeLatest},
		{Mode: api.BuildSelectionModeLatestAllBranches},
	} {
		actual, err := withRuntimeGitBranch(context.Background(), selection)
		if err != nil || actual != selection {
			t.Fatalf("selection was inferred again: %#v => %#v, %v", selection, actual, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := withRuntimeGitBranch(ctx, api.BuildSelection{}); err == nil {
		t.Fatal("cancelled detection was ignored")
	}
}

func TestTestExecutionCarriesGitBranchWithoutReplacingSavedSelection(t *testing.T) {
	root := selectGitBranchWorktree(t, "Feature/Customer")
	writeSettingsConfig(t, root, canonicalSettingsConfig(300))
	for _, testCase := range []struct {
		name    string
		choice  testExecutionBuildSelection
		context string
		branch  string
		build   string
	}{
		{name: "saved", context: "Feature/Customer"},
		{name: "automatic latest", choice: testExecutionBuildSelection{Selection: api.BuildSelection{Mode: api.BuildSelectionModeLatest}}, context: "Feature/Customer"},
		{name: "explicit branch", choice: testExecutionBuildSelection{Selection: api.BuildSelection{BranchID: pickerBranchID}}, branch: pickerBranchID},
		{name: "exact build", choice: testExecutionBuildSelection{Selection: api.BuildSelection{BuildID: pickerBuildID, Mode: api.BuildSelectionModePinned}}, build: pickerBuildID},
		{name: "explicit automatic", choice: testExecutionBuildSelection{Selection: api.BuildSelection{Mode: api.BuildSelectionModeInherit}, OverrideAppID: pickerAppID}, branch: pickerBranchID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			branchReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if handleTestCLITraceFallback(w, r) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/resolve":
					branchReads++
					var payload map[string]string
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if payload["git_branch_name"] != "Feature/Customer" {
						t.Errorf("wrong context: %+v", payload)
					}
					fmt.Fprintf(w, `{"branch_id":%q}`, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					fmt.Fprintf(w, `{"id":%q,"name":"Feature/Customer"}`, pickerBranchID)
				case "/api/v1/execution/api/execute_test_id_async":
					var payload map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					for key, want := range map[string]string{"git_branch_name": testCase.context, "branch_id": testCase.branch, "build_version_id": testCase.build} {
						got, _ := payload[key].(string)
						if got != want {
							t.Errorf("%s=%q, want %q", key, got, want)
						}
					}
					if _, ok := payload["branch_name"]; ok {
						t.Error("detected context became an explicit branch name")
					}
					fmt.Fprint(w, `{"task_id":"run"}`)
				default:
					t.Errorf("unexpected selection read: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			msg := startMonitoredExecutionCmd("test", "Checkout", "test", false, testCase.choice)()
			if _, ok := msg.(ExecutionStartedMsg); !ok {
				t.Fatalf("execution did not start: %#v", msg)
			}
			if (branchReads == 1) != (testCase.choice.OverrideAppID != "") {
				t.Fatalf("branch reads=%d; saved resolution must stay server-owned", branchReads)
			}
		})
	}
}

func TestExplicitAutomaticWithoutGitBranchResolvesAppDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"default"}`, pickerBranchID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	selection, _, err := prepareTestRunBuildSelection(context.Background(), api.NewClientWithBaseURL("test", server.URL), testExecutionBuildSelection{
		Selection: api.BuildSelection{Mode: api.BuildSelectionModeLatest}, OverrideAppID: pickerAppID,
	})
	if err != nil || selection.BranchID != pickerBranchID || selection.GitBranchName != "" || selection.Mode != api.BuildSelectionModeLatest {
		t.Fatalf("default override not frozen: %#v, %v", selection, err)
	}
}

func TestBuildBrowsingIgnoresGitBranch(t *testing.T) {
	selectGitBranchWorktree(t, "Feature/Customer")
	for _, branchID := range []string{"", pickerBranchID} {
		t.Run(branchID, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					if branchID == "" {
						t.Error("All browsing resolved a branch")
					}
					fmt.Fprintf(w, `{"id":%q,"name":"Feature/Customer"}`, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/builds":
					if r.URL.Query().Get("branch_id") != branchID {
						t.Errorf("wrong browse scope: %s", r.URL)
					}
					fmt.Fprint(w, `{"items":[]}`)
				default:
					t.Errorf("browsing resolved Git branch: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			msg := fetchAppBuildsCmd(api.NewClientWithBaseURL("test", server.URL), pickerAppID, appBuildQuery{BranchID: branchID})().(AppBuildVersionsMsg)
			if msg.Err != nil {
				t.Fatal(msg.Err)
			}
		})
	}
}

func TestRunAndUploadPickersNameTheDetectedGitBranch(t *testing.T) {
	base := api.BuildBranch{ID: pickerBranchID, Name: "base"}
	feature := api.BuildBranch{ID: pickerOtherBranchID, Name: "feature-login"}
	loaded := func(runtime, allowCreate bool, gitBranch string) buildPickerModel {
		m := newBuildPicker(nil, pickerAppID, "Build")
		m.runtime, m.allowCreate = runtime, allowCreate
		m, _ = m.Update(buildPickerDataMsg{id: m.id, page: 1, defaultBranch: &base, branches: []api.BuildBranch{base, feature}, gitBranch: gitBranch})
		return m
	}

	saved := loaded(false, false, "feature-login")
	if got := saved.options()[0]; strings.Contains(got, "Git") || !strings.HasPrefix(got, "Default branch (base)") {
		t.Fatalf("saved picker's first choice = %q, want Default branch (base)", got)
	}
	run := loaded(true, false, "feature-login")
	if got, want := run.options(), []string{"Git branch (feature-login) · falls back to base", "base (default) · Latest build (no builds)", "feature-login · Latest build (no builds)"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("run picker options = %q, want %q", got, want)
	}
	upload := loaded(true, true, "feature-login")
	if got := upload.options()[0]; got != "Git branch (feature-login)" {
		t.Fatalf("upload picker's first choice = %q", got)
	}
	onDefault := loaded(true, false, "base")
	if got, want := onDefault.options(), []string{"Git branch (base)", "base (default) · Latest build (no builds)", "feature-login · Latest build (no builds)"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("run picker on the default branch's checkout options = %q, want %q", got, want)
	}
	outside := loaded(true, false, "")
	if got, want := outside.options(), []string{"Default branch (base) · Latest build (no builds)", "feature-login · Latest build (no builds)"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("run picker outside Git options = %q, want %q", got, want)
	}

	run, _ = run.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if run.label != "Git branch (feature-login)" || run.selection.BranchID != "" {
		t.Fatalf("choosing the Git branch: label %q, selection %+v", run.label, run.selection)
	}
}

func TestExactPickerUsesGitBranchOnlyForRuntimeChoice(t *testing.T) {
	selectGitBranchWorktree(t, "Feature/Customer")
	for _, runtime := range []bool{false, true} {
		t.Run(fmt.Sprint(runtime), func(t *testing.T) {
			wantedBranch, wantedName := pickerBranchID, "default"
			if runtime {
				wantedBranch, wantedName = pickerOtherBranchID, "Feature/Customer"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + pickerAppID:
					if runtime {
						t.Error("runtime exact picker fell back to app default")
					}
					fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					fmt.Fprintf(w, `{"id":%q,"name":"default"}`, pickerBranchID)
				case "/api/v1/apps/resolve":
					var payload map[string]string
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if !runtime || payload["git_branch_name"] != wantedName {
						t.Errorf("wrong context: %+v", payload)
					}
					fmt.Fprintf(w, `{"branch_id":%q}`, wantedBranch)
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerOtherBranchID:
					fmt.Fprintf(w, `{"id":%q,"name":%q}`, wantedBranch, wantedName)
				case "/api/v1/apps/" + pickerAppID + "/builds":
					if r.URL.Query().Get("branch_id") != wantedBranch {
						t.Errorf("wrong exact build scope: %s", r.URL)
					}
					fmt.Fprintf(w, `{"items":[{"id":%q,"version":"v1"}]}`, pickerBuildID)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Pin build")
			m.runtime, m.step = runtime, buildPickerExact
			m, _ = m.Update(m.fetchBuilds()())
			m, _ = pickerKey(m, tea.KeyEnter)
			if m.err != nil || !m.done || m.selection.BranchID != wantedBranch || m.selection.BuildID != pickerBuildID || m.selection.GitBranchName != "" || !strings.HasPrefix(m.label, wantedName) {
				t.Fatalf("exact picker did not freeze branch/build: %#v, label=%q, error=%v", m.selection, m.label, m.err)
			}
		})
	}
}

func TestGitBranchUploadRetryKeepsFrozenBranchAndVersionAfterGitChanges(t *testing.T) {
	root := selectGitBranchWorktree(t, "Feature/Customer")
	file := filepath.Join(root, "app.apk")
	if err := os.WriteFile(file, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	branchNames, versions := []string{}, []string{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleTestCLITraceFallback(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			branchNames = append(branchNames, r.URL.Query().Get("name"))
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"Feature/Customer"}],"total_pages":1}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"renamed"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/builds/upload-session":
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			version, _ := payload["version"].(string)
			versions = append(versions, version)
			if len(versions) == 1 {
				if payload["branch_id"] != nil || payload["git_branch_name"] != "Feature/Customer" {
					t.Errorf("automatic upload must retain Git context: %#v", payload)
				}
			} else if payload["branch_id"] != pickerBranchID || payload["git_branch_name"] != nil {
				t.Errorf("retry retargeted: %#v", payload)
			}
			if len(versions) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"detail":"fixture transfer unavailable"}`)
				return
			}
			fmt.Fprintf(w, `{"upload_id":"retry","upload_url":%q,"content_type":"application/octet-stream","branch_id":%q}`, server.URL+"/artifact", pickerBranchID)
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			fmt.Fprintf(w, `{"id":%q,"version":"uploaded","branch_id":%q}`, pickerBuildID, pickerBranchID)
		case "/api/v1/apps/resolve":
			fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"resolution_status":"resolved"}`, pickerBuildID, pickerBranchID)
		case "/api/v1/apps/builds/" + pickerBuildID:
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"uploaded"}`, pickerBuildID, pickerAppID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newUploadBuildModel(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "App", 80, 40)
	m.step, m.resolvedPath = uploadStepConfirm, file
	loaded, _ := m.Update(m.fetchUploadBranchContext()())
	m = loaded.(uploadBuildModel)
	next, cmd := m.handleConfirmKey("enter")
	m = next.(uploadBuildModel)
	first := cmd().(tea.BatchMsg)[1]().(BuildUploadedMsg)
	if first.Err == nil || first.Branch == nil {
		t.Fatalf("expected failure with frozen destination: %#v", first)
	}
	next, _ = m.Update(first)
	m = next.(uploadBuildModel)
	if output, err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/other").CombinedOutput(); err != nil {
		t.Fatalf("change fixture branch: %v: %s", err, output)
	}
	next, cmd = m.handleConfirmKey("enter")
	m = next.(uploadBuildModel)
	second := cmd().(tea.BatchMsg)[1]().(BuildUploadedMsg)
	if second.Err != nil || second.VersionID != pickerBuildID {
		t.Fatalf("retry failed: %#v", second)
	}
	if second.BranchSummary != "renamed (Latest build: runs use this upload)" {
		t.Fatalf("upload summary = %q", second.BranchSummary)
	}
	if len(branchNames) != 1 || branchNames[0] != "Feature/Customer" || len(versions) != 2 || versions[0] == "" || versions[0] != versions[1] {
		t.Fatalf("retry changed logical upload: branches=%v, versions=%v", branchNames, versions)
	}
	if m.branchSelection.BranchID != "" || m.branchSelection.GitBranchName != "" {
		t.Fatal("runtime freeze changed the declared Automatic destination")
	}
	next, _ = m.Update(BuildUploadedMsg{Err: fmt.Errorf("frozen branch unavailable")})
	m = next.(uploadBuildModel)
	if m.pendingBranch == nil || m.pendingBranch.ID != pickerBranchID {
		t.Fatal("failed retry lookup discarded the frozen destination")
	}
	next, _ = m.handleConfirmKey("backspace")
	m = next.(uploadBuildModel)
	if m.pendingBranch != nil || m.pendingVersion != "" {
		t.Fatal("editing a new upload retained the previous retry identity")
	}
}
