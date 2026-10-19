package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestAllBranchesSelectionValidation(t *testing.T) {
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, test := range []struct {
		name      string
		selection BuildSelection
		wantErr   string
	}{
		{name: "alone", selection: BuildSelection{Mode: BuildSelectionModeLatestAllBranches}},
		{name: "with ignored Git branch", selection: BuildSelection{Mode: BuildSelectionModeLatestAllBranches, GitBranchName: "feature/login"}},
		{name: "branch name", selection: BuildSelection{Mode: BuildSelectionModeLatestAllBranches, BranchName: "main"}, wantErr: "All branches · Latest build can't be combined with a branch"},
		{name: "branch ID", selection: BuildSelection{Mode: BuildSelectionModeLatestAllBranches, BranchID: branchTestID}, wantErr: "All branches · Latest build can't be combined with a branch"},
		{name: "exact build", selection: BuildSelection{Mode: BuildSelectionModeLatestAllBranches, BuildID: buildID}, wantErr: "an exact build can't be combined"},
		{name: "unknown mode", selection: BuildSelection{Mode: "everything"}, wantErr: "inherit, latest, latest_all_branches, or pinned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.selection.Validate()
			if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("Validate() = %v, want %q", err, test.wantErr)
			}
		})
	}
	saved, err := NewSavedBuildSelection("", string(BuildSelectionModeLatestAllBranches), "")
	if err != nil || saved.BranchID != nil || saved.PinnedBuildID != nil || saved.BuildSelection != BuildSelectionModeLatestAllBranches {
		t.Fatalf("saved all-branches selection = %+v, %v", saved, err)
	}
	if _, err := NewSavedBuildSelection(branchTestID, string(BuildSelectionModeLatestAllBranches), ""); err == nil {
		t.Fatal("a saved all-branches selection accepted a branch")
	}
}

func TestLatestAllBranchesRequestsNeverSendGitBranch(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const buildID = "33333333-3333-4333-8333-333333333333"
	bodies := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/telemetry/cli-traces" {
			http.Error(w, "collector unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"v1"}`, buildID, appID)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies[r.URL.Path] = body
		switch r.URL.Path {
		case "/api/v1/apps/resolve":
			fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"feature","resolution_status":"resolved"}`, buildID, branchTestID)
		case "/api/v1/execution/start_device":
			fmt.Fprint(w, `{"workflow_run_id":"00000000-0000-4000-8000-000000000001"}`)
		default:
			fmt.Fprint(w, `{"task_id":"task"}`)
		}
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test", server.URL)
	ctx := context.Background()
	const gitBranch = "feature/login"
	resolved, err := client.ResolveBuildSelectionMetadata(ctx, appID, BuildSelection{Mode: BuildSelectionModeLatestAllBranches, GitBranchName: gitBranch})
	if err != nil || resolved.BranchName != "feature" {
		t.Fatalf("resolve = %+v, %v", resolved, err)
	}
	testRequest := &ExecuteTestRequest{TestID: "test", BuildSelection: BuildSelectionModeLatestAllBranches, GitBranchName: gitBranch}
	if _, err := client.ExecuteTest(ctx, testRequest); err != nil {
		t.Fatal(err)
	}
	if testRequest.GitBranchName != gitBranch {
		t.Fatal("ExecuteTest changed the caller's request")
	}
	if _, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{WorkflowID: "workflow", BuildSelection: BuildSelectionModeLatestAllBranches, GitBranchName: gitBranch}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartDevice(ctx, &StartDeviceRequest{Platform: "ios", TestID: "test", BuildSelection: BuildSelectionModeLatestAllBranches, GitBranchName: gitBranch}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/apps/resolve", "/api/v1/execution/api/execute_test_id_async", "/api/v1/workflow-executions", "/api/v1/execution/start_device"} {
		body, ok := bodies[path]
		if !ok {
			t.Fatalf("no request to %s", path)
		}
		if body["build_selection"] != string(BuildSelectionModeLatestAllBranches) {
			t.Errorf("%s build_selection = %v", path, body["build_selection"])
		}
		for _, field := range []string{"git_branch_name", "branch_id", "branch_name"} {
			if _, sent := body[field]; sent {
				t.Errorf("%s sent %s with latest across all branches: %v", path, field, body)
			}
		}
	}
	if _, err := client.ExecuteTest(ctx, &ExecuteTestRequest{TestID: "test", BuildSelection: BuildSelectionModeLatest, GitBranchName: gitBranch}); err != nil {
		t.Fatal(err)
	}
	if bodies["/api/v1/execution/api/execute_test_id_async"]["git_branch_name"] != gitBranch {
		t.Fatal("a branch-scoped run lost its Git branch")
	}
}
