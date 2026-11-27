package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func TestWorkflowCreateDoesNotExposeRetiredConfigSyncFlag(t *testing.T) {
	if flag := workflowCreateCmd.Flags().Lookup("no-sync"); flag != nil {
		t.Fatalf("workflow create still exposes retired flag %q", flag.Name)
	}
	if strings.Contains(workflowDeleteCmd.Long, "config alias") {
		t.Fatalf("workflow delete still describes retired config aliases: %q", workflowDeleteCmd.Long)
	}
	if strings.Contains(workflowRunCmd.Long, ".revyl/config.yaml") {
		t.Fatalf("workflow run still describes the retired workflow alias cache: %q", workflowRunCmd.Long)
	}
}

func TestCreateRemoteTest_UsesValidatedOrgIDAndPreservesRequestFields(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	tasks := []interface{}{
		map[string]interface{}{
			"type":      "module_import",
			"module":    "Login module",
			"module_id": "mod-1",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/tests/create":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}

			if got := req["org_id"]; got != "org-live" {
				t.Fatalf("org_id = %v, want org-live", got)
			}
			if got := req["app_id"]; got != "app-123" {
				t.Fatalf("app_id = %v, want app-123", got)
			}
			taskList, ok := req["tasks"].([]any)
			if !ok || len(taskList) != 1 {
				t.Fatalf("tasks = %#v, want single task", req["tasks"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-1","version":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)
	resp, err := createRemoteTest(context.Background(), client, "dfa", "ios", tasks, "app-123")
	if err != nil {
		t.Fatalf("createRemoteTest() error = %v", err)
	}
	if resp.ID != "test-1" {
		t.Fatalf("ID = %q, want test-1", resp.ID)
	}
}

func TestCreateRemoteTest_FallsBackToValidatedOrgIDAndNormalizesEmptyTasks(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			validateCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/tests/create":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if got := req["org_id"]; got != "org-live" {
				t.Fatalf("org_id = %v, want org-live", got)
			}
			if got := req["app_id"]; got != "app-live" {
				t.Fatalf("app_id = %v, want app-live", got)
			}
			taskList, ok := req["tasks"].([]any)
			if !ok || len(taskList) != 0 {
				t.Fatalf("tasks = %#v, want empty list", req["tasks"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-2","version":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	resp, err := createRemoteTest(context.Background(), client, "dfa", "android", nil, "app-live")
	if err != nil {
		t.Fatalf("createRemoteTest() error = %v", err)
	}
	if resp.ID != "test-2" {
		t.Fatalf("ID = %q, want test-2", resp.ID)
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}

func TestCreateForceFailsWhenSavedSelectionUpdateIsRejected(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	projectRoot := t.TempDir()
	gitInitCreateRepository(t, projectRoot)
	writeCanonicalCreateConfig(t, projectRoot, "project:\n  id: 00000000-0000-4000-8000-000000000001\n")
	t.Chdir(projectRoot)
	oldPlatform, oldAppID, oldMode := createTestPlatform, createTestAppID, createTestBuildSelection
	oldForce, oldNoOpen := createTestForce, createTestNoOpen
	t.Cleanup(func() {
		createTestPlatform, createTestAppID, createTestBuildSelection = oldPlatform, oldAppID, oldMode
		createTestForce, createTestNoOpen = oldForce, oldNoOpen
	})
	createTestPlatform, createTestAppID, createTestBuildSelection = "ios", "app", "latest"
	createTestForce, createTestNoOpen = true, true
	updated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tests/get_simple_tests":
			_, _ = w.Write([]byte(`{"tests":[{"id":"test","name":"Existing"}]}`))
		case "/api/v1/tests/update/test":
			updated = true
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["build_selection"] != "latest" || body["branch_id"] != nil || body["pinned_build_id"] != nil {
				t.Errorf("selection=%v", body)
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"Selected app is unavailable"}`))
		default:
			t.Errorf("unexpected request after failed update: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	err := runCreateTest(newLeafCommand("create", runCreateTest), []string{"Existing"})
	if !updated || err == nil || !strings.Contains(err.Error(), "update existing test app and build selection") {
		t.Fatalf("updated=%t err=%v", updated, err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".revyl", "tests", "Existing.yaml")); !os.IsNotExist(err) {
		t.Fatalf("failed update created local success state: %v", err)
	}
}

func gitInitCreateRepository(t *testing.T, root string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func writeCanonicalCreateConfig(t *testing.T, projectRoot, contents string) {
	t.Helper()
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTestFromFileBindsAppFlagAndPrintsOneJSONObject(t *testing.T) {
	for name, platformLine := range map[string]string{"platform in yaml": "    platform: ios\n", "platform from app": ""} {
		t.Run(name, func(t *testing.T) { assertCreateTestFromFileBindsApp(t, platformLine) })
	}
}

func assertCreateTestFromFileBindsApp(t *testing.T, platformLine string) {
	t.Setenv("REVYL_API_KEY", "test-key")
	testutil.SetHomeDir(t, t.TempDir())
	const appID = "11111111-2222-4333-8444-555555555555"
	var createReq map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/apps/" + appID:
			_, _ = w.Write([]byte(`{"id":"` + appID + `","name":"Checkout App","platform":"ios"}`))
		case "/api/v1/apps/":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + appID + `","name":"Checkout App","platform":"ios"}],"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false}`))
		case "/api/v1/tests/create":
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"remote-test-1","version":1}`))
		case "/api/v1/tests/scripts":
			_, _ = w.Write([]byte(`{"scripts":[],"count":0}`))
		case "/api/v1/modules/list":
			_, _ = w.Write([]byte(`{"message":"ok","result":[]}`))
		case "/api/v1/variables/custom/delete_all":
			_, _ = w.Write([]byte(`{"message":"deleted all"}`))
		case "/api/v1/tests/yaml/validate-yaml":
			_, _ = w.Write([]byte(`{"is_valid":true,"validation_type":"full_test","errors":0,"warnings":0,"messages":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	workDir := t.TempDir()
	gitInitBuildRepository(t, workDir)
	writeProjectBuildConfig(t, workDir, "project:\n  id: 11111111-1111-4111-8111-111111111111\n")
	withWorkingDir(t, workDir)
	sourcePath := filepath.Join(workDir, "checkout.yaml")
	source := "test:\n  metadata:\n    name: checkout\n" + platformLine + "  blocks:\n    - type: instructions\n      step_description: Open checkout\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	oldFromFile, oldAppID, oldForce, oldDryRun := createTestFromFile, createTestAppID, createTestForce, createTestDryRun
	oldPushForce, oldPushDryRun := testsForce, testsPushDryRun
	t.Cleanup(func() {
		createTestFromFile, createTestAppID, createTestForce, createTestDryRun = oldFromFile, oldAppID, oldForce, oldDryRun
		testsForce, testsPushDryRun = oldPushForce, oldPushDryRun
	})
	createTestFromFile, createTestAppID, createTestForce, createTestDryRun = sourcePath, appID, false, false
	testsPushDryRun = false
	cmd := newLeafCommand("create", runCreateTestFromFile)
	if err := cmd.Root().PersistentFlags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}

	var runErr error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { runErr = runCreateTestFromFile(cmd, nil) })

	if runErr != nil {
		t.Fatalf("runCreateTestFromFile() error = %v", runErr)
	}
	if createReq["app_id"] != appID {
		t.Fatalf("create app_id = %v, want %s", createReq["app_id"], appID)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var report map[string]any
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if decoder.More() {
		t.Fatalf("stdout has more than one JSON value:\n%s", stdout)
	}
	if report["is_valid"] != true || report["files"] == nil {
		t.Fatalf("report = %#v, want is_valid and files", report)
	}
	saved, err := os.ReadFile(filepath.Join(workDir, ".revyl", "tests", "checkout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "platform: ios") {
		t.Fatalf("saved test has no platform:\n%s", saved)
	}
}
