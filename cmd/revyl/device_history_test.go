package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const deviceHistoryTestAppID = "11111111-1111-4111-8111-111111111111"

// newDeviceHistoryTestCommand builds a fresh command carrying device history's flags.
func newDeviceHistoryTestCommand(t *testing.T, flags map[string]string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	for _, name := range []string{"app", "app-id", "branch", "build", "from", "to", "source", "status", "platform"} {
		cmd.Flags().String(name, "", "")
	}
	cmd.Flags().Int("limit", 20, "")
	cmd.Flags().Int("offset", 0, "")
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().Bool("dev", false, "")
	for name, value := range flags {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	return cmd
}

// declaredHistoryParameters names the query parameters the history endpoint's
// contract declares. The backend silently ignores any other parameter.
func declaredHistoryParameters() map[string]bool {
	params := reflect.TypeOf(api.GetSessionHistoryApiV1ExecutionDeviceSessionsHistoryGetParams{})
	declared := make(map[string]bool, params.NumField())
	for i := 0; i < params.NumField(); i++ {
		name, _, _ := strings.Cut(params.Field(i).Tag.Get("form"), ",")
		declared[name] = true
	}
	return declared
}

func serveDeviceHistory(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-key")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
}

func TestDeviceHistoryBranchFilterReachesTheHistoryContract(t *testing.T) {
	declared := declaredHistoryParameters()
	for _, appFlag := range []string{"app", "app-id"} {
		t.Run(appFlag, func(t *testing.T) {
			var query url.Values
			serveDeviceHistory(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/execution/device-sessions/history" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				query = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"sessions":[{"id":"session-1","platform":"ios","status":"completed","created_at":"2026-10-12T00:00:00Z","branch_name":"release/2.0"}],"total":1,"limit":20,"offset":0}`))
			})

			cmd := newDeviceHistoryTestCommand(t, map[string]string{appFlag: deviceHistoryTestAppID, "branch": "release/2.0"})
			var runErr error
			stdout := captureStdout(t, func() { runErr = deviceHistoryCmd.RunE(cmd, nil) })
			if runErr != nil {
				t.Fatalf("device history: %v", runErr)
			}
			if query.Get("app_id") != deviceHistoryTestAppID || query.Get("branch_name") != "release/2.0" {
				t.Fatalf("query = %v, want the app and branch_name filters", query)
			}
			for key := range query {
				if !declared[key] {
					t.Errorf("device history sends %q, which the history endpoint does not declare", key)
				}
			}
			var result api.CLIDeviceSessionHistoryResponse
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Sessions) != 1 || result.Sessions[0].BranchName != "release/2.0" {
				t.Fatalf("--json output = %q (%v), want the branch's session", stdout, err)
			}
		})
	}
}

func TestDeviceHistorySurfacesAnUnknownBranch(t *testing.T) {
	serveDeviceHistory(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Branch \"release/2.0\" doesn't exist on \"My App\""}`))
	})

	err := deviceHistoryCmd.RunE(newDeviceHistoryTestCommand(t, map[string]string{"app-id": deviceHistoryTestAppID, "branch": "release/2.0"}), nil)
	want := `failed to fetch session history: Branch "release/2.0" doesn't exist on "My App"`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestDeviceHistoryBranchRequiresAnApp(t *testing.T) {
	serveDeviceHistory(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})

	err := deviceHistoryCmd.RunE(newDeviceHistoryTestCommand(t, map[string]string{"branch": "release/2.0"}), nil)
	if err == nil || err.Error() != "--branch requires --app or --app-id" {
		t.Fatalf("error = %v, want the flag requirement", err)
	}
}
