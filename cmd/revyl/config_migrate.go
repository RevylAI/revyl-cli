package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/workflowref"
)

type configMigrateOptions struct {
	projectID string
	check     bool
	write     bool
}

type configMigrateOutput struct {
	Outcome        string                             `json:"outcome"`
	ProjectID      string                             `json:"project_id"`
	Configuration  config.AuthoredConfig              `json:"configuration"`
	BackupPath     string                             `json:"backup_path,omitempty"`
	TestAliases    []config.LegacyConfigTestAliasPlan `json:"test_aliases,omitempty"`
	Omissions      []config.LegacyConfigOmission      `json:"omissions,omitempty"`
	Reconciliation *configMigrationReconciliation     `json:"reconciliation,omitempty"`
}

type configMigrationReconciliation struct {
	Status      string                      `json:"status"`
	Outcome     string                      `json:"outcome,omitempty"`
	Authority   *api.ConfigurationAuthority `json:"authority,omitempty"`
	NextAction  string                      `json:"next_action"`
	Explanation string                      `json:"explanation"`
}

type preparedLocalConfigMigration struct {
	WorktreeRoot                  string
	ConfigPath                    string
	RepositoryRelativeProjectRoot string
	CompilationContext            config.CompilationContext
	OriginalBytes                 []byte
	AlreadyCanonical              bool
	ProjectID                     string
	Authored                      config.AuthoredConfig
	CanonicalBytes                []byte
	ProjectConfigurationHash      string
	TestAliases                   []config.LegacyTestAlias
	TestAliasPlan                 []config.LegacyConfigTestAliasPlan
	Omissions                     []config.LegacyConfigOmission
	LegacyAppIDsByPlatformAndName map[string]map[string]string
	LegacyWorkflowIDsByName       map[string]string
}

type configMigrationIdentitySource string

type configMigrationWorkflowError struct {
	code    string
	message string
}

func (e *configMigrationWorkflowError) Error() string { return e.message }

type configMigrationAppError struct {
	code    string
	message string
}

func (e *configMigrationAppError) Error() string { return e.message }

type configMigrationAppClient interface {
	SearchApps(context.Context, string, string, int) (*api.CLIPaginatedAppsResponse, error)
}

const (
	configMigrationIdentityExistingCanonical   configMigrationIdentitySource = "existing_canonical"
	configMigrationIdentityLocalExplicit       configMigrationIdentitySource = "local_explicit"
	configMigrationIdentityLocalExisting       configMigrationIdentitySource = "local_existing"
	configMigrationIdentityLocalGenerated      configMigrationIdentitySource = "local_generated"
	configMigrationIdentityOriginUnsupported   configMigrationIdentitySource = "local_github_origin_unsupported"
	configMigrationIdentityRepositoryBlocked   configMigrationIdentitySource = "local_repository_inaccessible"
	configMigrationIdentityCatalogUnavailable  configMigrationIdentitySource = "local_catalog_unavailable"
	configMigrationIdentityCatalogAuthRequired configMigrationIdentitySource = "local_catalog_authentication_required"
	configMigrationIdentityCatalogAccessDenied configMigrationIdentitySource = "local_catalog_access_denied"
	configMigrationIdentityCatalogLimit        configMigrationIdentitySource = "local_catalog_limit_exceeded"
	configMigrationIdentityProviderUnavailable configMigrationIdentitySource = "local_repository_provider_unavailable"
	configMigrationIdentityCatalogAmbiguous    configMigrationIdentitySource = "local_catalog_ambiguous"
	configMigrationIdentityRemoteExact         configMigrationIdentitySource = "remote_exact"
	configMigrationIdentityRemoteExplicit      configMigrationIdentitySource = "remote_explicit"
)

var (
	prepareLocalConfigMigration                 = prepareConfigMigrationFromDisk
	backupAndReplaceConfig                      = config.BackupAndReplaceConfig
	backupAndReplaceConfigWithLegacyTestAliases = config.BackupAndReplaceConfigWithLegacyTestAliases
	prepareResolvedLocalConfigMigration         = prepareConfigMigrationFromDiskWithLookups
	newConfigMigrationAppClient                 = func(cmd *cobra.Command, token string) configMigrationAppClient {
		devMode, _ := cmd.Flags().GetBool("dev")
		return api.NewClientWithDevMode(token, devMode)
	}
	newConfigMigrationWorkflowClient = func(cmd *cobra.Command, token string) workflowref.BoundedCatalogClient {
		devMode, _ := cmd.Flags().GetBool("dev")
		return api.NewClientWithDevMode(token, devMode)
	}
	confirmConfigMigration         = ui.PromptConfirm
	selectConfigMigrationProject   = ui.Select
	configMigrationInputTTY        = ui.IsInputTTY
	generateConfigMigrationProject = deterministicConfigMigrationProjectID
)

func newConfigMigrateCommand() *cobra.Command {
	options := configMigrateOptions{}
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Convert the selected local config to the canonical contract",
		Long: `Convert the selected legacy .revyl/config.yaml to the canonical contract.

Migration mutates only local project files. When needed, authenticated read-only
lookups resolve the exact repository project ID, legacy app names, and legacy PR
workflow names. Legacy test aliases become conflict-checked .revyl/tests/ files.
The command prints a concise migration summary, creates an exact-byte config
backup before local replacement, and never creates, attaches, pulls, publishes,
or otherwise mutates server state.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := runConfigMigrate(cmd, options)
			if err != nil {
				recordConfigMigrationFailure(cmd, err)
			}
			return analytics.WithSafeDiagnostic(
				err,
				"project configuration migration failed",
			)
		},
	}
	command.Flags().StringVar(
		&options.projectID,
		"project",
		"",
		"Use this project UUID in the migrated configuration",
	)
	command.Flags().BoolVar(
		&options.check,
		"check",
		false,
		"Print the migration proposal without writing or creating a backup",
	)
	command.Flags().BoolVar(
		&options.write,
		"write",
		false,
		"Create a backup and replace the config without confirmation",
	)
	return command
}

func init() {
	configCmd.AddCommand(newConfigMigrateCommand())
}

func runConfigMigrate(cmd *cobra.Command, options configMigrateOptions) error {
	if options.check && options.write {
		return fmt.Errorf("--check and --write cannot be used together")
	}
	explicitProjectID, err := normalizeConfigMigrationProjectID(options.projectID)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	generatedProjectID := ""
	if explicitProjectID == "" {
		generatedProjectID, err = generateConfigMigrationProject(cwd)
		if err != nil {
			return err
		}
		generatedProjectID, err = normalizeConfigMigrationProjectID(generatedProjectID)
		if err != nil {
			return fmt.Errorf("generate project ID: %w", err)
		}
	}

	if projectConfigurationJSON(cmd) && !options.check && !options.write {
		return fmt.Errorf("JSON migration requires --check or --write because prompts are disabled")
	}
	result, err := prepareLocalConfigMigration(cwd, explicitProjectID, generatedProjectID)
	var appIDsByPlatformAndName map[string]map[string]string
	var appRequired *config.LegacyAppLookupsRequired
	var appLookupIssues map[string]*configMigrationAppError
	var appResolveErr error
	var workflowIDsByName map[string]string
	var workflowRequired *config.LegacyWorkflowLookupsRequired
	var workflowLookupIssues map[string]*workflowref.ExactNameResolutionError
	var workflowResolveErr error
	for attempts := 0; err != nil && attempts < 3; attempts++ {
		switch {
		case errors.As(err, &appRequired):
			appIDsByPlatformAndName, appLookupIssues, appResolveErr = resolveConfigMigrationAppLookups(cmd, appRequired)
			if appResolveErr != nil {
				var appError *configMigrationAppError
				if errors.As(appResolveErr, &appError) && appError.code == "app_lookup_invalid" {
					return appResolveErr
				}
				appIDsByPlatformAndName = map[string]map[string]string{}
			}
		case errors.As(err, &workflowRequired):
			workflowIDsByName, workflowLookupIssues, workflowResolveErr = resolveConfigMigrationWorkflowLookups(cmd, workflowRequired)
			if workflowResolveErr != nil {
				workflowIDsByName = map[string]string{}
			}
		default:
			return actionableLocalConfigError(err)
		}
		result, err = prepareResolvedLocalConfigMigration(
			cwd,
			explicitProjectID,
			generatedProjectID,
			appIDsByPlatformAndName,
			workflowIDsByName,
		)
	}
	if err != nil {
		return actionableLocalConfigError(err)
	}
	if appRequired != nil {
		result.Omissions = annotateAppLookupMigrationChanges(
			result.Omissions,
			appRequired,
			appLookupIssues,
			appResolveErr,
		)
	}
	if workflowRequired != nil {
		if workflowResolveErr != nil {
			result.Omissions = appendWorkflowLookupMigrationChange(result.Omissions, workflowResolveErr)
		} else {
			result.Omissions = annotateWorkflowLookupMigrationChanges(result.Omissions, workflowRequired, workflowLookupIssues)
		}
	}

	jsonOutput := projectConfigurationJSON(cmd)
	if result.AlreadyCanonical {
		recordConfigMigrationOutcome(
			cmd,
			"already_canonical",
			configMigrationIdentityProperties(configMigrationIdentityExistingCanonical),
		)
		return printCompletedConfigMigration(
			jsonOutput,
			configMigrateOutput{
				Outcome:       "already_canonical",
				ProjectID:     result.ProjectID,
				Configuration: result.Authored,
				TestAliases:   result.TestAliasPlan,
			},
		)
	}
	identitySource, err := resolveConfigMigrationProjectIdentity(
		cmd,
		&result,
		explicitProjectID,
		generatedProjectID,
		options,
	)
	if err != nil {
		return err
	}
	reconciliation := inspectConfigMigrationReconciliation(cmd, result, identitySource)

	if options.check {
		recordConfigMigrationOutcome(
			cmd,
			"proposal",
			configMigrationProperties(identitySource, result.Omissions),
		)
		if jsonOutput {
			return printCompletedConfigMigration(
				true,
				configMigrateOutput{
					Outcome:        "proposal",
					ProjectID:      result.ProjectID,
					Configuration:  result.Authored,
					TestAliases:    result.TestAliasPlan,
					Omissions:      result.Omissions,
					Reconciliation: &reconciliation,
				},
			)
		}
		printConfigMigrationSummary(result)
		ui.PrintDim("No files changed (--check)")
		return nil
	}

	if !jsonOutput {
		printConfigMigrationSummary(result)
	}
	if !options.write {
		if !configMigrationInputTTY() {
			return fmt.Errorf("confirmation requires an interactive terminal; rerun %q to inspect or %q to apply", cliRecoveryCommand("config", "migrate", "--check"), cliRecoveryCommand("config", "migrate", "--write"))
		}
		confirmed, confirmErr := confirmConfigMigration(
			"Continue with migration? An exact-byte backup will be created first.",
			false,
		)
		if confirmErr != nil {
			return fmt.Errorf("confirm project configuration migration: %w", confirmErr)
		}
		if !confirmed {
			recordConfigMigrationOutcome(
				cmd,
				"declined",
				configMigrationProperties(identitySource, result.Omissions),
			)
			ui.PrintDim("Local configuration left unchanged")
			return nil
		}
	}

	backupPath := ""
	if len(result.TestAliases) > 0 {
		backupPath, err = backupAndReplaceConfigWithLegacyTestAliases(
			result.ConfigPath,
			result.CanonicalBytes,
			result.OriginalBytes,
			result.TestAliases,
		)
	} else {
		backupPath, err = backupAndReplaceConfig(
			result.ConfigPath,
			result.CanonicalBytes,
			result.OriginalBytes,
		)
	}
	if err != nil {
		actionableErr := actionableLocalConfigError(err)
		if backupPath != "" {
			properties := configMigrationProperties(identitySource, result.Omissions)
			properties["config_migration_backup_created"] = true
			recordConfigMigrationOutcome(cmd, "failed", properties)
			return fmt.Errorf("replace project configuration after creating backup at %s: %w", backupPath, actionableErr)
		}
		return actionableErr
	}
	output := configMigrateOutput{
		Outcome:        "migrated",
		ProjectID:      result.ProjectID,
		Configuration:  result.Authored,
		BackupPath:     backupPath,
		TestAliases:    result.TestAliasPlan,
		Omissions:      result.Omissions,
		Reconciliation: &reconciliation,
	}
	properties := configMigrationProperties(identitySource, result.Omissions)
	properties["config_migration_backup_created"] = true
	recordConfigMigrationOutcome(cmd, "migrated", properties)
	if jsonOutput {
		return printCompletedConfigMigration(true, output)
	}
	ui.PrintSuccess("Migrated the local project configuration")
	ui.PrintKeyValue("Backup:", backupPath)
	return nil
}

func deterministicConfigMigrationProjectID(cwd string) (string, error) {
	local, err := config.ResolveConfigFileContext(cwd, "")
	if err != nil {
		return "", actionableLocalConfigError(err)
	}
	repositoryIdentity := "worktree:" + local.WorktreeRoot
	if namespace, repositoryName, slugErr := resolveProjectRepoSlug(local.WorktreeRoot, ""); slugErr == nil {
		repositoryIdentity = "github:" + strings.ToLower(namespace) + "/" + strings.ToLower(repositoryName)
	}
	projectRoot := path.Clean(strings.ReplaceAll(local.RepositoryRelativeProjectRoot, "\\", "/"))
	return configMigrationProjectIDForLocator(repositoryIdentity, projectRoot), nil
}

func configMigrationProjectIDForLocator(repositoryIdentity, projectRoot string) string {
	identity := "https://revyl.ai/config-migration/project/" + repositoryIdentity + "/" + projectRoot
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

func resolveConfigMigrationAppLookups(
	cmd *cobra.Command,
	required *config.LegacyAppLookupsRequired,
) (map[string]map[string]string, map[string]*configMigrationAppError, error) {
	if required == nil || len(required.Lookups) == 0 {
		return nil, nil, &configMigrationAppError{code: "app_lookup_invalid", message: "legacy app lookup requirements were empty"}
	}
	token, err := readActiveConfigToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, nil, &configMigrationAppError{
			code: "app_authentication_required",
			message: fmt.Sprintf(
				"enabled legacy PR builds reference app names; run %q, then retry %q so migration can preserve their canonical app IDs",
				cliRecoveryCommand("auth", "login"),
				cliRecoveryCommand("config", "migrate"),
			),
		}
	}
	client := newConfigMigrationAppClient(cmd, token)
	resolved := map[string]map[string]string{}
	issues := map[string]*configMigrationAppError{}
	for _, lookup := range required.Lookups {
		if resolved[lookup.Platform] != nil && resolved[lookup.Platform][lookup.Name] != "" {
			continue
		}
		catalog, lookupErr := client.SearchApps(cmd.Context(), lookup.Name, lookup.Platform, 100)
		if lookupErr != nil {
			issues[strings.Join(lookup.Path, ".")] = &configMigrationAppError{
				code: "app_catalog_unavailable",
				message: fmt.Sprintf(
					"could not read the %s app catalog needed to preserve an enabled PR build; retry %q, then run %q if the failure continues",
					lookup.Platform,
					cliRecoveryCommand("config", "migrate"),
					cliRecoveryCommand("doctor"),
				),
			}
			continue
		}
		if catalog == nil || catalog.HasNext || catalog.Total > len(catalog.Items) {
			issues[strings.Join(lookup.Path, ".")] = &configMigrationAppError{
				code: "app_catalog_incomplete",
				message: fmt.Sprintf(
					"could not verify a complete %s app catalog for an enabled PR build; run %q, replace the legacy app name with its UUID, then retry %q",
					lookup.Platform,
					cliRecoveryCommand("app", "list", "--platform", lookup.Platform),
					cliRecoveryCommand("config", "migrate"),
				),
			}
			continue
		}
		matches := []api.App{}
		for _, app := range catalog.Items {
			if app.Name == lookup.Name && strings.EqualFold(app.Platform, lookup.Platform) {
				matches = append(matches, app)
			}
		}
		if len(matches) != 1 {
			code := "app_name_not_found"
			if len(matches) > 1 {
				code = "app_name_ambiguous"
			}
			issues[strings.Join(lookup.Path, ".")] = &configMigrationAppError{
				code: code,
				message: fmt.Sprintf(
					"could not resolve the enabled PR build app at %s to exactly one %s app; run %q, replace the legacy app name with its UUID, then retry %q",
					strings.Join(lookup.Path, "."),
					lookup.Platform,
					cliRecoveryCommand("app", "list", "--platform", lookup.Platform),
					cliRecoveryCommand("config", "migrate"),
				),
			}
			continue
		}
		parsed, parseErr := uuid.Parse(strings.TrimSpace(matches[0].ID))
		if parseErr != nil {
			issues[strings.Join(lookup.Path, ".")] = &configMigrationAppError{
				code: "app_id_invalid",
				message: fmt.Sprintf(
					"the matched app for %s has an invalid server ID; contact Revyl support before retrying migration",
					strings.Join(lookup.Path, "."),
				),
			}
			continue
		}
		if resolved[lookup.Platform] == nil {
			resolved[lookup.Platform] = map[string]string{}
		}
		resolved[lookup.Platform][lookup.Name] = parsed.String()
	}
	return resolved, issues, nil
}

func annotateAppLookupMigrationChanges(
	changes []config.LegacyConfigOmission,
	required *config.LegacyAppLookupsRequired,
	issues map[string]*configMigrationAppError,
	lookupErr error,
) []config.LegacyConfigOmission {
	if required == nil {
		return changes
	}
	var sharedIssue *configMigrationAppError
	if errors.As(lookupErr, &sharedIssue) {
		issues = make(map[string]*configMigrationAppError, len(required.Lookups))
		for _, lookup := range required.Lookups {
			issues[strings.Join(lookup.Path, ".")] = sharedIssue
		}
	}
	for index := range changes {
		change := &changes[index]
		if change.Code != "legacy_app_reference_unresolved" {
			continue
		}
		issue := issues[strings.Join(change.Path, ".")]
		if issue == nil {
			continue
		}
		change.Code = "legacy_" + issue.code
		change.Message = issue.message + "; the review build was omitted from this best-effort proposal"
	}
	return changes
}

func resolveConfigMigrationWorkflowLookups(
	cmd *cobra.Command,
	required *config.LegacyWorkflowLookupsRequired,
) (map[string]string, map[string]*workflowref.ExactNameResolutionError, error) {
	if required == nil || len(required.Lookups) == 0 {
		return nil, nil, fmt.Errorf("legacy workflow lookup requirements were empty")
	}
	token, err := readActiveConfigToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, nil, &configMigrationWorkflowError{
			"workflow_authentication_required",
			fmt.Sprintf("legacy PR workflow names require authentication; run %q, then retry %q", cliRecoveryCommand("auth", "login"), cliRecoveryCommand("config", "migrate")),
		}
	}
	names := make([]string, 0, len(required.Lookups))
	for _, lookup := range required.Lookups {
		names = append(names, lookup.Name)
	}
	resolutions, issues, err := workflowref.ResolveExactNamesBestEffort(cmd.Context(), newConfigMigrationWorkflowClient(cmd, token), names)
	if err != nil {
		var apiError *api.APIError
		if errors.As(err, &apiError) {
			switch apiError.StatusCode {
			case 401:
				return nil, nil, &configMigrationWorkflowError{
					code: "workflow_authentication_required",
					message: fmt.Sprintf(
						"legacy PR workflow names could not be read because Revyl authentication is no longer valid; run %q, then retry %q before applying the migration",
						cliRecoveryCommand("auth", "login"),
						cliRecoveryCommand("config", "migrate"),
					),
				}
			case 403:
				return nil, nil, &configMigrationWorkflowError{
					code: "workflow_access_denied",
					message: fmt.Sprintf(
						"legacy PR workflow names could not be read because the active account cannot access this organization's workflow catalog; run %q, ask an organization admin for access if needed, then retry %q before applying the migration",
						cliRecoveryCommand("auth", "status"),
						cliRecoveryCommand("config", "migrate"),
					),
				}
			}
		}
		var resolutionError *workflowref.ExactNameResolutionError
		code := "workflow_lookup_failed"
		if errors.As(err, &resolutionError) {
			code = "workflow_" + string(resolutionError.Kind)
		}
		return nil, nil, &configMigrationWorkflowError{code: code, message: err.Error()}
	}
	resolved := make(map[string]string, len(resolutions))
	for name, resolution := range resolutions {
		resolved[name] = resolution.ID
	}
	return resolved, issues, nil
}

func annotateWorkflowLookupMigrationChanges(
	changes []config.LegacyConfigOmission,
	required *config.LegacyWorkflowLookupsRequired,
	issues map[string]*workflowref.ExactNameResolutionError,
) []config.LegacyConfigOmission {
	if required == nil || len(issues) == 0 {
		return changes
	}
	issueByPath := make(map[string]*workflowref.ExactNameResolutionError, len(required.Lookups))
	for _, lookup := range required.Lookups {
		if issue := issues[lookup.Name]; issue != nil {
			issueByPath[strings.Join(lookup.Path, ".")] = issue
		}
	}
	for index := range changes {
		change := &changes[index]
		if change.Code != "legacy_workflow_reference_unresolved" {
			continue
		}
		issue := issueByPath[strings.Join(change.Path, ".")]
		if issue == nil {
			continue
		}
		switch issue.Kind {
		case workflowref.ExactNameNotFound:
			change.Code = "legacy_workflow_reference_not_found"
			change.Message = "the legacy workflow name was not found and was omitted"
		case workflowref.ExactNameAmbiguous:
			change.Code = "legacy_workflow_reference_ambiguous"
			change.Message = "the legacy workflow name matched multiple workflows and was omitted"
		case workflowref.ExactNameInvalidWorkflowID:
			change.Code = "legacy_workflow_reference_invalid"
			change.Message = "the matched workflow had an invalid canonical ID and was omitted"
		default:
			change.Code = "legacy_workflow_reference_unresolved"
			change.Message = "the legacy workflow name could not be resolved exactly and was omitted"
		}
	}
	return changes
}

func appendWorkflowLookupMigrationChange(
	changes []config.LegacyConfigOmission,
	err error,
) []config.LegacyConfigOmission {
	code := "workflow_lookup_unavailable"
	message := "legacy workflow names could not be resolved exactly and were omitted; retry migration when the workflow catalog is available to preserve them"
	var workflowError *configMigrationWorkflowError
	if errors.As(err, &workflowError) {
		code = workflowError.code
		switch workflowError.code {
		case "workflow_authentication_required":
			message = fmt.Sprintf("legacy workflow names could not be resolved because Revyl authentication is unavailable and were omitted; run %q, then retry %q before applying the migration to preserve them; if already applied, restore the reported migration backup first", cliRecoveryCommand("auth", "login"), cliRecoveryCommand("config", "migrate"))
		case "workflow_access_denied":
			message = fmt.Sprintf("legacy workflow names could not be resolved because the active account cannot access this organization's workflow catalog and were omitted; run %q, ask an organization admin for access if needed, then retry %q before applying the migration to preserve them; if already applied, restore the reported migration backup first", cliRecoveryCommand("auth", "status"), cliRecoveryCommand("config", "migrate"))
		}
	}
	changes = append(changes, config.LegacyConfigOmission{
		Code:        code,
		Path:        []string{"pr_review", "actions", "workflows"},
		Message:     message,
		Disposition: "omitted",
	})
	sort.SliceStable(changes, func(i, j int) bool {
		left := strings.Join(changes[i].Path, ".") + "\x00" + changes[i].Code
		right := strings.Join(changes[j].Path, ".") + "\x00" + changes[j].Code
		return left < right
	})
	return changes
}

func printConfigMigrationSummary(result preparedLocalConfigMigration) {
	counts := map[string]int{"omitted": 0, "defaulted": 0}
	for _, change := range result.Omissions {
		if _, exists := counts[change.Disposition]; exists {
			counts[change.Disposition]++
		}
	}
	ui.PrintInfo("Config migration ready")
	if counts["omitted"] == 0 && counts["defaulted"] == 0 {
		ui.PrintDim("  Legacy fields: no fields will be dropped or defaulted")
	} else {
		ui.PrintWarning(
			"  Legacy fields: %d will be dropped; %d will use canonical defaults",
			counts["omitted"],
			counts["defaulted"],
		)
		ui.PrintDim(
			"  Review: inspect %q; after writing, compare the reported backup or ask your coding agent to reconcile omissions",
			cliRecoveryCommand("config", "migrate", "--check", "--json"),
		)
	}
	ui.PrintDim("  Backup: an exact-byte backup will be created before replacement")
}

func inspectConfigMigrationReconciliation(
	cmd *cobra.Command,
	result preparedLocalConfigMigration,
	identitySource configMigrationIdentitySource,
) configMigrationReconciliation {
	githubStatus := cliRecoveryCommand("github", "status")
	githubConnect := cliRecoveryCommand("github", "connect")
	configPush := cliRecoveryCommand("config", "push")
	configPull := cliRecoveryCommand("config", "pull")
	configValidate := cliRecoveryCommand("config", "validate")
	if identitySource == configMigrationIdentityOriginUnsupported {
		addOrigin := gitRecoveryCommand(result.WorktreeRoot, "remote", "add", "origin", "https://github.com/<owner>/<repository>.git")
		setOrigin := gitRecoveryCommand(result.WorktreeRoot, "remote", "set-url", "origin", "https://github.com/<owner>/<repository>.git")
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "github_origin_unsupported",
			NextAction:  "repair_github_origin_then_retry_migration",
			Explanation: fmt.Sprintf("Canonical project lookup was skipped because this worktree has no supported GitHub origin. Run %q if origin is missing or %q to repair the existing origin, then retry %q before applying this proposal. If already migrated, run %q before %q, or deliberately remain local.", addOrigin, setOrigin, cliRecoveryCommand("config", "migrate", "--check"), configValidate, configPush),
		}
	}
	if identitySource == configMigrationIdentityRepositoryBlocked {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "repository_inaccessible",
			NextAction:  "run_github_status_then_connect_or_grant_access",
			Explanation: fmt.Sprintf("Revyl could not verify this repository. Run %q; if GitHub is disconnected, run %q, or grant this repository to the existing Revyl GitHub App. Then retry %q after migration, or deliberately remain local.", githubStatus, githubConnect, configPush),
		}
	}
	if identitySource == configMigrationIdentityCatalogUnavailable {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "catalog_unavailable",
			NextAction:  "retry_config_validate_when_connected",
			Explanation: fmt.Sprintf("Canonical project lookup was unavailable. The migration remains local; retry %q when Revyl is reachable before publishing, or deliberately remain local.", configValidate),
		}
	}
	if identitySource == configMigrationIdentityCatalogAuthRequired {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "catalog_authentication_required",
			NextAction:  "authenticate_then_restore_or_validate",
			Explanation: fmt.Sprintf("Canonical project lookup was skipped because Revyl authentication is no longer valid. The migration remains local; run %q, then run %q to restore an existing canonical project or %q before publishing a new one.", cliRecoveryCommand("auth", "login"), configPull, configValidate),
		}
	}
	if identitySource == configMigrationIdentityCatalogAccessDenied {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "catalog_access_denied",
			NextAction:  "verify_account_and_request_access",
			Explanation: fmt.Sprintf("Canonical project lookup was denied for the active account and organization. The migration remains local; run %q, ask an organization admin for access if needed, then run %q to restore an existing canonical project or %q before publishing a new one.", cliRecoveryCommand("auth", "status"), configPull, configValidate),
		}
	}
	if identitySource == configMigrationIdentityCatalogLimit {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "catalog_limit_exceeded",
			NextAction:  "contact_support_then_validate",
			Explanation: fmt.Sprintf("Canonical project lookup exceeded Revyl's bounded repository project catalog. The migration remains local; contact Revyl support to repair or raise the repository project limit, then run %q before publishing.", configValidate),
		}
	}
	if identitySource == configMigrationIdentityProviderUnavailable {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "repository_provider_unavailable",
			NextAction:  "retry_when_provider_is_available",
			Explanation: fmt.Sprintf("Canonical project lookup could not complete because Revyl or GitHub was temporarily unavailable. The migration remains local; retry %q, then run %q if the failure continues.", configValidate, cliRecoveryCommand("doctor")),
		}
	}
	if identitySource == configMigrationIdentityCatalogAmbiguous {
		return configMigrationReconciliation{
			Status:      "skipped",
			Outcome:     "catalog_ambiguous",
			NextAction:  "inspect_projects_then_validate",
			Explanation: fmt.Sprintf("Revyl could not select one exact canonical project for this config path. The migration remains local; inspect the repository's projects in Revyl, then run %q before publishing, or deliberately remain local.", configValidate),
		}
	}
	local := configMigrationReconciliation{
		Status:      "skipped",
		Outcome:     "no_canonical_project",
		NextAction:  "run_config_validate_then_push_or_remain_local",
		Explanation: fmt.Sprintf("No exact canonical project is registered for this config path. Run %q, then run %q to publish when ready, or deliberately remain local.", configValidate, configPush),
	}
	if identitySource != configMigrationIdentityRemoteExact && identitySource != configMigrationIdentityRemoteExplicit {
		return local
	}
	namespace, repositoryName, err := resolveProjectRepoSlug(result.WorktreeRoot, "")
	if err != nil {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "unavailable", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Canonical comparison is unavailable; run %q after migration.", configValidate),
		}
	}
	token, err := readActiveConfigToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "unavailable", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Canonical comparison is unavailable; authenticate and run %q after migration.", configValidate),
		}
	}
	authored, err := authoredConfigForAPI(result.Authored)
	if err != nil {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "unavailable", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Canonical comparison is unavailable; run %q after migration.", configValidate),
		}
	}
	validation, err := projectConfigurationClientForCommand(cmd, token).ValidateProjectConfiguration(
		cmd.Context(),
		result.ProjectID,
		api.ProjectConfigurationValidateRequest{
			Locator: api.ProjectConfigurationRepositoryLocator{
				Provider: "github", Namespace: namespace, RepositoryName: repositoryName,
				RepositoryRelativeProjectRoot: result.RepositoryRelativeProjectRoot,
			},
			Configuration: authored,
		},
	)
	if err != nil {
		if classified, ok := configMigrationValidationFailure(err, configValidate); ok {
			return classified
		}
		return configMigrationReconciliation{
			Status: "failed", Outcome: "unavailable", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Canonical comparison is unavailable; run %q after migration.", configValidate),
		}
	}
	if validation == nil {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "invalid_response", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Revyl returned no canonical comparison; retry with %q.", configValidate),
		}
	}
	if validation.CandidateProjectConfigurationHash != result.ProjectConfigurationHash {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "invalid_response", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Revyl returned an inconsistent candidate hash; retry with %q.", configValidate),
		}
	}
	if validation.Current.State == api.ProjectConfigurationReadResponseStateAbsent {
		return configMigrationReconciliation{
			Status: "succeeded", Outcome: "absent", NextAction: "publish_or_remain_divergent",
			Explanation: "Revyl has no canonical configuration for this project; publish it when ready or deliberately remain divergent.",
		}
	}
	if validation.Current.State != api.ProjectConfigurationReadResponseStatePresent || validation.Current.Resource == nil {
		return configMigrationReconciliation{
			Status: "failed", Outcome: "invalid_response", NextAction: "run_config_validate",
			Explanation: fmt.Sprintf("Revyl returned an invalid canonical state; retry with %q.", configValidate),
		}
	}
	resource := validation.Current.Resource
	authority := resource.Authority
	if resource.ProjectConfigurationHash == validation.CandidateProjectConfigurationHash {
		return configMigrationReconciliation{
			Status: "succeeded", Outcome: "aligned", Authority: &authority, NextAction: "none",
			Explanation: "The migration proposal already matches the exact canonical project.",
		}
	}
	if authority == api.ConfigurationAuthorityGitDefaultBranch {
		return configMigrationReconciliation{
			Status: "succeeded", Outcome: "divergent", Authority: &authority, NextAction: "validate_and_commit_or_pull_or_remain_divergent",
			Explanation: "Git owns the canonical configuration; validate and commit the designated file, pull the canonical version, or deliberately remain divergent.",
		}
	}
	return configMigrationReconciliation{
		Status: "succeeded", Outcome: "divergent", Authority: &authority, NextAction: "push_or_pull_or_remain_divergent",
		Explanation: "Manual authority is divergent; push with observed-hash protection, pull the canonical version, or deliberately remain divergent.",
	}
}

func resolveConfigMigrationProjectIdentity(
	cmd *cobra.Command,
	result *preparedLocalConfigMigration,
	explicitProjectID string,
	generatedProjectID string,
	options configMigrateOptions,
) (configMigrationIdentitySource, error) {
	localSource := configMigrationIdentityLocalExisting
	if explicitProjectID != "" {
		localSource = configMigrationIdentityLocalExplicit
	} else if result.ProjectID == generatedProjectID {
		localSource = configMigrationIdentityLocalGenerated
	}

	namespace, repositoryName, err := resolveProjectRepoSlug(result.WorktreeRoot, "")
	if err != nil {
		return configMigrationIdentityOriginUnsupported, nil
	}
	token, err := readActiveConfigToken()
	if err != nil {
		return configMigrationIdentityCatalogUnavailable, nil
	}
	if strings.TrimSpace(token) == "" {
		return localSource, nil
	}

	catalog, err := projectConfigurationClientForCommand(cmd, token).ListRepositoryProjects(
		cmd.Context(),
		api.RepositoryProjectCatalogQuery{
			Provider:       "github",
			Namespace:      namespace,
			RepositoryName: repositoryName,
		},
	)
	if err != nil {
		return configMigrationCatalogErrorSource(err), nil
	}
	if catalog == nil ||
		catalog.Repository.Provider != "github" ||
		!strings.EqualFold(catalog.Repository.Namespace, namespace) ||
		!strings.EqualFold(catalog.Repository.RepositoryName, repositoryName) {
		return configMigrationIdentityCatalogAmbiguous, nil
	}

	expectedRoot := result.RepositoryRelativeProjectRoot
	expectedConfigPath := path.Join(expectedRoot, ".revyl/config.yaml")
	exact := make([]api.RepositoryProjectCatalogItem, 0, 1)
	for _, project := range catalog.Projects {
		if project.RepositoryRelativeProjectRoot == expectedRoot &&
			project.RepositoryRelativeConfigPath == expectedConfigPath {
			exact = append(exact, project)
		}
	}
	if explicitProjectID != "" {
		for _, project := range catalog.Projects {
			if project.ProjectId.String() != explicitProjectID {
				continue
			}
			exactMatch := false
			for _, candidate := range exact {
				if candidate.ProjectId.String() == explicitProjectID {
					exactMatch = true
					break
				}
			}
			if len(exact) > 0 && !exactMatch {
				return configMigrationIdentityCatalogAmbiguous, fmt.Errorf(
					"--project does not match the Revyl project registered for this config path; omit --project to use the registered project",
				)
			}
			return configMigrationIdentityRemoteExplicit, nil
		}
		if len(exact) > 0 {
			return configMigrationIdentityCatalogAmbiguous, fmt.Errorf(
				"--project does not match the Revyl project registered for this config path; omit --project to use the registered project",
			)
		}
		return localSource, nil
	}
	if len(exact) != 1 {
		if len(catalog.Projects) == 0 {
			return localSource, nil
		}
		interactive := !options.check && !options.write && !projectConfigurationJSON(cmd) && configMigrationInputTTY()
		if !interactive {
			if options.write {
				return configMigrationIdentityCatalogAmbiguous, fmt.Errorf(
					"the verified repository has canonical projects but none matches this config path; rerun interactively to select one, or pass '--project <id>'",
				)
			}
			return configMigrationIdentityCatalogAmbiguous, nil
		}
		projects := append([]api.RepositoryProjectCatalogItem(nil), catalog.Projects...)
		sort.SliceStable(projects, func(i, j int) bool {
			if projects[i].RepositoryRelativeProjectRoot == projects[j].RepositoryRelativeProjectRoot {
				return projects[i].ProjectId.String() < projects[j].ProjectId.String()
			}
			return projects[i].RepositoryRelativeProjectRoot < projects[j].RepositoryRelativeProjectRoot
		})
		selectionOptions := make([]ui.SelectOption, 0, len(projects)+1)
		for _, project := range projects {
			selectionOptions = append(selectionOptions, ui.SelectOption{
				Label:       project.RepositoryRelativeProjectRoot,
				Value:       project.ProjectId.String(),
				Description: project.ProjectId.String(),
			})
		}
		selectionOptions = append(selectionOptions, ui.SelectOption{
			Label:       "Keep this configuration local",
			Value:       "local",
			Description: "Use a deterministic local project ID without attaching to a server project.",
		})
		_, selected, selectErr := selectConfigMigrationProject(
			"Select the existing Revyl project for this config:",
			selectionOptions,
			len(selectionOptions)-1,
		)
		if selectErr != nil {
			return configMigrationIdentityCatalogAmbiguous, fmt.Errorf("select canonical project: %w", selectErr)
		}
		if selected == "local" {
			return localSource, nil
		}
		if err := replacePreparedConfigMigrationProjectID(result, selected); err != nil {
			return configMigrationIdentityCatalogAmbiguous, actionableLocalConfigError(err)
		}
		return configMigrationIdentityRemoteExplicit, nil
	}

	canonicalProjectID := exact[0].ProjectId.String()
	if canonicalProjectID != result.ProjectID {
		if err := replacePreparedConfigMigrationProjectID(result, canonicalProjectID); err != nil {
			return configMigrationIdentityCatalogAmbiguous, nil
		}
	}
	return configMigrationIdentityRemoteExact, nil
}

func isRepositoryProjectsInaccessible(err error) bool {
	var apiError *api.APIError
	return errors.As(err, &apiError) &&
		apiError.StatusCode == 404 &&
		apiError.Code == "repository_projects_inaccessible"
}

func configMigrationCatalogErrorSource(err error) configMigrationIdentitySource {
	if isRepositoryProjectsInaccessible(err) {
		return configMigrationIdentityRepositoryBlocked
	}
	var apiError *api.APIError
	if !errors.As(err, &apiError) {
		return configMigrationIdentityCatalogUnavailable
	}
	switch {
	case apiError.StatusCode == 401:
		return configMigrationIdentityCatalogAuthRequired
	case apiError.StatusCode == 403:
		return configMigrationIdentityCatalogAccessDenied
	case apiError.Code == "repository_projects_limit_exceeded":
		return configMigrationIdentityCatalogLimit
	case apiError.Code == "repository_provider_unavailable",
		apiError.Code == "repository_projects_provider_unavailable",
		apiError.StatusCode >= 500:
		return configMigrationIdentityProviderUnavailable
	default:
		return configMigrationIdentityCatalogUnavailable
	}
}

func configMigrationValidationFailure(err error, configValidate string) (configMigrationReconciliation, bool) {
	var apiError *api.APIError
	if !errors.As(err, &apiError) {
		return configMigrationReconciliation{}, false
	}
	switch apiError.Code {
	case "referenced_workflow_not_available",
		"referenced_secret_not_available",
		"referenced_launch_variable_not_available":
		return configMigrationReferenceValidationFailure(apiError, configValidate), true
	}
	switch apiError.StatusCode {
	case 401:
		return configMigrationReconciliation{
			Status:      "failed",
			Outcome:     "authentication_required",
			NextAction:  "authenticate_then_validate",
			Explanation: fmt.Sprintf("Canonical comparison failed because Revyl authentication is no longer valid; run %q, then retry %q.", cliRecoveryCommand("auth", "login"), configValidate),
		}, true
	case 403:
		return configMigrationReconciliation{
			Status:      "failed",
			Outcome:     "access_denied",
			NextAction:  "verify_account_and_request_access",
			Explanation: fmt.Sprintf("Canonical comparison was denied for the active account and organization; run %q, ask an organization admin for access if needed, then retry %q.", cliRecoveryCommand("auth", "status"), configValidate),
		}, true
	case 422:
		return configMigrationReferenceValidationFailure(apiError, configValidate), true
	default:
		return configMigrationReconciliation{}, false
	}
}

func configMigrationReferenceValidationFailure(apiError *api.APIError, configValidate string) configMigrationReconciliation {
	outcome := "configuration_reference_rejected"
	repair := "repair the reported repository-bound reference in .revyl/config.yaml"
	issueType := apiError.Code
	for _, issue := range apiError.ValidationIssues {
		if strings.TrimSpace(issue.Type) != "" {
			issueType = issue.Type
		}
		if issueType == "referenced_app_not_available" {
			appList := cliRecoveryCommand("app", "list")
			switch {
			case strings.Contains(issue.Field, ".ios."):
				appList = cliRecoveryCommand("app", "list", "--platform", "ios")
			case strings.Contains(issue.Field, ".android."):
				appList = cliRecoveryCommand("app", "list", "--platform", "android")
			}
			outcome = "app_reference_unavailable"
			repair = fmt.Sprintf("run %q and replace the reported app_id with an active app accessible to this organization", appList)
		}
		break
	}
	switch issueType {
	case "referenced_workflow_not_available":
		outcome = "workflow_reference_unavailable"
		repair = fmt.Sprintf("run %q and replace the reported workflow_id with one accessible to this organization", cliRecoveryCommand("workflow", "list"))
	case "referenced_secret_not_available":
		outcome = "secret_reference_unavailable"
		repair = fmt.Sprintf("run %q and repair the reported build secret reference", cliRecoveryCommand("build", "secret", "list"))
	case "referenced_launch_variable_not_available":
		outcome = "launch_variable_reference_unavailable"
		repair = fmt.Sprintf("run %q and repair the reported launch variable reference", cliRecoveryCommand("global", "launch-var", "list"))
	}
	detail := strings.TrimSpace(apiError.Detail)
	if detail == "" {
		detail = "Revyl rejected a repository-bound configuration reference"
	}
	return configMigrationReconciliation{
		Status:      "failed",
		Outcome:     outcome,
		NextAction:  "repair_reference_then_validate",
		Explanation: fmt.Sprintf("Canonical comparison was rejected: %s; %s, then retry %q. The migration itself remains local.", detail, repair, configValidate),
	}
}

func replacePreparedConfigMigrationProjectID(
	result *preparedLocalConfigMigration,
	projectID string,
) error {
	migrationChanges := result.Omissions
	migrated, err := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data:                          result.OriginalBytes,
		Context:                       result.CompilationContext,
		ExplicitProjectID:             projectID,
		LegacyAppIDsByPlatformAndName: result.LegacyAppIDsByPlatformAndName,
		LegacyWorkflowIDsByName:       result.LegacyWorkflowIDsByName,
	})
	if err != nil {
		return err
	}
	if migrated.AlreadyCanonical {
		return fmt.Errorf("project configuration changed classification during migration")
	}
	result.ProjectID = migrated.ProjectID
	result.Authored = migrated.Authored
	result.CanonicalBytes = migrated.CanonicalBytes
	result.ProjectConfigurationHash = migrated.Aggregate.ProjectConfigurationHash
	result.TestAliases = migrated.TestAliases
	result.Omissions = migrationChanges
	return nil
}

func configMigrationIdentityProperties(
	source configMigrationIdentitySource,
) map[string]interface{} {
	analyticsSource := source
	switch source {
	case configMigrationIdentityOriginUnsupported:
		analyticsSource = configMigrationIdentityRepositoryBlocked
	case configMigrationIdentityCatalogAuthRequired,
		configMigrationIdentityCatalogAccessDenied,
		configMigrationIdentityCatalogLimit,
		configMigrationIdentityProviderUnavailable:
		analyticsSource = configMigrationIdentityCatalogUnavailable
	}
	return map[string]interface{}{
		"config_migration_identity_source": string(analyticsSource),
	}
}

func configMigrationProperties(
	source configMigrationIdentitySource,
	changes []config.LegacyConfigOmission,
) map[string]interface{} {
	properties := configMigrationIdentityProperties(source)
	counts := map[string]int{
		"omitted":   0,
		"defaulted": 0,
		"resolved":  0,
	}
	for _, change := range changes {
		if _, supported := counts[change.Disposition]; supported {
			counts[change.Disposition]++
		}
	}
	properties["config_migration_omitted_count"] = counts["omitted"]
	properties["config_migration_defaulted_count"] = counts["defaulted"]
	properties["config_migration_resolved_count"] = counts["resolved"]
	return properties
}

func recordConfigMigrationFailure(cmd *cobra.Command, err error) {
	properties := map[string]interface{}{}
	var configError *config.ConfigError
	if errors.As(err, &configError) {
		properties["config_migration_failure_stage"] = configError.Stage
		properties["config_migration_failure_code"] = configError.Code
	}
	var workflowError *configMigrationWorkflowError
	if errors.As(err, &workflowError) {
		properties["config_migration_failure_stage"] = "workflow_resolution"
		properties["config_migration_failure_code"] = workflowError.code
	}
	var appError *configMigrationAppError
	if errors.As(err, &appError) {
		properties["config_migration_failure_stage"] = "app_resolution"
		properties["config_migration_failure_code"] = appError.code
	}
	recordConfigMigrationOutcome(cmd, "failed", properties)
}

func recordConfigMigrationOutcome(cmd *cobra.Command, outcome string, properties map[string]interface{}) {
	if cmd == nil {
		return
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "config_migration",
		DomainStatus: outcome,
		Properties:   properties,
	})
}

func prepareConfigMigrationFromDisk(
	cwd string,
	explicitProjectID string,
	generatedProjectID string,
) (preparedLocalConfigMigration, error) {
	return prepareConfigMigrationFromDiskWithLookups(cwd, explicitProjectID, generatedProjectID, nil, nil)
}

func prepareConfigMigrationFromDiskWithLookups(
	cwd string,
	explicitProjectID string,
	generatedProjectID string,
	legacyAppIDsByPlatformAndName map[string]map[string]string,
	legacyWorkflowIDsByName map[string]string,
) (preparedLocalConfigMigration, error) {
	local, err := config.ResolveConfigFileContext(cwd, "")
	if err != nil {
		return preparedLocalConfigMigration{}, actionableLocalConfigError(err)
	}
	result, err := config.MigrateLegacyConfigBytes(config.LegacyConfigMigrationInput{
		Data:                          local.OriginalBytes,
		Context:                       local.CompilationContext(),
		ExplicitProjectID:             explicitProjectID,
		GeneratedProjectID:            generatedProjectID,
		LegacyAppIDsByPlatformAndName: legacyAppIDsByPlatformAndName,
		LegacyWorkflowIDsByName:       legacyWorkflowIDsByName,
	})
	if err != nil {
		var appRequired *config.LegacyAppLookupsRequired
		var workflowRequired *config.LegacyWorkflowLookupsRequired
		if errors.As(err, &appRequired) || errors.As(err, &workflowRequired) {
			return preparedLocalConfigMigration{}, err
		}
		return preparedLocalConfigMigration{}, actionableLocalConfigError(err)
	}
	aliasPlan, err := config.PlanLegacyConfigTestAliases(local.ConfigPath, result.TestAliases)
	if err != nil {
		return preparedLocalConfigMigration{}, actionableLocalConfigError(err)
	}
	return preparedLocalConfigMigration{
		WorktreeRoot:                  local.WorktreeRoot,
		ConfigPath:                    local.ConfigPath,
		RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
		CompilationContext:            local.CompilationContext(),
		OriginalBytes:                 local.OriginalBytes,
		AlreadyCanonical:              result.AlreadyCanonical,
		ProjectID:                     result.ProjectID,
		Authored:                      result.Authored,
		CanonicalBytes:                result.CanonicalBytes,
		ProjectConfigurationHash:      result.Aggregate.ProjectConfigurationHash,
		TestAliases:                   result.TestAliases,
		TestAliasPlan:                 aliasPlan,
		Omissions:                     result.Omissions,
		LegacyAppIDsByPlatformAndName: legacyAppIDsByPlatformAndName,
		LegacyWorkflowIDsByName:       legacyWorkflowIDsByName,
	}, nil
}

func normalizeConfigMigrationProjectID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid --project %q: expected a UUID", value)
	}
	return parsed.String(), nil
}

func printCompletedConfigMigration(jsonOutput bool, output configMigrateOutput) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	if output.Outcome == "already_canonical" {
		ui.PrintSuccess("Local project configuration already uses the canonical contract")
	}
	return nil
}
