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

func TestResolveWorkflowCreateTestIDs_UsesNearestCanonicalProject(t *testing.T) {
	repository := t.TempDir()
	gitInitCreateRepository(t, repository)
	projectRoot := filepath.Join(repository, "mobile")
	nested := filepath.Join(projectRoot, "packages", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCanonicalCreateConfig(t, projectRoot, "project:\n  id: 00000000-0000-4000-8000-000000000001\n")
	testsDir := filepath.Join(projectRoot, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testsDir, "checkout.yaml"), []byte("_meta:\n  remote_id: 11111111-1111-4111-8111-111111111111\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := resolveWorkflowCreateTestIDs(nested, "checkout,22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatalf("resolveWorkflowCreateTestIDs() error = %v", err)
	}
	want := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("IDs = %#v, want %#v", ids, want)
	}
}

func TestResolveWorkflowCreateTestIDs_PreservesConfiglessServerReference(t *testing.T) {
	ids, err := resolveWorkflowCreateTestIDs(t.TempDir(), "server-test-id")
	if err != nil {
		t.Fatalf("resolveWorkflowCreateTestIDs() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != "server-test-id" {
		t.Fatalf("IDs = %#v, want configless server reference", ids)
	}
}

func TestResolveWorkflowCreateTestIDs_RejectsLegacyConfig(t *testing.T) {
	repository := t.TempDir()
	gitInitCreateRepository(t, repository)
	configDir := filepath.Join(repository, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("project:\n  name: legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveWorkflowCreateTestIDs(repository, "checkout")
	if err == nil {
		t.Fatal("expected strict canonical config error")
	}
	if !strings.Contains(err.Error(), "revyl config migrate --check") ||
		!strings.Contains(err.Error(), "revyl config migrate") {
		t.Fatalf("error = %v, want migration recovery commands", err)
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
