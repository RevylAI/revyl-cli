package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/revyl/cli/internal/testutil"
	"github.com/spf13/cobra"
)

func TestDeviceArtifactExportsPreserveBytesAndConfineWrites(t *testing.T) {
	for _, command := range []*cobra.Command{deviceScreenshotCmd, deviceHierarchyCmd} {
		for _, symlink := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/symlink=%t", command.Name(), symlink), func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				out := filepath.Join(dir, "artifact output")
				original := "legacy artifact with longer contents"
				preservedPath := out
				if symlink {
					preservedPath = filepath.Join(t.TempDir(), "outside")
				}
				if err := os.WriteFile(preservedPath, []byte(original), 0o644); err != nil {
					t.Fatal(err)
				}
				if symlink {
					if err := os.Symlink(preservedPath, out); err != nil {
						t.Skipf("symlinks are unavailable: %v", err)
					}
				}
				output, runErr := runDeviceArtifactExport(t, command, out)
				contents, readErr := os.ReadFile(preservedPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if symlink {
					if runErr == nil || string(contents) != original || output != "" {
						t.Fatalf("unsafe export: error=%v contents=%q output=%q", runErr, contents, output)
					}
					return
				}
				want := `{"success":true,"action":"` + command.Name() + `"}`
				if runErr != nil || string(contents) != want {
					t.Fatalf("artifact export: error=%v contents=%q", runErr, contents)
				}
				var result map[string]string
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					t.Fatalf("invalid export JSON: %v\n%s", err, output)
				}
				if result["path"] != out || result["bytes"] != strconv.Itoa(len(contents)) {
					t.Fatalf("unexpected export result: %v", result)
				}
				info, err := os.Stat(out)
				if err != nil {
					t.Fatal(err)
				}
				testutil.AssertPOSIXPermissions(t, info, 0o600)
			})
		}
	}
}

func runDeviceArtifactExport(t *testing.T, command *cobra.Command, out string) (string, error) {
	t.Helper()
	flow := newIDTargetedFlowServer(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := newSessionTargetTestCommand()
	cmd.SetContext(ctx)
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().String("out", out, "")
	if err := cmd.Flags().Set("session-id", flowSessionID); err != nil {
		t.Fatal(err)
	}
	var runErr error
	output := captureStdout(t, func() { runErr = command.RunE(cmd, nil) })
	return output, runErr
}
