package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devloop"
)

func TestDeviceMCPGitBranchOnlyComesFromBoundProject(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	setDeviceMCPTestBranch(t, projectDir, "feature/customer")
	for _, tc := range []struct {
		name, binding, explicitDir, branch, wantContext string
	}{
		{name: "unbound server cwd"},
		{name: "bound project", binding: projectDir, wantContext: "feature/customer"},
		{name: "per call project", explicitDir: projectDir, wantContext: "feature/customer"},
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
					server := &Server{workDir: projectDir, explicitProjectDir: tc.binding, apiClient: client, sessionMgr: NewDeviceSessionManager(client, "")}
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
					contextName, _ := body["git_branch_name"].(string)
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
	_, _, err := manager.StartSession(context.Background(), StartSessionOptions{Platform: "android", TestID: "test", GitBranchName: "feature/context"})
	if err == nil || body["git_branch_name"] != "feature/context" || body["test_id"] != "test" {
		t.Fatalf("body=%#v err=%v", body, err)
	}
}

func TestMCPDevStartFreezesPresenceAndAbsenceOfSourceHint(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	setDeviceMCPTestBranch(t, projectDir, "feature/dev")
	for _, tc := range []struct {
		name, binding, explicitDir, branch, wantContext string
	}{
		{name: "unbound fallback"},
		{name: "bound", binding: projectDir, wantContext: "feature/dev"},
		{name: "explicit project", explicitDir: projectDir, wantContext: "feature/dev"},
		{name: "explicit branch", binding: projectDir, branch: "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeDevLoopRunner{startResult: devloop.StartResult{Context: "default"}}
			server := &Server{workDir: projectDir, explicitProjectDir: tc.binding, devLoopRunner: runner}
			_, output, err := server.handleStartDevLoopCommand(context.Background(), nil, StartDevLoopInput{ProjectDir: tc.explicitDir, BranchName: tc.branch, Remote: true})
			if err != nil || !output.Success {
				t.Fatalf("start=%+v err=%v", output, err)
			}
			if runner.startRequest.GitBranchName == nil || *runner.startRequest.GitBranchName != tc.wantContext {
				t.Fatalf("MCP must explicitly hand off both hint and absence: %+v", runner.startRequest)
			}
		})
	}
}

func setDeviceMCPTestBranch(t *testing.T, dir, name string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/"+name).CombinedOutput(); err != nil {
		t.Fatalf("set test branch: %v: %s", err, output)
	}
}

func TestDeviceMCPReportsBranchSource(t *testing.T) {
	const workflowID = "22222222-2222-4222-8222-222222222222"
	const sessionID = "44444444-4444-4444-8444-444444444444"
	enabled, disabled := true, false
	for _, operation := range []string{"start", "start_test", "install"} {
		for _, test := range []struct {
			name, branch string
			enabled      *bool
		}{
			{name: "old server"},
			{name: "old server explicit", branch: "base"},
			{name: "enabled", enabled: &enabled},
			{name: "disabled", enabled: &disabled},
			{name: "disabled explicit", branch: "base", enabled: &disabled},
		} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				branch := test.branch
				fallback := branch == "" && (test.enabled == nil || *test.enabled)
				source := api.BranchSourceExplicitBranch
				if branch == "" {
					source = api.BranchSourceDefaultBranch
				}
				project := writeDevLoopTestProject(t)
				setDeviceMCPTestBranch(t, project, "feature/missing")
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/apps/resolve":
						_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "build", "branch_id": "base-id", "branch_name": "base", "download_url": "https://artifact.example/app.apk", "branch_source": source, "auto_git_branch_enabled": test.enabled, "git_branch_name": map[bool]any{true: "feature/missing", false: nil}[fallback]})
					case "/api/v1/apps/builds/build":
						_, _ = w.Write([]byte(`{"id":"build","app_id":"app"}`))
					case "/api/v1/execution/start_device", "/api/v1/execution/device-proxy/" + workflowID + "/install":
						var body map[string]any
						_ = json.NewDecoder(r.Body).Decode(&body)
						if operation == "start_test" {
							if body["test_id"] != "test" || body["build_id"] != nil {
								t.Errorf("changed server-owned test selection: %#v", body)
							}
						} else if body["build_id"] != "build" {
							t.Errorf("lost selected build: %#v", body)
						}
						started := map[string]any{"workflow_run_id": workflowID, "session_id": sessionID, "Success": true}
						if operation == "start_test" {
							started["branch_source"] = source
							started["auto_git_branch_enabled"] = test.enabled
							if fallback {
								started["git_branch_name"] = "feature/missing"
							}
						}
						_ = json.NewEncoder(w).Encode(started)
					case "/api/v1/execution/streaming/worker-connection/" + workflowID:
						_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready", "worker_ws_url": "ws://" + r.Host + "/ws/stream?token=test"})
					case "/api/v1/execution/device-proxy/" + workflowID + "/health":
						_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
					default:
						http.NotFound(w, r)
					}
				}))
				defer backend.Close()
				client := api.NewClientWithBaseURL("test", backend.URL)
				manager := NewDeviceSessionManager(client, t.TempDir())
				if operation == "install" {
					manager.sessions[0] = &DeviceSession{Index: 0, SessionID: sessionID, WorkflowRunID: workflowID, WorkerBaseURL: "https://worker.example", IdleTimeout: time.Minute}
					manager.activeIndex = 0
				}
				server := &Server{workDir: project, explicitProjectDir: project, apiClient: client, sessionMgr: manager}
				var output any
				if operation != "install" {
					input := StartDeviceSessionInput{Platform: "android", AppID: "app", BranchName: branch, NoOpen: true, DisableInheritedLaunchVars: true}
					if operation == "start_test" {
						input.AppID, input.TestID = "", "test"
					}
					_, result, err := server.handleStartDeviceSession(context.Background(), nil, input)
					if err != nil || !result.Success {
						t.Fatalf("result=%+v err=%v", result, err)
					}
					output = result
				} else {
					_, result, err := server.handleInstallApp(context.Background(), nil, InstallAppInput{AppID: "app", BranchName: branch})
					if err != nil || !result.Success {
						t.Fatalf("result=%+v err=%v", result, err)
					}
					output = result
				}
				encoded, err := json.Marshal(output)
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]any
				if err := json.Unmarshal(encoded, &result); err != nil {
					t.Fatal(err)
				}
				if result["branch_source"] != string(source) || (result["git_branch_name"] == "feature/missing") != fallback {
					t.Fatalf("result=%s", encoded)
				}
				if test.enabled == nil {
					if _, exists := result["auto_git_branch_enabled"]; exists {
						t.Fatalf("invented policy for old server: %s", encoded)
					}
				} else if result["auto_git_branch_enabled"] != *test.enabled {
					t.Fatalf("policy omitted or changed: %s", encoded)
				}
				if operation != "install" {
					state := readStoreTestState(t, manager.workDir)
					if len(state.Sessions) != 1 {
						t.Fatalf("persisted sessions=%+v", state.Sessions)
					}
					policy := state.Sessions[0].AutoGitBranchEnabled
					if (policy == nil) != (test.enabled == nil) || policy != nil && *policy != *test.enabled {
						t.Fatalf("persisted policy=%v, want %v", policy, test.enabled)
					}
				}
				if operation == "start_test" {
					if result["build_id"] != nil || result["branch_name"] != nil {
						t.Fatalf("invented server-owned build identity: %s", encoded)
					}
				} else if result["build_id"] != "build" || result["branch_name"] != "base" {
					t.Fatalf("result=%s", encoded)
				}
			})
		}
	}
}
