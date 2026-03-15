package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/testutil"
)

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
	withWorkingDir(t, tmp)

	originalRunTestExecution := runTestExecution
	originalRunNoWait := runNoWait
	originalRunOpen := runOpen
	originalRunRetries := runRetries
	originalRunOutputJSON := runOutputJSON
	originalRunBuildID := runBuildID
	originalRunLocation := runLocation
	originalRunHotReload := runHotReload
	t.Cleanup(func() {
		runTestExecution = originalRunTestExecution
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
		runBuildID = originalRunBuildID
		runLocation = originalRunLocation
		runHotReload = originalRunHotReload
	})

	var monitoringMode sse.MonitoringMode
	runTestExecution = func(ctx context.Context, apiKey string, cfg *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
		monitoringMode = params.MonitoringMode
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
	runHotReload = false

	cmd := newLeafCommand("run", runTestExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", 3600, "")
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
}

func TestRunWorkflowExec_UsesPollingMonitoringMode(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	testutil.SetHomeDir(t, t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

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
	cmd.Flags().Int("timeout", 3600, "")
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
