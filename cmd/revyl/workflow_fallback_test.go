package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func TestWorkflowRunFallbackNoticesOncePerAppBranch(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	withWorkingDir(t, executionSourceCheckout(t))
	configureGitBranchRunFlags(t)
	runNoWait, runOutputJSON = false, true
	const workflowID = "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/get_workflow_info" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"` + workflowID + `","name":"Smoke"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	previous := runWorkflowExecution
	t.Cleanup(func() { runWorkflowExecution = previous })
	runWorkflowExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunWorkflowParams) (*execution.RunWorkflowResult, error) {
		children := []api.CLIWorkflowChildExecution{
			{ID: "one", AppID: "ios", AppName: "iOS App", BranchID: "main-ios", BranchName: "base", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "Feature/git-branch"},
			{ID: "two", AppID: "ios", AppName: "iOS App", BranchID: "main-ios", BranchName: "base", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "Feature/git-branch"},
			{ID: "three", AppID: "android", AppName: "Android App", BranchID: "main-android", BranchName: "main", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "Feature/git-branch"},
			{ID: "strict", AppID: "strict", AppName: "Strict App", BranchID: "explicit", BranchName: "saved", BranchSource: api.BranchSourceExplicitBranch},
		}
		progress := &sse.WorkflowStatus{TaskID: "task", Status: "running", ChildTasks: children}
		params.OnProgress(progress)
		params.OnProgress(progress)
		tests := make([]execution.WorkflowTestResult, 0, len(children))
		for _, child := range children {
			tests = append(tests, execution.WorkflowTestResult{TaskID: child.ID, AppID: child.AppID, AppName: child.AppName, BranchID: child.BranchID, BranchName: child.BranchName, BranchSource: child.BranchSource, GitBranchName: child.GitBranchName})
		}
		return &execution.RunWorkflowResult{TaskID: "task", Success: true, Status: "completed", Tests: tests}, nil
	}
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		if err := runWorkflowExec(newWorkflowRunTestCommand(), []string{workflowID}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(stdout, "has no usable build") {
		t.Fatalf("notice polluted JSON: %s", stdout)
	}
	for _, app := range []string{"iOS App", "Android App"} {
		if strings.Count(stderr, fmt.Sprintf(`Git branch "Feature/git-branch" has no usable build on %q`, app)) != 1 {
			t.Fatalf("notice for %s duplicated or absent: %s", app, stderr)
		}
	}
	if strings.Contains(stderr, `"Strict App"`) {
		t.Fatal("strict selection claimed fallback")
	}
	// The final JSON object is independently parseable even on versions with legacy progress prefixes.
	offset := strings.Index(stdout, "{\n")
	if offset < 0 {
		t.Fatalf("JSON absent: %s", stdout)
	}
	var result struct {
		Tests []execution.WorkflowTestResult `json:"tests"`
	}
	if err := json.Unmarshal([]byte(stdout[offset:]), &result); err != nil || len(result.Tests) != 4 || result.Tests[0].BranchSource != api.BranchSourceDefaultBranch || result.Tests[0].GitBranchName != "Feature/git-branch" || result.Tests[3].BranchSource != api.BranchSourceExplicitBranch {
		t.Fatalf("result=%+v err=%v stdout=%s", result, err, stdout)
	}
}

func TestWorkflowStatusAndReportDescribeFallbackFromFinishedRun(t *testing.T) {
	const taskID = "11111111-1111-4111-8111-111111111111"
	const wantNotice = `Git branch "feature-login" has no usable build on "Example"; using the app's default branch "base".`
	fallbackChild := `"app_id":"app","app_name":"Example","branch_id":"branch","branch_name":"base","branch_source":"default_branch","git_branch_name":"feature-login"`
	for _, command := range []string{"status", "report"} {
		for _, jsonOutput := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%t", command, jsonOutput), func(t *testing.T) {
				directCalls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/workflows/get_with_last_status":
						_, _ = w.Write([]byte(`{"data":[{"id":"workflow","name":"Smoke"}]}`))
					case "/api/v1/workflows/status/history/workflow":
						_, _ = w.Write([]byte(`{"executions":[{"execution_id":"` + taskID + `","status":"completed"}]}`))
					case "/api/v1/workflows/status/" + taskID:
						directCalls++
						_, _ = w.Write([]byte(`{"execution_id":"` + taskID + `","status":"completed","child_tasks":[{"id":"child",` + fallbackChild + `},{"id":"other","app_id":"app","app_name":"Example","branch_id":"branch","branch_name":"base","branch_source":"default_branch","git_branch_name":"feature-login"},{"id":"strict","app_id":"strict","app_name":"Strict App","branch_name":"saved","branch_source":"explicit_branch"}]}`))
					case "/api/v1/workflows/share/unified-report":
						_, _ = w.Write([]byte(`{"workflow_task":{"task_id":"` + taskID + `","status":"completed"},"child_tasks":[{"task_id":"child",` + fallbackChild + `},{"task_id":"strict","app_id":"strict","app_name":"Strict App","branch_name":"saved","branch_source":"explicit_branch"}]}`))
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				withMockClient(t, server)
				oldStatus, oldReport := wfStatusOutputJSON, wfReportOutputJSON
				t.Cleanup(func() { wfStatusOutputJSON, wfReportOutputJSON = oldStatus, oldReport })
				wfStatusOutputJSON, wfReportOutputJSON = jsonOutput, jsonOutput
				stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
					if command == "status" {
						cmd := newLeafCommand("status", runWorkflowStatus)
						if err := runWorkflowStatus(cmd, []string{"Smoke"}); err != nil {
							t.Fatal(err)
						}
					} else {
						cmd := newLeafCommand("report", runWorkflowReport)
						if err := runWorkflowReport(cmd, []string{taskID}); err != nil {
							t.Fatal(err)
						}
					}
				})
				if command == "status" && directCalls != 1 {
					t.Fatalf("history status did not refresh child metadata: %d", directCalls)
				}
				if !jsonOutput {
					if strings.Count(stderr, wantNotice) != 1 || strings.Contains(stdout, "has no usable build") || strings.Contains(stderr, `"Strict App"`) {
						t.Fatalf("finished run notice absent, duplicated, or misplaced: stdout=%s stderr=%s", stdout, stderr)
					}
					return
				}
				parsed := parseJSON(t, stdout)
				tests := parsed["tests"].([]interface{})
				first := tests[0].(map[string]interface{})
				strict := tests[len(tests)-1].(map[string]interface{})
				if first["branch_source"] != "default_branch" || first["git_branch_name"] != "feature-login" || first["app_name"] != "Example" || first["branch_name"] != "base" {
					t.Fatalf("lost metadata: %v", first)
				}
				if _, present := strict["git_branch_name"]; present || strict["branch_source"] != "explicit_branch" {
					t.Fatalf("explicit branch claimed a Git branch: %v", strict)
				}
				if strings.Contains(stdout, "has no usable build") || strings.Contains(stderr, "has no usable build") {
					t.Fatal("structured command printed notice")
				}
			})
		}
	}
}

func TestWorkflowQueuedJSONOmitsFallback(t *testing.T) {
	stdout := captureStdout(t, func() { outputWorkflowResultJSON(&execution.RunWorkflowResult{Status: "queued", Success: true}) })
	parsed := parseJSON(t, stdout)
	if parsed["queued"] != true || strings.Contains(stdout, "branch_source") {
		t.Fatalf("queued result=%s", stdout)
	}
}

func TestWorkflowFallbackWarningUsesStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		notices := &ui.BranchFallbackNotices{}
		children := []api.CLIWorkflowChildExecution{{AppID: "app", AppName: "Example", BranchID: "base", BranchName: "base", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "feature/login"}}
		printWorkflowBranchFallbacks(notices, children)
		printWorkflowBranchFallbacks(notices, children)
	})
	if stdout != "" || strings.Count(stderr, "has no usable build") != 1 {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}
