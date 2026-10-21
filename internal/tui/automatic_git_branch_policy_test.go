package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

func TestRuntimePickerReadsAppAutomaticBranchPolicy(t *testing.T) {
	selectGitBranchWorktree(t, "feature-login")
	for _, enabled := range []bool{false, true} {
		for _, upload := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled_%t_upload_%t", enabled, upload), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/apps/" + pickerAppID:
						_ = json.NewEncoder(w).Encode(api.App{ID: pickerAppID, DefaultBranchID: pickerBranchID, DefaultBranchName: "base", AutoGitBranchEnabled: &enabled})
					case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
						fmt.Fprintf(w, `{"id":%q,"name":"base"}`, pickerBranchID)
					case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerOtherBranchID:
						fmt.Fprintf(w, `{"id":%q,"name":"feature-login"}`, pickerOtherBranchID)
					case "/api/v1/apps/resolve":
						var payload map[string]string
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Error(err)
						}
						if payload["git_branch_name"] != "feature-login" || payload["branch_id"] != "" {
							t.Errorf("exact picker bypassed server policy: %+v", payload)
						}
						if enabled {
							fmt.Fprintf(w, `{"branch_id":%q,"branch_source":"git_branch","git_branch_name":"feature-login","auto_git_branch_enabled":true}`, pickerOtherBranchID)
						} else {
							fmt.Fprintf(w, `{"branch_id":%q,"branch_source":"default_branch","auto_git_branch_enabled":false}`, pickerBranchID)
						}
					case "/api/v1/apps/" + pickerAppID + "/builds":
						want := pickerBranchID
						if enabled {
							want = pickerOtherBranchID
						}
						if r.URL.Query().Get("branch_id") != want {
							t.Errorf("exact picker used wrong branch: %s", r.URL)
						}
						fmt.Fprintf(w, `{"items":[{"id":%q,"version":"v1"}],"has_next":false}`, pickerBuildID)
					case "/api/v1/apps/" + pickerAppID + "/branches":
						fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"base"},{"id":%q,"name":"feature-login"}],"total_pages":1}`, pickerBranchID, pickerOtherBranchID)
					default:
						t.Errorf("unexpected request: %s", r.URL)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				picker := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
				picker.runtime, picker.allowCreate, picker.branchOnly = true, upload, upload
				picker, _ = picker.Update(picker.Init()())
				if picker.err != nil {
					t.Fatal(picker.err)
				}
				first := picker.options()[0]
				if enabled && !strings.HasPrefix(first, "Git branch (feature-login)") || !enabled && !strings.HasPrefix(first, "Default branch (base)") {
					t.Fatalf("automatic choice = %q, enabled=%t", first, enabled)
				}
				if !enabled && !strings.Contains(picker.View(), "Automatic Git branch detection is off") {
					t.Fatalf("disabled setting is unexplained: %s", picker.View())
				}
				if !upload {
					exact, _ := pickerKey(picker, tea.KeyEnter)
					exact.cursor = 2
					exact, command := pickerKey(exact, tea.KeyEnter)
					exact, _ = exact.Update(command())
					if exact.err != nil {
						t.Fatal(exact.err)
					}
					if !enabled && (!strings.Contains(exact.View(), "automatic Git branch detection is off") || strings.Contains(exact.View(), "has no usable build")) {
						t.Fatalf("exact picker misreports disabled policy: %s", exact.View())
					}
				}
				explicit := picker
				explicit.cursor = explicit.cursorForBranch(pickerOtherBranchID)
				explicit, _ = pickerKey(explicit, tea.KeyEnter)
				if explicit.selection.BranchID != pickerOtherBranchID {
					t.Fatalf("disabled detection removed explicit choice: %+v", explicit.selection)
				}
				picker, _ = pickerKey(picker, tea.KeyEnter)
				if !upload {
					picker, _ = pickerKey(picker, tea.KeyEnter)
				}
				if picker.selection.BranchID != "" || picker.selection.BranchName != "" || picker.gitBranch != "feature-login" {
					t.Fatalf("automatic choice froze a branch or lost Git context: %+v, git=%q", picker.selection, picker.gitBranch)
				}
			})
		}
	}
}

func TestUploadConfirmationShowsDisabledPolicyAndPreservesGitHint(t *testing.T) {
	selectGitBranchWorktree(t, "feature/one")
	fixture := newUploadConfirmationFixture(t)
	disabled := false
	fixture.autoGitBranchEnabled = &disabled
	fixture.defaultBranchID = pickerOtherBranchID
	upload := openUploadConfirmation(t, fixture.client, appBuildQuery{})
	upload.branchLabel = "Git branch (previous-upload)"
	if !strings.Contains(upload.View(), "Default branch (feature/two)") || !strings.Contains(upload.View(), "Automatic Git branch detection is off") || strings.Contains(upload.View(), "Git branch (feature/one)") {
		t.Fatalf("upload confirmation misreports destination: %s", upload.View())
	}
	confirmUploadDestination(t, upload, fixture, pickerOtherBranchID)
	if len(fixture.branchNames) != 0 {
		t.Fatalf("disabled automatic upload looked up the Git branch: %v", fixture.branchNames)
	}
	if stringOrEmpty(fixture.sessions[0].GitBranchName) != "feature/one" || fixture.sessions[0].BranchId != nil {
		t.Fatalf("automatic upload bypassed server policy: %+v", fixture.sessions[0])
	}
}

func TestUploadWaitsForPolicyAndRetainsContextWhilePickerIsOpen(t *testing.T) {
	upload := newUploadBuildModel(nil, pickerAppID, "App", 80, 24)
	upload.step = uploadStepConfirm
	next, cmd := upload.handleConfirmKey("enter")
	if cmd != nil || next.(uploadBuildModel).uploading {
		t.Fatal("upload proceeded before the app branch policy loaded")
	}
	picker := newBuildPicker(nil, pickerAppID, "Build")
	upload.buildPicker = &picker
	disabled := false
	next, _ = upload.Update(uploadGitBranchMsg{name: "feature", autoGitBranchEnabled: &disabled, defaultBranchName: "base"})
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyEsc})
	upload = next.(uploadBuildModel)
	if upload.branchContextLoading || upload.gitBranch != "feature" || !strings.Contains(upload.View(), "Default branch (base)") {
		t.Fatalf("picker swallowed loaded app branch context: %s", upload.View())
	}
}
