package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDirectUploadAppPreservesExplicitConfiglessUUID(t *testing.T) {
	previousApp := uploadAppFlag
	uploadAppFlag = "00000000-0000-4000-8000-000000000042"
	t.Cleanup(func() { uploadAppFlag = previousApp })

	appID, err := resolveDirectUploadApp(nil, nil, t.TempDir(), "ios", false)
	if err != nil {
		t.Fatalf("resolveDirectUploadApp() error = %v", err)
	}
	if appID != uploadAppFlag {
		t.Fatalf("appID = %q, want %q", appID, uploadAppFlag)
	}
}

func TestResolveDirectUploadAppRejectsNonInteractiveMissingBinding(t *testing.T) {
	previousApp := uploadAppFlag
	uploadAppFlag = ""
	t.Cleanup(func() { uploadAppFlag = previousApp })

	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, projectBuildConfigYAML("development", "ios", "build/app.app", false))

	_, err := resolveDirectUploadApp(nil, nil, repository, "ios", false)
	if err == nil || !strings.Contains(err.Error(), "--app <name-or-id>") || !strings.Contains(err.Error(), "build.profiles.development.ios.app_id") {
		t.Fatalf("resolveDirectUploadApp() error = %v, want noninteractive binding guidance", err)
	}
}

func TestBuildUploadPlatformHelpUsesCanonicalPlatforms(t *testing.T) {
	usage := buildUploadCmd.Flags().Lookup("platform").Usage
	if !strings.Contains(usage, "ios or android") || strings.Contains(usage, "ios-dev") || strings.Contains(usage, "build key") {
		t.Fatalf("--platform usage = %q", usage)
	}
}

func TestResolveUploadBindingUsesNearestCanonicalProjectFromNestedDirectory(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	projectRoot := filepath.Join(repository, "apps", "mobile")
	nested := filepath.Join(projectRoot, "src", "screens")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectBuildConfig(t, projectRoot, projectBuildConfigYAML("development", "android", "build/app.apk", true))

	binding, appID, err := resolveUploadBinding(nested, "android")
	if err != nil {
		t.Fatalf("resolveUploadBinding() error = %v", err)
	}
	if binding == nil {
		t.Fatal("resolveUploadBinding() binding = nil")
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ProjectRoot != resolvedProjectRoot || binding.Profile != "development" || binding.Platform != "android" {
		t.Fatalf("binding = %#v", binding)
	}
	if appID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("appID = %q", appID)
	}
}

func TestResolveUploadBindingAllowsConfiglessDirectUpload(t *testing.T) {
	binding, appID, err := resolveUploadBinding(t.TempDir(), "ios")
	if err != nil {
		t.Fatalf("resolveUploadBinding() error = %v", err)
	}
	if binding != nil || appID != "" {
		t.Fatalf("resolveUploadBinding() = (%#v, %q), want configless", binding, appID)
	}
}

func TestResolveUploadBindingRejectsLegacyConfig(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	configDirectory := filepath.Join(repository, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.yaml"), []byte(`project:
  name: Legacy
build:
  platforms:
    ios:
      command: xcodebuild
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolveUploadBinding(repository, "ios")
	if err == nil {
		t.Fatal("resolveUploadBinding() error = nil, want strict canonical-config rejection")
	}
}

func TestResolveUploadBindingRequiresExplicitAppForConflictingProfiles(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, `project:
  id: 00000000-0000-4000-8000-000000000099
build:
  framework: expo
  profiles:
    development:
      ios:
        app_id: 00000000-0000-4000-8000-000000000001
        build_commands: ["true"]
        output_path: build/dev.app
    release:
      ios:
        app_id: 00000000-0000-4000-8000-000000000002
        build_commands: ["true"]
        output_path: build/release.app
`)

	_, _, err := resolveUploadBinding(repository, "ios")
	if err == nil || !strings.Contains(err.Error(), "pass --app") {
		t.Fatalf("resolveUploadBinding() error = %v, want explicit app guidance", err)
	}
}

func TestResolveUploadBindingUsesSharedAppWithoutChoosingProfile(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, `project:
  id: 00000000-0000-4000-8000-000000000099
build:
  framework: expo
  profiles:
    development:
      android:
        app_id: 00000000-0000-4000-8000-000000000001
        build_commands: ["true"]
        output_path: build/dev.apk
    release:
      android:
        app_id: 00000000-0000-4000-8000-000000000001
        build_commands: ["true"]
        output_path: build/release.apk
`)

	binding, appID, err := resolveUploadBinding(repository, "android")
	if err != nil {
		t.Fatalf("resolveUploadBinding() error = %v", err)
	}
	if binding != nil {
		t.Fatalf("binding = %#v, want nil because no profile was selected", binding)
	}
	if appID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("appID = %q", appID)
	}
}

func TestResolvedUploadBindingPersistsWithExactByteCAS(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, projectBuildConfigYAML("development", "ios", "build/app.app", false))

	binding, appID, err := resolveUploadBinding(repository, "ios")
	if err != nil {
		t.Fatalf("resolveUploadBinding() error = %v", err)
	}
	if binding == nil || appID != "" {
		t.Fatalf("resolveUploadBinding() = (%#v, %q), want bindable empty app", binding, appID)
	}
	const selectedAppID = "00000000-0000-4000-8000-000000000007"
	if err := saveBuildAppBinding(*binding, selectedAppID); err != nil {
		t.Fatalf("saveBuildAppBinding() error = %v", err)
	}

	_, appID, err = resolveUploadBinding(repository, "ios")
	if err != nil {
		t.Fatalf("resolveUploadBinding() after save error = %v", err)
	}
	if appID != selectedAppID {
		t.Fatalf("appID after save = %q, want %q", appID, selectedAppID)
	}
	if err := saveBuildAppBinding(*binding, "00000000-0000-4000-8000-000000000008"); err == nil {
		t.Fatal("stale saveBuildAppBinding() error = nil, want CAS rejection")
	}
}
