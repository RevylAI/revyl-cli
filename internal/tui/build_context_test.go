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
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

func selectBranchContextWorktree(t *testing.T, branch string) string {
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

func TestTestExecutionCarriesContextWithoutReplacingSavedSelection(t *testing.T) {
	root := selectBranchContextWorktree(t, "Feature/Customer")
	writeSettingsConfig(t, root, canonicalSettingsConfig(300))
	for _, testCase := range []struct {
		name    string
		choice  testExecutionBuildSelection
		context string
		branch  string
		build   string
	}{
		{name: "saved"},
		{name: "automatic latest", choice: testExecutionBuildSelection{Selection: api.BuildSelection{Mode: api.BuildSelectionModeLatest}}},
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
				case "/api/v1/apps/" + pickerAppID:
					branchReads++
					fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					fmt.Fprintf(w, `{"id":%q,"name":"default"}`, pickerBranchID)
				case "/api/v1/execution/api/execute_test_id_async":
					var payload map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					for key, want := range map[string]string{"context_branch_name": testCase.context, "branch_id": testCase.branch, "build_version_id": testCase.build} {
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

func TestExplicitAutomaticWithoutContextResolvesAppDefault(t *testing.T) {
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
	selection, err := prepareTestRunBuildSelection(context.Background(), api.NewClientWithBaseURL("test", server.URL), testExecutionBuildSelection{
		Selection: api.BuildSelection{Mode: api.BuildSelectionModeLatest}, OverrideAppID: pickerAppID,
	})
	if err != nil || selection.BranchID != pickerBranchID || selection.ContextBranchName != "" || selection.Mode != api.BuildSelectionModeLatest {
		t.Fatalf("default override not frozen: %#v, %v", selection, err)
	}
}

func TestExplicitUploadRetryKeepsFrozenBranchAndVersionAfterGitChanges(t *testing.T) {
	root := selectBranchContextWorktree(t, "Feature/Customer")
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
			if payload["branch_id"] != pickerBranchID {
				t.Errorf("upload retargeted: %#v", payload)
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
	m.branchSelection = api.BuildSelection{BranchName: "Feature/Customer"}
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
	if m.branchSelection.BranchID != "" || m.branchSelection.ContextBranchName != "" {
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
