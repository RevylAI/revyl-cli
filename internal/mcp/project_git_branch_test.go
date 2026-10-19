package mcp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func gitBranchProject(t *testing.T, branch string) string {
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

func TestMCPGitBranchRequiresExplicitCustomerProject(t *testing.T) {
	ambient := gitBranchProject(t, "host/checkout")
	bound := gitBranchProject(t, "customer/Bound")
	explicit := gitBranchProject(t, "customer/Explicit")
	for _, tc := range []struct {
		name, bound, requested, want string
	}{
		{"unbound server checkout", "", "", ""},
		{"environment binding", bound, "", "customer/Bound"},
		{"per-call binding", bound, explicit, "customer/Explicit"},
		{"configless project", "", explicit, "customer/Explicit"},
		{"non-git project", bound, t.TempDir(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{workDir: ambient, explicitProjectDir: tc.bound}
			got, err := s.detectGitBranch(context.Background(), tc.requested)
			if err != nil || got != tc.want {
				t.Fatalf("context = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Server{workDir: ambient}).detectGitBranch(ctx, explicit); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestMCPServerCapturesEnvironmentProjectBinding(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	project := gitBranchProject(t, "customer/env")
	t.Setenv("REVYL_PROJECT_DIR", project)
	s, err := NewServer("test", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVYL_PROJECT_DIR", gitBranchProject(t, "changed/environment"))
	got, err := s.detectGitBranch(context.Background(), "")
	if err != nil || got != "customer/env" {
		t.Fatalf("server binding drifted: %q, %v", got, err)
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

func TestMCPRunTestGitBranchRemainsSeparateFromOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, binding, mode, branch, build, want string
	}{
		{name: "bound Automatic", binding: "bound", want: "customer/feature"},
		{name: "bound latest", binding: "bound", mode: "latest", want: "customer/feature"},
		{name: "unbound host", want: ""},
		{name: "explicit branch", binding: "bound", branch: "staging"},
		{name: "exact build", binding: "bound", build: branchToolBuildID},
		{name: "bound all branches", binding: "bound", mode: "latest_all_branches"},
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
			s.workDir = gitBranchProject(t, "customer/feature")
			if tc.binding != "" {
				s.explicitProjectDir = s.workDir
			}
			_, _, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunTestInput{TestName: branchToolTestID, BranchName: tc.branch, BuildSelection: tc.mode, BuildVersionID: tc.build})
			if err != nil || sent == nil {
				t.Fatalf("execution boundary not reached: %v", err)
			}
			if got, _ := sent["git_branch_name"].(string); got != tc.want {
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
	_, out, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunTestInput{TestName: branchToolTestID, ProjectDir: gitBranchProject(t, "customer/configless")})
	if err != nil || sent["git_branch_name"] != "customer/configless" {
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
	s.explicitProjectDir = gitBranchProject(t, "customer/not-saved")
	_, out, err := s.handleUpdateTest(context.Background(), nil, UpdateTestInput{TestNameOrID: branchToolTestID, BuildSelection: "inherit", YAMLContent: "test:\n  blocks: []\n"})
	if err != nil || !out.Success || sent["branch_id"] != nil || sent["pinned_build_id"] != nil || sent["build_selection"] != "inherit" || sent["git_branch_name"] != nil {
		t.Fatalf("saved Automatic changed: %+v, %v, body=%#v", out, err, sent)
	}
}

func TestMCPWorkflowGitBranchSupportsMixedExactAndAutomaticApps(t *testing.T) {
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
					if body["git_branch_name"] != nil || body["pinned_build_id"] != branchToolBuildID {
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
			input := RunWorkflowInput{WorkflowName: branchToolWorkflowID, IOSAppID: branchToolAppID, AndroidAppID: branchToolAppID, IOSBuildID: branchToolBuildID, ProjectDir: gitBranchProject(t, "customer/workflow")}
			if exactAndroid {
				input.AndroidBuildID = branchToolBuildID
			}
			_, out, err := s.handleRunWorkflow(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, input)
			if err != nil || sent == nil {
				t.Fatalf("workflow boundary not reached: %+v, %v", out, err)
			}
			want := "customer/workflow"
			if exactAndroid {
				want = ""
			}
			if got, _ := sent["git_branch_name"].(string); got != want {
				t.Fatalf("context=%q, want %q", got, want)
			}
		})
	}
}

func TestMCPBuildListIgnoresProjectGitBranch(t *testing.T) {
	calls := 0
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/apps/"+branchToolAppID+"/builds" {
			t.Errorf("browsing resolved Git branch: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Has("branch_id") {
			t.Errorf("Git narrowed browsing: %s", r.URL)
		}
		respondBranchToolJSON(t, w, map[string]any{"items": []any{}, "total": 0})
	})
	_, out, err := s.handleListBuilds(context.Background(), nil, ListBuildsInput{AppID: branchToolAppID, ProjectDir: gitBranchProject(t, "customer/missing")})
	if err != nil || out.ErrorMessage != "" || out.BranchID != "" || out.BranchName != "" || calls != 1 {
		t.Fatalf("browsing=%+v, %v, calls=%d", out, err, calls)
	}
}

func TestMCPUploadGitBranchFrozenBeforeArtifactTransfer(t *testing.T) {
	project := gitBranchProject(t, "customer/upload")
	var sessionBody, finalBody map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/" + branchToolAppID + "/builds/upload-session":
			sessionBody = decodeBranchToolBody(t, r)
			setMCPGitBranch(t, project, "symbolic-ref", "HEAD", "refs/heads/customer/changed")
			respondBranchToolJSON(t, w, map[string]any{"upload_id": "frozen-session", "upload_url": "http://" + r.Host + "/artifact", "content_type": "application/octet-stream", "branch_id": branchToolBranchID, "branch_name": "customer/upload"})
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + branchToolAppID + "/builds":
			finalBody = decodeBranchToolBody(t, r)
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolBuildID, "version": "v1", "branch_id": branchToolBranchID, "branch_name": "customer/upload"})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	artifact := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(artifact, []byte("local transport fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, out, err := s.handleUploadBuild(context.Background(), nil, UploadBuildInput{AppID: branchToolAppID, FilePath: artifact, Version: "v1", ProjectDir: project})
	if err != nil || !out.Success || out.BranchID != branchToolBranchID {
		t.Fatalf("upload failed: %+v, %v", out, err)
	}
	if sessionBody["git_branch_name"] != "customer/upload" || sessionBody["branch_name"] != nil || finalBody["upload_id"] != "frozen-session" || finalBody["git_branch_name"] != nil {
		t.Fatalf("context/frozen identity changed: session=%#v final=%#v", sessionBody, finalBody)
	}
}
