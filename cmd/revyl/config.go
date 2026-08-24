// Package main provides project configuration commands for .revyl/config.yaml.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and synchronize project configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
	Long: `Inspect, validate, migrate, pull, and publish .revyl/config.yaml.

EXAMPLES:
  revyl config path
  revyl config show
  revyl config validate
  revyl config migrate --check
  revyl config push
  revyl config pull
  revyl config authorize-cursor-proof`,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show the selected project config path",
	Args:  cobra.NoArgs,
	RunE:  runConfigPath,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the selected project configuration",
	Args:  cobra.NoArgs,
	RunE:  runConfigShow,
}

func init() {
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configShowCmd)
}

type configPathOutput struct {
	Path string `json:"path"`
}

type configShowOutput struct {
	Path                          string                `json:"path"`
	RepositoryRelativeProjectRoot string                `json:"repository_relative_project_root"`
	Configuration                 config.AuthoredConfig `json:"configuration"`
}

var resolveLocalConfigFileContext = config.ResolveConfigFileContext

func runConfigPath(cmd *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	local, err := resolveLocalConfigFileContext(cwd, "")
	if err != nil {
		return actionableLocalConfigError(err)
	}
	if projectConfigurationJSON(cmd) {
		return json.NewEncoder(os.Stdout).Encode(configPathOutput{Path: local.ConfigPath})
	}
	_, err = fmt.Fprintln(os.Stdout, local.ConfigPath)
	return err
}

func runConfigShow(cmd *cobra.Command, _ []string) error {
	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	if projectConfigurationJSON(cmd) {
		return json.NewEncoder(os.Stdout).Encode(configShowOutput{
			Path:                          local.ConfigPath,
			RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
			Configuration:                 *local.Authored,
		})
	}
	if _, err := os.Stdout.Write(local.OriginalBytes); err != nil {
		return fmt.Errorf("show project configuration: %w", err)
	}
	if len(local.OriginalBytes) == 0 || local.OriginalBytes[len(local.OriginalBytes)-1] != '\n' {
		_, err = fmt.Fprintln(os.Stdout)
	}
	return err
}

func actionableLocalConfigError(err error) error {
	return analytics.WithSafeDiagnostic(
		actionableLocalConfigMessage(err),
		"project configuration could not be used",
	)
}

func actionableLocalConfigMessage(err error) error {
	var configError *config.ConfigError
	if !errors.As(err, &configError) {
		return err
	}

	field := ""
	if len(configError.Path) > 0 {
		field = fmt.Sprintf(" at %s", strings.Join(configError.Path, "."))
	} else if configError.Line > 0 && configError.Column > 0 {
		field = fmt.Sprintf(" at line %d, column %d", configError.Line, configError.Column)
	} else if configError.Line > 0 {
		field = fmt.Sprintf(" at line %d", configError.Line)
	}
	migrateCheck := cliRecoveryCommand("config", "migrate", "--check")
	migrate := cliRecoveryCommand("config", "migrate")
	pull := cliRecoveryCommand("config", "pull")
	initialize := cliRecoveryCommand("init", "-y")
	forceInitialize := cliRecoveryCommand("init", "--force", "-y")
	validate := cliRecoveryCommand("config", "validate")
	configPath := cliRecoveryCommand("config", "path")
	configShow := cliRecoveryCommand("config", "show")
	doctor := cliRecoveryCommand("doctor")
	switch configError.Code {
	case "legacy_config_requires_migration", "mixed_config_formats":
		return fmt.Errorf("local configuration uses a legacy format; run `%s` to preview the conversion, then run `%s`", migrateCheck, migrate)
	case "config_not_found":
		if recovery := nestedProjectConfigRecovery(); recovery != "" {
			return errors.New(recovery)
		}
		return fmt.Errorf("no .revyl/config.yaml applies to the current directory; run `%s` to restore an existing Revyl project, or run `%s` to create one", pull, initialize)
	case "git_worktree_unavailable", "path_outside_git_worktree", "worktree_root_unavailable", "effective_directory_outside_worktree":
		return fmt.Errorf("the current directory is not a usable Git worktree; run from the project directory or pass '-C <project-dir>'; for a new repository, run 'git init', then %q", initialize)
	case "effective_directory_unavailable", "effective_directory_not_directory":
		return fmt.Errorf("the selected project directory is unavailable; pass '-C <project-dir>' with an existing directory, then run %q", validate)
	case "config_not_regular_file":
		return fmt.Errorf(".revyl/config.yaml is not a regular file; move the conflicting path aside, then run %q to restore it or %q to create it", pull, initialize)
	case "config_too_large", "normalized_config_too_large", "canonical_config_too_large":
		return fmt.Errorf(".revyl/config.yaml is too large to load%s; reduce it below the supported limit, then run %q; to replace it, back it up and run %q", field, validate, forceInitialize)
	case "invalid_utf8":
		return fmt.Errorf(".revyl/config.yaml is not valid UTF-8; run %q to locate it, re-save it as UTF-8, then run %q", configPath, validate)
	case "invalid_yaml", "single_mapping_document_required", "unsupported_yaml_structure", "invalid_mapping_key", "duplicate_mapping_key":
		return fmt.Errorf(".revyl/config.yaml is not a supported single YAML mapping%s; run %q to locate and repair it, then run %q; to replace it, back it up and run %q", field, configPath, validate, forceInitialize)
	case "unknown_field":
		return fmt.Errorf(".revyl/config.yaml contains an unsupported field%s; run %q to locate it, remove that field, then run %q", field, configPath, validate)
	case "missing_field":
		return fmt.Errorf(".revyl/config.yaml is missing a required field%s; run `%s` to locate it, add that field, then run `%s`; to replace the file, back it up and run `%s`", field, configPath, validate, forceInitialize)
	case "invalid_contract":
		return fmt.Errorf(".revyl/config.yaml contains an invalid value%s; run `%s` to locate it, correct that field, then run `%s`; to replace the file, back it up and run `%s`", field, configPath, validate, forceInitialize)
	case "config_changed_during_read", "config_changed_before_write", "config_changed_during_write", "test_alias_destination_changed":
		return fmt.Errorf(".revyl/config.yaml changed while Revyl was using it; wait for the other writer to finish, then run %q and retry the command", validate)
	case "test_alias_directory_conflict":
		return fmt.Errorf(".revyl/tests conflicts with the directory required for migrated test aliases; move the conflicting path aside, then run %q and retry %q", migrateCheck, migrate)
	case "test_alias_directory_unreadable", "test_alias_directory_create_failed":
		return fmt.Errorf(".revyl/tests could not be read or created; make .revyl readable and writable, then run %q and retry %q", migrateCheck, migrate)
	case "test_alias_destination_conflict", "test_alias_destination_invalid":
		return fmt.Errorf("a migrated test alias conflicts with the existing local test%s; move or repair that file, then run %q and retry %q", field, migrateCheck, migrate)
	case "test_alias_destination_unreadable":
		return fmt.Errorf("a migrated test alias destination could not be read%s; make that file readable, then run %q and retry %q", field, migrateCheck, migrate)
	case "test_alias_encode_failed", "test_alias_rollback_failed":
		return fmt.Errorf("Revyl could not safely materialize the migrated test alias files%s; retry %q, then run %q if it still fails", field, migrate, doctor)
	case "unknown_or_ineligible_profile":
		profile := lastConfigErrorPathSegment(configError.Path)
		return fmt.Errorf("build profile %q is not configured; run %q to list configured profiles, then retry with '--profile <name>' and, when needed, '--platform ios|android'", profile, configShow)
	case "no_build_profiles":
		return fmt.Errorf("no build profiles are configured; add a recipe under 'build.profiles.<name>.ios' or 'build.profiles.<name>.android', then run %q", validate)
	case "no_build_profile_for_platform":
		platform := lastConfigErrorPathSegment(configError.Path)
		return fmt.Errorf("no build profile configures platform %q; run %q to list configured recipes, then add that platform recipe or retry with '--platform ios|android'", platform, configShow)
	case "profile_platform_not_configured":
		profile, platform := trailingConfigErrorPathSegments(configError.Path)
		return fmt.Errorf("build profile %q does not configure platform %q; run %q to list configured recipes, then retry with a supported '--profile' and '--platform' pair", profile, platform, configShow)
	case "environment_secret_collision":
		name := lastConfigErrorPathSegment(configError.Path)
		return fmt.Errorf("build variable %q is declared as both plaintext env and an encrypted secret%s; remove one declaration, then run %q", name, field, validate)
	case "recipe_not_runnable":
		return fmt.Errorf("the selected build recipe is not runnable%s; add build commands in .revyl/config.yaml, then run %q", field, validate)
	}

	switch configError.Stage {
	case "yaml_syntax", "classification", "contract", "normalization", "validation":
		return fmt.Errorf(".revyl/config.yaml is invalid%s; run %q to locate it, fix the reported field, then run %q; to replace it, back it up and run %q", field, configPath, validate, forceInitialize)
	case "read":
		return fmt.Errorf(".revyl/config.yaml could not be read%s; run %q to locate it, make it a readable regular file, then run %q", field, configPath, validate)
	case "write":
		return fmt.Errorf(".revyl/config.yaml could not be updated%s; make the file and its .revyl directory writable, then run %q and retry the command", field, validate)
	case "selection":
		return fmt.Errorf("the project configuration is ambiguous%s; run %q, then retry with '--profile <name>' and '--platform ios|android'", field, configShow)
	case "legacy_translation":
		return fmt.Errorf("the legacy configuration could not be migrated%s; run %q to inspect the conversion, then retry %q", field, migrateCheck, migrate)
	default:
		return fmt.Errorf("the project configuration could not be used%s; run %q to locate and back it up, then run %q and %q", field, configPath, forceInitialize, validate)
	}
}

func lastConfigErrorPathSegment(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func trailingConfigErrorPathSegments(path []string) (string, string) {
	if len(path) < 2 {
		return "", lastConfigErrorPathSegment(path)
	}
	return path[len(path)-2], path[len(path)-1]
}

func nestedProjectConfigRecovery() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	projectRoots, err := config.FindNestedProjectRoots(cwd)
	if err != nil || len(projectRoots) == 0 {
		return ""
	}
	if len(projectRoots) == 1 {
		command := fmt.Sprintf("revyl -C %s config path", quoteCLIRecoveryArgument(projectRoots[0]))
		return fmt.Sprintf("no .revyl/config.yaml applies to the current directory, but a nested Revyl project exists; run %q to select it", command)
	}
	commands := make([]string, 0, len(projectRoots))
	for _, root := range projectRoots {
		commands = append(commands, fmt.Sprintf("revyl -C %s config path", quoteCLIRecoveryArgument(root)))
	}
	return fmt.Sprintf("no .revyl/config.yaml applies to the current directory; select one nested Revyl project with %s", strings.Join(commands, " or "))
}
