package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestDeviceCommandsInferFromConfiglessEffectiveDirectory(t *testing.T) {
	root := initDevRepository(t)
	setDevTestBranch(t, root, "feature/device")
	withWorkingDirectory(t, root)
	t.Setenv("REVYL_API_KEY", "test")
	for _, operation := range []string{"start", "install"} {
		for _, scenario := range []struct{ branch, mode string }{{"", "latest"}, {"main", "latest"}, {"", "latest_all_branches"}} {
			branch := scenario.branch
			t.Run(operation+"/"+branch+"/"+scenario.mode, func(t *testing.T) {
				var body map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/apps/resolve" {
						_ = json.NewDecoder(r.Body).Decode(&body)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"detail":"selected branch has no builds"}`))
						return
					}
					if strings.Contains(r.URL.Path, "start_device") || strings.Contains(r.URL.Path, "/install") {
						t.Errorf("unexpected worker dispatch: %s", r.URL.Path)
					}
					http.NotFound(w, r)
				}))
				defer server.Close()
				t.Setenv("REVYL_BACKEND_URL", server.URL)
				var cmd *cobra.Command
				var run func(*cobra.Command, []string) error
				if operation == "start" {
					cmd = newDeviceStartTestCommand(context.Background())
					_ = cmd.Flags().Set("platform", "android")
					run = deviceStartCmd.RunE
				} else {
					cmd = newDeviceInstallTestCommand(context.Background())
					run = deviceInstallCmd.RunE
				}
				cmd.Flags().String("branch", branch, "")
				cmd.Flags().String("build-selection", scenario.mode, "")
				_ = cmd.Flags().Set("app-id", "11111111-1111-4111-8111-111111111111")
				_ = cmd.Flags().Set("json", "true")
				err := run(cmd, nil)
				if err == nil || !strings.Contains(err.Error(), "selected branch has no builds") {
					t.Fatalf("unexpected error: %v", err)
				}
				gitBranchName, _ := body["git_branch_name"].(string)
				wantGitBranch := ""
				if branch == "" && scenario.mode == "latest" {
					wantGitBranch = "feature/device"
				}
				if gitBranchName != wantGitBranch || body["build_selection"] != scenario.mode {
					t.Fatalf("selector=%#v want context %q", body, wantGitBranch)
				}
			})
		}
	}
}

func TestDeviceCommandsReportContextFallback(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const workflowID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	const sessionID = "44444444-4444-4444-8444-444444444444"
	for _, operation := range []string{"start", "install"} {
		for _, branch := range []string{"", "base"} {
			t.Run(operation+"/"+branch, func(t *testing.T) {
				root := initDevRepository(t)
				setDevTestBranch(t, root, "feature/missing")
				withWorkingDirectory(t, root)
				writePersistedDeviceSession(t, root, workflowID)
				t.Setenv("REVYL_API_KEY", "test")
				fallback := branch == ""
				source := api.BranchSourceExplicitBranch
				if fallback {
					source = api.BranchSourceDefaultBranch
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v1/entity/users/get_user_uuid":
						_, _ = w.Write([]byte(`{"org_id":"org-1","email":"fixture@example.test"}`))
					case "/api/v1/execution/device-sessions/active":
						_ = json.NewEncoder(w).Encode(map[string]any{"org_id": "org-1", "sessions": []map[string]any{{"id": "sess-1", "workflow_run_id": workflowID, "platform": "ios", "user_email": "fixture@example.test"}}})
					case "/api/v1/apps/resolve":
						_ = json.NewEncoder(w).Encode(map[string]any{"version_id": buildID, "branch_id": "base-id", "branch_name": "base", "download_url": "https://artifact.example/app.apk", "branch_source": source, "git_branch_name": map[bool]any{true: "feature/missing", false: nil}[fallback]})
					case "/api/v1/apps/builds/" + buildID:
						_ = json.NewEncoder(w).Encode(map[string]any{"id": buildID, "app_id": appID})
					case "/api/v1/execution/start_device", "/api/v1/execution/device-proxy/" + workflowID + "/install":
						var body map[string]any
						_ = json.NewDecoder(r.Body).Decode(&body)
						if body["build_id"] != buildID {
							t.Errorf("lost selected build: %#v", body)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"workflow_run_id": workflowID, "session_id": sessionID, "Success": true})
					case "/api/v1/execution/streaming/worker-connection/" + workflowID:
						_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready", "worker_ws_url": "ws://" + r.Host + "/ws/stream?token=test"})
					case "/api/v1/execution/device-proxy/" + workflowID + "/health":
						_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				t.Setenv("REVYL_BACKEND_URL", server.URL)
				cmd, run := newDeviceInstallTestCommand(context.Background()), deviceInstallCmd.RunE
				if operation == "start" {
					cmd, run = newDeviceStartTestCommand(context.Background()), deviceStartCmd.RunE
					_ = cmd.Flags().Set("platform", "android")
				}
				cmd.Flags().String("branch", branch, "")
				cmd.Flags().String("build-selection", "", "")
				_ = cmd.Flags().Set("app-id", appID)
				_ = cmd.Flags().Set("json", "true")
				stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
					if err := run(cmd, nil); err != nil {
						t.Fatal(err)
					}
				})
				var result map[string]any
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("stdout is not JSON: %v: %s", err, stdout)
				}
				if result["build_id"] != buildID || result["branch_name"] != "base" || result["branch_source"] != string(source) || (result["git_branch_name"] == "feature/missing") != fallback {
					t.Fatalf("result=%#v", result)
				}
				if strings.Contains(stderr, `using the app's default branch "base"`) != fallback {
					t.Fatalf("fallback=%t stderr=%s", fallback, stderr)
				}
			})
		}
	}
}
