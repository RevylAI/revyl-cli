package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

const (
	appTestProjectID = "11111111-1111-4111-8111-111111111111"
	appTestID        = "22222222-2222-4222-8222-222222222222"
	appTestOtherID   = "33333333-3333-4333-8333-333333333333"
)

func TestReplaceAppBindingUpdatesOnlySelectedRecipeWithCAS(t *testing.T) {
	repository := t.TempDir()
	gitInitForAppTest(t, repository)
	writeProjectAppTestConfig(t, repository, projectAppTestConfigYAML(""))

	project, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceAppBinding(project, projectAppRecipeRef{Profile: "development", Platform: "ios"}, appTestID); err != nil {
		t.Fatal(err)
	}

	after, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	development := after.Authored.Build.Profiles["development"]
	production := after.Authored.Build.Profiles["production"]
	if development.IOS.AppID == nil || *development.IOS.AppID != appTestID {
		t.Fatalf("development/ios app_id = %v, want %s", development.IOS.AppID, appTestID)
	}
	if production.IOS.AppID == nil || *production.IOS.AppID != appTestOtherID {
		t.Fatalf("production/ios app_id = %v, want unchanged %s", production.IOS.AppID, appTestOtherID)
	}
	if err := config.ReplaceConfigAtomically(project.ConfigPath, project.OriginalBytes, project.OriginalBytes); err == nil {
		t.Fatal("stale expected bytes unexpectedly replaced the saved config")
	}
}

func TestPrepareAppBindingRemovalClearsEveryCanonicalReference(t *testing.T) {
	repository := t.TempDir()
	gitInitForAppTest(t, repository)
	writeProjectAppTestConfig(t, repository, projectAppTestConfigYAML(appTestID))

	project, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	refs, replacement, err := prepareAppBindingRemoval(project, appTestID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := projectAppRefStrings(refs), []string{"development/android", "development/ios"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %v, want %v", got, want)
	}
	if err := config.ReplaceConfigAtomically(project.ConfigPath, replacement, project.OriginalBytes); err != nil {
		t.Fatal(err)
	}

	after, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	development := after.Authored.Build.Profiles["development"]
	production := after.Authored.Build.Profiles["production"]
	if development.IOS.AppID != nil || development.Android.AppID != nil {
		t.Fatalf("development bindings were not cleared: ios=%v android=%v", development.IOS.AppID, development.Android.AppID)
	}
	if production.IOS.AppID == nil || *production.IOS.AppID != appTestOtherID {
		t.Fatalf("unrelated production binding = %v, want %s", production.IOS.AppID, appTestOtherID)
	}
}

func TestRunAppCreateRejectsInvalidConfigBeforeRemoteSideEffect(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	repository := t.TempDir()
	gitInitForAppTest(t, repository)
	writeProjectAppTestConfig(t, repository, "project:\n  id: not-a-uuid\n")
	withWorkingDir(t, repository)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		t.Fatalf("remote request occurred before config preflight: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalName, originalPlatform, originalJSON := appCreateName, appCreatePlatform, appCreateJSON
	t.Cleanup(func() {
		appCreateName, appCreatePlatform, appCreateJSON = originalName, originalPlatform, originalJSON
	})
	appCreateName, appCreatePlatform, appCreateJSON = "Invalid Config App", "ios", false

	cmd := newLeafCommand("create", runAppCreate)
	if err := runAppCreate(cmd, nil); err == nil || !strings.Contains(err.Error(), "cannot inspect project config") {
		t.Fatalf("runAppCreate() error = %v, want canonical config preflight failure", err)
	}
	if requestCount != 0 {
		t.Fatalf("remote request count = %d, want 0", requestCount)
	}
}

func TestRunAppCreateJSONRemainsConfigless(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	workingDirectory := t.TempDir()
	withWorkingDir(t, workingDirectory)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": appTestID, "name": "JSON App", "platform": "iOS"})
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalName, originalPlatform, originalJSON := appCreateName, appCreatePlatform, appCreateJSON
	t.Cleanup(func() {
		appCreateName, appCreatePlatform, appCreateJSON = originalName, originalPlatform, originalJSON
	})
	appCreateName, appCreatePlatform, appCreateJSON = "JSON App", "ios", true

	cmd := newLeafCommand("create", runAppCreate)
	captureStdout(t, func() {
		if err := runAppCreate(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if requestCount != 1 {
		t.Fatalf("remote request count = %d, want 1", requestCount)
	}
}

func TestRunAppDeleteRejectsInvalidConfigBeforeRemoteDeletion(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	repository := t.TempDir()
	gitInitForAppTest(t, repository)
	writeProjectAppTestConfig(t, repository, "project:\n  id: not-a-uuid\n")
	withWorkingDir(t, repository)

	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appTestID:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": appTestID, "name": "Bound App", "platform": "iOS",
				"versions_count": 1, "latest_version": "1.0",
			})
		case r.Method == http.MethodDelete:
			deleteCalled = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalForce := appDeleteForce
	t.Cleanup(func() { appDeleteForce = originalForce })
	appDeleteForce = true

	cmd := newLeafCommand("delete", runAppDelete)
	if err := runAppDelete(cmd, []string{appTestID}); err == nil || !strings.Contains(err.Error(), "cannot inspect project config") {
		t.Fatalf("runAppDelete() error = %v, want canonical config preflight failure", err)
	}
	if deleteCalled {
		t.Fatal("remote delete occurred before config preflight")
	}
}

func TestRunAppDeleteByExplicitIDRemainsConfigless(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	workingDirectory := t.TempDir()
	withWorkingDir(t, workingDirectory)

	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appTestID:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": appTestID, "name": "Standalone App", "platform": "iOS",
				"versions_count": 1, "latest_version": "1.0",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/apps/"+appTestID:
			deleteCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"detached_tests": 0})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalForce := appDeleteForce
	t.Cleanup(func() { appDeleteForce = originalForce })
	appDeleteForce = true

	cmd := newLeafCommand("delete", runAppDelete)
	cmd.SetContext(context.Background())
	captureStdout(t, func() {
		if err := runAppDelete(cmd, []string{appTestID}); err != nil {
			t.Fatal(err)
		}
	})
	if !deleteCalled {
		t.Fatal("explicit configless app delete was not sent")
	}
}

func TestRunAppDeleteClearsCanonicalProfileBindings(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	repository := t.TempDir()
	gitInitForAppTest(t, repository)
	writeProjectAppTestConfig(t, repository, projectAppTestConfigYAML(appTestID))
	withWorkingDir(t, repository)

	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appTestID:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": appTestID, "name": "Bound App", "platform": "iOS",
				"versions_count": 1, "latest_version": "1.0",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/apps/"+appTestID:
			deleteCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"detached_tests": 0})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalForce := appDeleteForce
	t.Cleanup(func() { appDeleteForce = originalForce })
	appDeleteForce = true

	cmd := newLeafCommand("delete", runAppDelete)
	captureStdout(t, func() {
		if err := runAppDelete(cmd, []string{appTestID}); err != nil {
			t.Fatal(err)
		}
	})
	if !deleteCalled {
		t.Fatal("remote delete was not sent")
	}
	project, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	development := project.Authored.Build.Profiles["development"]
	production := project.Authored.Build.Profiles["production"]
	if development.IOS.AppID != nil || development.Android.AppID != nil {
		t.Fatalf("deleted app bindings remain: ios=%v android=%v", development.IOS.AppID, development.Android.AppID)
	}
	if production.IOS.AppID == nil || *production.IOS.AppID != appTestOtherID {
		t.Fatalf("unrelated production binding = %v, want %s", production.IOS.AppID, appTestOtherID)
	}
}

func projectAppRefStrings(refs []projectAppRecipeRef) []string {
	result := make([]string, len(refs))
	for i, ref := range refs {
		result[i] = ref.String()
	}
	return result
}

func gitInitForAppTest(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func writeProjectAppTestConfig(t *testing.T, root, source string) {
	t.Helper()
	directory := filepath.Join(root, ".revyl")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func projectAppTestConfigYAML(developmentAppID string) string {
	developmentApp := ""
	if developmentAppID != "" {
		developmentApp = "\n        app_id: " + developmentAppID
	}
	return `project:
  id: ` + appTestProjectID + `
build:
  framework: expo
  profiles:
    development:
      ios:
        build_commands: ["true"]` + developmentApp + `
      android:
        build_commands: ["true"]` + developmentApp + `
    production:
      ios:
        build_commands: ["true"]
        app_id: ` + appTestOtherID + "\n"
}
