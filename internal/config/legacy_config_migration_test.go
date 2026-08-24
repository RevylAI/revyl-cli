package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	migrationExistingProjectID  = "11111111-1111-4111-8111-111111111111"
	migrationExplicitProjectID  = "22222222-2222-4222-8222-222222222222"
	migrationGeneratedProjectID = "33333333-3333-4333-8333-333333333333"
)

func TestMigrateLegacyConfigBytesPreservesValidLegacyProjectID(t *testing.T) {
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(migrationLegacyConfig("  id: " + migrationExistingProjectID + "\n")),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyCanonical {
		t.Fatal("legacy config reported already canonical")
	}
	if result.ProjectID != migrationExistingProjectID || result.Authored.Project.ID != migrationExistingProjectID {
		t.Fatalf("project IDs = %q, %q", result.ProjectID, result.Authored.Project.ID)
	}
	if result.Aggregate.ProjectID != migrationExistingProjectID {
		t.Fatalf("aggregate project ID = %q", result.Aggregate.ProjectID)
	}
	parsed, err := ParseAuthoredConfig(result.CanonicalBytes)
	if err != nil {
		t.Fatalf("canonical proposal is not strict canonical config: %v", err)
	}
	if parsed.Project.ID != migrationExistingProjectID {
		t.Fatalf("canonical project ID = %q", parsed.Project.ID)
	}
	if strings.Contains(string(result.CanonicalBytes), "project name") || strings.Contains(string(result.CanonicalBytes), "open_browser") {
		t.Fatalf("retired legacy fields leaked into proposal:\n%s", result.CanonicalBytes)
	}
}

func TestMigrateLegacyConfigBytesProjectIDResolution(t *testing.T) {
	tests := []struct {
		name      string
		project   string
		explicit  string
		generated string
		want      string
	}{
		{
			name:      "explicit override wins",
			project:   "  id: " + migrationExistingProjectID + "\n",
			explicit:  migrationExplicitProjectID,
			generated: migrationGeneratedProjectID,
			want:      migrationExplicitProjectID,
		},
		{
			name:      "generated ID fills missing legacy identity",
			project:   "",
			generated: migrationGeneratedProjectID,
			want:      migrationGeneratedProjectID,
		},
		{
			name:      "generated ID replaces invalid legacy identity",
			project:   "  id: not-a-uuid\n",
			generated: migrationGeneratedProjectID,
			want:      migrationGeneratedProjectID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:               []byte(migrationLegacyConfig(test.project)),
				Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
				ExplicitProjectID:  test.explicit,
				GeneratedProjectID: test.generated,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ProjectID != test.want {
				t.Fatalf("project ID = %q, want %q", result.ProjectID, test.want)
			}
			if test.name == "generated ID replaces invalid legacy identity" {
				assertLegacyMigrationChange(t, result, "legacy_project_id_invalid", "project.id")
			}
		})
	}
}

func TestMigrateLegacyConfigBytesValidatesCallerProjectIDs(t *testing.T) {
	base := LegacyConfigMigrationInput{
		Data:    []byte(migrationLegacyConfig("")),
		Context: CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	}
	input := base
	input.ExplicitProjectID = "invalid"
	_, err := MigrateLegacyConfigBytes(input)
	assertConfigError(t, err, "legacy_translation", "explicit_project_id_invalid")

	input = base
	input.GeneratedProjectID = "invalid"
	_, err = MigrateLegacyConfigBytes(input)
	assertConfigError(t, err, "legacy_translation", "generated_project_id_invalid")

	_, err = MigrateLegacyConfigBytes(base)
	assertConfigError(t, err, "legacy_translation", "generated_project_id_required")
}

func TestMigrateLegacyConfigBytesReportsAlreadyCanonicalWithoutApplyingOverride(t *testing.T) {
	raw := "# keep local comments unless an explicit writer runs\n" + projectFileTestConfig(migrationExistingProjectID, "build")
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:              []byte(raw),
		Context:           CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		ExplicitProjectID: migrationExplicitProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyCanonical {
		t.Fatal("canonical config was not reported already canonical")
	}
	if result.ProjectID != migrationExistingProjectID {
		t.Fatalf("already-canonical project ID = %q, want authored %q", result.ProjectID, migrationExistingProjectID)
	}
	if strings.Contains(string(result.CanonicalBytes), "keep local comments") {
		t.Fatalf("canonical bytes retained comments: %s", result.CanonicalBytes)
	}
}

func TestMigrateLegacyConfigBytesHandlesLegacyDetectorPlaceholdersWithoutInventingFrameworks(t *testing.T) {
	t.Run("empty unknown build is omitted", func(t *testing.T) {
		result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
			Data:               []byte("project:\n  name: example\nbuild:\n  system: Unknown\n"),
			Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
			GeneratedProjectID: migrationGeneratedProjectID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Authored.Build != nil {
			t.Fatalf("build = %+v, want omitted", result.Authored.Build)
		}
	})

	t.Run("single-platform build tool uses platform framework", func(t *testing.T) {
		result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
			Data:               []byte("project:\n  name: example\nbuild:\n  system: Swift Package Manager\n  platforms:\n    ios:\n      command: swift build\n      output: build/App.app\n"),
			Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
			GeneratedProjectID: migrationGeneratedProjectID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Authored.Build == nil || result.Authored.Build.Framework != "ios" {
			t.Fatalf("build = %+v, want ios framework", result.Authored.Build)
		}
	})

	t.Run("mixed unsupported build is omitted", func(t *testing.T) {
		result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
			Data:               []byte("project:\n  name: example\nbuild:\n  system: Bazel\n  platforms:\n    ios:\n      command: bazel build //ios:app\n    android:\n      command: bazel build //android:app\n"),
			Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
			GeneratedProjectID: migrationGeneratedProjectID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Authored.Build != nil {
			t.Fatalf("build = %#v, want omitted", result.Authored.Build)
		}
		assertLegacyMigrationChange(t, result, "legacy_framework_ambiguous", "build.system")
	})
}

func TestMigrateLegacyConfigBytesExpandsLegacyYAMLAnchors(t *testing.T) {
	raw := `project:
  name: anchored
build:
  system: expo
  platforms:
    ios:
      command: npx expo prebuild --platform ios
      output: build/App.app
      env: &shared-env
        CI: "true"
        CHANNEL: preview
      caches:
        - key: shared-ios
          paths: &shared-paths
            - node_modules
    ios-automation:
      command: npx expo prebuild --platform ios
      output: build/Automation.app
      env:
        <<: *shared-env
        CHANNEL: automation
      caches:
        - key: automation-ios
          paths: *shared-paths
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	automation := result.Authored.Build.Profiles["automation"].IOS
	if automation == nil || !reflect.DeepEqual(automation.Env, map[string]string{"CI": "true", "CHANNEL": "automation"}) {
		t.Fatalf("expanded automation env = %#v", automation)
	}
	if len(automation.Caches) != 1 || !reflect.DeepEqual(automation.Caches[0].Paths, []string{"node_modules"}) {
		t.Fatalf("expanded automation caches = %#v", automation.Caches)
	}
	canonical := string(result.CanonicalBytes)
	if strings.Contains(canonical, "&shared") || strings.Contains(canonical, "*shared") || strings.Contains(canonical, "<<:") {
		t.Fatalf("canonical config retained YAML alias syntax:\n%s", canonical)
	}
}

func TestMigrateLegacyConfigBytesReportsYAMLSyntaxLine(t *testing.T) {
	_, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte("project:\n  name: broken\nbuild:\n  system: expo\n  platforms:\n    ios:\n      command: build\n      output: [unterminated\n"),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	var configError *ConfigError
	if !errors.As(err, &configError) || configError.Code != "invalid_yaml" || configError.Line <= 0 {
		t.Fatalf("error = %#v, want invalid_yaml with a source line", err)
	}
}

func TestMigrateLegacyConfigBytesResolvesEnabledReviewAppNames(t *testing.T) {
	raw := `project:
  name: named-app
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      framework: expo_ios
      app: Preview iOS
      build_command: npx expo prebuild --platform ios
      artifact_path: build/App.app
`
	input := LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	}
	_, err := MigrateLegacyConfigBytes(input)
	var required *LegacyAppLookupsRequired
	if !errors.As(err, &required) {
		t.Fatalf("error = %v, want app lookup signal", err)
	}
	wantLookups := []LegacyAppLookup{{
		Platform: "ios",
		Name:     "Preview iOS",
		Path:     []string{"pr_review", "builds", "ios", "app"},
	}}
	if !reflect.DeepEqual(required.Lookups, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", required.Lookups, wantLookups)
	}

	input.LegacyAppIDsByPlatformAndName = map[string]map[string]string{
		"ios": {"Preview iOS": migrationExistingProjectID},
	}
	result, err := MigrateLegacyConfigBytes(input)
	if err != nil {
		t.Fatal(err)
	}
	recipe := result.Authored.Build.Profiles["pr-review"].IOS
	if recipe == nil || recipe.AppID == nil || *recipe.AppID != migrationExistingProjectID {
		t.Fatalf("review recipe = %#v", recipe)
	}
	if result.Authored.PRReview == nil || result.Authored.PRReview.Build.Profile == nil || *result.Authored.PRReview.Build.Profile != "pr-review" {
		t.Fatalf("PR review = %#v", result.Authored.PRReview)
	}
	change := findLegacyMigrationChange(result, "legacy_app_reference_resolved", "pr_review.builds.ios.app")
	if change == nil || change.Disposition != "resolved" {
		t.Fatalf("resolved app migration change = %#v", change)
	}
}

func TestMigrateLegacyConfigBytesReportsUnresolvedEnabledReviewBuilds(t *testing.T) {
	raw := `project:
  name: unresolved-app
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      framework: expo_ios
      app: missing app
      build_command: npx expo prebuild --platform ios
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:                          []byte(raw),
		Context:                       CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID:            migrationGeneratedProjectID,
		LegacyAppIDsByPlatformAndName: map[string]map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	change := findLegacyMigrationChange(result, "legacy_app_reference_unresolved", "pr_review.builds.ios.app")
	if change == nil || change.Disposition != "omitted" || !strings.Contains(change.Message, "review build was omitted") {
		t.Fatalf("unresolved app migration change = %#v", change)
	}
	if result.Authored.PRReview != nil {
		t.Fatalf("PR review = %#v, want best-effort omission", result.Authored.PRReview)
	}
}

func TestMigrateLegacyConfigBytesInfersExpoAcrossLegacyNativeReviewLabels(t *testing.T) {
	raw := `project:
  name: expo-native-labels
build:
  platforms:
    ios:
      app_id: 11111111-1111-4111-8111-111111111111
      commands: [npm ci, npx expo prebuild --platform ios]
      output: build/App.app
    android:
      app_id: 22222222-2222-4222-8222-222222222222
      commands: [npm ci, npx expo prebuild --platform android]
      output: build/app.apk
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      framework: native_ios
      app: 11111111-1111-4111-8111-111111111111
      build_command: npx expo prebuild --platform ios
      artifact_path: build/App.app
    android:
      enabled: true
      framework: native_android
      app: 22222222-2222-4222-8222-222222222222
      build_command: npx expo prebuild --platform android
      artifact_path: build/app.apk
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authored.Build == nil || result.Authored.Build.Framework != "expo" {
		t.Fatalf("build = %#v, want inferred Expo framework", result.Authored.Build)
	}
	if result.Authored.Build.Profiles["development"].IOS == nil || result.Authored.Build.Profiles["development"].Android == nil ||
		result.Authored.Build.Profiles["pr-review"].IOS == nil || result.Authored.Build.Profiles["pr-review"].Android == nil {
		t.Fatalf("profiles = %#v, want both platforms preserved", result.Authored.Build.Profiles)
	}
	if result.Authored.PRReview == nil || result.Authored.PRReview.Build.Profile == nil || *result.Authored.PRReview.Build.Profile != "pr-review" {
		t.Fatalf("PR review = %#v", result.Authored.PRReview)
	}
}

func TestMigrateLegacyConfigBytesReportsDroppedLegacyBehavior(t *testing.T) {
	tests := []struct {
		name  string
		extra string
		code  string
		path  []string
	}{
		{name: "invalid top-level test id", extra: "tests:\n  login: not-a-uuid\n", code: "legacy_test_id_invalid", path: []string{"tests", "login"}},
		{name: "invalid top-level workflow cache", extra: "workflows:\n  smoke: not-a-uuid\n", code: "retired_workflow_alias_cache", path: []string{"workflows"}},
		{name: "unknown field", extra: "customer_behavior: enabled\n", code: "legacy_unsupported_field", path: []string{"customer_behavior"}},
		{name: "workflow alias lookup", extra: "pr_review:\n  enabled: true\n  skip_drafts: true\n  actions:\n    workflows: [smoke]\n", code: "legacy_workflow_reference_unresolved", path: []string{"pr_review", "actions", "workflows", "0"}},
		{name: "scalar path filters", extra: "pr_review:\n  enabled: true\n  path_filters: apps/mobile/**\n", code: "legacy_container_invalid", path: []string{"pr_review", "path_filters"}},
		{name: "invalid path filter", extra: "pr_review:\n  enabled: true\n  path_filters: [/apps/mobile/**]\n", code: "legacy_review_path_filter_invalid", path: []string{"pr_review", "path_filters", "0"}},
		{name: "scalar label filters", extra: "pr_review:\n  enabled: true\n  label_filters: mobile\n", code: "legacy_container_invalid", path: []string{"pr_review", "label_filters"}},
		{name: "scalar checks", extra: "pr_review:\n  enabled: true\n  actions:\n    checks: launch\n", code: "legacy_container_invalid", path: []string{"pr_review", "actions", "checks"}},
		{name: "scalar workflows", extra: "pr_review:\n  enabled: true\n  actions:\n    workflows: smoke\n", code: "legacy_container_invalid", path: []string{"pr_review", "actions", "workflows"}},
		{name: "blank workflow", extra: "pr_review:\n  enabled: true\n  actions:\n    workflows: ['']\n", code: "legacy_list_item_invalid", path: []string{"pr_review", "actions", "workflows", "0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := migrationLegacyConfig("")
			if strings.HasPrefix(test.extra, "build:") {
				raw = "project:\n  name: project name\n" + test.extra
			} else {
				raw += test.extra
			}
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:                    []byte(raw),
				Context:                 CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
				GeneratedProjectID:      migrationGeneratedProjectID,
				LegacyWorkflowIDsByName: map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			assertLegacyMigrationChange(t, result, test.code, strings.Join(test.path, "."))
		})
	}
}

func TestMigrateLegacyConfigBytesDropsOnlyInvalidReviewPathFilters(t *testing.T) {
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data: []byte(migrationLegacyConfig("") + `pr_review:
  enabled: true
  path_filters:
    - apps/mobile/**
    - /apps/mobile/**
`),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authored.PRReview == nil || result.Authored.PRReview.ReviewTriggers == nil {
		t.Fatalf("PR review = %#v, want preserved review policy", result.Authored.PRReview)
	}
	if got := result.Authored.PRReview.ReviewTriggers.Paths; !reflect.DeepEqual(got, []string{"apps/mobile/**"}) {
		t.Fatalf("review paths = %#v, want only valid path", got)
	}
	assertLegacyMigrationChange(t, result, "legacy_review_path_filter_invalid", "pr_review.path_filters.1")
}

func TestMigrateLegacyConfigBytesReportsOnlyExplicitSafeOmissions(t *testing.T) {
	raw := `project:
  name: legacy app
  org_id: legacy-org
build:
  system: expo
  no_build: false
  platforms:
    ios-dev:
      command: build
      output: build/app.app
      keep_derived_data: true
defaults:
  open_browser: true
auth_bypass:
  launch_vars: [REVYL_AUTH_BYPASS_TOKEN]
  deep_link: example://auth
  refresh_command: ./refresh
  refresh_interval: 60
last_synced_at: 2026-01-01T00:00:00Z
workflows:
  smoke: 44444444-4444-4444-8444-444444444444
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"auth_bypass.refresh_command",
		"auth_bypass.refresh_interval",
		"build.no_build",
		"build.platforms.ios-dev.keep_derived_data",
		"defaults.open_browser",
		"last_synced_at",
		"project.name",
		"project.org_id",
		"workflows",
	}
	gotPaths := make([]string, 0, len(result.Omissions))
	for _, omission := range result.Omissions {
		gotPaths = append(gotPaths, strings.Join(omission.Path, "."))
		if omission.Code == "" || omission.Message == "" || omission.Disposition == "" {
			t.Fatalf("unbounded omission = %#v", omission)
		}
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("omission paths = %#v, want %#v", gotPaths, wantPaths)
	}
	if result.Authored.Session == nil || result.Authored.Session.AuthBypass == nil {
		t.Fatalf("auth bypass was not preserved: %#v", result.Authored.Session)
	}
	if !reflect.DeepEqual(result.Authored.Session.AuthBypass.LaunchVars, []string{"REVYL_AUTH_BYPASS_TOKEN"}) ||
		result.Authored.Session.AuthBypass.DeepLink == nil || *result.Authored.Session.AuthBypass.DeepLink != "example://auth" {
		t.Fatalf("auth bypass = %#v", result.Authored.Session.AuthBypass)
	}
	canonical := string(result.CanonicalBytes)
	for _, retired := range []string{"keep_derived_data", "refresh_command", "refresh_interval"} {
		if strings.Contains(canonical, retired) {
			t.Fatalf("canonical config retained %s:\n%s", retired, canonical)
		}
	}
}

func TestMigrateLegacyConfigBytesReportsSemanticLossWithoutFailing(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		path string
	}{
		{
			name: "persisted browser preference",
			raw:  "project:\n  name: app\nbuild:\n  system: expo\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\ndefaults:\n  open_browser: false\n",
			path: "defaults.open_browser",
		},
		{
			name: "persisted no-build behavior",
			raw:  "project:\n  name: app\nbuild:\n  system: expo\n  no_build: true\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\n",
			path: "build.no_build",
		},
		{
			name: "remote source behavior",
			raw:  "project:\n  name: app\nbuild:\n  system: expo\n  source:\n    type: git\n    repo_url: https://example.invalid/app.git\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\n",
			path: "build.source",
		},
		{
			name: "authored hot-reload behavior",
			raw:  migrationLegacyConfig("") + "hotreload:\n  default: expo\n",
			path: "hotreload",
		},
		{
			name: "invalid hot-reload container",
			raw:  migrationLegacyConfig("") + "hotreload: expo\n",
			path: "hotreload",
		},
		{
			name: "disabled PR publication",
			raw:  migrationLegacyConfig("") + "pr_review:\n  enabled: true\n  actions:\n    preview_link: false\n",
			path: "pr_review.actions.preview_link",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:               []byte(test.raw),
				Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
				GeneratedProjectID: migrationGeneratedProjectID,
			})
			if err != nil {
				t.Fatal(err)
			}
			code := "retired_legacy_value"
			switch test.path {
			case "defaults.open_browser":
				code = "retired_browser_preference"
			case "build.no_build":
				code = "retired_no_build_preference"
			case "build.source":
				code = "retired_source_configuration"
			case "hotreload":
				if test.name == "invalid hot-reload container" {
					code = "retired_hotreload_configuration"
				} else {
					code = "retired_hotreload_configuration"
				}
			case "pr_review.actions.preview_link":
				code = "retired_preview_link"
			}
			assertLegacyMigrationChange(t, result, code, test.path)
		})
	}
}

func TestMigrateLegacyConfigBytesNormalizesLabelFilters(t *testing.T) {
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(migrationLegacyConfig("pr_review:\n  enabled: false\n  label_filters: [' mobile ', mobile, '', '!', ' !skip ', 7]\n")),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authored.PRReview == nil {
		t.Fatal("PR review was omitted")
	}
	got := result.Authored.PRReview.ReviewTriggers.Labels
	want := []string{"mobile", "!skip"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
	change := findLegacyMigrationChange(result, "legacy_label_filter_normalized", "pr_review.label_filters.0")
	if change == nil || change.Disposition != "resolved" {
		t.Fatalf("normalization change = %#v", change)
	}
}

func TestMigrateLegacyConfigBytesPreservesLegacyTestAliasesOutsideCanonicalYAML(t *testing.T) {
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(migrationLegacyConfig("") + "tests:\n  login-flow: 44444444-4444-4444-8444-444444444444\n  checkout: 55555555-5555-4555-8555-555555555555\nworkflows:\n  retired-cache: 66666666-6666-4666-8666-666666666666\n"),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAliases := []LegacyTestAlias{
		{Alias: "checkout", RemoteID: "55555555-5555-4555-8555-555555555555"},
		{Alias: "login-flow", RemoteID: "44444444-4444-4444-8444-444444444444"},
	}
	if !reflect.DeepEqual(result.TestAliases, wantAliases) {
		t.Fatalf("TestAliases = %#v, want %#v", result.TestAliases, wantAliases)
	}
	if strings.Contains(string(result.CanonicalBytes), "tests:") || strings.Contains(string(result.CanonicalBytes), "workflows:") {
		t.Fatalf("canonical config retained legacy alias maps:\n%s", result.CanonicalBytes)
	}
}

func TestMigrateLegacyConfigBytesOmitsUnsafeLegacyTestAliases(t *testing.T) {
	for _, alias := range []string{"../login", " login", "NUL", "Login", "login.name", "login."} {
		t.Run(alias, func(t *testing.T) {
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:               []byte(migrationLegacyConfig("") + "tests:\n  valid: 55555555-5555-4555-8555-555555555555\n  \"" + alias + "\": 44444444-4444-4444-8444-444444444444\n"),
				Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
				GeneratedProjectID: migrationGeneratedProjectID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.TestAliases, []LegacyTestAlias{{Alias: "valid", RemoteID: "55555555-5555-4555-8555-555555555555"}}) {
				t.Fatalf("test aliases = %#v, want valid sibling", result.TestAliases)
			}
			assertLegacyMigrationChange(t, result, "legacy_test_alias_invalid", "tests."+alias)
		})
	}
}

func TestMigrateLegacyConfigBytesResolvesLegacyWorkflowNames(t *testing.T) {
	raw := migrationLegacyConfig("") + "pr_review:\n  enabled: true\n  preset: smoke_every_pr\n  actions:\n    workflows: [custom, 55555555-5555-4555-8555-555555555555]\n"
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	var lookupsRequired *LegacyWorkflowLookupsRequired
	if !errors.As(err, &lookupsRequired) {
		t.Fatalf("error = %v, want workflow lookup signal", err)
	}
	wantLookups := []LegacyWorkflowLookup{{Name: "custom", Path: []string{"pr_review", "actions", "workflows", "0"}}}
	if !reflect.DeepEqual(lookupsRequired.Lookups, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", lookupsRequired.Lookups, wantLookups)
	}

	result, err = MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:                    []byte(raw),
		Context:                 CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID:      migrationGeneratedProjectID,
		LegacyWorkflowIDsByName: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Authored.PRReview.WorkflowIDs, []string{"55555555-5555-4555-8555-555555555555"}) {
		t.Fatalf("WorkflowIDs = %#v", result.Authored.PRReview.WorkflowIDs)
	}
	assertLegacyMigrationChange(t, result, "legacy_workflow_reference_unresolved", "pr_review.actions.workflows.0")

	result, err = MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:                    []byte(raw),
		Context:                 CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID:      migrationGeneratedProjectID,
		LegacyWorkflowIDsByName: map[string]string{"custom": "44444444-4444-4444-8444-444444444444"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"}
	if !reflect.DeepEqual(result.Authored.PRReview.WorkflowIDs, want) {
		t.Fatalf("WorkflowIDs = %#v, want %#v", result.Authored.PRReview.WorkflowIDs, want)
	}
	if strings.Contains(string(result.CanonicalBytes), "preset:") {
		t.Fatalf("canonical config retained legacy preset:\n%s", result.CanonicalBytes)
	}
	change := findLegacyMigrationChange(result, "legacy_workflow_reference_resolved", "pr_review.actions.workflows.0")
	if change == nil || change.Disposition != "resolved" {
		t.Fatalf("resolved workflow change = %#v", change)
	}
}

func TestMigrateLegacyConfigBytesReportsLocalDegradationWithoutWorkflowLookup(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		code string
		path string
	}{
		{name: "unknown field", raw: migrationLegacyConfig("") + "customer_behavior: enabled\npr_review:\n  enabled: true\n  actions:\n    workflows: [smoke]\n", code: "legacy_unsupported_field", path: "customer_behavior"},
		{name: "invalid boolean", raw: migrationLegacyConfig("") + "pr_review:\n  enabled: nope\n  actions:\n    workflows: [smoke]\n", code: "legacy_boolean_invalid", path: "pr_review.enabled"},
		{name: "invalid translated app ID", raw: "project:\n  name: project name\nbuild:\n  system: expo\n  platforms:\n    ios-dev:\n      app_id: not-a-uuid\n      command: build\n      output: build/app.app\npr_review:\n  enabled: true\n  actions:\n    workflows: [smoke]\n", code: "legacy_uuid_invalid", path: "build.platforms.ios-dev.app_id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:                    []byte(test.raw),
				Context:                 CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
				GeneratedProjectID:      migrationGeneratedProjectID,
				LegacyWorkflowIDsByName: map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			change := findLegacyMigrationChange(result, test.code, test.path)
			if change == nil {
				t.Fatalf("migration changes = %#v, want %s at %s", result.Omissions, test.code, test.path)
			}
			if test.code == "legacy_boolean_invalid" && change.Disposition != "defaulted" {
				t.Fatalf("invalid boolean disposition = %q", change.Disposition)
			}
		})
	}
}

func TestMigrateLegacyConfigBytesMaterializesSmokeDefaultOnlyWhenWorkflowsAbsent(t *testing.T) {
	base := migrationLegacyConfig("") + "pr_review:\n  enabled: true\n  preset: smoke_every_pr\n"
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(base),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	var lookupsRequired *LegacyWorkflowLookupsRequired
	if !errors.As(err, &lookupsRequired) {
		t.Fatalf("error = %v, want workflow lookup signal", err)
	}
	wantLookups := []LegacyWorkflowLookup{{Name: "smoke", Path: []string{"pr_review", "preset"}}}
	if !reflect.DeepEqual(lookupsRequired.Lookups, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", lookupsRequired.Lookups, wantLookups)
	}

	result, err = MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:                    []byte(base),
		Context:                 CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID:      migrationGeneratedProjectID,
		LegacyWorkflowIDsByName: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyMigrationChange(t, result, "legacy_workflow_reference_unresolved", "pr_review.preset")

	explicitEmpty := migrationLegacyConfig("") + "pr_review:\n  enabled: true\n  preset: smoke_every_pr\n  actions:\n    workflows: []\n"
	result, err = MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(explicitEmpty),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Authored.PRReview.WorkflowIDs) != 0 {
		t.Fatalf("WorkflowIDs = %#v, want explicit empty override", result.Authored.PRReview.WorkflowIDs)
	}
}

func TestMigrateLegacyConfigBytesSanitizedNativeIOSFullSurface(t *testing.T) {
	raw := `project:
  name: Native Example
build:
  system: Xcode
  command: xcodebuild top-level
  output: build/top-level.app
  platforms:
    ios:
      app_id: 22222222-2222-4222-8222-222222222222
      command: xcodebuild development
      output: build/development.app
defaults:
  open_browser: false
  timeout: 300
pr_review:
  enabled: true
  skip_drafts: true
  actions:
    preview_link: true
    proof_of_changes: true
  builds:
    ios:
      enabled: true
      framework: native_ios
      app: 22222222-2222-4222-8222-222222222222
      root_dir: .
      build_command: xcodebuild review
      artifact_path: build/review.app
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               []byte(raw),
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != migrationGeneratedProjectID || result.Authored.Build == nil || result.Authored.Build.Framework != "ios" {
		t.Fatalf("migration result = %#v", result)
	}
	development := result.Authored.Build.Profiles["development"].IOS
	if development == nil || development.AppID == nil || *development.AppID != "22222222-2222-4222-8222-222222222222" ||
		development.BuildCommands == nil || !reflect.DeepEqual(*development.BuildCommands, []string{"xcodebuild development"}) {
		t.Fatalf("development recipe = %#v", development)
	}
	review := result.Authored.Build.Profiles["pr-review"].IOS
	if review == nil || review.BuildCommands == nil || !reflect.DeepEqual(*review.BuildCommands, []string{"xcodebuild review"}) {
		t.Fatalf("review recipe = %#v", review)
	}
	if result.Authored.Session == nil || result.Authored.Session.IdleTimeoutSeconds == nil || *result.Authored.Session.IdleTimeoutSeconds != 300 {
		t.Fatalf("session = %#v", result.Authored.Session)
	}
	if result.Authored.PRReview == nil || result.Authored.PRReview.Build.Profile == nil || *result.Authored.PRReview.Build.Profile != "pr-review" ||
		result.Authored.PRReview.ProofOfChanges == nil || result.Authored.PRReview.ProofOfChanges.Enabled == nil || !*result.Authored.PRReview.ProofOfChanges.Enabled {
		t.Fatalf("PR review = %#v", result.Authored.PRReview)
	}
	for _, expected := range []struct {
		code        string
		path        string
		disposition string
	}{
		{code: "shadowed_top_level_build_field", path: "build.command", disposition: "omitted"},
		{code: "shadowed_top_level_build_field", path: "build.output", disposition: "omitted"},
		{code: "retired_browser_preference", path: "defaults.open_browser", disposition: "omitted"},
		{code: "retired_preview_link", path: "pr_review.actions.preview_link", disposition: "omitted"},
		{code: "canonical_project_root", path: "pr_review.builds.ios.root_dir", disposition: "resolved"},
		{code: "retired_project_metadata", path: "project.name", disposition: "omitted"},
	} {
		change := findLegacyMigrationChange(result, expected.code, expected.path)
		if change == nil || change.Disposition != expected.disposition {
			t.Fatalf("change %s %s = %#v", expected.code, expected.path, change)
		}
	}
	if _, err := ParseAuthoredConfig(result.CanonicalBytes); err != nil {
		t.Fatalf("canonical proposal does not round-trip: %v", err)
	}
}

func TestMigrateLegacyConfigBytesPreservesValidCanonicalSiblingsInMixedDocument(t *testing.T) {
	raw := `project:
  id: 11111111-1111-4111-8111-111111111111
  name: Legacy Name
session:
  idle_timeout_seconds: 321
defaults:
  timeout: 123
before_session:
  script: scripts/setup.sh
build:
  framework: ios
  profiles:
    development:
      ios:
        build_commands: [canonical build]
  system: Xcode
  platforms:
    ios-preview:
      command: legacy preview build
      output: build/Preview.app
pr_review:
  enabled: true
  build:
    kind: revyl
    profile: development
  actions:
    proof_of_changes: true
`
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:    []byte(raw),
		Context: CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authored.Session == nil || result.Authored.Session.IdleTimeoutSeconds == nil || *result.Authored.Session.IdleTimeoutSeconds != 321 {
		t.Fatalf("canonical session sibling was not preserved: %#v", result.Authored.Session)
	}
	if result.Authored.Session.BeforeScript == nil || result.Authored.Session.BeforeScript.ScriptPath == nil || *result.Authored.Session.BeforeScript.ScriptPath != "scripts/setup.sh" {
		t.Fatalf("translated session sibling was not preserved: %#v", result.Authored.Session)
	}
	if result.Authored.Build == nil || result.Authored.Build.Profiles["development"].IOS == nil {
		t.Fatalf("canonical build sibling was not preserved: %#v", result.Authored.Build)
	}
	if result.Authored.Build.Profiles["preview"].IOS == nil {
		t.Fatalf("translated build sibling was not preserved: %#v", result.Authored.Build)
	}
	if result.Authored.PRReview == nil || result.Authored.PRReview.ProofOfChanges == nil || result.Authored.PRReview.ProofOfChanges.Enabled == nil || !*result.Authored.PRReview.ProofOfChanges.Enabled {
		t.Fatalf("translated PR review sibling was not preserved: %#v", result.Authored.PRReview)
	}
	assertLegacyMigrationChange(t, result, "mixed_canonical_section_preserved", "session")
	assertLegacyMigrationChange(t, result, "mixed_canonical_section_preserved", "build")
	for _, path := range []string{"session", "build.framework", "build.profiles", "pr_review.build", "pr_review.enabled"} {
		if change := findLegacyMigrationChange(result, "legacy_unsupported_field", path); change != nil {
			t.Fatalf("preserved canonical value reported as omitted at %s: %#v", path, change)
		}
	}
	assertLegacyMigrationChange(t, result, "retired_project_metadata", "project.name")
}

func TestResolveConfigFileContextAllowsExplicitLegacyMigration(t *testing.T) {
	worktreeRoot := t.TempDir()
	gitInitConfigTestRepository(t, worktreeRoot)
	projectRoot := filepath.Join(worktreeRoot, "apps", "mobile")
	legacy := migrationLegacyConfig("")
	writeProjectFileTestConfig(t, projectRoot, legacy, 0o600)
	nested := filepath.Join(projectRoot, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	fileContext, err := ResolveConfigFileContext(nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(fileContext.OriginalBytes) != legacy {
		t.Fatalf("legacy original bytes = %q", fileContext.OriginalBytes)
	}
	if fileContext.RepositoryRelativeProjectRoot != "apps/mobile" {
		t.Fatalf("project root = %q", fileContext.RepositoryRelativeProjectRoot)
	}
	if fileContext.CompilationContext().ExecutionDirectory != "apps/mobile/src" {
		t.Fatalf("execution directory = %q", fileContext.CompilationContext().ExecutionDirectory)
	}
	_, err = ResolveProjectContext(nested, "")
	assertConfigError(t, err, "classification", "legacy_config_requires_migration")
}

func TestCreateConfigIfAbsentNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".revyl", "config.yaml")
	first := []byte(projectFileTestConfig(migrationExistingProjectID, "first"))
	if err := CreateConfigIfAbsent(configPath, first, 0o640); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		metadata, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Mode().Perm() != 0o640 {
			t.Fatalf("created mode = %o, want 640", metadata.Mode().Perm())
		}
	}
	second := []byte(projectFileTestConfig(migrationExplicitProjectID, "second"))
	err := CreateConfigIfAbsent(configPath, second, 0o600)
	assertConfigError(t, err, "write", "config_already_exists")
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(first) {
		t.Fatalf("existing config overwritten with %q", got)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".config.yaml.create-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary create files leaked: %v", temporaryFiles)
	}
}

func TestBackupAndReplaceConfigUsesOneExpectedByteCAS(t *testing.T) {
	root := t.TempDir()
	original := "# exact original\n" + projectFileTestConfig(migrationExistingProjectID, "first")
	configPath := writeProjectFileTestConfig(t, root, original, 0o640)
	replacement := []byte(projectFileTestConfig(migrationExplicitProjectID, "second"))
	originalNow := configBackupNow
	configBackupNow = func() time.Time {
		return time.Date(2026, time.August, 12, 1, 2, 3, 0, time.UTC)
	}
	t.Cleanup(func() { configBackupNow = originalNow })

	if backupPath, err := BackupAndReplaceConfig(configPath, replacement, []byte("stale")); err == nil || backupPath != "" {
		t.Fatalf("stale CAS = %q, %v; want no backup and error", backupPath, err)
	}
	backupPath, err := BackupAndReplaceConfig(configPath, replacement, []byte(original))
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatalf("backup = %q, want exact %q", backup, original)
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(replacement) {
		t.Fatalf("replacement = %q, want %q", current, replacement)
	}
}

func migrationLegacyConfig(projectFields string) string {
	return "project:\n  name: project name\n" + projectFields + "build:\n  system: expo\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\n"
}

func assertLegacyMigrationChange(t *testing.T, result *LegacyConfigMigrationResult, code, path string) {
	t.Helper()
	if change := findLegacyMigrationChange(result, code, path); change == nil {
		t.Fatalf("migration changes = %#v, want %s at %s", result.Omissions, code, path)
	}
}

func findLegacyMigrationChange(result *LegacyConfigMigrationResult, code, path string) *LegacyConfigOmission {
	for index := range result.Omissions {
		change := &result.Omissions[index]
		if change.Code == code && strings.Join(change.Path, ".") == path {
			return change
		}
	}
	return nil
}
