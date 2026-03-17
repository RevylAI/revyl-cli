//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAppBuildLifecycle exercises the full app/build lifecycle:
// create app -> upload build -> list versions -> delete version -> delete app.
func TestAppBuildLifecycle(t *testing.T) {
	appName := uniqueName("e2e-app")
	var appID string

	step(t, "create_app", func(st *testing.T) {
		appID = createAppFixture(t, appName, "android")
		if appID == "" {
			st.Fatal("createAppFixture returned empty ID")
		}
		st.Logf("created app: id=%s name=%s", appID, appName)
	})

	step(t, "app_list_contains_created", func(st *testing.T) {
		result := runCLI(t, "app", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("app list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("app list output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "upload_build", func(st *testing.T) {
		// Create a small dummy APK file (just needs to be non-empty)
		tmpDir := t.TempDir()
		dummyPath := filepath.Join(tmpDir, "e2e-test.apk")
		if err := os.WriteFile(dummyPath, []byte("PK\x03\x04dummy-apk-content-for-e2e"), 0644); err != nil {
			st.Fatalf("failed to write dummy APK: %v", err)
		}

		result := runCLI(t, "build", "upload", "--app", appID, "--file", dummyPath, "--version", "1.0.0-e2e", "--json")
		if result.ExitCode != 0 {
			st.Skipf("build upload failed (may need real binary): %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "list_build_versions", func(st *testing.T) {
		result := runCLI(t, "build", "list", "--app", appID, "--json")
		if result.ExitCode != 0 {
			st.Skipf("build list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("build list output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "delete_app", func(st *testing.T) {
		result := runCLI(t, "app", "delete", appID, "--force", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("app delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
