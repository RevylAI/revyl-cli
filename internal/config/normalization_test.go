package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type compilerFixtureManifest struct {
	FixtureVersion int                      `json:"fixture_version"`
	ValidCases     []validCompilerFixture   `json:"valid_cases"`
	InvalidCases   []invalidCompilerFixture `json:"invalid_cases"`
}

type validCompilerFixture struct {
	Name              string                 `json:"name"`
	Context           CompilationContext     `json:"context"`
	YAML              string                 `json:"yaml"`
	ExpectedProjectID string                 `json:"expected_project_id,omitempty"`
	Projection        json.RawMessage        `json:"expected_projection"`
	ExpectedHashes    expectedCompilerHashes `json:"expected_hashes"`
}

type expectedCompilerHashes struct {
	ProjectConfigurationHash string            `json:"project_configuration_hash"`
	BuildProfileHashes       map[string]string `json:"build_profile_hashes"`
	BuildDefinitionHashes    map[string]string `json:"build_definition_hashes"`
	ReviewPolicyHash         *string           `json:"review_policy_hash"`
}

type invalidCompilerFixture struct {
	Name          string              `json:"name"`
	Context       *CompilationContext `json:"context,omitempty"`
	YAML          string              `json:"yaml"`
	ExpectedError ConfigError         `json:"expected_error"`
}

func loadCompilerFixtures(t *testing.T) compilerFixtureManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "config_contract", "compiler_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest compilerFixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func compilerFixtureKind(raw string) (string, error) {
	_, document, err := loadYAMLDocument([]byte(raw))
	if err != nil {
		return "", err
	}
	return ClassifyConfigDocument(document)
}

func assertValidCompilerFixture(t *testing.T, fixture validCompilerFixture, aggregate *NormalizedProjectAggregate) {
	t.Helper()
	if fixture.ExpectedProjectID != "" && aggregate.ProjectID != fixture.ExpectedProjectID {
		t.Fatalf("project id = %q, want %q", aggregate.ProjectID, fixture.ExpectedProjectID)
	}
	gotCanonical, err := CanonicalRevylJSON(ProjectHashProjection(*aggregate))
	if err != nil {
		t.Fatal(err)
	}
	var expectedProjection any
	if err := json.Unmarshal(fixture.Projection, &expectedProjection); err != nil {
		t.Fatal(err)
	}
	wantCanonical, err := CanonicalRevylJSON(expectedProjection)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCanonical) != string(wantCanonical) {
		t.Fatalf("canonical projection mismatch\ngot  %s\nwant %s", gotCanonical, wantCanonical)
	}
	if aggregate.ProjectConfigurationHash != fixture.ExpectedHashes.ProjectConfigurationHash {
		t.Fatalf("project hash = %s, want %s", aggregate.ProjectConfigurationHash, fixture.ExpectedHashes.ProjectConfigurationHash)
	}
	profileHashes := map[string]string{}
	definitionHashes := map[string]string{}
	for _, profile := range aggregate.Profiles {
		profileHashes[profile.Name] = profile.BuildProfileHash
		for _, configuration := range profile.Configurations {
			definitionHashes[profile.Name+":"+configuration.Platform] = configuration.BuildDefinitionHash
		}
	}
	if !reflect.DeepEqual(profileHashes, fixture.ExpectedHashes.BuildProfileHashes) {
		t.Fatalf("profile hashes = %#v, want %#v", profileHashes, fixture.ExpectedHashes.BuildProfileHashes)
	}
	if !reflect.DeepEqual(definitionHashes, fixture.ExpectedHashes.BuildDefinitionHashes) {
		t.Fatalf("definition hashes = %#v, want %#v", definitionHashes, fixture.ExpectedHashes.BuildDefinitionHashes)
	}
	var reviewHash *string
	if aggregate.ReviewPolicy != nil {
		reviewHash = &aggregate.ReviewPolicy.ReviewPolicyHash
	}
	if !reflect.DeepEqual(reviewHash, fixture.ExpectedHashes.ReviewPolicyHash) {
		got := "<nil>"
		if reviewHash != nil {
			got = *reviewHash
		}
		want := "<nil>"
		if fixture.ExpectedHashes.ReviewPolicyHash != nil {
			want = *fixture.ExpectedHashes.ReviewPolicyHash
		}
		t.Fatalf("review hash = %s, want %s", got, want)
	}
}

func TestSharedCompilerCanonicalProjectionsAndHashes(t *testing.T) {
	manifest := loadCompilerFixtures(t)
	if manifest.FixtureVersion != 1 || len(manifest.ValidCases) == 0 {
		t.Fatalf("invalid fixture manifest: %#v", manifest)
	}
	for _, fixture := range manifest.ValidCases {
		fixture := fixture
		kind, err := compilerFixtureKind(fixture.YAML)
		if err != nil {
			t.Fatalf("classify %s: %v", fixture.Name, err)
		}
		if kind != "canonical" {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			aggregate, err := CompileConfigBytes([]byte(fixture.YAML), fixture.Context)
			if err != nil {
				t.Fatal(err)
			}
			assertValidCompilerFixture(t, fixture, aggregate)
		})
	}
}

func TestSharedCompilerLegacyFixturesUsePublicMigration(t *testing.T) {
	manifest := loadCompilerFixtures(t)
	legacyCount := 0
	for _, fixture := range manifest.ValidCases {
		fixture := fixture
		kind, err := compilerFixtureKind(fixture.YAML)
		if err != nil || kind != "legacy" {
			continue
		}
		legacyCount++
		t.Run(fixture.Name, func(t *testing.T) {
			result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
				Data:    []byte(fixture.YAML),
				Context: fixture.Context,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertValidCompilerFixture(t, fixture, result.Aggregate)
		})
	}
	if legacyCount == 0 {
		t.Fatal("legacy fixture not found")
	}
}

func TestSharedCompilerStructuredErrors(t *testing.T) {
	for _, fixture := range loadCompilerFixtures(t).InvalidCases {
		fixture := fixture
		kind, classifyErr := compilerFixtureKind(fixture.YAML)
		if classifyErr == nil && kind == "legacy" {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			context := CompilationContext{".", "."}
			if fixture.Context != nil {
				context = *fixture.Context
			}
			_, err := CompileConfigBytes([]byte(fixture.YAML), context)
			var configError *ConfigError
			if !errorsAs(err, &configError) {
				t.Fatalf("error = %T %v", err, err)
			}
			if configError.Stage != fixture.ExpectedError.Stage || configError.Code != fixture.ExpectedError.Code || !reflect.DeepEqual(configError.Path, fixture.ExpectedError.Path) {
				t.Fatalf("error = %#v, want %#v", configError, fixture.ExpectedError)
			}
		})
	}
}

func TestSemanticContractErrorsPreserveFieldPaths(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
		path []string
	}{
		{
			name: "absolute output path",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: ios\n  profiles:\n    development:\n      ios:\n        build_commands: [build]\n        output_path: /tmp/app.ipa\n",
			path: []string{"build", "profiles", "development", "ios", "output_path"},
		},
		{
			name: "invalid cache key",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: ios\n  caches: [{key: bad/key, paths: [cache]}]\n",
			path: []string{"build", "caches", "0", "key"},
		},
		{
			name: "unknown managed profile",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: ios\n  profiles:\n    development:\n      ios: {build_commands: [build]}\npr_review:\n  build: {kind: revyl, profile: missing}\n",
			path: []string{"pr_review", "build", "profile"},
		},
		{
			name: "revyl harness model",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  proof_of_changes:\n    harness: {kind: revyl, model_id: invalid}\n",
			path: []string{"pr_review", "proof_of_changes", "harness", "model_id"},
		},
		{
			name: "empty external app mapping",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {}}\n",
			path: []string{"pr_review", "build", "app_ids"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileConfigBytes([]byte(test.yaml), CompilationContext{".", "."})
			var configError *ConfigError
			if !errorsAs(err, &configError) {
				t.Fatalf("error = %T %v", err, err)
			}
			if configError.Stage != "contract" || configError.Code != "invalid_contract" || !reflect.DeepEqual(configError.Path, test.path) {
				t.Fatalf("error = %#v, want path %#v", configError, test.path)
			}
		})
	}
}

func TestCompileConfigBytesRejectsLegacyWithoutTranslation(t *testing.T) {
	for _, fixture := range loadCompilerFixtures(t).ValidCases {
		kind, err := compilerFixtureKind(fixture.YAML)
		if err != nil || kind != "legacy" {
			continue
		}
		_, err = CompileConfigBytes([]byte(fixture.YAML), fixture.Context)
		assertConfigError(t, err, "classification", "legacy_config_requires_migration")
		return
	}
	t.Fatal("legacy fixture not found")
}

func TestMappingOrderIsIrrelevantButCommandsAreNot(t *testing.T) {
	first := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios:\n        build_commands: [one, two, one]\n")
	reordered := []byte("build:\n  profiles:\n    dev:\n      ios: {build_commands: [one, two, one]}\n  framework: expo\nproject:\n  id: 11111111-1111-4111-8111-111111111111\n")
	changed := []byte(strings.Replace(string(reordered), "[one, two, one]", "[two, one]", 1))
	context := CompilationContext{".", "."}
	a := mustCompileConfig(t, first, context)
	b := mustCompileConfig(t, reordered, context)
	c := mustCompileConfig(t, changed, context)
	if a.ProjectConfigurationHash != b.ProjectConfigurationHash || a.ProjectConfigurationHash == c.ProjectConfigurationHash {
		t.Fatalf("hashes = %s, %s, %s", a.ProjectConfigurationHash, b.ProjectConfigurationHash, c.ProjectConfigurationHash)
	}
}

func TestNestedInvocationDoesNotChangeSavedRecipeMeaning(t *testing.T) {
	raw := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios: {build_commands: [build]}\n")
	atRoot := mustCompileConfig(t, raw, CompilationContext{"apps/mobile", "apps/mobile"})
	nested := mustCompileConfig(t, raw, CompilationContext{"apps/mobile", "apps/mobile/src/features"})
	if atRoot.ProjectConfigurationHash != nested.ProjectConfigurationHash {
		t.Fatalf("nested invocation changed hash: %s != %s", atRoot.ProjectConfigurationHash, nested.ProjectConfigurationHash)
	}
	if got := nested.Profiles[0].Configurations[0].Recipe.ExecutionDirectory; got != "apps/mobile" {
		t.Fatalf("execution directory = %q", got)
	}
}

func TestSelectionResolverBranches(t *testing.T) {
	fixture := loadCompilerFixtures(t).ValidCases[0]
	aggregate, err := CompileConfigBytes([]byte(fixture.YAML), fixture.Context)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := ResolveProfilePlatform(*aggregate, "production", "android", false)
	if err != nil || explicit.Resolved == nil || explicit.Resolved.Profile != "production" {
		t.Fatalf("explicit = %#v, %v", explicit, err)
	}
	development, err := ResolveProfilePlatform(*aggregate, "", "", true)
	if err != nil || development.Resolved == nil || development.Resolved.Profile != "ios-dev" {
		t.Fatalf("development = %#v, %v", development, err)
	}
	sole, err := ResolveProfilePlatform(*aggregate, "", "android", false)
	if err != nil || sole.Resolved == nil || sole.Resolved.Profile != "production" {
		t.Fatalf("sole = %#v, %v", sole, err)
	}
	ambiguous, err := ResolveProfilePlatform(*aggregate, "", "", false)
	if err != nil || ambiguous.Ambiguity == nil || ambiguous.Ambiguity.RequiredFlag != "--profile" || len(ambiguous.Ambiguity.Choices) != 2 {
		t.Fatalf("ambiguity = %#v, %v", ambiguous, err)
	}
}

func TestSelectionResolverExplainsUnavailableChoices(t *testing.T) {
	fixture := loadCompilerFixtures(t).ValidCases[0]
	aggregate, err := CompileConfigBytes([]byte(fixture.YAML), fixture.Context)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		aggregate NormalizedProjectAggregate
		profile   string
		platform  string
		code      string
		path      []string
	}{
		{
			name:      "no profiles",
			aggregate: NormalizedProjectAggregate{},
			code:      "no_build_profiles",
			path:      []string{"build", "profiles"},
		},
		{
			name:      "unknown profile",
			aggregate: *aggregate,
			profile:   "missing",
			platform:  "android",
			code:      "unknown_or_ineligible_profile",
			path:      []string{"build", "profiles", "missing"},
		},
		{
			name:      "profile does not configure platform",
			aggregate: *aggregate,
			profile:   "ios-dev",
			platform:  "android",
			code:      "profile_platform_not_configured",
			path:      []string{"build", "profiles", "ios-dev", "android"},
		},
		{
			name:      "no profile configures platform",
			aggregate: *aggregate,
			platform:  "windows",
			code:      "no_build_profile_for_platform",
			path:      []string{"build", "profiles", "windows"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, selectionErr := ResolveProfilePlatform(test.aggregate, test.profile, test.platform, false)
			var configError *ConfigError
			if !errorsAs(selectionErr, &configError) {
				t.Fatalf("error = %T %v", selectionErr, selectionErr)
			}
			if configError.Stage != "selection" || configError.Code != test.code || !reflect.DeepEqual(configError.Path, test.path) {
				t.Fatalf("error = %#v, want code %q path %#v", configError, test.code, test.path)
			}
		})
	}
}

func TestAuthoringExecutionAndPublicationStagesStaySeparate(t *testing.T) {
	raw := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios:\n        build_commands: []\n")
	aggregate := mustCompileConfig(t, raw, CompilationContext{".", "."})
	recipe := aggregate.Profiles[0].Configurations[0].Recipe
	assertConfigError(t, ValidateExecutionRecipe(recipe), "validation", "recipe_not_runnable")
	if err := ValidatePublication(*aggregate, PublicationValidationContext{}); err != nil {
		t.Fatalf("unmanaged local-only profile publication = %v", err)
	}
}

func TestOmittedStrictCheckDefaultsTrue(t *testing.T) {
	raw := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build:\n    kind: ci_upload_to_revyl\n    app_ids: {ios: 22222222-2222-4222-8222-222222222222}\n")
	aggregate := mustCompileConfig(t, raw, CompilationContext{".", "."})
	if !aggregate.ReviewPolicy.StrictBuildCheck {
		t.Fatal("omitted strict build check defaulted false")
	}
}

func TestExplicitStrictChecksRemainFalse(t *testing.T) {
	t.Run("canonical", func(t *testing.T) {
		raw := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  strict_ci_check: {build: false}\n")
		aggregate := mustCompileConfig(t, raw, CompilationContext{".", "."})
		if aggregate.ReviewPolicy.StrictBuildCheck {
			t.Fatal("explicit false normalized true")
		}
	})
}

func TestPublicationRequiresAppOnlyForManagedSelectedProfile(t *testing.T) {
	missingSelected := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios: {build_commands: [dev]}\n    local:\n      android: {build_commands: [local]}\npr_review:\n  build: {kind: revyl, profile: dev}\n")
	aggregate := mustCompileConfig(t, missingSelected, CompilationContext{".", "."})
	assertConfigError(t, ValidatePublication(*aggregate, PublicationValidationContext{}), "validation", "published_recipe_app_id_required")

	selectedApp := strings.Replace(string(missingSelected), "ios: {build_commands: [dev]}", "ios: {app_id: 22222222-2222-4222-8222-222222222222, build_commands: [dev]}", 1)
	aggregate = mustCompileConfig(t, []byte(selectedApp), CompilationContext{".", "."})
	if err := ValidatePublication(*aggregate, PublicationValidationContext{ActiveAppIDs: map[string]struct{}{"22222222-2222-4222-8222-222222222222": {}}}); err != nil {
		t.Fatalf("publication with unrelated local-only profile = %v", err)
	}
}

func TestPublicationContextUsesOnlySuppliedOrganizationFacts(t *testing.T) {
	fixture := loadCompilerFixtures(t).ValidCases[0]
	aggregate, err := CompileConfigBytes([]byte(fixture.YAML), fixture.Context)
	if err != nil {
		t.Fatal(err)
	}
	context := PublicationValidationContext{
		ActiveAppIDs: map[string]struct{}{
			"22222222-2222-4222-8222-222222222222": {}, "33333333-3333-4333-8333-333333333333": {},
		},
		ActiveWorkflowIDs:   map[string]struct{}{"44444444-4444-4444-8444-444444444444": {}},
		AvailableSecretRefs: map[string]struct{}{"TOKEN": {}, "CERT": {}},
		AvailableLaunchVars: map[string]struct{}{"AUTH_TOKEN": {}, "AUTH_MODE": {}},
	}
	if err := ValidatePublication(*aggregate, context); err != nil {
		t.Fatal(err)
	}
	context.AvailableLaunchVars = nil
	assertConfigError(t, ValidatePublication(*aggregate, context), "validation", "launch_var_not_available_for_organization")
}

func TestEffectiveDirectoryAndNearestConfigStayInsideWorktree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(root, "apps", "mobile", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	rootConfig := filepath.Join(root, ".revyl", "config.yaml")
	nestedConfig := filepath.Join(root, "apps", "mobile", ".revyl", "config.yaml")
	for _, configPath := range []string{rootConfig, nestedConfig} {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("project: {id: 11111111-1111-4111-8111-111111111111}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	effective, err := ResolveEffectiveDirectory(root, filepath.Join("apps", "mobile", "src"))
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := DiscoverConfigPath(effective, root)
	resolvedNestedConfig, resolveErr := filepath.EvalSymlinks(nestedConfig)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || discovered != resolvedNestedConfig {
		t.Fatalf("discovered = %q, %v", discovered, err)
	}
	if _, err := ReadConfigFile(discovered); err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverConfigPath(filepath.Dir(root), root)
	assertConfigError(t, err, "read", "effective_directory_outside_worktree")
}

func TestSymlinkSpecialFileAndBoundedReadsFailSafely(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	configDir := filepath.Join(root, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("project: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.Symlink(outside, configPath); err != nil {
		t.Fatal(err)
	}
	_, err := DiscoverConfigPath(root, root)
	assertConfigError(t, err, "read", "config_not_regular_file")
	_, err = ReadConfigFile(configPath)
	assertConfigError(t, err, "read", "config_not_regular_file")
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configPath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverConfigPath(root, root)
	assertConfigError(t, err, "read", "config_not_regular_file")
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, make([]byte, MaxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverConfigPath(root, root)
	assertConfigError(t, err, "read", "config_too_large")
}

func TestReadConfigFileRejectsSymlinkedConfigDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "config.yaml"), []byte("project: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, ".revyl")
	if err := os.Symlink(outside, configDir); err != nil {
		t.Fatal(err)
	}
	_, err := ReadConfigFile(filepath.Join(configDir, "config.yaml"))
	assertConfigError(t, err, "read", "config_not_regular_file")
}

func TestCompileConfigBoundsInheritedConfigurationExpansion(t *testing.T) {
	var source strings.Builder
	source.WriteString("project: {id: 11111111-1111-4111-8111-111111111111}\n")
	source.WriteString("build:\n  framework: expo\n  env:\n    SHARED: ")
	source.WriteString(strings.Repeat("x", 900_000))
	source.WriteString("\n  profiles:\n")
	for index := 0; index < 10; index++ {
		source.WriteString("    profile-")
		source.WriteString(strconv.Itoa(index))
		source.WriteString(":\n      android:\n        build_commands: [build]\n")
	}

	_, err := CompileConfigBytes(
		[]byte(source.String()),
		CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	)
	assertConfigError(t, err, "normalization", "normalized_config_too_large")
}

func TestCompileConfigBoundsProfileFanout(t *testing.T) {
	var source strings.Builder
	source.WriteString("project: {id: 11111111-1111-4111-8111-111111111111}\n")
	source.WriteString("build:\n  framework: expo\n  profiles:\n")
	for index := 0; index < MaxConfigProfiles+1; index++ {
		source.WriteString("    profile-")
		source.WriteString(strconv.Itoa(index))
		source.WriteString(":\n      android:\n        build_commands: [build]\n")
	}

	_, err := CompileConfigBytes(
		[]byte(source.String()),
		CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	)
	assertConfigError(t, err, "normalization", "too_many_build_profiles")
}

func TestNormalizeConfigBoundsCompactUnicodeEncoding(t *testing.T) {
	commands := []string{"build"}
	authored := AuthoredConfig{
		Project: AuthoredProject{ID: projectFileTestProjectID},
		Build: &AuthoredBuild{
			Framework: "expo",
			Env:       map[string]string{"SEPARATOR": strings.Repeat("\u2028", 180_000)},
			Profiles: map[string]AuthoredBuildProfile{
				"development": {
					Android: &AuthoredBuildRecipe{BuildCommands: &commands},
				},
			},
		},
	}

	_, err := NormalizeAuthoredConfig(
		authored,
		CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	)
	assertConfigError(t, err, "normalization", "canonical_config_too_large")
}

func TestNormalizeConfigUsesExpandedDefaultCompactBoundary(t *testing.T) {
	commands := []string{"build"}
	authored := AuthoredConfig{
		Project: AuthoredProject{ID: projectFileTestProjectID},
		Build: &AuthoredBuild{
			Framework: "expo",
			Env:       map[string]string{"PADDING": ""},
			Profiles: map[string]AuthoredBuildProfile{
				"development": {
					Android: &AuthoredBuildRecipe{BuildCommands: &commands},
				},
			},
		},
	}
	base, err := encodeCanonicalAuthoredConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	authored.Build.Env["PADDING"] = strings.Repeat("x", MaxConfigBytes-len(base))
	encoded, err := encodeCanonicalAuthoredConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != MaxConfigBytes {
		t.Fatalf("encoded size = %d, want %d", len(encoded), MaxConfigBytes)
	}

	if _, err := NormalizeAuthoredConfig(
		authored,
		CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	); err != nil {
		t.Fatal(err)
	}
}

func FuzzCompileConfigBytes(f *testing.F) {
	f.Add([]byte("project: {id: 11111111-1111-4111-8111-111111111111}\n"))
	f.Add([]byte("project: &p {}\ncopy: *p\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = CompileConfigBytes(data, CompilationContext{".", "."})
	})
}

func mustCompileConfig(t *testing.T, data []byte, context CompilationContext) *NormalizedProjectAggregate {
	t.Helper()
	aggregate, err := CompileConfigBytes(data, context)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func assertConfigError(t *testing.T, err error, stage, code string) {
	t.Helper()
	var configError *ConfigError
	if !errorsAs(err, &configError) || configError.Stage != stage || configError.Code != code {
		t.Fatalf("error = %T %#v, want %s/%s", err, err, stage, code)
	}
}

func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	configError, ok := err.(*ConfigError)
	if !ok {
		return false
	}
	pointer, ok := target.(**ConfigError)
	if !ok {
		return false
	}
	*pointer = configError
	return true
}
