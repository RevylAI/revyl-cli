package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
)

func writeConfiglessLocalYAML(t *testing.T, path, name, buildName string) {
	t.Helper()

	content := `test:
  metadata:
    name: "` + name + `"
    platform: "ios"
  build:
    name: "` + buildName + `"
  blocks:
    - type: instructions
      step_description: "Open inbox"
`

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
}

func TestRunTestsPush_BootstrapsConfigWithoutProjectConfig(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".revyl", "credentials.json"), []byte(`{"api_key":"file-key","org_id":"org-stale"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(credentials.json) error = %v", err)
	}

	var createReq map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/builds/vars":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"id":"app-ios","name":"ios-test","platform":"ios","versions_count":2,"latest_version":"1.0.0"}],"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false}`))
		case "/api/v1/tests/create":
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				t.Fatalf("Decode create request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"remote-test-1","version":3}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	testsDir := filepath.Join(tmp, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	testName := "yahoo-login-onboarding-inbox-ios"
	testPath := filepath.Join(testsDir, testName+".yaml")
	writeConfiglessLocalYAML(t, testPath, testName, "ios-test")

	originalPushDryRun := testsPushDryRun
	originalTestsForce := testsForce
	t.Cleanup(func() {
		testsPushDryRun = originalPushDryRun
		testsForce = originalTestsForce
	})
	testsPushDryRun = false
	testsForce = false

	cmd := newLeafCommand("push", runTestsPush)
	if err := runTestsPush(cmd, []string{testName}); err != nil {
		t.Fatalf("runTestsPush() error = %v", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig() error = %v", err)
	}
	if got := cfg.Tests[testName]; got != "remote-test-1" {
		t.Fatalf("cfg.Tests[%q] = %q, want remote-test-1", testName, got)
	}

	localTest, err := config.LoadLocalTest(testPath)
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if got := localTest.Meta.RemoteID; got != "remote-test-1" {
		t.Fatalf("local remote_id = %q, want remote-test-1", got)
	}
	if got := localTest.Meta.RemoteVersion; got != 3 {
		t.Fatalf("local remote_version = %d, want 3", got)
	}
	if got := createReq["app_id"]; got != "app-ios" {
		t.Fatalf("app_id = %v, want app-ios", got)
	}
	if got := createReq["org_id"]; got != "org-live" {
		t.Fatalf("org_id = %v, want org-live", got)
	}
}

func TestRunTestsPushDryRun_WorksWithoutProjectConfig(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", "https://example.invalid")
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	testsDir := filepath.Join(tmp, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	testName := "local-only-test"
	writeConfiglessLocalYAML(t, filepath.Join(testsDir, testName+".yaml"), testName, "ios-test")

	originalPushDryRun := testsPushDryRun
	originalTestsForce := testsForce
	t.Cleanup(func() {
		testsPushDryRun = originalPushDryRun
		testsForce = originalTestsForce
	})
	testsPushDryRun = true
	testsForce = false

	cmd := newLeafCommand("push", runTestsPush)
	if err := runTestsPush(cmd, []string{testName}); err != nil {
		t.Fatalf("runTestsPush() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmp, ".revyl", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config file unexpectedly created during dry-run: %v", err)
	}
}

func TestRunTestExec_ResolvesRemoteNameWithoutProjectConfig(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

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
	t.Cleanup(func() {
		runTestExecution = originalRunTestExecution
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
	})

	resolvedID := ""
	runTestExecution = func(ctx context.Context, apiKey string, cfg *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
		resolvedID = params.TestNameOrID
		return &execution.RunTestResult{
			TaskID:    "task-123",
			ReportURL: "https://app.example/report/task-123",
		}, nil
	}
	runNoWait = true
	runOpen = false
	runRetries = 1
	runOutputJSON = false

	cmd := newLeafCommand("run", runTestExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", 3600, "")
	if err := cmd.Flags().Set("open", "false"); err != nil {
		t.Fatalf("Set(open) error = %v", err)
	}

	if err := runTestExec(cmd, []string{"Login Flow"}); err != nil {
		t.Fatalf("runTestExec() error = %v", err)
	}
	if resolvedID != "test-uuid-001" {
		t.Fatalf("resolved test id = %q, want test-uuid-001", resolvedID)
	}
}
