package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/testutil"
)

func TestResolveRunTimeoutUsesRunDefaultSeparateFromConfigDefault(t *testing.T) {
	cfg := &config.ProjectConfig{
		Defaults: config.Defaults{
			Timeout: config.DefaultTimeoutSeconds,
		},
	}

	got := resolveRunTimeout(nil, cfg, execution.DefaultRunTimeoutSeconds)
	if got != execution.DefaultRunTimeoutSeconds {
		t.Fatalf("resolveRunTimeout() = %d, want %d", got, execution.DefaultRunTimeoutSeconds)
	}
}

func TestResolveRunTestProjectContextRejectsLegacyConfig(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, "project:\n  name: Legacy\n  org_id: org-1\n")

	_, err := resolveRunTestProjectContext(repository, "Login Flow")
	if err == nil || !strings.Contains(err.Error(), "config migrate") {
		t.Fatalf("resolveRunTestProjectContext() error = %v, want migration guidance", err)
	}
}

func TestResolveRunTestProjectContextKeepsUUIDConfigless(t *testing.T) {
	project, err := resolveRunTestProjectContext(t.TempDir(), "11111111-1111-4111-8111-111111111111")
	if err != nil || project != nil {
		t.Fatalf("resolveRunTestProjectContext() = (%#v, %v), want configless", project, err)
	}
}

func TestCompletedRunErrorsExcludeFormattedDurations(t *testing.T) {
	tests := []struct {
		name              string
		err               error
		forbiddenProperty string
	}{
		{
			name: "test",
			err: completedTestRunError(
				&execution.RunTestResult{
					TaskID:   "test-task-123",
					TestID:   "test-123",
					Status:   "failed",
					Duration: "12.4s",
				},
				errors.New("test failed"),
			),
			forbiddenProperty: "test_duration",
		},
		{
			name: "workflow",
			err: completedWorkflowRunError(
				&execution.RunWorkflowResult{
					TaskID:     "workflow-task-123",
					WorkflowID: "workflow-123",
					Status:     "failed",
					Duration:   "18.2s",
				},
				errors.New("workflow failed"),
			),
			forbiddenProperty: "workflow_duration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var completedErr *analytics.CompletedError
			if !errors.As(test.err, &completedErr) {
				t.Fatalf("error type = %T, want *analytics.CompletedError", test.err)
			}
			if _, ok := completedErr.Completion().Properties[test.forbiddenProperty]; ok {
				t.Fatalf("completion included formatted %q", test.forbiddenProperty)
			}
		})
	}
}

func TestRunTestExec_UsesPollingMonitoringMode(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	testutil.SetHomeDir(t, t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_simple_tests":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tests":[{"id":"test-uuid-001","name":"Login Flow","platform":"ios"}],"count":1}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	tmp := t.TempDir()
	gitInitBuildRepository(t, tmp)
	writeProjectBuildConfig(t, tmp, "project:\n  id: 11111111-1111-4111-8111-111111111111\n")
	withWorkingDir(t, tmp)

	originalRunTestExecution := runTestExecution
	originalRunNoWait := runNoWait
	originalRunOpen := runOpen
	originalRunRetries := runRetries
	originalRunOutputJSON := runOutputJSON
	originalRunBuildID := runBuildID
	originalRunLocation := runLocation
	t.Cleanup(func() {
		runTestExecution = originalRunTestExecution
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
		runBuildID = originalRunBuildID
		runLocation = originalRunLocation
	})

	var monitoringMode sse.MonitoringMode
	var testsDir string
	runTestExecution = func(ctx context.Context, apiKey string, cfg *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
		monitoringMode = params.MonitoringMode
		testsDir = params.TestsDir
		return &execution.RunTestResult{
			TaskID:    "task-123",
			ReportURL: "https://app.example/report/task-123",
		}, nil
	}
	runNoWait = true
	runOpen = false
	runRetries = 1
	runOutputJSON = false
	runBuildID = ""
	runLocation = ""

	cmd := newLeafCommand("run", runTestExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", execution.DefaultRunTimeoutSeconds, "")
	_ = cmd.Flags().Set("open", "false")

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runTestExec(cmd, []string{"Login Flow"})
	})
	if runErr != nil {
		t.Fatalf("runTestExec() error = %v", runErr)
	}
	if monitoringMode != sse.MonitoringModePolling {
		t.Fatalf("MonitoringMode = %q, want %q", monitoringMode, sse.MonitoringModePolling)
	}
	resolvedRoot, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolvedRoot, ".revyl", "tests"); testsDir != want {
		t.Fatalf("TestsDir = %q, want %q", testsDir, want)
	}
}

func TestRunWorkflowExec_UsesPollingMonitoringMode(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	testutil.SetHomeDir(t, t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/get_workflow_info" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"11111111-1111-4111-8111-111111111111","name":"workflow-by-id"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalRunWorkflowExecution := runWorkflowExecution
	originalRunNoWait := runNoWait
	originalRunOpen := runOpen
	originalRunRetries := runRetries
	originalRunOutputJSON := runOutputJSON
	originalRunWorkflowBuild := runWorkflowBuild
	originalRunWorkflowIOSAppID := runWorkflowIOSAppID
	originalRunWorkflowAndroidAppID := runWorkflowAndroidAppID
	originalRunLocation := runLocation
	t.Cleanup(func() {
		runWorkflowExecution = originalRunWorkflowExecution
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
		runWorkflowBuild = originalRunWorkflowBuild
		runWorkflowIOSAppID = originalRunWorkflowIOSAppID
		runWorkflowAndroidAppID = originalRunWorkflowAndroidAppID
		runLocation = originalRunLocation
	})

	var monitoringMode sse.MonitoringMode
	runWorkflowExecution = func(ctx context.Context, apiKey string, cfg *config.ProjectConfig, params execution.RunWorkflowParams) (*execution.RunWorkflowResult, error) {
		monitoringMode = params.MonitoringMode
		return &execution.RunWorkflowResult{
			Success:   true,
			TaskID:    "task-456",
			Status:    "completed",
			ReportURL: "https://app.example/report/task-456",
		}, nil
	}
	runNoWait = false
	runOpen = false
	runRetries = 1
	runOutputJSON = false
	runWorkflowBuild = false
	runWorkflowIOSAppID = ""
	runWorkflowAndroidAppID = ""
	runLocation = ""

	cmd := newLeafCommand("run", runWorkflowExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", execution.DefaultRunTimeoutSeconds, "")
	_ = cmd.Flags().Set("open", "false")

	var runErr error
	_ = captureStdout(t, func() {
		runErr = runWorkflowExec(cmd, []string{"11111111-1111-4111-8111-111111111111"})
	})
	if runErr != nil {
		t.Fatalf("runWorkflowExec() error = %v", runErr)
	}
	if monitoringMode != sse.MonitoringModePolling {
		t.Fatalf("MonitoringMode = %q, want %q", monitoringMode, sse.MonitoringModePolling)
	}
}
