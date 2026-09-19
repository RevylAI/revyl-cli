package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestDeviceStartCommandSupportsOptionalAndLegacyConfig(t *testing.T) {
	tests := []struct {
		name            string
		config          string
		gitRepository   bool
		overrideTimeout bool
		wantTimeout     int
	}{
		{name: "no config or Git", wantTimeout: 300},
		{name: "no config in Git", gitRepository: true, wantTimeout: 300},
		{name: "legacy outside Git", config: "project:\n  name: old-app\nbuild:\n  system: Xcode\n", wantTimeout: 300},
		{name: "legacy in Git", config: "defaults:\n  timeout: 600\nbuild:\n  system: Xcode\n", gitRepository: true, wantTimeout: 600},
		{name: "legacy timeout overridden", config: "defaults:\n  timeout: 600\n", overrideTimeout: true, wantTimeout: 450},
		{name: "unrelated canonical build failure", config: "session:\n  idle_timeout_seconds: 600\nbuild:\n  profiles: invalid\n", gitRepository: true, wantTimeout: 600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetSessionRuntimes(t)
			root := newBeforeSessionRepo(t, test.config, "")
			if test.config == "" {
				if err := os.Remove(filepath.Join(root, ".revyl", "config.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			if test.gitRepository {
				if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
					t.Fatalf("git init: %v: %s", err, output)
				}
			}
			withWorkingDirectory(t, root)
			requests := mockDeviceConfigStart(t)
			cmd := newDeviceStartTestCommand(context.Background())
			flags := map[string]string{"platform": "ios", "build-version-id": "11111111-1111-4111-8111-111111111111", "json": "true"}
			if test.overrideTimeout {
				flags["timeout"] = "450"
			}
			for name, value := range flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatal(err)
				}
			}
			output := captureStdout(t, func() {
				if err := deviceStartCmd.RunE(cmd, nil); err != nil {
					t.Fatal(err)
				}
			})
			if !json.Valid([]byte(output)) {
				t.Fatal("device start did not return valid JSON")
			}
			request := <-requests
			if request.BuildID != flags["build-version-id"] || request.IdleTimeoutSeconds != test.wantTimeout {
				t.Fatalf("build = %q, timeout = %d; want %q, %d", request.BuildID, request.IdleTimeoutSeconds, flags["build-version-id"], test.wantTimeout)
			}
			if test.config != "" {
				data, err := os.ReadFile(filepath.Join(root, ".revyl", "config.yaml"))
				if err != nil || string(data) != test.config {
					t.Fatalf("device start changed config: %v", err)
				}
			}
		})
	}
}

func TestDeviceStartCommandPreservesLegacySessionSetup(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)
	root := newBeforeSessionRepo(t, `project:
  name: old-app
before_session:
  script: ./setup.sh
auth_bypass:
  launch_vars: [TEST_MODE]
`, "#!/bin/sh\necho TEST_MODE=enabled\n")
	withWorkingDirectory(t, root)
	requests := mockDeviceConfigStart(t)
	cmd := newDeviceStartTestCommand(context.Background())
	for name, value := range map[string]string{"platform": "ios", "json": "true"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	captureStdout(t, func() {
		if err := deviceStartCmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	request := <-requests
	if request.EnvVars["TEST_MODE"] != "enabled" || len(request.LaunchEnvVarIds) != 0 {
		t.Fatal("legacy session setup was not applied to the device request")
	}
}

func TestDeviceSessionManagerDoesNotParseProjectConfig(t *testing.T) {
	for _, content := range []string{
		"project:\n  name: legacy\nbuild:\n  system: Xcode\n",
		"session: [broken YAML\n",
	} {
		t.Run(content, func(t *testing.T) {
			root := newBeforeSessionRepo(t, content, "")
			withWorkingDirectory(t, root)
			t.Setenv("REVYL_API_KEY", "test-api-key")
			cmd := newSessionTargetTestCommand()
			if err := cmd.Flags().Set("session-id", flowSessionID); err != nil {
				t.Fatal(err)
			}
			manager, err := getDeviceSessionMgr(cmd)
			if err != nil {
				t.Fatal(err)
			}
			if manager.WorkDir() != root {
				t.Fatalf("workdir = %q, want %q", manager.WorkDir(), root)
			}
		})
	}
}

func TestDeviceStartCommandRejectsInvalidSessionBeforeProvisioning(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	for _, content := range []string{
		"session: [broken YAML\n",
		"session:\n  idle_timeout_seconds: invalid\n",
		"before_session:\n  script: ./setup.sh\n",
		"before_session:\n  scripts: ./setup.sh\n",
		"auth_bypass:\n  launch_var: [TEST_MODE]\n",
		"auth_bypass:\n  deeplink: example://auth\n",
	} {
		t.Run(content, func(t *testing.T) {
			resetSessionRuntimes(t)
			root := newBeforeSessionRepo(t, content, "#!/bin/sh\nexit 1\n")
			withWorkingDirectory(t, root)
			requests := mockDeviceConfigStart(t)
			cmd := newDeviceStartTestCommand(context.Background())
			for name, value := range map[string]string{"platform": "ios", "json": "true"} {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := deviceStartCmd.RunE(cmd, nil); err == nil {
				t.Fatal("expected invalid session setup to fail")
			}
			select {
			case <-requests:
				t.Fatal("invalid session setup provisioned a device")
			default:
			}
		})
	}
}

func mockDeviceConfigStart(t *testing.T) <-chan api.StartDeviceRequest {
	t.Helper()
	requests := make(chan api.StartDeviceRequest, 1)
	var started atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/builds/11111111-1111-4111-8111-111111111111":
			_, _ = w.Write([]byte(`{"id":"11111111-1111-4111-8111-111111111111","download_url":"https://example.com/app.zip","platform":"ios"}`))
		case "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		case "/api/v1/execution/start_device":
			if !started.CompareAndSwap(false, true) {
				t.Error("device start sent a duplicate provisioning request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var request api.StartDeviceRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			requests <- request
			_, _ = w.Write([]byte(`{"workflow_run_id":"` + flowWorkflowRunID + `","session_id":"` + flowSessionID + `"}`))
		case "/api/v1/execution/streaming/worker-connection/" + flowWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + flowWorkflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	return requests
}
