package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestDeviceListDisplayPreservesPhoneAndJSON(t *testing.T) {
	for _, test := range []struct {
		name, platform, appName, wantLabel string
	}{
		{name: "ios", platform: "ios", wantLabel: "ios"},
		{name: "android", platform: "android", wantLabel: "android"},
		{name: "web with app", platform: "web", appName: "Example App", wantLabel: "Web · Chrome · Example App"},
		{name: "web without app", platform: "web", wantLabel: "Web · Chrome"},
		{name: "unknown", platform: "linux", wantLabel: "linux"},
		{name: "missing", wantLabel: "Unknown"},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", test.name, jsonOutput), func(t *testing.T) {
				dir := t.TempDir()
				withWorkingDirectory(t, dir)
				t.Setenv("REVYL_API_KEY", "fixture-key")
				t.Setenv(sessionIDEnvVar, "")
				var detailReads atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/v1/entity/users/get_user_uuid":
						_, _ = w.Write([]byte(`{"org_id":"fixture-org"}`))
					case "/api/v1/execution/device-sessions/active":
						_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{{"id": "fixture-session", "platform": test.platform, "app_name": test.appName}}})
					case "/api/v1/execution/device-sessions/fixture-session":
						detailReads.Add(1)
						_ = json.NewEncoder(w).Encode(map[string]any{"app_name": test.appName})
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(backend.Close)
				t.Setenv("REVYL_BACKEND_URL", backend.URL)
				now := time.Now().Truncate(time.Second)
				state := persistedDeviceSessionState{Active: 0, NextIdx: 1, Sessions: []persistedDeviceSession{{
					Index: 0, SessionID: "fixture-session", WorkflowRunID: "fixture-workflow", Platform: test.platform,
					StartedAt: now, LastActivity: now, IdleTimeout: time.Minute,
				}}}
				data, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".revyl", "device-sessions.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := &cobra.Command{Use: "list"}
				cmd.SetContext(context.Background())
				cmd.Flags().Bool("json", jsonOutput, "")
				cmd.Flags().Bool("dev", false, "")
				var runErr error
				stdout := captureStdout(t, func() { runErr = deviceListCmd.RunE(cmd, nil) })
				if runErr != nil {
					t.Fatal(runErr)
				}
				if jsonOutput {
					var sessions []map[string]any
					if err := json.Unmarshal([]byte(stdout), &sessions); err != nil {
						t.Fatalf("invalid JSON: %v", err)
					}
					if len(sessions) != 1 || sessions[0]["platform"] != test.platform || len(sessions[0]) != 9 {
						t.Fatalf("JSON contract changed: %s", stdout)
					}
					if detailReads.Load() != 0 {
						t.Fatal("JSON output fetched display-only metadata")
					}
					return
				}
				lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
				header := fmt.Sprintf("  %-3s %-10s %-10s %-12s %s", "#", "PLATFORM", "STATUS", "SESSION ID", "UPTIME")
				prefix := fmt.Sprintf("* %-3d %-10s %-10s %-12s ", 0, test.wantLabel, "running", "fixture-")
				if len(lines) != 2 || lines[0] != header || !strings.HasPrefix(lines[1], prefix) {
					t.Fatalf("human output = %q, want original header and row prefix %q", stdout, prefix)
				}
				if _, err := time.ParseDuration(strings.TrimPrefix(lines[1], prefix)); err != nil {
					t.Fatalf("uptime format changed: %q", lines[1])
				}
				if detailReads.Load() != 0 {
					t.Fatalf("display fetched session details %d times", detailReads.Load())
				}
			})
		}
	}
}
