package mcp

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func branchContextProject(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	setMCPGitBranch(t, dir, "init", "--quiet", "--initial-branch="+branch)
	return dir
}

func setMCPGitBranch(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git fixture: %v: %s", err, output)
	}
}

func TestMCPRuntimeSchemasExposeCustomerProject(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	s, err := NewServer("test", false)
	if err != nil {
		t.Fatal(err)
	}
	tools := listServerTools(t, s)
	for _, name := range []string{"run_test", "run_workflow", "list_builds", "upload_build"} {
		requireSchemaPropertyType(t, serverToolByName(t, tools, name).InputSchema, "project_dir", "string")
	}
}

func TestMCPRunTestContextRemainsSeparateFromOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, binding, mode, branch, build, want string
	}{
		{name: "bound Automatic", binding: "bound"},
		{name: "bound latest", binding: "bound", mode: "latest"},
		{name: "unbound host", want: ""},
		{name: "explicit branch", binding: "bound", branch: "staging"},
		{name: "exact build", binding: "bound", build: branchToolBuildID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				sent = decodeBranchToolBody(t, r)
				w.WriteHeader(http.StatusBadRequest)
				respondBranchToolJSON(t, w, map[string]any{"detail": "dispatch boundary"})
			})
			s.workDir = branchContextProject(t, "customer/feature")
			_, _, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunTestInput{TestName: branchToolTestID, BranchName: tc.branch, BuildSelection: tc.mode, BuildVersionID: tc.build})
			if err != nil || sent == nil {
				t.Fatalf("execution boundary not reached: %v", err)
			}
			if got, _ := sent["context_branch_name"].(string); got != tc.want {
				t.Fatalf("context=%q, want %q", got, tc.want)
			}
			if got, _ := sent["branch_name"].(string); got != tc.branch {
				t.Fatal("context became explicit override")
			}
		})
	}
}

func TestMCPRunTestExplicitProjectIgnoresHostConfig(t *testing.T) {
	var sent map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		sent = decodeBranchToolBody(t, r)
		w.WriteHeader(http.StatusBadRequest)
		respondBranchToolJSON(t, w, map[string]any{"detail": "dispatch boundary"})
	})
	if err := os.MkdirAll(filepath.Join(s.workDir, ".revyl"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.workDir, ".revyl", "config.yaml"), []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	_, out, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunTestInput{TestName: branchToolTestID, ProjectDir: branchContextProject(t, "customer/configless")})
	if err != nil || sent["context_branch_name"] != nil {
		t.Fatalf("host config interfered with bound project: %+v, %v", out, err)
	}
}

func TestMCPSavedSelectionDoesNotCaptureBoundGit(t *testing.T) {
	var sent map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_test_by_id/" + branchToolTestID:
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolTestID, "app_id": branchToolAppID})
		case "/api/v1/tests/update/" + branchToolTestID:
			sent = decodeBranchToolBody(t, r)
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolTestID, "version": 2})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	s.workDir = branchContextProject(t, "customer/not-saved")
	_, out, err := s.handleUpdateTest(context.Background(), nil, UpdateTestInput{TestNameOrID: branchToolTestID, BuildSelection: "inherit", YAMLContent: "test:\n  blocks: []\n"})
	if err != nil || !out.Success || sent["branch_id"] != nil || sent["pinned_build_id"] != nil || sent["build_selection"] != "inherit" || sent["context_branch_name"] != nil {
		t.Fatalf("saved Automatic changed: %+v, %v, body=%#v", out, err, sent)
	}
}

func TestMCPWorkflowContextSupportsMixedExactAndAutomaticApps(t *testing.T) {
	for _, exactAndroid := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed", true: "all exact"}[exactAndroid], func(t *testing.T) {
			var sent map[string]any
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/workflows/get_workflow_info" {
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolWorkflowID, "name": "workflow"})
					return
				}
				if r.URL.Path == "/api/v1/apps/resolve" {
					body := decodeBranchToolBody(t, r)
					if body["context_branch_name"] != nil || body["pinned_build_id"] != branchToolBuildID {
						t.Error("exact platform resolution changed")
					}
					respondBranchToolJSON(t, w, map[string]any{"version_id": branchToolBuildID, "branch_id": branchToolBranchID, "download_url": "https://artifact.example/app.apk"})
					return
				}
				if r.URL.Path == "/api/v1/apps/builds/"+branchToolBuildID {
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolBuildID, "app_id": branchToolAppID, "branch_id": branchToolBranchID, "version": "v1"})
					return
				}
				if r.URL.Path != "/api/v1/workflow-executions" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				sent = decodeBranchToolBody(t, r)
				w.WriteHeader(http.StatusBadRequest)
				respondBranchToolJSON(t, w, map[string]any{"detail": "dispatch boundary"})
			})
			input := RunWorkflowInput{WorkflowName: branchToolWorkflowID, IOSAppID: branchToolAppID, AndroidAppID: branchToolAppID, IOSBuildID: branchToolBuildID, ProjectDir: branchContextProject(t, "customer/workflow")}
			if exactAndroid {
				input.AndroidBuildID = branchToolBuildID
			}
			_, out, err := s.handleRunWorkflow(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, input)
			if err != nil || sent == nil {
				t.Fatalf("workflow boundary not reached: %+v, %v", out, err)
			}
			want := ""
			if got, _ := sent["context_branch_name"].(string); got != want {
				t.Fatalf("context=%q, want %q", got, want)
			}
		})
	}
}
