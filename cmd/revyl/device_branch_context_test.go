package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestDeviceCommandsPreserveDefaultAndExplicitBranch(t *testing.T) {
	root := initDevRepository(t)
	setDevTestBranch(t, root, "feature/device")
	withWorkingDirectory(t, root)
	t.Setenv("REVYL_API_KEY", "test")
	for _, operation := range []string{"start", "install"} {
		for _, branch := range []string{"", "main"} {
			t.Run(operation+"/"+branch, func(t *testing.T) {
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
				cmd.Flags().String("build-selection", "latest", "")
				_ = cmd.Flags().Set("app-id", "11111111-1111-4111-8111-111111111111")
				_ = cmd.Flags().Set("json", "true")
				err := run(cmd, nil)
				if err == nil || !strings.Contains(err.Error(), "selected branch has no builds") {
					t.Fatalf("unexpected error: %v", err)
				}
				contextName, _ := body["context_branch_name"].(string)
				wantContext := ""
				if contextName != wantContext || body["build_selection"] != "latest" {
					t.Fatalf("selector=%#v want context %q", body, wantContext)
				}
			})
		}
	}
}
