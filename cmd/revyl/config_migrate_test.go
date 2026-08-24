package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/workflowref"
	"github.com/spf13/cobra"
)

const generatedMigrationProjectID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestConfigMigrationGeneratedProjectIDIsStablePerRepositoryProject(t *testing.T) {
	first := configMigrationProjectIDForLocator("github:acme/mobile", "apps/ios")
	second := configMigrationProjectIDForLocator("github:acme/mobile", "apps/ios")
	if first != second {
		t.Fatalf("generated project IDs differ: %q != %q", first, second)
	}
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("generated project ID is invalid: %v", err)
	}
	if other := configMigrationProjectIDForLocator("github:acme/mobile", "apps/android"); other == first {
		t.Fatalf("different project roots generated the same project ID %q", first)
	}
}

func migrationTestResult(projectID string) preparedLocalConfigMigration {
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: projectID},
	}
	canonical, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		panic(err)
	}
	aggregate, err := config.NormalizeAuthoredConfig(authored, config.CompilationContext{
		RepositoryRelativeProjectRoot: ".",
		ExecutionDirectory:            ".",
	})
	if err != nil {
		panic(err)
	}
	return preparedLocalConfigMigration{
		WorktreeRoot:                  "/repo",
		ConfigPath:                    "/repo/.revyl/config.yaml",
		RepositoryRelativeProjectRoot: ".",
		CompilationContext: config.CompilationContext{
			RepositoryRelativeProjectRoot: ".",
			ExecutionDirectory:            ".",
		},
		OriginalBytes:            []byte("project:\n  name: legacy\n"),
		ProjectID:                projectID,
		Authored:                 authored,
		CanonicalBytes:           canonical,
		ProjectConfigurationHash: aggregate.ProjectConfigurationHash,
	}
}

func withConfigMigrationDependencies(
	t *testing.T,
	prepare func(string, string, string) (preparedLocalConfigMigration, error),
) {
	t.Helper()
	originalPrepare := prepareLocalConfigMigration
	originalBackup := backupAndReplaceConfig
	originalAliasBackup := backupAndReplaceConfigWithLegacyTestAliases
	originalResolvedPrepare := prepareResolvedLocalConfigMigration
	originalAppClient := newConfigMigrationAppClient
	originalWorkflowClient := newConfigMigrationWorkflowClient
	originalConfirm := confirmConfigMigration
	originalSelectProject := selectConfigMigrationProject
	originalTTY := configMigrationInputTTY
	originalGenerate := generateConfigMigrationProject
	originalSlug := resolveProjectRepoSlug
	originalToken := readActiveConfigToken
	prepareLocalConfigMigration = prepare
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		return "/repo/.revyl/config.yaml.bak", nil
	}
	backupAndReplaceConfigWithLegacyTestAliases = func(string, []byte, []byte, []config.LegacyTestAlias) (string, error) {
		return "/repo/.revyl/config.yaml.bak", nil
	}
	confirmConfigMigration = func(string, bool) (bool, error) { return true, nil }
	configMigrationInputTTY = func() bool { return true }
	generateConfigMigrationProject = func(string) (string, error) { return generatedMigrationProjectID, nil }
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "", "", errors.New("not a GitHub checkout")
	}
	readActiveConfigToken = func() (string, error) { return "", nil }
	t.Cleanup(func() {
		prepareLocalConfigMigration = originalPrepare
		backupAndReplaceConfig = originalBackup
		backupAndReplaceConfigWithLegacyTestAliases = originalAliasBackup
		prepareResolvedLocalConfigMigration = originalResolvedPrepare
		newConfigMigrationAppClient = originalAppClient
		newConfigMigrationWorkflowClient = originalWorkflowClient
		confirmConfigMigration = originalConfirm
		selectConfigMigrationProject = originalSelectProject
		configMigrationInputTTY = originalTTY
		generateConfigMigrationProject = originalGenerate
		resolveProjectRepoSlug = originalSlug
		readActiveConfigToken = originalToken
	})
}

type fakeConfigMigrationWorkflowClient struct {
	workflows []api.SimpleWorkflow
	err       error
	calls     int
}

type fakeConfigMigrationAppClient struct {
	responses map[string]*api.CLIPaginatedAppsResponse
	err       error
	calls     []string
}

func (f *fakeConfigMigrationAppClient) SearchApps(
	_ context.Context,
	search string,
	platform string,
	_ int,
) (*api.CLIPaginatedAppsResponse, error) {
	f.calls = append(f.calls, platform+":"+search)
	if f.err != nil {
		return nil, f.err
	}
	return f.responses[platform+":"+search], nil
}

func (f *fakeConfigMigrationWorkflowClient) ListWorkflowsBounded(context.Context, int, int) ([]api.SimpleWorkflow, error) {
	f.calls++
	return f.workflows, f.err
}

func requiredConfigMigrationWorkflowLookup(t *testing.T) error {
	t.Helper()
	_, err := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data:               []byte("project:\n  name: legacy\nbuild:\n  system: expo\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\npr_review:\n  enabled: true\n  actions:\n    workflows: [smoke, smoke]\n"),
		Context:            config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: generatedMigrationProjectID,
	})
	var required *config.LegacyWorkflowLookupsRequired
	if !errors.As(err, &required) {
		t.Fatalf("lookup fixture error = %v", err)
	}
	return err
}

func requiredConfigMigrationAppLookup(t *testing.T) error {
	t.Helper()
	_, err := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data: []byte(`project:
  name: legacy
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      framework: expo_ios
      app: Preview iOS
      build_command: npx expo prebuild --platform ios
`),
		Context:            config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: generatedMigrationProjectID,
	})
	var required *config.LegacyAppLookupsRequired
	if !errors.As(err, &required) {
		t.Fatalf("lookup fixture error = %v", err)
	}
	return err
}

func TestConfigMigrateResolvesEnabledLegacyReviewAppNames(t *testing.T) {
	lookupErr := requiredConfigMigrationAppLookup(t)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) { return "token", nil }
	appClient := &fakeConfigMigrationAppClient{responses: map[string]*api.CLIPaginatedAppsResponse{
		"ios:Preview iOS": {
			Items: []api.App{{
				ID:       "44444444-4444-4444-8444-444444444444",
				Name:     "Preview iOS",
				Platform: "ios",
			}},
		},
	}}
	newConfigMigrationAppClient = func(*cobra.Command, string) configMigrationAppClient { return appClient }
	var gotApps map[string]map[string]string
	prepareResolvedLocalConfigMigration = func(
		_, _, generated string,
		apps map[string]map[string]string,
		_ map[string]string,
	) (preparedLocalConfigMigration, error) {
		gotApps = apps
		return migrationTestResult(generated), nil
	}

	_ = captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(appClient.calls, []string{"ios:Preview iOS"}) {
		t.Fatalf("app catalog calls = %#v", appClient.calls)
	}
	if gotApps["ios"]["Preview iOS"] != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("resolved apps = %#v", gotApps)
	}
}

func TestConfigMigrationAppLookupPreservesExactMatchesAndReportsAmbiguity(t *testing.T) {
	required := &config.LegacyAppLookupsRequired{Lookups: []config.LegacyAppLookup{
		{Platform: "ios", Name: "Preview iOS", Path: []string{"pr_review", "builds", "ios", "app"}},
		{Platform: "android", Name: "Preview Android", Path: []string{"pr_review", "builds", "android", "app"}},
	}}
	readActiveConfigToken = func() (string, error) { return "token", nil }
	newConfigMigrationAppClient = func(*cobra.Command, string) configMigrationAppClient {
		return &fakeConfigMigrationAppClient{responses: map[string]*api.CLIPaginatedAppsResponse{
			"ios:Preview iOS": {
				Items: []api.App{{ID: "44444444-4444-4444-8444-444444444444", Name: "Preview iOS", Platform: "ios"}},
			},
			"android:Preview Android": {
				Items: []api.App{
					{ID: "55555555-5555-4555-8555-555555555555", Name: "Preview Android", Platform: "android"},
					{ID: "66666666-6666-4666-8666-666666666666", Name: "Preview Android", Platform: "android"},
				},
			},
		}}
	}

	resolved, issues, err := resolveConfigMigrationAppLookups(testConfigCommand(), required)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["ios"]["Preview iOS"] != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("resolved apps = %#v", resolved)
	}
	issue := issues["pr_review.builds.android.app"]
	if issue == nil || issue.code != "app_name_ambiguous" {
		t.Fatalf("lookup issues = %#v", issues)
	}

	changes := annotateAppLookupMigrationChanges(
		[]config.LegacyConfigOmission{{
			Code:        "legacy_app_reference_unresolved",
			Path:        []string{"pr_review", "builds", "android", "app"},
			Disposition: "omitted",
		}},
		required,
		issues,
		nil,
	)
	if changes[0].Code != "legacy_app_name_ambiguous" || !strings.Contains(changes[0].Message, "review build was omitted") {
		t.Fatalf("annotated changes = %#v", changes)
	}
}

func TestConfigMigrateReportsAmbiguousEnabledLegacyReviewAppAndWritesAfterOptIn(t *testing.T) {
	lookupErr := requiredConfigMigrationAppLookup(t)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) { return "token", nil }
	newConfigMigrationAppClient = func(*cobra.Command, string) configMigrationAppClient {
		return &fakeConfigMigrationAppClient{responses: map[string]*api.CLIPaginatedAppsResponse{
			"ios:Preview iOS": {
				Items: []api.App{
					{ID: "44444444-4444-4444-8444-444444444444", Name: "Preview iOS", Platform: "ios"},
					{ID: "55555555-5555-4555-8555-555555555555", Name: "Preview iOS", Platform: "ios"},
				},
			},
		}}
	}
	prepareResolvedLocalConfigMigration = func(
		_, _, generated string,
		apps map[string]map[string]string,
		_ map[string]string,
	) (preparedLocalConfigMigration, error) {
		if len(apps) != 0 {
			t.Fatalf("resolved apps = %#v, want no ambiguous resolution", apps)
		}
		result := migrationTestResult(generated)
		result.Omissions = []config.LegacyConfigOmission{{
			Code:        "legacy_app_reference_unresolved",
			Path:        []string{"pr_review", "builds", "ios", "app"},
			Message:     "the legacy app name could not be resolved and the review build was omitted",
			Disposition: "omitted",
		}}
		return result, nil
	}
	wrote := false
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		wrote = true
		return "/repo/.revyl/config.yaml.bak", nil
	}
	output := captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{write: true}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"will be dropped", "--check --json", "reported backup", "Migrated the local project configuration"} {
		if !strings.Contains(output, want) {
			t.Fatalf("migration output missing %q:\n%s", want, output)
		}
	}
	if !wrote {
		t.Fatal("accepted lossy migration did not write")
	}
}

func TestConfigMigrateResolvesLegacyWorkflowNamesWithOneReadOnlyCatalog(t *testing.T) {
	lookupErr := requiredConfigMigrationWorkflowLookup(t)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) { return "token", nil }
	workflowClient := &fakeConfigMigrationWorkflowClient{workflows: []api.SimpleWorkflow{{ID: "44444444-4444-4444-8444-444444444444", Name: "smoke"}}}
	newConfigMigrationWorkflowClient = func(*cobra.Command, string) workflowref.BoundedCatalogClient { return workflowClient }
	var gotResolved map[string]string
	prepareResolvedLocalConfigMigration = func(_, _, generated string, _ map[string]map[string]string, resolved map[string]string) (preparedLocalConfigMigration, error) {
		gotResolved = resolved
		return migrationTestResult(generated), nil
	}

	_ = captureStdout(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{check: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	if workflowClient.calls != 1 {
		t.Fatalf("workflow catalog calls = %d, want 1", workflowClient.calls)
	}
	if gotResolved["smoke"] != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("resolved workflows = %#v", gotResolved)
	}
}

func TestConfigMigratePreservesResolvableWorkflowWhenSiblingIsMissing(t *testing.T) {
	_, lookupErr := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data:               []byte("project:\n  name: legacy\nbuild:\n  system: expo\n  platforms:\n    ios-dev:\n      command: build\n      output: build/app.app\npr_review:\n  enabled: true\n  actions:\n    workflows: [smoke, missing]\n"),
		Context:            config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: generatedMigrationProjectID,
	})
	var required *config.LegacyWorkflowLookupsRequired
	if !errors.As(lookupErr, &required) {
		t.Fatalf("lookup fixture error = %v", lookupErr)
	}
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) { return "token", nil }
	workflowClient := &fakeConfigMigrationWorkflowClient{workflows: []api.SimpleWorkflow{{ID: "44444444-4444-4444-8444-444444444444", Name: "smoke"}}}
	newConfigMigrationWorkflowClient = func(*cobra.Command, string) workflowref.BoundedCatalogClient { return workflowClient }
	var gotResolved map[string]string
	prepareResolvedLocalConfigMigration = func(_, _, generated string, _ map[string]map[string]string, resolved map[string]string) (preparedLocalConfigMigration, error) {
		gotResolved = resolved
		prepared := migrationTestResult(generated)
		prepared.Omissions = []config.LegacyConfigOmission{
			{Code: "legacy_workflow_reference_resolved", Path: []string{"pr_review", "actions", "workflows", "0"}, Message: "resolved", Disposition: "resolved"},
			{Code: "legacy_workflow_reference_unresolved", Path: []string{"pr_review", "actions", "workflows", "1"}, Message: "unresolved", Disposition: "omitted"},
		}
		return prepared, nil
	}
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	if gotResolved["smoke"] == "" || gotResolved["missing"] != "" {
		t.Fatalf("resolved workflow IDs = %#v", gotResolved)
	}
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Omissions) != 2 || decoded.Omissions[1].Code != "legacy_workflow_reference_not_found" {
		t.Fatalf("migration changes = %#v", decoded.Omissions)
	}
}

func TestConfigMigrateLegacyWorkflowLookupAuthenticationFailureIsReportedAndWrites(t *testing.T) {
	lookupErr := requiredConfigMigrationWorkflowLookup(t)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) { return "", nil }
	var resolved map[string]string
	prepareResolvedLocalConfigMigration = func(_, _, generated string, _ map[string]map[string]string, workflowIDs map[string]string) (preparedLocalConfigMigration, error) {
		resolved = workflowIDs
		return migrationTestResult(generated), nil
	}
	wrote := false
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		wrote = true
		return "/tmp/config.yaml.bak", nil
	}
	if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{write: true}); err != nil {
		t.Fatalf("runConfigMigrate() error = %v", err)
	}
	if resolved == nil || len(resolved) != 0 {
		t.Fatalf("workflow resolutions = %#v, want non-nil empty best-effort fallback", resolved)
	}
	if !wrote {
		t.Fatal("best-effort workflow migration did not write config")
	}
}

func TestConfigMigrateLegacyWorkflowLookupAPIAccessFailuresAreActionableAndLossy(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		wantCode    string
		wantCommand string
	}{
		{name: "expired authentication", statusCode: 401, wantCode: "workflow_authentication_required", wantCommand: "revyl auth login"},
		{name: "organization access denied", statusCode: 403, wantCode: "workflow_access_denied", wantCommand: "revyl auth status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookupErr := requiredConfigMigrationWorkflowLookup(t)
			withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
				return preparedLocalConfigMigration{}, lookupErr
			})
			readActiveConfigToken = func() (string, error) { return "token", nil }
			newConfigMigrationWorkflowClient = func(*cobra.Command, string) workflowref.BoundedCatalogClient {
				return &fakeConfigMigrationWorkflowClient{err: &api.APIError{StatusCode: test.statusCode}}
			}
			prepareResolvedLocalConfigMigration = func(_, _, generated string, _ map[string]map[string]string, resolved map[string]string) (preparedLocalConfigMigration, error) {
				if resolved == nil || len(resolved) != 0 {
					t.Fatalf("workflow resolutions = %#v, want non-nil empty best-effort fallback", resolved)
				}
				return migrationTestResult(generated), nil
			}
			command := testConfigCommand()
			_ = command.Flags().Set("json", "true")
			output := captureStdout(t, func() {
				if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
					t.Fatalf("lossy migration failed: %v", err)
				}
			})
			var decoded configMigrateOutput
			if err := json.Unmarshal([]byte(output), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Omissions) != 1 || decoded.Omissions[0].Code != test.wantCode ||
				!strings.Contains(decoded.Omissions[0].Message, test.wantCommand) ||
				!strings.Contains(decoded.Omissions[0].Message, "revyl config migrate") {
				t.Fatalf("migration changes = %#v", decoded.Omissions)
			}
		})
	}
}

func TestConfigMigrateRejectsPromptingJSONBeforeWorkflowLookup(t *testing.T) {
	lookupErr := requiredConfigMigrationWorkflowLookup(t)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return preparedLocalConfigMigration{}, lookupErr
	})
	readActiveConfigToken = func() (string, error) {
		t.Fatal("invalid JSON mode read authentication")
		return "", nil
	}
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	err := runConfigMigrate(command, configMigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "requires --check or --write") {
		t.Fatalf("error = %v, want stable JSON mode error", err)
	}
}

func migrationCatalog(projects ...api.RepositoryProjectCatalogItem) *api.RepositoryProjectCatalogResponse {
	return &api.RepositoryProjectCatalogResponse{
		Repository: api.RepositoryProjectCatalogRepository{
			Provider: "github", Namespace: "acme", RepositoryName: "mobile",
		},
		Projects: projects,
	}
}

func connectedMigration(
	t *testing.T,
	client *fakeProjectConfigurationClient,
) {
	t.Helper()
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "acme", "mobile", nil
	}
	readActiveConfigToken = func() (string, error) { return "token", nil }
	originalClient := newProjectConfigClient
	newProjectConfigClient = func(string, bool) projectConfigurationClient { return client }
	t.Cleanup(func() { newProjectConfigClient = originalClient })
}

func TestConfigMigrateCheckPrintsConciseSummaryWithoutFilesystemMutation(t *testing.T) {
	var gotExplicit, gotGenerated string
	withConfigMigrationDependencies(t, func(_, explicit, generated string) (preparedLocalConfigMigration, error) {
		gotExplicit, gotGenerated = explicit, generated
		return migrationTestResult(generated), nil
	})
	backupCalled := false
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		backupCalled = true
		return "", nil
	}

	output := captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{check: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	if gotExplicit != "" || gotGenerated != generatedMigrationProjectID {
		t.Fatalf("project IDs = explicit %q generated %q", gotExplicit, gotGenerated)
	}
	if backupCalled {
		t.Fatal("--check created a backup or replaced the config")
	}
	for _, want := range []string{"Config migration ready", "exact-byte backup", "No files changed (--check)"} {
		if !strings.Contains(output, want) {
			t.Fatalf("migration summary does not contain %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Proposed .revyl/config.yaml") || strings.Contains(output, "project:\n") {
		t.Fatalf("human migration summary printed the full proposal:\n%s", output)
	}
}

func TestConfigMigrateInteractiveSummaryHasOneStableShape(t *testing.T) {
	for _, test := range []struct {
		name       string
		omissions  []config.LegacyConfigOmission
		wantLegacy string
	}{
		{name: "clean", wantLegacy: "no fields will be dropped or defaulted"},
		{
			name: "lossy",
			omissions: []config.LegacyConfigOmission{
				{Code: "retired_one", Path: []string{"one"}, Disposition: "omitted"},
				{Code: "invalid_two", Path: []string{"two"}, Disposition: "defaulted"},
			},
			wantLegacy: "1 will be dropped; 1 will use canonical defaults",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := migrationTestResult(generatedMigrationProjectID)
			prepared.Omissions = test.omissions
			withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
				return prepared, nil
			})
			confirmation := ""
			confirmConfigMigration = func(message string, _ bool) (bool, error) {
				confirmation = message
				return false, nil
			}
			output := captureStdoutAndStderr(t, func() {
				if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{}); err != nil {
					t.Fatal(err)
				}
			})
			for _, want := range []string{"Config migration ready", test.wantLegacy, "exact-byte backup", "Local configuration left unchanged"} {
				if !strings.Contains(output, want) {
					t.Fatalf("summary does not contain %q:\n%s", want, output)
				}
			}
			if confirmation != "Continue with migration? An exact-byte backup will be created first." {
				t.Fatalf("confirmation = %q", confirmation)
			}
		})
	}
}

func TestConfigMigrateUnsupportedOriginKeepsHumanSummaryConcise(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})

	output := captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})

	for _, want := range []string{"Config migration ready", "exact-byte backup"} {
		if !strings.Contains(output, want) {
			t.Fatalf("migration summary does not contain %q:\n%s", want, output)
		}
	}
	for _, verbose := range []string{"github_origin_unsupported", "git -C", "revyl config validate", "revyl config push", "Proposed .revyl/config.yaml"} {
		if strings.Contains(output, verbose) {
			t.Fatalf("human migration summary contains verbose detail %q:\n%s", verbose, output)
		}
	}
}

func TestConfigMigrateCheckJSONIsOneStableObject(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	prepared.Omissions = []config.LegacyConfigOmission{{
		Code: "retired_keep_derived_data", Path: []string{"build", "platforms", "ios", "keep_derived_data"},
		Message: "the baseline CLI had no functioning derived-data retention behavior",
	}}
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		prepared.ProjectID = generated
		prepared.Authored.Project.ID = generated
		return prepared, nil
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("migration output is not JSON: %v\n%s", err, output)
	}
	if decoded.Outcome != "proposal" || decoded.ProjectID != generatedMigrationProjectID {
		t.Fatalf("migration output = %#v", decoded)
	}
	if len(decoded.Omissions) != 1 || decoded.Omissions[0].Code != "retired_keep_derived_data" {
		t.Fatalf("migration omissions = %#v", decoded.Omissions)
	}
	if decoded.Reconciliation == nil || decoded.Reconciliation.Status != "skipped" || decoded.Reconciliation.Outcome != "github_origin_unsupported" {
		t.Fatalf("migration reconciliation = %#v", decoded.Reconciliation)
	}
}

func mixedCanonicalMigrationLedgerResult(t *testing.T) preparedLocalConfigMigration {
	t.Helper()
	raw := `project:
  id: aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
  name: Legacy Name
session:
  idle_timeout_seconds: 321
defaults:
  timeout: 123
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
	migrated, err := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data:    []byte(raw),
		Context: config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	return preparedLocalConfigMigration{
		WorktreeRoot:                  "/repo",
		ConfigPath:                    "/repo/.revyl/config.yaml",
		RepositoryRelativeProjectRoot: ".",
		CompilationContext:            config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		OriginalBytes:                 []byte(raw),
		ProjectID:                     migrated.ProjectID,
		Authored:                      migrated.Authored,
		CanonicalBytes:                migrated.CanonicalBytes,
		ProjectConfigurationHash:      migrated.Aggregate.ProjectConfigurationHash,
		Omissions:                     migrated.Omissions,
	}
}

func TestConfigMigrateMixedCanonicalLedgerIsTruthfulForHumansAndJSON(t *testing.T) {
	for _, outputMode := range []string{"human", "json"} {
		t.Run(outputMode, func(t *testing.T) {
			prepared := mixedCanonicalMigrationLedgerResult(t)
			withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
				return prepared, nil
			})
			command := testConfigCommand()
			if outputMode == "json" {
				_ = command.Flags().Set("json", "true")
			}
			output := captureStdoutAndStderr(t, func() {
				if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
					t.Fatal(err)
				}
			})
			preservedPaths := []string{"session", "build.framework", "build.profiles", "pr_review.build", "pr_review.enabled"}
			if outputMode == "human" {
				for _, want := range []string{"Config migration ready", "will be dropped", "exact-byte backup"} {
					if !strings.Contains(output, want) {
						t.Fatalf("human summary does not contain %q:\n%s", want, output)
					}
				}
				for _, verbose := range []string{"resolved session:", "omitted ", "project:\n"} {
					if strings.Contains(output, verbose) {
						t.Fatalf("human summary contains verbose migration detail %q:\n%s", verbose, output)
					}
				}
				return
			}

			var decoded configMigrateOutput
			if err := json.Unmarshal([]byte(output), &decoded); err != nil {
				t.Fatalf("migration output is not JSON: %v\n%s", err, output)
			}
			resolvedSections := map[string]bool{}
			for _, omission := range decoded.Omissions {
				if omission.Code == "mixed_canonical_section_preserved" {
					resolvedSections[strings.Join(omission.Path, ".")] = true
				}
				if omission.Code != "legacy_unsupported_field" {
					continue
				}
				path := strings.Join(omission.Path, ".")
				for _, preservedPath := range preservedPaths {
					if path == preservedPath {
						t.Fatalf("JSON ledger reported preserved %s as omitted: %#v", path, decoded.Omissions)
					}
				}
			}
			for _, section := range []string{"session", "build", "pr_review"} {
				if !resolvedSections[section] {
					t.Fatalf("JSON ledger did not report preserved %s section: %#v", section, decoded.Omissions)
				}
			}
		})
	}
}

func TestConfigMigrationPropertiesCountOnlyBoundedDispositions(t *testing.T) {
	properties := configMigrationProperties(configMigrationIdentityLocalGenerated, []config.LegacyConfigOmission{
		{Disposition: "omitted"},
		{Disposition: "omitted"},
		{Disposition: "defaulted"},
		{Disposition: "resolved"},
		{Disposition: "unexpected"},
	})
	if properties["config_migration_omitted_count"] != 2 ||
		properties["config_migration_defaulted_count"] != 1 ||
		properties["config_migration_resolved_count"] != 1 {
		t.Fatalf("migration properties = %#v", properties)
	}
}

func TestConfigMigrationIdentityPropertiesKeepCatalogFailureTelemetryBounded(t *testing.T) {
	for _, source := range []configMigrationIdentitySource{
		configMigrationIdentityCatalogAuthRequired,
		configMigrationIdentityCatalogAccessDenied,
		configMigrationIdentityCatalogLimit,
		configMigrationIdentityProviderUnavailable,
	} {
		properties := configMigrationIdentityProperties(source)
		if properties["config_migration_identity_source"] != string(configMigrationIdentityCatalogUnavailable) {
			t.Fatalf("source %q properties = %#v", source, properties)
		}
	}
}

func TestConfigMigrateComparesExactCanonicalProjectWithoutWriting(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	client := &fakeProjectConfigurationClient{
		catalogResult: migrationCatalog(api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(generatedMigrationProjectID),
			RepositoryRelativeProjectRoot: ".",
			RepositoryRelativeConfigPath:  ".revyl/config.yaml",
		}),
		validateResult: &api.ProjectConfigurationValidateResponse{
			Status:                            "valid",
			CandidateProjectConfigurationHash: prepared.ProjectConfigurationHash,
			Current: api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority:                api.ConfigurationAuthorityManual,
					ProjectConfigurationHash: prepared.ProjectConfigurationHash,
				},
			},
		},
	}
	connectedMigration(t, client)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reconciliation == nil || decoded.Reconciliation.Status != "succeeded" || decoded.Reconciliation.Outcome != "aligned" || decoded.Reconciliation.NextAction != "none" {
		t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
	}
}

func TestInspectConfigMigrationReconciliationGuidesDivergenceWithoutMutation(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	tests := []struct {
		name       string
		current    api.ProjectConfigurationReadResponse
		outcome    string
		nextAction string
	}{
		{
			name:       "canonical project is absent",
			current:    api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStateAbsent},
			outcome:    "absent",
			nextAction: "publish_or_remain_divergent",
		},
		{
			name: "manual authority is divergent",
			current: api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority: api.ConfigurationAuthorityManual, ProjectConfigurationHash: "different",
				},
			},
			outcome:    "divergent",
			nextAction: "push_or_pull_or_remain_divergent",
		},
		{
			name: "Git authority is divergent",
			current: api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority: api.ConfigurationAuthorityGitDefaultBranch, ProjectConfigurationHash: "different",
				},
			},
			outcome:    "divergent",
			nextAction: "validate_and_commit_or_pull_or_remain_divergent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
				return prepared, nil
			})
			client := &fakeProjectConfigurationClient{validateResult: &api.ProjectConfigurationValidateResponse{
				Status:                            "valid",
				CandidateProjectConfigurationHash: prepared.ProjectConfigurationHash,
				Current:                           test.current,
			}}
			connectedMigration(t, client)

			got := inspectConfigMigrationReconciliation(
				testConfigCommand(), prepared, configMigrationIdentityRemoteExact,
			)
			if got.Status != "succeeded" || got.Outcome != test.outcome || got.NextAction != test.nextAction {
				t.Fatalf("reconciliation = %#v", got)
			}
			if client.replaceRequest != nil {
				t.Fatalf("reconciliation performed a write: %#v", client.replaceRequest)
			}
		})
	}
}

func TestInspectConfigMigrationReconciliationClassifiesConnectedAccessAndReferences(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	tests := []struct {
		name        string
		apiError    *api.APIError
		wantOutcome string
		wantText    []string
	}{
		{
			name: "expired authentication", apiError: &api.APIError{StatusCode: 401},
			wantOutcome: "authentication_required", wantText: []string{"revyl auth login", "revyl config validate"},
		},
		{
			name: "organization access denied", apiError: &api.APIError{StatusCode: 403},
			wantOutcome: "access_denied", wantText: []string{"revyl auth status", "revyl config validate"},
		},
		{
			name: "app reference", apiError: migrationValidationAPIError("referenced_app_not_available", "configuration.build.profiles.development.ios.app_id"),
			wantOutcome: "app_reference_unavailable", wantText: []string{"configuration.build.profiles.development.ios.app_id", "revyl app list --platform ios", "revyl config validate"},
		},
		{
			name: "workflow reference", apiError: migrationForbiddenReferenceAPIError("referenced_workflow_not_available", "configuration.pr_review.workflow_ids.0"),
			wantOutcome: "workflow_reference_unavailable", wantText: []string{"configuration.pr_review.workflow_ids.0", "revyl workflow list", "revyl config validate"},
		},
		{
			name: "secret reference", apiError: migrationForbiddenReferenceAPIError("referenced_secret_not_available", "configuration.build.secrets.0"),
			wantOutcome: "secret_reference_unavailable", wantText: []string{"configuration.build.secrets.0", "revyl build secret list", "revyl config validate"},
		},
		{
			name: "launch variable reference", apiError: migrationForbiddenReferenceAPIError("referenced_launch_variable_not_available", "configuration.auth_bypass.launch_vars.0"),
			wantOutcome: "launch_variable_reference_unavailable", wantText: []string{"configuration.auth_bypass.launch_vars.0", "revyl global launch-var list", "revyl config validate"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
				return prepared, nil
			})
			client := &fakeProjectConfigurationClient{validateErr: test.apiError}
			connectedMigration(t, client)
			got := inspectConfigMigrationReconciliation(testConfigCommand(), prepared, configMigrationIdentityRemoteExact)
			if got.Status != "failed" || got.Outcome != test.wantOutcome {
				t.Fatalf("reconciliation = %#v", got)
			}
			for _, want := range test.wantText {
				if !strings.Contains(got.Explanation, want) {
					t.Fatalf("explanation = %q, want %q", got.Explanation, want)
				}
			}
		})
	}
}

func migrationValidationAPIError(issueType, field string) *api.APIError {
	message := "Select an active organization resource, then republish."
	return &api.APIError{
		StatusCode: 422,
		Detail:     field + ": " + message,
		ValidationIssues: []api.APIValidationIssue{{
			Field: field, Message: message, Type: issueType,
		}},
	}
}

func migrationForbiddenReferenceAPIError(code, field string) *api.APIError {
	return &api.APIError{
		StatusCode: 403,
		Code:       code,
		Detail:     field + ": Select an active organization resource, then republish.",
	}
}

func TestConfigMigrateWriteBacksUpAndCASReplacesWithoutPrompt(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	confirmConfigMigration = func(string, bool) (bool, error) {
		t.Fatal("--write prompted")
		return false, nil
	}
	var gotPath string
	var gotReplacement, gotExpected []byte
	backupAndReplaceConfig = func(path string, replacement, expected []byte) (string, error) {
		gotPath = path
		gotReplacement = append([]byte(nil), replacement...)
		gotExpected = append([]byte(nil), expected...)
		return path + ".bak", nil
	}
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	output := captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{write: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	if gotPath != prepared.ConfigPath || string(gotReplacement) != string(prepared.CanonicalBytes) || string(gotExpected) != string(prepared.OriginalBytes) {
		t.Fatalf("backup/CAS args = path %q replacement %q expected %q", gotPath, gotReplacement, gotExpected)
	}
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Outcome != "migrated" || decoded.BackupPath != prepared.ConfigPath+".bak" {
		t.Fatalf("migration output = %#v", decoded)
	}
}

func TestConfigMigrateReportsBackupCreatedBeforeReplacementFailure(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	backupAndReplaceConfig = func(path string, _, _ []byte) (string, error) {
		return path + ".bak", errors.New("replace failed")
	}

	err := runConfigMigrate(testConfigCommand(), configMigrateOptions{write: true})
	if err == nil || !strings.Contains(err.Error(), prepared.ConfigPath+".bak") {
		t.Fatalf("runConfigMigrate() error = %v, want created backup path", err)
	}
}

func TestConfigMigrateDefaultConfirmsBeforeWriting(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	confirmed := false
	confirmConfigMigration = func(message string, defaultYes bool) (bool, error) {
		confirmed = true
		if defaultYes || !strings.Contains(message, "backup") {
			t.Fatalf("confirmation = %q defaultYes=%t", message, defaultYes)
		}
		return true, nil
	}
	backupCalled := false
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		backupCalled = true
		return "/repo/config.bak", nil
	}

	_ = captureStdout(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	if !confirmed || !backupCalled {
		t.Fatalf("confirmed=%t backupCalled=%t", confirmed, backupCalled)
	}
}

func TestConfigMigrateCancellationLeavesLocalBytesUntouched(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	confirmConfigMigration = func(string, bool) (bool, error) { return false, nil }
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		t.Fatal("cancelled migration wrote the config")
		return "", nil
	}

	_ = captureStdout(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
}

func TestConfigMigrateAlreadyCanonicalNeverRewritesCanonicalFormatting(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	prepared.AlreadyCanonical = true
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		return prepared, nil
	})
	confirmConfigMigration = func(string, bool) (bool, error) {
		t.Fatal("already-canonical config prompted")
		return false, nil
	}
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		t.Fatal("already-canonical config was rewritten")
		return "", nil
	}
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		t.Fatal("already-canonical migration performed repository lookup")
		return "", "", nil
	}

	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{write: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Outcome != "already_canonical" {
		t.Fatalf("outcome = %q", decoded.Outcome)
	}
}

func TestConfigMigrateReusesExactCanonicalRepositoryProject(t *testing.T) {
	const canonicalProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	client := &fakeProjectConfigurationClient{
		catalogResult: migrationCatalog(api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(canonicalProjectID),
			RepositoryRelativeProjectRoot: ".",
			RepositoryRelativeConfigPath:  ".revyl/config.yaml",
		}),
	}
	connectedMigration(t, client)

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, canonicalProjectID) || strings.Contains(output, generatedMigrationProjectID) {
		t.Fatalf("proposal did not reuse canonical project:\n%s", output)
	}
	if client.catalogCalls != 1 || client.catalogRequest.Namespace != "acme" || client.catalogRequest.RepositoryName != "mobile" {
		t.Fatalf("catalog call = %d request = %#v", client.catalogCalls, client.catalogRequest)
	}
}

func TestConfigMigrateCheckReusesExactCanonicalProjectForNestedRoot(t *testing.T) {
	const canonicalProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		prepared := migrationTestResult(generated)
		prepared.ConfigPath = "/repo/ios/.revyl/config.yaml"
		prepared.RepositoryRelativeProjectRoot = "ios"
		prepared.CompilationContext.RepositoryRelativeProjectRoot = "ios"
		prepared.CompilationContext.ExecutionDirectory = "ios"
		return prepared, nil
	})
	client := &fakeProjectConfigurationClient{
		catalogResult: migrationCatalog(api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(canonicalProjectID),
			RepositoryRelativeProjectRoot: "ios",
			RepositoryRelativeConfigPath:  "ios/.revyl/config.yaml",
		}),
	}
	connectedMigration(t, client)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("migration output is not JSON: %v\n%s", err, output)
	}
	if decoded.ProjectID != canonicalProjectID {
		t.Fatalf("project ID = %q, want %q", decoded.ProjectID, canonicalProjectID)
	}
	if client.catalogCalls != 1 {
		t.Fatalf("catalog calls = %d, want 1", client.catalogCalls)
	}
}

func TestReplacePreparedConfigMigrationProjectIDPreservesLookupAnnotations(t *testing.T) {
	prepared := migrationTestResult(generatedMigrationProjectID)
	prepared.LegacyWorkflowIDsByName = map[string]string{}
	prepared.Omissions = []config.LegacyConfigOmission{{
		Code:        "workflow_authentication_required",
		Path:        []string{"pr_review", "actions", "workflows"},
		Message:     "authentication unavailable",
		Disposition: "omitted",
	}}
	if err := replacePreparedConfigMigrationProjectID(&prepared, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if len(prepared.Omissions) != 1 || prepared.Omissions[0].Code != "workflow_authentication_required" {
		t.Fatalf("migration changes = %#v", prepared.Omissions)
	}
}

func TestConfigMigrateExplicitProjectStaysLocalWithoutCanonicalMatch(t *testing.T) {
	const explicitProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, explicit, generated string) (preparedLocalConfigMigration, error) {
		if generated != "" {
			t.Fatalf("generated ID = %q", generated)
		}
		return migrationTestResult(explicit), nil
	})
	client := &fakeProjectConfigurationClient{catalogResult: migrationCatalog(
		api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
			RepositoryRelativeProjectRoot: "apps/other",
			RepositoryRelativeConfigPath:  "apps/other/.revyl/config.yaml",
		},
	)}
	connectedMigration(t, client)

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{projectID: explicitProjectID, check: true}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, explicitProjectID) {
		t.Fatalf("proposal did not preserve explicit local ID:\n%s", output)
	}
}

func TestConfigMigrateAcceptsExplicitProjectFromVerifiedRepositoryAfterRootMove(t *testing.T) {
	const explicitProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, explicit, _ string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(explicit), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{catalogResult: migrationCatalog(
		api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(explicitProjectID),
			RepositoryRelativeProjectRoot: "apps/other",
			RepositoryRelativeConfigPath:  "apps/other/.revyl/config.yaml",
		},
	)})

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{projectID: explicitProjectID, check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ProjectID != explicitProjectID {
		t.Fatalf("project ID = %q, want explicitly selected %q", decoded.ProjectID, explicitProjectID)
	}
	if decoded.Reconciliation == nil || decoded.Reconciliation.Outcome == "project_identity_conflict" {
		t.Fatalf("reconciliation = %#v, want selected repository project", decoded.Reconciliation)
	}
}

func TestConfigMigrateRejectsExplicitProjectThatConflictsWithExactCanonicalMatch(t *testing.T) {
	const explicitProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const canonicalProjectID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	withConfigMigrationDependencies(t, func(_, explicit, _ string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(explicit), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{
		catalogResult: migrationCatalog(api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(canonicalProjectID),
			RepositoryRelativeProjectRoot: ".",
			RepositoryRelativeConfigPath:  ".revyl/config.yaml",
		}),
	})

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigMigrate(command, configMigrateOptions{projectID: explicitProjectID, check: true})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "--project does not match the Revyl project registered for this config path") {
		t.Fatalf("error = %v", runErr)
	}
	if output != "" || strings.Contains(runErr.Error(), canonicalProjectID) {
		t.Fatalf("conflict output leaked project details: stdout=%q error=%q", output, runErr)
	}
}

func TestConfigMigrateRejectsMovedProjectWhenExactCatalogIsDuplicated(t *testing.T) {
	const explicitProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, explicit, _ string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(explicit), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{
		catalogResult: migrationCatalog(
			api.RepositoryProjectCatalogItem{
				ProjectId:                     uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"),
				RepositoryRelativeProjectRoot: ".",
				RepositoryRelativeConfigPath:  ".revyl/config.yaml",
			},
			api.RepositoryProjectCatalogItem{
				ProjectId:                     uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd"),
				RepositoryRelativeProjectRoot: ".",
				RepositoryRelativeConfigPath:  ".revyl/config.yaml",
			},
			api.RepositoryProjectCatalogItem{
				ProjectId:                     uuid.MustParse(explicitProjectID),
				RepositoryRelativeProjectRoot: "apps/previous-root",
				RepositoryRelativeConfigPath:  "apps/previous-root/.revyl/config.yaml",
			},
		),
	})

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	err := runConfigMigrate(command, configMigrateOptions{projectID: explicitProjectID, check: true})
	if err == nil || !strings.Contains(err.Error(), "--project does not match the Revyl project registered for this config path") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigMigrateReportsDuplicateCatalogWhileRemainingLocal(t *testing.T) {
	project := api.RepositoryProjectCatalogItem{
		ProjectId:                     uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
		RepositoryRelativeProjectRoot: ".",
		RepositoryRelativeConfigPath:  ".revyl/config.yaml",
	}
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{catalogResult: migrationCatalog(project, project)})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reconciliation == nil || decoded.Reconciliation.Outcome != "catalog_ambiguous" {
		t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
	}
}

func TestConfigMigrateInteractivelySelectsExistingRepositoryProject(t *testing.T) {
	const selectedProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	client := &fakeProjectConfigurationClient{catalogResult: migrationCatalog(
		api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse(selectedProjectID),
			RepositoryRelativeProjectRoot: "apps/previous-root",
			RepositoryRelativeConfigPath:  "apps/previous-root/.revyl/config.yaml",
		},
	)}
	connectedMigration(t, client)
	selectCalls := 0
	selectConfigMigrationProject = func(message string, options []ui.SelectOption, defaultIndex int) (int, string, error) {
		selectCalls++
		if !strings.Contains(message, "existing Revyl project") || len(options) != 2 || options[0].Value != selectedProjectID || options[1].Value != "local" || defaultIndex != 1 {
			t.Fatalf("selection prompt = %q options = %#v default = %d", message, options, defaultIndex)
		}
		return 0, selectedProjectID, nil
	}
	written := []byte(nil)
	backupAndReplaceConfig = func(_ string, replacement, _ []byte) (string, error) {
		written = append([]byte(nil), replacement...)
		return "/repo/.revyl/config.yaml.bak", nil
	}

	_ = captureStdoutAndStderr(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if selectCalls != 1 || !strings.Contains(string(written), selectedProjectID) {
		t.Fatalf("selection calls = %d written config:\n%s", selectCalls, written)
	}
}

func TestConfigMigrateWriteRequiresExplicitProjectWhenRepositorySelectionIsAmbiguous(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{catalogResult: migrationCatalog(
		api.RepositoryProjectCatalogItem{
			ProjectId:                     uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
			RepositoryRelativeProjectRoot: "apps/previous-root",
			RepositoryRelativeConfigPath:  "apps/previous-root/.revyl/config.yaml",
		},
	)})
	wrote := false
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		wrote = true
		return "", nil
	}
	err := runConfigMigrate(testConfigCommand(), configMigrateOptions{write: true})
	if err == nil || !strings.Contains(err.Error(), "rerun interactively") || !strings.Contains(err.Error(), "--project <id>") {
		t.Fatalf("error = %v, want explicit project-selection guidance", err)
	}
	if wrote {
		t.Fatal("ambiguous non-interactive migration wrote the config")
	}
}

func TestConfigMigrateReportsInaccessibleRepositoryWhileRemainingLocal(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{
		catalogErr: &api.APIError{StatusCode: 404, Code: "repository_projects_inaccessible"},
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigMigrate(command, configMigrateOptions{check: true})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reconciliation == nil ||
		decoded.Reconciliation.Outcome != "repository_inaccessible" ||
		!strings.Contains(decoded.Reconciliation.Explanation, "revyl github connect") ||
		!strings.Contains(decoded.Reconciliation.Explanation, "revyl config push") {
		t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
	}
}

func TestConfigMigrateExplicitInaccessibleCatalogRemainsLocalWithoutIdentifiers(t *testing.T) {
	const explicitProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	withConfigMigrationDependencies(t, func(_, explicit, _ string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(explicit), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{
		catalogErr: &api.APIError{StatusCode: 404, Code: "repository_projects_inaccessible"},
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{projectID: explicitProjectID, check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reconciliation == nil || decoded.Reconciliation.Outcome != "repository_inaccessible" {
		t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
	}
}

func TestConfigMigrateReportsUnavailableCatalogWhileRemainingLocal(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	connectedMigration(t, &fakeProjectConfigurationClient{
		catalogErr: &api.APIError{StatusCode: 502, Code: "repository_provider_unavailable"},
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
			t.Fatal(err)
		}
	})
	var decoded configMigrateOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Reconciliation == nil ||
		decoded.Reconciliation.Outcome != "repository_provider_unavailable" ||
		!strings.Contains(decoded.Reconciliation.Explanation, "revyl config validate") {
		t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
	}
}

func TestConfigMigrateClassifiesCatalogFailuresWhileRemainingLocal(t *testing.T) {
	tests := []struct {
		name        string
		apiError    *api.APIError
		wantOutcome string
		wantText    []string
	}{
		{
			name: "expired authentication", apiError: &api.APIError{StatusCode: 401},
			wantOutcome: "catalog_authentication_required", wantText: []string{"revyl auth login", "revyl config pull", "revyl config validate"},
		},
		{
			name: "organization access denied", apiError: &api.APIError{StatusCode: 403},
			wantOutcome: "catalog_access_denied", wantText: []string{"revyl auth status", "revyl config pull", "revyl config validate"},
		},
		{
			name: "bounded catalog limit", apiError: &api.APIError{StatusCode: 409, Code: "repository_projects_limit_exceeded"},
			wantOutcome: "catalog_limit_exceeded", wantText: []string{"contact Revyl support", "revyl config validate"},
		},
		{
			name: "provider unavailable", apiError: &api.APIError{StatusCode: 503, Code: "repository_provider_unavailable"},
			wantOutcome: "repository_provider_unavailable", wantText: []string{"Revyl or GitHub", "revyl config validate", "revyl doctor"},
		},
		{
			name: "unclassified server failure", apiError: &api.APIError{StatusCode: 502, Code: "unexpected_server_failure"},
			wantOutcome: "repository_provider_unavailable", wantText: []string{"Revyl or GitHub", "revyl config validate", "revyl doctor"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
				return migrationTestResult(generated), nil
			})
			connectedMigration(t, &fakeProjectConfigurationClient{catalogErr: test.apiError})
			command := testConfigCommand()
			_ = command.Flags().Set("json", "true")
			output := captureStdout(t, func() {
				if err := runConfigMigrate(command, configMigrateOptions{check: true}); err != nil {
					t.Fatalf("best-effort migration failed: %v", err)
				}
			})
			var decoded configMigrateOutput
			if err := json.Unmarshal([]byte(output), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Reconciliation == nil || decoded.Reconciliation.Outcome != test.wantOutcome {
				t.Fatalf("reconciliation = %#v", decoded.Reconciliation)
			}
			for _, want := range test.wantText {
				if !strings.Contains(decoded.Reconciliation.Explanation, want) {
					t.Fatalf("explanation = %q, want %q", decoded.Reconciliation.Explanation, want)
				}
			}
		})
	}
}

func TestConfigMigrateRejectsInvalidExplicitProjectBeforeReading(t *testing.T) {
	prepared := false
	withConfigMigrationDependencies(t, func(_, _, _ string) (preparedLocalConfigMigration, error) {
		prepared = true
		return preparedLocalConfigMigration{}, nil
	})

	err := runConfigMigrate(testConfigCommand(), configMigrateOptions{projectID: "not-a-uuid", check: true})
	if err == nil || !strings.Contains(err.Error(), "invalid --project") {
		t.Fatalf("runConfigMigrate() error = %v", err)
	}
	if prepared {
		t.Fatal("invalid --project read the local config")
	}
}

func TestConfigMigrateCanonicalizesExplicitProjectWithoutGeneratingOne(t *testing.T) {
	const explicit = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	var gotExplicit, gotGenerated string
	withConfigMigrationDependencies(t, func(_, selected, generated string) (preparedLocalConfigMigration, error) {
		gotExplicit, gotGenerated = selected, generated
		return migrationTestResult(selected), nil
	})
	generateConfigMigrationProject = func(string) (string, error) {
		t.Fatal("explicit --project generated another project ID")
		return "", nil
	}

	_ = captureStdout(t, func() {
		if err := runConfigMigrate(
			testConfigCommand(),
			configMigrateOptions{projectID: explicit, check: true},
		); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
	if gotExplicit != generatedMigrationProjectID || gotGenerated != "" {
		t.Fatalf("project IDs = explicit %q generated %q", gotExplicit, gotGenerated)
	}
}

func TestConfigMigrateNonGithubCheckoutHasNoRemoteDependency(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	originalToken := readActiveConfigToken
	originalClient := newProjectConfigClient
	readActiveConfigToken = func() (string, error) {
		return "", errors.New("unexpected authentication lookup")
	}
	newProjectConfigClient = func(string, bool) projectConfigurationClient {
		panic("unexpected project configuration client")
	}
	t.Cleanup(func() {
		readActiveConfigToken = originalToken
		newProjectConfigClient = originalClient
	})

	_ = captureStdout(t, func() {
		if err := runConfigMigrate(testConfigCommand(), configMigrateOptions{check: true}); err != nil {
			t.Fatalf("runConfigMigrate() error = %v", err)
		}
	})
}

func TestConfigMigrateDefaultRequiresTTYAndJSONRequiresExplicitMode(t *testing.T) {
	withConfigMigrationDependencies(t, func(_, _, generated string) (preparedLocalConfigMigration, error) {
		return migrationTestResult(generated), nil
	})
	configMigrationInputTTY = func() bool { return false }
	backupAndReplaceConfig = func(string, []byte, []byte) (string, error) {
		t.Fatal("non-interactive migration wrote the config")
		return "", nil
	}

	_ = captureStdout(t, func() {
		err := runConfigMigrate(testConfigCommand(), configMigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
			t.Fatalf("non-interactive error = %v", err)
		}
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		err := runConfigMigrate(command, configMigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "requires --check or --write") {
			t.Fatalf("JSON error = %v", err)
		}
	})
	if output != "" {
		t.Fatalf("failed JSON migration polluted stdout: %q", output)
	}
}

func TestConfigMigrateConflictingModesHaveDirectError(t *testing.T) {
	command := newConfigMigrateCommand()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"--check", "--write"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--check and --write cannot be used together") {
		t.Fatalf("error = %v", err)
	}
}
