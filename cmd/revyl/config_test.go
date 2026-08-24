package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestConfigCommandOwnsOnlyCanonicalInspectionAndLifecycleCommands(t *testing.T) {
	want := map[string]bool{
		"migrate":  false,
		"path":     false,
		"pull":     false,
		"push":     false,
		"show":     false,
		"validate": false,
	}
	for _, command := range configCmd.Commands() {
		if _, expected := want[command.Name()]; expected {
			want[command.Name()] = true
		}
		if command.Name() == "set" || command.Name() == "edit" {
			t.Fatalf("legacy config mutator %q is still registered", command.Name())
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("config command %q is not registered", name)
		}
	}
}

func TestConfigCommandRejectsRemovedMutators(t *testing.T) {
	for _, removed := range []string{"edit", "set"} {
		t.Run(removed, func(t *testing.T) {
			err := configCmd.Args(configCmd, []string{removed})
			if err == nil || !strings.Contains(err.Error(), `unknown command "`+removed+`"`) {
				t.Fatalf("config %s error = %v, want unknown command", removed, err)
			}
		})
	}
}

func TestRunConfigPathUsesCanonicalWorktreeBoundedContext(t *testing.T) {
	configPath := "/repo/apps/mobile/.revyl/config.yaml"
	originalResolve := resolveLocalConfigFileContext
	resolveLocalConfigFileContext = func(string, string) (*config.ConfigFileContext, error) {
		return &config.ConfigFileContext{ConfigPath: configPath}, nil
	}
	t.Cleanup(func() { resolveLocalConfigFileContext = originalResolve })

	output := captureStdout(t, func() {
		if err := runConfigPath(testConfigCommand(), nil); err != nil {
			t.Fatalf("runConfigPath() error = %v", err)
		}
	})
	if output != configPath+"\n" {
		t.Fatalf("path output = %q, want %q", output, configPath+"\n")
	}
}

func TestRunConfigPathJSONIsOneParseableObject(t *testing.T) {
	configPath := "/repo/.revyl/config.yaml"
	originalResolve := resolveLocalConfigFileContext
	resolveLocalConfigFileContext = func(string, string) (*config.ConfigFileContext, error) {
		return &config.ConfigFileContext{ConfigPath: configPath}, nil
	}
	t.Cleanup(func() { resolveLocalConfigFileContext = originalResolve })
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	output := captureStdout(t, func() {
		if err := runConfigPath(command, nil); err != nil {
			t.Fatalf("runConfigPath() error = %v", err)
		}
	})
	var decoded configPathOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("path output is not JSON: %v\n%s", err, output)
	}
	if decoded.Path != configPath {
		t.Fatalf("path = %q, want %q", decoded.Path, configPath)
	}
}

func TestRunConfigShowPreservesValidatedLocalBytesForHumans(t *testing.T) {
	local := configContext(t, 300)
	local.OriginalBytes = []byte("# retained comment\nproject:\n  id: " + configRemoteProjectID)
	withProjectConfigurationDependencies(t, local, "", nil)

	output := captureStdout(t, func() {
		if err := runConfigShow(testConfigCommand(), nil); err != nil {
			t.Fatalf("runConfigShow() error = %v", err)
		}
	})
	if output != string(local.OriginalBytes)+"\n" {
		t.Fatalf("show output = %q", output)
	}
}

func TestRunConfigShowJSONUsesCanonicalAuthoredContract(t *testing.T) {
	local := configContext(t, 300)
	local.RepositoryRelativeProjectRoot = "apps/mobile"
	withProjectConfigurationDependencies(t, local, "", nil)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	output := captureStdout(t, func() {
		if err := runConfigShow(command, nil); err != nil {
			t.Fatalf("runConfigShow() error = %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("show output is not JSON: %v\n%s", err, output)
	}
	configuration, ok := decoded["configuration"].(map[string]any)
	if !ok {
		t.Fatalf("configuration = %#v", decoded["configuration"])
	}
	project, ok := configuration["project"].(map[string]any)
	if !ok || project["id"] != configRemoteProjectID {
		t.Fatalf("project = %#v", configuration["project"])
	}
	if _, legacy := configuration["defaults"]; legacy {
		t.Fatalf("show emitted legacy defaults: %#v", configuration)
	}
	if decoded["repository_relative_project_root"] != "apps/mobile" {
		t.Fatalf("project root = %#v", decoded["repository_relative_project_root"])
	}
}

func TestConfigShowDirectsLegacyFilesToMigration(t *testing.T) {
	originalResolve := resolveProjectContext
	resolveProjectContext = func(string, string) (*config.ProjectContext, error) {
		return nil, &config.ConfigError{
			Stage: "classification",
			Code:  "legacy_config_requires_migration",
		}
	}
	t.Cleanup(func() { resolveProjectContext = originalResolve })

	err := runConfigShow(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "revyl config migrate") {
		t.Fatalf("runConfigShow() error = %v", err)
	}
	var configError *config.ConfigError
	if errors.As(err, &configError) {
		t.Fatalf("legacy compiler error leaked without actionable guidance: %v", err)
	}
}

func TestActionableLocalConfigErrorProvidesRecoveryCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		err  *config.ConfigError
		want []string
	}{
		{
			name: "missing",
			err:  &config.ConfigError{Stage: "read", Code: "config_not_found"},
			want: []string{"revyl config pull", "revyl init -y"},
		},
		{
			name: "legacy",
			err:  &config.ConfigError{Stage: "classification", Code: "legacy_config_requires_migration"},
			want: []string{"revyl config migrate --check", "revyl config migrate"},
		},
		{
			name: "invalid field",
			err:  &config.ConfigError{Stage: "contract", Code: "missing_field", Path: []string{"project", "id"}},
			want: []string{"project.id", "revyl config path", "revyl config validate", "revyl init --force -y"},
		},
		{
			name: "invalid yaml source line",
			err:  &config.ConfigError{Stage: "yaml_syntax", Code: "invalid_yaml", Line: 8, Column: 12},
			want: []string{"line 8, column 12", "revyl config path", "revyl config validate"},
		},
		{
			name: "unknown build profile",
			err:  &config.ConfigError{Stage: "selection", Code: "unknown_or_ineligible_profile", Path: []string{"build", "profiles", "release"}},
			want: []string{`build profile "release" is not configured`, "revyl config show", "--profile <name>"},
		},
		{
			name: "profile platform unavailable",
			err:  &config.ConfigError{Stage: "selection", Code: "profile_platform_not_configured", Path: []string{"build", "profiles", "release", "android"}},
			want: []string{`build profile "release" does not configure platform "android"`, "revyl config show"},
		},
		{
			name: "no profiles",
			err:  &config.ConfigError{Stage: "selection", Code: "no_build_profiles", Path: []string{"build", "profiles"}},
			want: []string{"no build profiles are configured", "revyl config validate"},
		},
		{
			name: "environment secret collision",
			err:  &config.ConfigError{Stage: "normalization", Code: "environment_secret_collision", Path: []string{"build", "profiles", "development", "ios", "env", "TOKEN"}},
			want: []string{`build variable "TOKEN"`, "both plaintext env and an encrypted secret", "build.profiles.development.ios.env.TOKEN", "revyl config validate"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recovery := actionableLocalConfigError(test.err)
			for _, want := range test.want {
				if !strings.Contains(recovery.Error(), want) {
					t.Fatalf("actionableLocalConfigError() = %q, want %q", recovery, want)
				}
			}
			if strings.Contains(recovery.Error(), test.err.Stage+": "+test.err.Code) {
				t.Fatalf("raw compiler identity leaked: %v", recovery)
			}
		})
	}
}

func TestActionableLocalConfigErrorPreservesQuotedRecoveryCommands(t *testing.T) {
	originalRoot, _ := rootCmd.PersistentFlags().GetString("chdir")
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("chdir", originalRoot)
	})
	_ = rootCmd.PersistentFlags().Set("chdir", "apps/My App")
	t.Chdir(t.TempDir())

	for _, test := range []struct {
		name     string
		err      *config.ConfigError
		commands [][]string
	}{
		{
			name:     "missing",
			err:      &config.ConfigError{Stage: "read", Code: "config_not_found"},
			commands: [][]string{{"config", "pull"}, {"init", "-y"}},
		},
		{
			name:     "legacy",
			err:      &config.ConfigError{Stage: "classification", Code: "legacy_config_requires_migration"},
			commands: [][]string{{"config", "migrate", "--check"}, {"config", "migrate"}},
		},
		{
			name:     "invalid field",
			err:      &config.ConfigError{Stage: "contract", Code: "missing_field", Path: []string{"project", "id"}},
			commands: [][]string{{"config", "path"}, {"config", "validate"}, {"init", "--force", "-y"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := actionableLocalConfigError(test.err).Error()
			for _, arguments := range test.commands {
				command := cliRecoveryCommand(arguments...)
				if !strings.Contains(message, "`"+command+"`") {
					t.Fatalf("actionableLocalConfigError() = %q, want readable command delimiter around %q", message, command)
				}
			}
		})
	}
}

func TestActionableLocalConfigErrorSelectsNestedProject(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".revyl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".revyl", "config.yaml"), []byte("project: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	recovery := actionableLocalConfigError(&config.ConfigError{Stage: "read", Code: "config_not_found"})
	want := "revyl -C " + quoteCLIRecoveryArgument(projectRoot) + " config path"
	if !strings.Contains(recovery.Error(), strconv.Quote(want)) {
		t.Fatalf("actionableLocalConfigError() = %q, want %q", recovery, want)
	}
	if strings.Contains(recovery.Error(), "config pull") || strings.Contains(recovery.Error(), "init -y") {
		t.Fatalf("nested-project recovery suggested creating a root project: %v", recovery)
	}
}

func TestActionableLocalConfigErrorListsAmbiguousNestedProjects(t *testing.T) {
	root := t.TempDir()
	projectRoots := []string{
		filepath.Join(root, "apps", "android"),
		filepath.Join(root, "apps", "ios"),
	}
	for _, projectRoot := range projectRoots {
		if err := os.MkdirAll(filepath.Join(projectRoot, ".revyl"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectRoot, ".revyl", "config.yaml"), []byte("project: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	recovery := actionableLocalConfigError(&config.ConfigError{Stage: "read", Code: "config_not_found"})
	for _, projectRoot := range projectRoots {
		want := "revyl -C " + quoteCLIRecoveryArgument(projectRoot) + " config path"
		if !strings.Contains(recovery.Error(), want) {
			t.Fatalf("actionableLocalConfigError() = %q, want %q", recovery, want)
		}
	}
}
