package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestDeviceMCPContextOnlyComesFromBoundProject(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	setDeviceMCPTestBranch(t, projectDir, "feature/customer")
	for _, tc := range []struct {
		name, binding, explicitDir, branch, wantContext string
	}{
		{name: "unbound server cwd"},
		{name: "bound project", binding: projectDir},
		{name: "per call project", explicitDir: projectDir},
		{name: "explicit branch wins", binding: projectDir, branch: "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"start", "install"} {
				t.Run(operation, func(t *testing.T) {
					var body map[string]any
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/api/v1/apps/resolve" {
							t.Errorf("unexpected dispatch: %s", r.URL.Path)
						}
						_ = json.NewDecoder(r.Body).Decode(&body)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"detail":"no build in selected branch"}`))
					}))
					defer backend.Close()
					client := api.NewClientWithBaseURL("test", backend.URL)
					server := &Server{workDir: projectDir, apiClient: client, sessionMgr: NewDeviceSessionManager(client, "")}
					if operation == "start" {
						_, output, err := server.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{Platform: "android", AppID: "app", ProjectDir: tc.explicitDir, BranchName: tc.branch, BuildSelection: "latest", NoOpen: true})
						if err != nil || output.Success || output.Error == "" {
							t.Fatalf("start=%+v err=%v", output, err)
						}
					} else {
						_, output, err := server.handleInstallApp(context.Background(), nil, InstallAppInput{AppID: "app", ProjectDir: tc.explicitDir, BranchName: tc.branch, BuildSelection: "latest"})
						if err != nil || output.Success || output.Error == "" {
							t.Fatalf("install=%+v err=%v", output, err)
						}
					}
					contextName, _ := body["context_branch_name"].(string)
					branchName, _ := body["branch_name"].(string)
					if contextName != tc.wantContext || branchName != tc.branch || body["build_selection"] != "latest" {
						t.Fatalf("wrong selector: %#v", body)
					}
				})
			}
		})
	}
}

func TestTestBackedDeviceStartForwardsHintWithoutResolvingSavedChoice(t *testing.T) {
	var body map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/telemetry/cli-traces" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/api/v1/execution/start_device" {
			t.Errorf("client tried to resolve server-owned saved choice: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"validation-only boundary"}`))
	}))
	defer backend.Close()
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test", backend.URL), "")
	_, _, err := manager.StartSession(context.Background(), StartSessionOptions{Platform: "android", TestID: "test", ContextBranchName: "feature/context"})
	if err == nil || body["context_branch_name"] != "feature/context" || body["test_id"] != "test" {
		t.Fatalf("body=%#v err=%v", body, err)
	}
}

func setDeviceMCPTestBranch(t *testing.T, dir, name string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/"+name).CombinedOutput(); err != nil {
		t.Fatalf("set test branch: %v: %s", err, output)
	}
}
