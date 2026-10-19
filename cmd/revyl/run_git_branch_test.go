package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/testutil"
)

func executionSourceCheckout(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	gitInitBuildRepository(t, directory)
	if output, err := exec.Command("git", "-C", directory, "symbolic-ref", "HEAD", "refs/heads/Feature/git-branch").CombinedOutput(); err != nil {
		t.Fatalf("set source branch: %v: %s", err, output)
	}
	return directory
}

func configureGitBranchRunFlags(t *testing.T) {
	t.Helper()
	previousRetries, previousNoWait, previousOpen := runRetries, runNoWait, runOpen
	previousBranch, previousMode, previousBuild := runBranchName, runBuildSelection, runBuildID
	previousTestBuild, previousWorkflowBuild, previousJSON := runTestBuild, runWorkflowBuild, runOutputJSON
	t.Cleanup(func() {
		runRetries, runNoWait, runOpen = previousRetries, previousNoWait, previousOpen
		runBranchName, runBuildSelection, runBuildID = previousBranch, previousMode, previousBuild
		runTestBuild, runWorkflowBuild, runOutputJSON = previousTestBuild, previousWorkflowBuild, previousJSON
	})
	runRetries, runNoWait, runOpen = 1, true, false
	runBranchName, runBuildSelection, runBuildID = "", "", ""
	runTestBuild, runWorkflowBuild, runOutputJSON = false, false, false
}

func TestRunConfiglessTestCarriesGitBranchSeparatelyFromSelectors(t *testing.T) {
	for _, scenario := range []struct{ name, branch, mode, build, wantGitBranch string }{
		{"automatic", "", "", "", "Feature/git-branch"},
		{"latest", "", "latest", "", "Feature/git-branch"},
		{"explicit branch", "main", "", "", ""},
		{"exact build", "", "", "22222222-2222-4222-8222-222222222222", ""},
		{"all branches", "", "latest_all_branches", "", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			withWorkingDir(t, executionSourceCheckout(t))
			configureGitBranchRunFlags(t)
			runBranchName, runBuildSelection, runBuildID = scenario.branch, scenario.mode, scenario.build
			const testID = "11111111-1111-4111-8111-111111111111"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/tests/get_test_by_id/"+testID {
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"id":"` + testID + `","name":"Saved test","branch_id":"saved-branch","build_selection":"pinned","pinned_build_id":"saved-build"}`))
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			previous := runTestExecution
			t.Cleanup(func() { runTestExecution = previous })
			var requested execution.RunTestParams
			runTestExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
				requested = params
				return &execution.RunTestResult{TaskID: "task", Status: "queued"}, nil
			}
			if err := runTestExec(newWorkflowRunTestCommand(), []string{testID}); err != nil {
				t.Fatal(err)
			}
			if requested.GitBranchName != scenario.wantGitBranch || requested.BranchName != scenario.branch || requested.BuildSelection != scenario.mode || requested.BuildVersionID != scenario.build || requested.TestsDir != "" {
				t.Fatalf("context changed selector or required config: %+v", requested)
			}
		})
	}
}

func TestWorkflowRunGitBranchMatchesWaitingAndQueuedInvocations(t *testing.T) {
	for _, scenario := range []struct {
		name                                            string
		noWait, explicitBranch, mixedExact, allBranches bool
	}{
		{name: "queued", noWait: true},
		{name: "waiting"},
		{name: "explicit branch", noWait: true, explicitBranch: true},
		{name: "mixed exact and automatic", mixedExact: true},
		{name: "all branches", noWait: true, allBranches: true},
		{name: "waiting all branches", allBranches: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			withWorkingDir(t, executionSourceCheckout(t))
			configureGitBranchRunFlags(t)
			runNoWait, runBuildSelection = scenario.noWait, "latest"
			wantGitBranch := "Feature/git-branch"
			if scenario.explicitBranch {
				runBranchName, wantGitBranch = "main", ""
			}
			if scenario.allBranches {
				runBuildSelection, wantGitBranch = "latest_all_branches", ""
			}
			previousIOSApp, previousIOSBuild := runWorkflowIOSAppID, runWorkflowIOSBuildID
			t.Cleanup(func() { runWorkflowIOSAppID, runWorkflowIOSBuildID = previousIOSApp, previousIOSBuild })
			const appID = "33333333-3333-4333-8333-333333333333"
			if scenario.mixedExact {
				runWorkflowIOSAppID, runWorkflowIOSBuildID = appID, "22222222-2222-4222-8222-222222222222"
				runBuildSelection = ""
			}
			const workflowID = "11111111-1111-4111-8111-111111111111"
			var body map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/workflows/get_workflow_info":
					_, _ = w.Write([]byte(`{"id":"` + workflowID + `","name":"Workflow"}`))
				case "/api/v1/apps/" + appID:
					_, _ = w.Write([]byte(`{"id":"` + appID + `","name":"App","platform":"ios","versions_count":1,"latest_version":"1.0"}`))
				case "/api/v1/workflow-executions":
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					_, _ = w.Write([]byte(`{"task_id":"task"}`))
				default:
					t.Errorf("unexpected saved-target lookup: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			previous := runWorkflowExecution
			t.Cleanup(func() { runWorkflowExecution = previous })
			var requested execution.RunWorkflowParams
			runWorkflowExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunWorkflowParams) (*execution.RunWorkflowResult, error) {
				requested = params
				return &execution.RunWorkflowResult{TaskID: "task", Success: true, Status: "completed"}, nil
			}
			if err := runWorkflowExec(newWorkflowRunTestCommand(), []string{workflowID}); err != nil {
				t.Fatal(err)
			}
			if scenario.noWait {
				if body["build_selection"] != runBuildSelection {
					t.Fatalf("queued request=%v", body)
				}
				_, hasGitBranch := body["git_branch_name"]
				switch {
				case scenario.explicitBranch:
					if hasGitBranch || body["branch_name"] != "main" {
						t.Fatalf("explicit branch detected context: %v", body)
					}
				case scenario.allBranches:
					if hasGitBranch || body["branch_name"] != nil {
						t.Fatalf("All branches detected context: %v", body)
					}
				case body["git_branch_name"] != wantGitBranch || body["branch_name"] != nil:
					t.Fatalf("context became explicit or was omitted: %v", body)
				}
			} else if requested.GitBranchName != wantGitBranch || requested.BranchName != "" || requested.BuildSelection != runBuildSelection || requested.IOSBuildID != runWorkflowIOSBuildID {
				t.Fatalf("waiting request=%+v", requested)
			}
		})
	}
}

const missingBranchDetail = `Branch "typo" doesn't exist on "Shop iOS"`

func missingBranchServer(t *testing.T, startPath string) (*httptest.Server, *int) {
	t.Helper()
	starts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/11111111-1111-4111-8111-111111111111":
			_, _ = w.Write([]byte(`{"id":"11111111-1111-4111-8111-111111111111","name":"Smoke","app_id":"app"}`))
		case "/api/v1/workflows/get_workflow_info":
			_, _ = w.Write([]byte(`{"id":"11111111-1111-4111-8111-111111111111","name":"Smoke workflow"}`))
		case startPath:
			starts++
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["branch_name"] != "typo" {
				t.Errorf("start request branch = %v", request["branch_name"])
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"detail": missingBranchDetail, "code": "invalid_request"})
		case "/api/v1/telemetry/cli-analytics":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request after the server refused to start: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &starts
}

func assertRunNotStarted(t *testing.T, runErr error, stdout, stderr string, jsonOutput bool, starts int) {
	t.Helper()
	if runErr == nil || runErr.Error() != missingBranchDetail {
		t.Fatalf("error = %v, want the server's reason %q", runErr, missingBranchDetail)
	}
	var safe *analytics.SafeDiagnosticError
	if !errors.As(runErr, &safe) {
		t.Fatalf("analytics would record the app name: %T", runErr)
	}
	if starts != 1 {
		t.Fatalf("start requests = %d, want 1", starts)
	}
	for _, unwanted := range []string{"Workflow failed", "Test failed", "test failed", "Report:"} {
		if strings.Contains(stdout+stderr, unwanted) {
			t.Fatalf("output says %q for a run that never started:\nstdout:\n%s\nstderr:\n%s", unwanted, stdout, stderr)
		}
	}
	if !jsonOutput {
		if stdout != "" {
			t.Fatalf("human output wrote to stdout: %q", stdout)
		}
		return
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("JSON output %q: %v", stdout, err)
	}
	if result["error"] != missingBranchDetail || result["success"] != false || result["task_id"] != "" {
		t.Fatalf("JSON result = %v", result)
	}
	id, _ := result["test_id"].(string)
	if id == "" {
		id, _ = result["workflow_id"].(string)
	}
	if id == "" {
		t.Fatalf("JSON result names neither the test nor the workflow it refused: %v", result)
	}
}

func TestRunTestRefusedBeforeStartReportsTheServerReason(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[jsonOutput], func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			withWorkingDir(t, t.TempDir())
			configureGitBranchRunFlags(t)
			runNoWait, runBranchName, runOutputJSON = false, "typo", jsonOutput
			server, starts := missingBranchServer(t, "/api/v1/execution/api/execute_test_id_async")
			t.Setenv("REVYL_API_KEY", "key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			var runErr error
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
				runErr = runTestExec(newWorkflowRunTestCommand(), []string{"11111111-1111-4111-8111-111111111111"})
			})
			assertRunNotStarted(t, runErr, stdout, stderr, jsonOutput, *starts)
		})
	}
}

func TestWorkflowRunRefusedBeforeStartReportsTheServerReason(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		noWait, jsonOutput bool
	}{
		{"waiting human", false, false},
		{"waiting json", false, true},
		{"queued human", true, false},
		{"queued json", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			withWorkingDir(t, t.TempDir())
			configureGitBranchRunFlags(t)
			runNoWait, runBranchName, runOutputJSON = scenario.noWait, "typo", scenario.jsonOutput
			server, starts := missingBranchServer(t, "/api/v1/workflow-executions")
			t.Setenv("REVYL_API_KEY", "key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			var runErr error
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
				runErr = runWorkflowExec(newWorkflowRunTestCommand(), []string{"11111111-1111-4111-8111-111111111111"})
			})
			assertRunNotStarted(t, runErr, stdout, stderr, scenario.jsonOutput, *starts)
		})
	}
}

func TestRunJSONSaysWhichBuildEachRunUsed(t *testing.T) {
	testOutput := captureStdout(t, func() {
		outputTestResultJSON(newWorkflowRunTestCommand(), &execution.RunTestResult{
			TaskID: "task", Status: "completed", Success: true,
			BranchName: "main", BuildVersion: "v1.3", BuildID: "build-13",
		}, false)
	})
	testResult := parseJSON(t, testOutput)
	for key, want := range map[string]string{"branch_name": "main", "build_version": "v1.3", "build_id": "build-13"} {
		if testResult[key] != want {
			t.Fatalf("test run %s = %v, want %q", key, testResult[key], want)
		}
	}

	workflowOutput := captureStdout(t, func() {
		outputWorkflowResultJSON(&execution.RunWorkflowResult{TaskID: "workflow-task", Status: "completed", Tests: []execution.WorkflowTestResult{
			{TestName: "Login", Platform: "ios", BranchName: "main", BuildVersion: "v1.3", BuildID: "build-13"},
			{TestName: "Checkout", Platform: "android"},
		}})
	})
	tests, _ := parseJSON(t, workflowOutput)["tests"].([]interface{})
	if len(tests) != 2 {
		t.Fatalf("tests = %v", tests)
	}
	first, second := tests[0].(map[string]interface{}), tests[1].(map[string]interface{})
	if first["branch_name"] != "main" || first["build_version"] != "v1.3" || first["build_id"] != "build-13" {
		t.Fatalf("first test entry = %v", first)
	}
	for _, key := range []string{"branch_name", "build_version", "build_id"} {
		if value, present := second[key]; !present || value != "" {
			t.Fatalf("a test without a resolved build must still carry %s as empty: %v", key, second)
		}
	}
}

func TestRunJSONKeepsAmpersandsAndAngleBracketsLiteral(t *testing.T) {
	const testName = "Checkout & <Pay>"
	const failure = "expected <Pay> & saw nothing"
	testResult := &execution.RunTestResult{
		TaskID: "task", TestName: testName, Status: "failed", ErrorMessage: failure,
		ReportURL: "https://app.example.com/tests/report?taskId=task&tab=steps",
	}
	testOutput := captureStdout(t, func() {
		outputTestResultJSON(newWorkflowRunTestCommand(), testResult, false)
	})
	withDefault := newWorkflowRunTestCommand()
	recordAppliedDefault(withDefault, appliedDefault{Flag: "os-version", Value: "iOS 18.4", Reason: "newest_runtime_for_model"})
	testWithDefaultOutput := captureStdout(t, func() {
		outputTestResultJSON(withDefault, testResult, true)
	})
	if !strings.Contains(testWithDefaultOutput, `"defaults_applied"`) {
		t.Fatalf("test run JSON lost the applied default:\n%s", testWithDefaultOutput)
	}
	workflowOutput := captureStdout(t, func() {
		outputWorkflowResultJSON(&execution.RunWorkflowResult{
			TaskID: "workflow-task", WorkflowName: "Smoke & <Regression>", Status: "failed",
			Tests: []execution.WorkflowTestResult{{TestName: testName, Platform: "ios", ErrorMessage: failure}},
		})
	})
	for command, output := range map[string]string{
		"test run":                         testOutput,
		"test run with an applied default": testWithDefaultOutput,
		"workflow run":                     workflowOutput,
	} {
		if strings.Contains(output, `\u00`) {
			t.Fatalf("%s JSON escapes characters the GitHub Actions print verbatim:\n%s", command, output)
		}
		if !strings.Contains(output, `"`+testName+`"`) || !strings.Contains(output, `"`+failure+`"`) {
			t.Fatalf("%s JSON lost the literal test name or error:\n%s", command, output)
		}
		parseJSON(t, output)
	}
	if !strings.Contains(testOutput, "taskId=task&tab=steps") {
		t.Fatalf("test run JSON escaped the report link:\n%s", testOutput)
	}
}

func TestRunOutputNamesTheBuildsBranch(t *testing.T) {
	for _, test := range []struct{ build, branch, want string }{
		{"v1.2.3", "main", "v1.2.3 from branch main"},
		{"v1.2.3", "", "v1.2.3"},
		{"", "main", "a build from branch main"},
		{"", "", ""},
	} {
		if got := buildFromBranchLabel(test.build, test.branch); got != test.want {
			t.Errorf("buildFromBranchLabel(%q, %q) = %q, want %q", test.build, test.branch, got, test.want)
		}
	}
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		printWorkflowRunBuilds([]execution.WorkflowTestResult{
			{Platform: "ios", BuildVersion: "v2", BranchName: "feature"},
			{Platform: "ios", BuildVersion: "v2", BranchName: "feature"},
			{Platform: "android", BuildID: "android-build", BranchName: "main"},
			{Platform: "android"},
		})
	})
	if stdout != "" {
		t.Fatalf("build lines went to stdout: %q", stdout)
	}
	for _, want := range []string{"Build (ios): v2 from branch feature", "Build (android): android-build from branch main"} {
		if strings.Count(stderr, want) != 1 {
			t.Fatalf("stderr should name %q once:\n%s", want, stderr)
		}
	}
}
