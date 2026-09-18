package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/testutil"
)

func TestRootCommandIdentity(t *testing.T) {
	if rootCmd.Use != "revyl-computer" {
		t.Fatalf("root command = %q, want revyl-computer", rootCmd.Use)
	}
	cmd, _, err := rootCmd.Find([]string{"ssh"})
	if err != nil || cmd != sshCmd {
		t.Fatalf("find ssh = %v, %v", cmd, err)
	}
	if cmd.CommandPath() != "revyl-computer ssh" {
		t.Fatalf("command path = %q", cmd.CommandPath())
	}
	for _, name := range []string{"auth", "device", "test", "build"} {
		if _, _, err := rootCmd.Find([]string{name}); err == nil {
			t.Errorf("unexpected revyl subcommand %q", name)
		}
	}
}

func TestRootVersionOutput(t *testing.T) {
	var output strings.Builder
	rootCmd.SetOut(&output)
	rootCmd.SetArgs([]string{"--version"})
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetArgs(nil)
		_ = rootCmd.Flags().Set("version", "false")
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "revyl-computer version " + version + "\n"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestSSHRejectsMachineArgument(t *testing.T) {
	if err := sshCmd.Args(sshCmd, []string{"some-machine"}); err == nil {
		t.Fatal("ssh accepted a caller-supplied machine")
	}
}

func TestSSHRequiresAuthentication(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	sshCmd.SetContext(context.Background())
	if err := sshCmd.RunE(sshCmd, nil); err == nil || err.Error() != "not authenticated" {
		t.Fatalf("ssh without credentials = %v", err)
	}
}

func TestSSHUsesSharedRevylCredentials(t *testing.T) {
	for _, source := range []string{"environment", "saved"} {
		t.Run(source, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "")
			if source == "environment" {
				t.Setenv("REVYL_API_KEY", "test-api-key")
			} else if err := auth.NewManager().SaveAPIKeyCredentials("test-api-key", "user@example.com", "test-org", "test-user"); err != nil {
				t.Fatal(err)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/mac/shell-sessions" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-api-key" {
					t.Error("request did not use shared Revyl credentials")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 || r.URL.RawQuery != "" {
					t.Error("shell request must not send caller-supplied targeting")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"detail":"No machine available"}`)
			}))
			t.Cleanup(server.Close)
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			sshCmd.SetContext(context.Background())

			err := sshCmd.RunE(sshCmd, nil)
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				t.Fatalf("ssh error = %v, want backend 404", err)
			}
		})
	}
}
